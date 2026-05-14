// Layout shell for /repos and /repos/$repoId.
import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/repos")({
  component: ReposLayout,
});

function ReposLayout() {
  return <Outlet />;
}
