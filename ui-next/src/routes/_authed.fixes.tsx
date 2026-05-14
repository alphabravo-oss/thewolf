// Layout shell for /fixes and /fixes/$fixId. The list lives in
// _authed.fixes.index.tsx; TanStack Router nests $fixId as a child of
// this file, so we must render <Outlet /> for the child to display.
import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/fixes")({
  component: FixesLayout,
});

function FixesLayout() {
  return <Outlet />;
}
