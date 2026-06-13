// Layout shell for /scans/$scanId and /scans/$scanId/live. The static
// detail view lives in _authed.scans.$scanId.index.tsx; TanStack
// Router nests $scanId/live as a child of this file, so we render
// <Outlet /> to let the child show.
import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/scans/$scanId")({
  component: ScanDetailLayout,
});

function ScanDetailLayout() {
  return <Outlet />;
}
