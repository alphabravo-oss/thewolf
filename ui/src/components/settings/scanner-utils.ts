// Pure helpers for classifying scanner images; the Scanners and Images sections share them.

/**
 * Classify the tag on the resolved scanner image into the release channel it
 * represents, so an operator can tell at a glance whether this deployment
 * tracks a moving channel or is pinned to an exact set.
 *
 * The channels are produced by the scanner release factory
 * (.github/workflows/scanners-image.yml); `stable`/`latest` move only on an
 * approved or scheduled release, `candidate` on every gated build, and
 * `scanner-set-YYYY.WW.N` never moves.
 */
export function describeScannerChannel(image: string): {
  label: string;
  detail: string;
  tone: string;
} {
  // Strip the registry host before looking for the tag separator, or the
  // colon in "host:port" would be mistaken for one.
  const lastSlash = image.lastIndexOf("/");
  const namePart = lastSlash === -1 ? image : image.slice(lastSlash + 1);
  const atDigest = namePart.indexOf("@");
  if (atDigest !== -1) {
    return {
      label: "Pinned digest",
      detail: "Immutable — never moves.",
      tone: "completed",
    };
  }
  const colon = namePart.lastIndexOf(":");
  const tag = colon === -1 ? "" : namePart.slice(colon + 1);

  switch (tag) {
    case "":
      return {
        label: "Unpinned",
        detail: "No tag resolved; the registry default applies.",
        tone: "warning",
      };
    case "stable":
    case "latest":
      return {
        label: tag,
        detail: "Approved release set. Updates on each scanner release.",
        tone: "completed",
      };
    case "candidate":
      return {
        label: "candidate",
        detail: "Newest gated build, not yet promoted to stable.",
        tone: "running",
      };
    default:
      if (/^scanner-set-\d{4}\.\d{2}\.\d+$/.test(tag)) {
        return {
          label: tag,
          detail: "Pinned to an exact release set — never moves.",
          tone: "completed",
        };
      }
      return {
        label: tag,
        detail: "Custom or locally built tag.",
        tone: "info",
      };
  }
}

/**
 * Is this one of the images Wolf builds and releases itself?
 *
 * Matches on the repository path rather than the registry host, so a mirror
 * or a private re-host of the same images still classifies correctly — an
 * operator pointing WOLF_SCANNERS_IMAGE at their own registry should still
 * see those under "built by Wolf", not lumped in with third-party tools.
 */
export function isWolfBuiltImage(image: string): boolean {
  const name = (image.split("@")[0].split("/").pop() ?? "").split(":")[0];
  return name.startsWith("wolf-scanners") || name.startsWith("wolf-fixer");
}
