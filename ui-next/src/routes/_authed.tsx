// Pathless layout route. Every child route under `_authed/` renders inside
// the AppShell (sidebar + topbar). The `beforeLoad` redirects to /login if
// no token is present.
import { createFileRoute, redirect } from "@tanstack/react-router";
import { AppShell } from "@/components/app-shell";
import { getToken } from "@/lib/api";

export const Route = createFileRoute("/_authed")({
  beforeLoad: ({ location }) => {
    if (!getToken()) {
      throw redirect({
        to: "/login",
        search: { from: location.pathname },
      });
    }
  },
  component: AppShell,
});
