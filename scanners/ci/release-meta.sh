#!/usr/bin/env bash
# Resolve untrusted GitHub event/dispatch values into a small, validated scanner
# release-factory contract. This script deliberately owns all tag construction;
# workflow run blocks must not interpolate event-controlled strings.
set -euo pipefail

die() {
    printf 'release metadata error: %s\n' "$*" >&2
    exit 2
}

emit() {
    local key="$1"
    local value="$2"
    printf '%s=%s\n' "$key" "$value"
    if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
        printf '%s=%s\n' "$key" "$value" >>"$GITHUB_OUTPUT"
    fi
}

safe_slug() {
    printf '%s' "$1" \
        | tr '[:upper:]' '[:lower:]' \
        | sed -E 's/[^a-z0-9._-]+/-/g; s/^-+//; s/-+$//; s/-+/-/g' \
        | cut -c1-48
}

validate_candidate_id() {
    [[ "$1" =~ ^scanner-candidate-[a-z0-9][a-z0-9._-]{0,94}$ ]] ||
        die "candidate_id must match scanner-candidate-[a-z0-9][a-z0-9._-]{0,94}"
}

validate_release_id() {
    [[ "$1" =~ ^scanner-set-[0-9]{4}\.(0[1-9]|[1-4][0-9]|5[0-3])\.[1-9][0-9]*$ ]] ||
        die "release_id must match scanner-set-YYYY.WW.N with an ISO week from 01 through 53"
}

validate_scanner_version() {
    [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
        die "scanner_version must be exact semver MAJOR.MINOR.PATCH"
}

validate_channel() {
    case "$1" in
        none | candidate | stable) ;;
        *) die "channel must be one of: none, candidate, stable" ;;
    esac
}

validate_mirror_mode() {
    case "$1" in
        auto | required | disabled) ;;
        *) die "mirror_mode must be one of: auto, required, disabled" ;;
    esac
}

validate_os_package_snapshot() {
    [[ "$1" =~ ^[0-9]{8}T[0-9]{6}Z$ ]] ||
        die "os_package_snapshot must use YYYYMMDDTHHMMSSZ"
}

resolve() {
    local event_name="${EVENT_NAME:-}"
    local schedule="${EVENT_SCHEDULE:-}"
    local input_operation="${INPUT_OPERATION:-}"
    local operation
    local run_discovery=false
    local run_validation=false
    local run_build=false
    local run_os_package_refresh=false
	local run_vulnerability_db_refresh=false
    local publish=false
    local immutable_id=""
    local candidate_id=""
    local aliases=""
    local channel="${INPUT_CHANNEL:-none}"
    local mirror_mode="${INPUT_MIRROR_MODE:-auto}"
    local os_package_snapshot="${INPUT_OS_PACKAGE_SNAPSHOT:-}"
    local scanner_version="${INPUT_SCANNER_VERSION:-}"
    local sha="${GIT_SHA:-}"
    local short_sha
    local ref_slug
    local run_suffix

    [[ "$sha" =~ ^[a-f0-9]{40}$ ]] || die "GIT_SHA must be a 40-character lowercase Git SHA"
    short_sha="${sha:0:12}"
    ref_slug="$(safe_slug "${REF_NAME:-ref}")"
    run_suffix="r${RUN_ID:-0}-a${RUN_ATTEMPT:-1}"
    [[ -n "$ref_slug" ]] || ref_slug="ref"
    validate_channel "$channel"
    validate_mirror_mode "$mirror_mode"

    case "$event_name" in
        schedule)
            case "$schedule" in
                "17 2 * * *")
                    operation="discover"
                    run_discovery=true
					run_vulnerability_db_refresh=true
                    ;;
                "43 3 * * 0")
                    # Unattended weekly release. Unlike the `release` dispatch
                    # — which promotes an already-built candidate and requires
                    # a protected-environment approval receipt — this path
                    # builds and then moves the public channels itself, once
                    # every quality, security, SBOM, signature, and provenance
                    # gate has passed. There is deliberately no human in this
                    # loop; `release` remains the gated route for anything a
                    # person drives.
                    operation="scheduled-release"
                    run_discovery=true
                    run_validation=true
                    run_build=true
                    publish=true
                    # A release identity, not a candidate one: this run
                    # publishes the set the world consumes. The attempt number
                    # supplies the sequence, so a re-run after a failure mints
                    # a fresh immutable ID rather than colliding with the
                    # partial one already pushed.
                    immutable_id="scanner-set-$(date -u +%G.%V).${RUN_ATTEMPT:-1}"
                    validate_release_id "$immutable_id"
                    aliases="candidate,stable,latest"
                    ;;
                *)
                    die "unrecognized scanner factory schedule"
                    ;;
            esac
            ;;
        push)
            operation="candidate"
            run_discovery=true
            run_validation=true
            run_build=true
            publish=false
            immutable_id="scanner-candidate-main-${short_sha}-${run_suffix}"
            ;;
        pull_request)
            operation="validate"
            run_validation=true
            run_build=true
            immutable_id="scanner-candidate-pr-${ref_slug}-${short_sha}"
            ;;
        release)
            [[ -n "${RELEASE_TAG_NAME:-}" ]] || die "release event is missing a tag name"
            local app_version="${RELEASE_TAG_NAME#v}"
            [[ "$app_version" =~ ^[0-9]+(\.[0-9]+){1,2}([._-][0-9A-Za-z.-]+)?$ ]] ||
                die "application release tag is not semver-like"
            operation="legacy-release"
            run_discovery=true
            run_validation=true
            run_build=true
            publish=true
            immutable_id="scanner-candidate-app-${app_version}-${short_sha}-${run_suffix}"
            aliases="$(legacy_aliases "$app_version")"
            ;;
        workflow_dispatch)
            operation="${input_operation:-discover}"
            case "$operation" in
                validate)
                    run_validation=true
                    ;;
                discover)
                    run_discovery=true
                    ;;
                candidate)
                    [[ "${REF_NAME:-}" == main ]] ||
                        die "managed candidate dispatches must run from protected main"
                    run_discovery=true
                    run_validation=true
                    run_build=true
                    publish="$(parse_bool "${INPUT_PUBLISH:-true}")"
                    immutable_id="${INPUT_CANDIDATE_ID:-scanner-candidate-manual-${short_sha}-${run_suffix}}"
                    validate_candidate_id "$immutable_id"
					[[ "$channel" == "none" || "$channel" == "candidate" ]] ||
						die "candidate operations may move only the candidate channel"
                    [[ "$channel" == "none" ]] || aliases="$channel"
                    ;;
                security-rebuild)
                    [[ "${REF_NAME:-}" == main ]] ||
                        die "managed security rebuilds must run from protected main"
                    run_discovery=true
                    run_validation=true
                    run_build=true
                    publish="$(parse_bool "${INPUT_PUBLISH:-true}")"
                    immutable_id="${INPUT_CANDIDATE_ID:-scanner-candidate-security-${short_sha}-${run_suffix}}"
                    validate_candidate_id "$immutable_id"
					[[ "$channel" == "none" || "$channel" == "candidate" ]] ||
						die "security-rebuild operations may move only the candidate channel"
                    [[ "$channel" == "none" ]] || aliases="$channel"
                    ;;
                release)
                    [[ "${REF_NAME:-}" == main ]] ||
                        die "managed releases must run from protected main"
                    publish=true
                    candidate_id="${INPUT_CANDIDATE_ID:-}"
                    validate_candidate_id "$candidate_id"
                    immutable_id="${INPUT_RELEASE_ID:-}"
                    validate_release_id "$immutable_id"
					[[ "$channel" == "none" || "$channel" == "stable" ]] ||
						die "release operations may move only the stable channel"
                    # `latest` is an alias of `stable`, never of `candidate`:
                    # an unqualified pull must land on the approved set.
                    [[ "$channel" == "none" ]] || aliases="stable,latest"
                    if [[ -n "$scanner_version" ]]; then
                        [[ "$channel" == "stable" ]] ||
                            die "scanner_version may only be applied to a stable release"
                        validate_scanner_version "$scanner_version"
                        aliases+=",$(version_aliases "$scanner_version")"
                    fi
                    ;;
                verify)
                    immutable_id="${INPUT_RELEASE_ID:-}"
                    validate_release_id "$immutable_id"
                    ;;
                refresh-os-packages)
                    [[ -n "$os_package_snapshot" ]] ||
                        die "refresh-os-packages requires os_package_snapshot"
                    validate_os_package_snapshot "$os_package_snapshot"
                    run_os_package_refresh=true
                    ;;
				refresh-vulnerability-dbs)
					run_vulnerability_db_refresh=true
					;;
                *)
					die "operation must be validate, discover, candidate, security-rebuild, release, verify, refresh-os-packages, or refresh-vulnerability-dbs"
                    ;;
            esac
            ;;
        *)
            die "unsupported event: ${event_name:-empty}"
            ;;
    esac

    if [[ "$publish" == "true" && "$event_name" == "pull_request" ]]; then
        die "pull requests may not publish scanner artifacts"
    fi
	if [[ "$publish" != "true" && -n "$aliases" ]]; then
		die "a channel move requires publication"
	fi
	# `stable` — and therefore `latest` — may move from exactly two operations:
	# the human-driven, approval-gated `release`, and the unattended weekly
	# `scheduled-release`. Every other operation, including manual `candidate`
	# and `security-rebuild` dispatches, is still refused.
	if [[ ",$aliases," == *,stable,* &&
		"$operation" != "release" && "$operation" != "scheduled-release" ]]; then
		die "the stable channel requires the release or scheduled-release operation"
	fi
	# `latest` must never point somewhere `stable` does not. The app-release
	# path is the one exception: it tags Wolf-versioned compatibility aliases.
	if [[ ",$aliases," == *,latest,* && ",$aliases," != *,stable,* &&
		"$operation" != "legacy-release" ]]; then
		die "latest may only move together with stable"
	fi
    if [[ "$run_build" == "true" && -z "$immutable_id" ]]; then
        die "build operation did not resolve an immutable identifier"
    fi
    if [[ -n "$immutable_id" && ! "$immutable_id" =~ ^[a-z0-9][a-z0-9._-]{0,126}$ ]]; then
        die "resolved immutable ID is not an OCI-safe lower-case tag"
    fi
    local alias
    IFS=, read -ra resolved_aliases <<<"$aliases"
    for alias in "${resolved_aliases[@]}"; do
        [[ -z "$alias" || "$alias" =~ ^[a-z0-9][a-z0-9._-]{0,126}$ ]] ||
            die "resolved alias is not an OCI-safe lower-case tag: $alias"
    done

    emit operation "$operation"
    emit run_discovery "$run_discovery"
    emit run_validation "$run_validation"
    emit run_build "$run_build"
    emit run_os_package_refresh "$run_os_package_refresh"
	emit run_vulnerability_db_refresh "$run_vulnerability_db_refresh"
    emit publish "$publish"
    emit immutable_id "$immutable_id"
    emit candidate_id "$candidate_id"
    emit aliases "$aliases"
    emit mirror_mode "$mirror_mode"
    emit os_package_snapshot "$os_package_snapshot"
}

parse_bool() {
    case "$1" in
        true) printf 'true' ;;
        false) printf 'false' ;;
        *) die "boolean input must be true or false" ;;
    esac
}

# Expand a version into its rolling family: 2.1.0 -> "2.1.0,2.1,2". A
# pre-release or otherwise non-numeric version expands to itself only, so
# "2.1.0-rc1" never captures the plain "2.1" / "2" tags.
version_aliases() {
    local version="$1"
    local major minor patch rest
    IFS=. read -r major minor patch rest <<<"$version"
    local aliases="$version"
    if [[ -n "${patch:-}" && -z "${rest:-}" && "$patch" =~ ^[0-9]+$ ]]; then
        aliases+=",${major}.${minor},${major}"
    fi
    printf '%s' "$aliases"
}

# App-release (v*.*.*) compatibility tags: the version family plus `latest`.
# Shape is unchanged from before version_aliases was factored out.
legacy_aliases() {
    printf '%s,latest' "$(version_aliases "$1")"
}

tags() {
    local primary="${PRIMARY_REPOSITORY:-}"
    local mirror="${MIRROR_REPOSITORY:-}"
    local immutable_id="${IMMUTABLE_ID:-}"
    local image_name="${IMAGE_NAME:-}"
    local aliases="${ALIASES:-}"
    local run_id="${RUN_ID:-}"
    local run_attempt="${RUN_ATTEMPT:-}"

    [[ "$primary" =~ ^[a-z0-9.-]+(/[a-z0-9._-]+)+$ ]] || die "invalid primary repository"
    [[ "$mirror" =~ ^[a-z0-9.-]+(/[a-z0-9._-]+)+$ ]] || die "invalid mirror repository"
    [[ "$image_name" =~ ^wolf-(scanners|fixer)(-[a-z0-9]+)?$ ]] || die "invalid image name"
    [[ "$immutable_id" =~ ^[a-z0-9][a-z0-9._-]{0,126}$ ]] || die "invalid immutable tag"
    [[ "$run_id" =~ ^[0-9]+$ ]] || die "RUN_ID must be numeric"
    [[ "$run_attempt" =~ ^[0-9]+$ ]] || die "RUN_ATTEMPT must be numeric"

    local staging="build-${immutable_id}-${run_id}-${run_attempt}"
    [[ ${#staging} -le 128 ]] || die "staging tag exceeds the OCI tag length limit"

    local alias
    IFS=, read -ra alias_values <<<"$aliases"
    for alias in "${alias_values[@]}"; do
        [[ -z "$alias" || "$alias" =~ ^[a-z0-9][a-z0-9._-]{0,126}$ ]] ||
            die "invalid channel/compatibility alias: $alias"
        [[ "$alias" != "$immutable_id" ]] || die "immutable tag cannot also be a channel alias"
    done

    emit primary_repository "${primary}/${image_name}"
    emit mirror_repository "${mirror}/${image_name}"
    emit staging_tag "$staging"
    emit staging_ref "${primary}/${image_name}:${staging}"
    emit immutable_ref "${primary}/${image_name}:${immutable_id}"
    emit mirror_immutable_ref "${mirror}/${image_name}:${immutable_id}"
}

case "${1:-}" in
    resolve) resolve ;;
    tags) tags ;;
    *) die "usage: release-meta.sh resolve|tags" ;;
esac
