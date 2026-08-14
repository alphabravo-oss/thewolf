// /fixes is retired — Agents is the public surface. Keep the route file so
// TanStack still owns /fixes/$fixId and can redirect it.
import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/fixes")({
  component: FixesLayout,
});

function FixesLayout() {
  return <Outlet />;
}
