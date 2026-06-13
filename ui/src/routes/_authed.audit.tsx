// ui-next/src/routes/_authed.audit.tsx
import { Outlet, createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/audit")({
  component: () => <Outlet />,
});
