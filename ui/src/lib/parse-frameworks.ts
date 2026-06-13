// Frameworks come from the API as a JSON-encoded array stored in
// repo.detected_frameworks. Some legacy rows are comma-separated plain
// strings — handle both, fall back to [] on garbage.
export function parseFrameworks(raw: string | null | undefined): string[] {
  if (!raw) return [];
  const trimmed = raw.trim();
  if (!trimmed) return [];

  if (trimmed.startsWith("[")) {
    try {
      const parsed = JSON.parse(trimmed);
      if (Array.isArray(parsed)) {
        return parsed.filter((v): v is string => typeof v === "string");
      }
    } catch {
      // fall through to the comma-split path
    }
  }

  return trimmed.split(",").map((s) => s.trim()).filter(Boolean);
}
