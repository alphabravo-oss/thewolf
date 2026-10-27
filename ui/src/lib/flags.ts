// Shared feature-flag hooks. The server exposes flags as settings rows
// (GET /settings). The whole nav and several surfaces gate on these, so they
// live in one place and share the ["settings"] query cache.
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { entitled, REMEDIATION, useEdition } from "./edition";

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

// Flags that also need an edition capability. Autonomous fixing is Enterprise.
const FLAG_CAPABILITY: Record<string, string> = { autofix_enabled: REMEDIATION };

// useFlag returns whether a boolean setting is "true" (and, for Enterprise
// features, that the edition grants it). It defaults to false while loading and
// on error, so a gated surface stays hidden until the flag is confirmed on (no
// flash of an off-by-default feature).
export function useFlag(key: string) {
  const q = useSettingsMap();
  const edition = useEdition();
  const capability = FLAG_CAPABILITY[key];
  const gated = capability !== undefined;
  return {
    enabled: !q.isError && q.data?.[key] === "true" && (!gated || entitled(edition.data, capability)),
    isLoading: q.isLoading || (gated && edition.isLoading),
    isError: q.isError || (gated && edition.isError),
    refetch: () => { void q.refetch(); if (gated) void edition.refetch(); },
  };
}
