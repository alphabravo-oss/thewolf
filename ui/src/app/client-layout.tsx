"use client";

import { config } from "@fortawesome/fontawesome-svg-core";
import "@fortawesome/fontawesome-svg-core/styles.css";
config.autoAddCss = false;

import { usePathname } from "next/navigation";
import { SidebarProvider, SidebarInset } from "@/components/ui/sidebar";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ThemeProvider } from "@/components/theme-provider";
import { AppSidebar } from "@/components/app-sidebar";
import { AppHeader } from "@/components/app-header";
import { Toaster } from "@/components/ui/sonner";
import { ErrorBoundary } from "@/components/error-boundary";
import { AuthGuard } from "@/components/auth-guard";
import { QueryProvider } from "@/lib/query-provider";

const authPages = ["/login", "/register"];

export function ClientLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const isAuthPage = authPages.includes(pathname);

  if (isAuthPage) {
    return (
      <ThemeProvider>
        <QueryProvider>
          <TooltipProvider>
            <ErrorBoundary>
              {children}
            </ErrorBoundary>
            <Toaster />
          </TooltipProvider>
        </QueryProvider>
      </ThemeProvider>
    );
  }

  return (
    <ThemeProvider>
      <QueryProvider>
        <TooltipProvider>
          <AuthGuard>
            <SidebarProvider>
              <AppSidebar />
              <SidebarInset>
                <AppHeader />
                <main className="flex-1 overflow-x-hidden p-4 sm:p-6">
                  <ErrorBoundary>
                    {children}
                  </ErrorBoundary>
                </main>
              </SidebarInset>
            </SidebarProvider>
          </AuthGuard>
          <Toaster />
        </TooltipProvider>
      </QueryProvider>
    </ThemeProvider>
  );
}
