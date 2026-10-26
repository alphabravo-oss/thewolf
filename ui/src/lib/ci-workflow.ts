// The hosted runner already provides curl and jq. Credentials stay in Actions
// secrets; the scan is pinned to the PR head, including on forked PR events.
export function ciSnippet(repoId: string) {
  return `name: Wolf
on: [push, pull_request]
permissions:
  contents: read
jobs:
  scan:
    runs-on: ubuntu-latest
    timeout-minutes: 35
    env:
      WOLF_URL: \${{ secrets.WOLF_URL }}
      WOLF_TOKEN: \${{ secrets.WOLF_TOKEN }}
      WOLF_REPO_ID: ${JSON.stringify(repoId)}
      WOLF_REVISION: \${{ github.event.pull_request.head.sha || github.sha }}
      WOLF_REQUEST_KEY: \${{ github.repository }}:\${{ github.run_id }}:\${{ github.run_attempt }}
    steps:
      - name: Scan this revision and enforce the gate
        shell: bash
        run: |
${ciScript.split("\n").map((line) => `          ${line}`).join("\n")}
`;
}

export const ciScript = `set -euo pipefail
: "\${WOLF_URL:?Set the WOLF_URL Actions secret}"
: "\${WOLF_TOKEN:?Set the WOLF_TOKEN Actions secret}"
api() {
  curl --fail-with-body --silent --show-error --connect-timeout 10 --max-time 60 \\
    -H "Authorization: Bearer $WOLF_TOKEN" -H "Content-Type: application/json" "$@"
}
request=$(jq -n --arg repo "$WOLF_REPO_ID" --arg revision "$WOLF_REVISION" \\
  --arg reference "$WOLF_REQUEST_KEY" \\
  '{repo_id:$repo, branch:$revision, profile:"pr", client_reference:$reference}')
created=$(api -X POST "\${WOLF_URL%/}/api/v1/scans" \\
  -H "Idempotency-Key: $WOLF_REQUEST_KEY" -d "$request")
scan_id=$(jq -er '.data.id // .id' <<< "$created")
[[ "$scan_id" =~ ^[a-zA-Z0-9_-]+$ ]] || { echo "Invalid scan ID" >&2; exit 1; }
deadline=$((SECONDS + 1800))
while (( SECONDS < deadline )); do
  scan=$(api "\${WOLF_URL%/}/api/v1/scans/$scan_id")
  status=$(jq -er '.data.status' <<< "$scan")
  case "$status" in
    completed) break ;;
    pending|running) sleep 2 ;;
    *) echo "Scan $scan_id ended with status $status" >&2; exit 1 ;;
  esac
done
[[ "$status" == completed ]] || { echo "Scan timed out" >&2; exit 1; }
[[ "$(jq -r '.data.commit_sha' <<< "$scan")" == "$WOLF_REVISION" ]] || \\
  { echo "Scan revision does not match this commit" >&2; exit 1; }
gate=$(api "\${WOLF_URL%/}/api/v1/scans/$scan_id/gate")
jq . <<< "$gate"
case "$(jq -er '.data.evaluation.status' <<< "$gate")" in
  pass|warn) exit 0 ;;
  *) exit 1 ;;
esac`;
