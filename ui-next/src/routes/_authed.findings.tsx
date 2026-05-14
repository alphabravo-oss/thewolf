// Layout shell for /findings and /findings/$findingId. The list lives
// in _authed.findings.index.tsx; TanStack Router nests $findingId as a
// child of this file, so we must render <Outlet /> for the child to
// display.
import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/findings")({
  component: FindingsLayout,
});

function FindingsLayout() {
  return <Outlet />;
}
