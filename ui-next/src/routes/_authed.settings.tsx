// Settings — Phase 1 stub. The full settings panel (api keys, git creds,
// AI providers, scan presets) ports in Phase 2.
import { createFileRoute } from "@tanstack/react-router";
import { SettingsIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";

export const Route = createFileRoute("/_authed/settings")({
  component: SettingsPage,
});

function SettingsPage() {
  return (
    <div className="p-6 space-y-4 max-w-3xl">
      <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
      <EmptyState
        icon={SettingsIcon}
        title="Coming soon"
        description="The full settings panel (API keys, git credentials, AI providers, scan presets) is ported in Phase 2 of the UI rewrite."
      />
    </div>
  );
}
