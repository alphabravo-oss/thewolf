"use client";

import { SidebarTrigger } from "@/components/ui/sidebar";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { useTheme } from "@/components/theme-provider";
import { useAuthStore } from "@/lib/store";
import { clearToken } from "@/lib/api";
import { useRouter } from "next/navigation";

export function AppHeader() {
  const { theme, setTheme } = useTheme();
  const { user, logout } = useAuthStore();
  const router = useRouter();

  const handleLogout = () => {
    clearToken();
    logout();
    router.push("/login");
  };

  const initials = user?.email
    ? user.email.substring(0, 2).toUpperCase()
    : "WF";

  return (
    <header className="flex h-14 items-center gap-4 border-b px-4">
      <SidebarTrigger />
      <div className="flex-1" />
      <Button
        variant="ghost"
        size="sm"
        aria-label={`Switch theme, currently ${theme}`}
        onClick={() =>
          setTheme(
            theme === "dark"
              ? "light"
              : theme === "light"
                ? "system"
                : "dark",
          )
        }
      >
        {theme === "dark" ? "Dark" : theme === "light" ? "Light" : "System"}
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="default" className="rounded-full p-1" aria-label="User menu">
            <Avatar className="h-8 w-8">
              <AvatarFallback>{initials}</AvatarFallback>
            </Avatar>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {user && (
            <>
              <DropdownMenuItem disabled>{user.email}</DropdownMenuItem>
              <DropdownMenuSeparator />
            </>
          )}
          <DropdownMenuItem onClick={() => router.push("/settings")}>
            Settings
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={handleLogout}>Log out</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </header>
  );
}
