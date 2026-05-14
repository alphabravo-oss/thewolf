import {
  Outlet,
  createRootRouteWithContext,
} from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import { Suspense, lazy } from "react";

// Lazy-load the devtools so they don't bloat the production bundle.
const TanStackRouterDevtools =
  import.meta.env.PROD
    ? () => null
    : lazy(() =>
        import("@tanstack/router-devtools").then((m) => ({
          default: m.TanStackRouterDevtools,
        })),
      );

// The router-context exposes the QueryClient to every loader so they
// can prime caches before a route renders. Plain `Outlet` mounts the
// matched child route.
export const Route = createRootRouteWithContext<{
  queryClient: QueryClient;
}>()({
  component: RootComponent,
});

function RootComponent() {
  return (
    <>
      <Outlet />
      <Suspense fallback={null}>
        <TanStackRouterDevtools position="bottom-right" />
      </Suspense>
    </>
  );
}
