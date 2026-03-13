"use client";

import { useEffect, useState } from "react";
import { useRouter, usePathname } from "next/navigation";
import { useAuthStore } from "@/lib/store";
import api from "@/lib/api";
import type { User } from "@/lib/types";
import { LoadingSpinner } from "@/components/loading-spinner";

const publicPaths = ["/login", "/register"];

export function AuthGuard({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const { user, setAuth, token } = useAuthStore();
  const [checking, setChecking] = useState(true);

  useEffect(() => {
    // Skip auth check on public pages
    if (publicPaths.includes(pathname)) {
      setChecking(false);
      return;
    }

    // If we already have user state, we're good
    if (user && token) {
      setChecking(false);
      return;
    }

    // Check if there's a cookie token and try to validate it
    api
      .get<User>("/auth/me")
      .then((res) => {
        // Token is valid — restore auth state
        const cookieToken = document.cookie
          .match(/(?:^|;\s*)wolf_token=([^;]*)/)
          ?.[1];
        if (cookieToken && res.data) {
          setAuth(res.data, decodeURIComponent(cookieToken));
        }
        setChecking(false);
      })
      .catch(() => {
        // No valid session — redirect to login
        router.replace("/login");
      });
  }, [pathname, user, token, setAuth, router]);

  if (publicPaths.includes(pathname)) {
    return <>{children}</>;
  }

  if (checking) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <LoadingSpinner />
      </div>
    );
  }

  return <>{children}</>;
}
