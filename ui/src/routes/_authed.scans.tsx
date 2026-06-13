// Layout shell for /scans and /scans/$scanId. The list lives in
// _authed.scans.index.tsx; TanStack Router nests $scanId as a child of
// this file, so we must render <Outlet /> for the child to display.
import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/scans")({
  component: ScansLayout,
});

function ScansLayout() {
  return <Outlet />;
}
