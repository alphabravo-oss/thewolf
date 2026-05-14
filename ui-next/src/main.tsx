import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createRouter,
} from "@tanstack/react-router";
import { Toaster } from "sonner";

import { routeTree } from "./routeTree.gen";
import "./styles/globals.css";

// One QueryClient for the entire app; persists across route changes.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Wolf APIs are mostly slow-ish (DB + sometimes AI), so cache
      // aggressively and refetch on focus only when stale.
      staleTime: 30_000,
      gcTime: 5 * 60_000,
      refetchOnWindowFocus: false,
      retry: 1,
    },
  },
});

// The type-safe router instance. The route tree is generated at build time
// by TanStackRouterVite from files in src/routes/.
const router = createRouter({
  routeTree,
  defaultPreload: "intent",
  context: {
    queryClient,
  },
  // SPA mode — no SSR, but TanStack Router handles deep links cleanly.
  defaultPendingMinMs: 100,
  defaultPendingMs: 250,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

const rootEl = document.getElementById("root")!;
createRoot(rootEl).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <Toaster
        position="bottom-right"
        theme="dark"
        toastOptions={{
          className: "glass-card",
        }}
      />
    </QueryClientProvider>
  </StrictMode>,
);
