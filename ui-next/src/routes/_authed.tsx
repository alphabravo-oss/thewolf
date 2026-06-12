// Pathless layout route. Every child route under `_authed/` renders inside
// the AppShell (sidebar + topbar). The `beforeLoad` redirects to /login if
// no token is present.
import { createFileRoute, redirect } from "@tanstack/react-router";
import { AppShell } from "@/components/app-shell";
import { hasSession } from "@/lib/api";

export const Route = createFileRoute("/_authed")({
  beforeLoad: async ({ location }) => {
    if (!(await hasSession())) {
      throw redirect({
        to: "/login",
        search: { from: location.pathname },
      });
    }
  },
  component: AppShell,
});
