import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/vulnerabilities")({
  component: VulnerabilitiesLayout,
});

function VulnerabilitiesLayout() {
  return <Outlet />;
}
