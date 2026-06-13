// Layout shell for /loops and /loops/$loopId. The list lives in
// _authed.loops.index.tsx; TanStack Router nests $loopId as a child of
// this file, so we must render <Outlet /> for the child to display.
import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/loops")({
  component: LoopsLayout,
});

function LoopsLayout() {
  return <Outlet />;
}
