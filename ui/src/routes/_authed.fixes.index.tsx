import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/fixes/")({
  beforeLoad: () => {
    throw redirect({ to: "/agents" });
  },
});
