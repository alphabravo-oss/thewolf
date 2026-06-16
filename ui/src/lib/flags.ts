// Shared feature-flag hooks. The server exposes flags as settings rows
// (GET /settings). The whole nav and several surfaces gate on these, so they
// live in one place and share the ["settings"] query cache.
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

type SettingRow = { key: string; value: string };

// useSettingsMap reads GET /settings (an array of {key,value} or a flat map)
// into a single string map.
export function useSettingsMap() {
  return useQuery({
    queryKey: ["settings"],
    queryFn: async () => {
      const r = await api.get<SettingRow[] | Record<string, string>>("/settings");
      const out: Record<string, string> = {};
      if (Array.isArray(r.data)) {
        for (const row of r.data) out[row.key] = row.value;
      } else if (r.data && typeof r.data === "object") {
        for (const [k, v] of Object.entries(r.data)) out[k] = String(v);
      }
      return out;
    },
  });
}

// useFlag returns whether a boolean setting is "true". It defaults to false
// while loading and on error, so a gated surface stays hidden until the flag is
// confirmed on (no flash of an off-by-default feature).
export function useFlag(key: string) {
  const q = useSettingsMap();
  return { enabled: q.data?.[key] === "true", isLoading: q.isLoading };
}
