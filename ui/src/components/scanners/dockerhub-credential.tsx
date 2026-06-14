// DockerHub credential card for the Scanner Images page.
//
// Stores a DockerHub username + Personal Access Token as a `dockerhub_token`
// secret (key_type=dockerhub_token, key_name=<username>, value=<PAT>). The PAT
// is the only thing that gates *publishing* images — local builds (`--load`)
// never need it — so the card is labelled clearly OPTIONAL.
//
// Configured / not-configured state comes from GET /config/secrets filtered to
// the dockerhub_token type (same query key the rest of the UI uses, so saving
// here invalidates and other consumers refresh).
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2Icon, Loader2Icon, ShipIcon } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";

// The wire shape GET /config/secrets actually returns (masked value). The
// shared Secret type in lib/types.ts expects a `masked_value` field the API
// doesn't send, so we declare the real shape locally.
interface MaskedSecret {
  id: string;
  key_type: string;
  key_name: string;
  value: string;
  created_at: string;
}

export function DockerHubCredentialCard() {
  const qc = useQueryClient();
  const [username, setUsername] = useState("");
  const [pat, setPat] = useState("");

  const secretsQ = useQuery({
    queryKey: ["config", "secrets", "all"],
    queryFn: async () => {
      const r = await api.get<MaskedSecret[]>("/config/secrets");
      return r.data ?? [];
    },
  });

  const dockerhubSecret = useMemo(
    () =>
      (secretsQ.data ?? []).find((s) => s.key_type === "dockerhub_token") ??
      null,
    [secretsQ.data],
  );
  const configured = dockerhubSecret !== null;

  const saveMut = useMutation({
    mutationFn: async () => {
      const r = await api.post<MaskedSecret>("/config/secrets", {
        key_type: "dockerhub_token",
        key_name: username.trim(),
        value: pat,
      });
      return r.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["config", "secrets"] });
      setPat("");
      toast.success("DockerHub credential saved");
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Failed to save credential");
    },
  });

  const canSave =
    username.trim().length > 0 && pat.length > 0 && !saveMut.isPending;

  return (
    <div className="panel space-y-4">
      <div className="flex items-start justify-between gap-3">
        <div className="space-y-1">
          <div className="flex items-center gap-2">
            <ShipIcon className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-semibold">DockerHub credential</h3>
            <span className="chip">Optional</span>
          </div>
          <p className="text-xs text-muted-foreground max-w-prose">
            Only needed to <strong>publish</strong> images. Local rebuilds load
            straight into the Docker daemon and never require credentials.
          </p>
        </div>
        {configured ? (
          <span className="chip" style={{ color: "hsl(150 56% 52%)" }}>
            <CheckCircle2Icon className="size-3.5" /> Configured
          </span>
        ) : (
          <span className="chip">Not configured</span>
        )}
      </div>

      {configured && dockerhubSecret && (
        <div className="text-xs text-muted-foreground">
          Publishing as{" "}
          <span className="mono text-foreground">
            {dockerhubSecret.key_name}
          </span>{" "}
          (token <span className="mono">{dockerhubSecret.value}</span>). Saving
          again replaces it.
        </div>
      )}

      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (canSave) saveMut.mutate();
        }}
      >
        <label className="block space-y-1">
          <span className="text-xs text-muted-foreground">
            DockerHub username
          </span>
          <input
            type="text"
            name="dockerhub-username"
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="alphabravodevops"
            className="w-full h-9 px-3 rounded-md bg-muted/30 border border-border focus:border-ring focus:bg-muted/50 outline-none text-sm"
          />
        </label>
        <label className="block space-y-1">
          <span className="text-xs text-muted-foreground">
            Personal Access Token (PAT)
          </span>
          <input
            type="password"
            name="dockerhub-pat"
            autoComplete="new-password"
            value={pat}
            onChange={(e) => setPat(e.target.value)}
            placeholder="dckr_pat_…"
            className="w-full h-9 px-3 rounded-md bg-muted/30 border border-border focus:border-ring focus:bg-muted/50 outline-none text-sm"
          />
          <span className="text-[11px] text-muted-foreground">
            Create a PAT with Read &amp; Write scope at
            hub.docker.com/settings/security. Stored encrypted; never logged.
          </span>
        </label>
        <button type="submit" className="btn btn-primary" disabled={!canSave}>
          {saveMut.isPending ? (
            <>
              <Loader2Icon className="animate-spin" /> Saving…
            </>
          ) : configured ? (
            "Replace credential"
          ) : (
            "Save credential"
          )}
        </button>
      </form>
    </div>
  );
}
