import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createRouter,
} from "@tanstack/react-router";
import { Toaster } from "@/components/ui/sonner";
import { ThemeProvider } from "next-themes";

import { routeTree } from "./routeTree.gen";

// Self-hosted variable fonts (bundled into dist → fully offline, no CDN).
// Hanken Grotesk for UI, JetBrains Mono for data/IDs/paths.
import "@fontsource-variable/hanken-grotesk";
import "@fontsource-variable/jetbrains-mono";

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
    {/* next-themes manages the `dark`/`light` class on <html> + localStorage.
        defaultTheme dark keeps the Nocturne dark look as the default; the
        sidebar toggle flips it. enableSystem lets it follow the OS the very
        first time if the user hasn't chosen yet. */}
    <ThemeProvider
      attribute="class"
      defaultTheme="dark"
      enableSystem
      disableTransitionOnChange
    >
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
        <Toaster position="bottom-right" toastOptions={{ className: "glass-card" }} />
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
