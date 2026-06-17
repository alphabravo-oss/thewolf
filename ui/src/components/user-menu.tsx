// Top-right account menu: avatar + name trigger with a dropdown to the
// personal surfaces (Account, two-factor, API keys) and sign-out.
import { useNavigate } from "@tanstack/react-router";
import {
  ChevronDownIcon,
  KeyRoundIcon,
  LockIcon,
  LogOutIcon,
  UserIcon,
} from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { api, clearToken } from "@/lib/api";
import { useMe, displayLabel } from "@/lib/me";

export function UserMenu() {
  const navigate = useNavigate();
  const me = useMe();

  async function handleLogout() {
    try {
      await api.post("/auth/logout");
    } catch {
      // best-effort; drop the local cookie regardless
    }
    clearToken();
    navigate({ to: "/login" });
  }

  if (!me.data) return null;
  const label = displayLabel(me.data);
  const initial = label.charAt(0).toUpperCase();

  const go = (tab: "account" | "security" | "apikeys") =>
    navigate({ to: "/settings", search: { tab } });

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="inline-flex items-center gap-2 h-9 pl-1.5 pr-2 rounded-md hover:bg-muted/50 outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          aria-label="Account menu"
        >
          <span className="grid size-7 shrink-0 place-items-center rounded-full bg-primary/15 text-primary text-xs font-semibold">
            {initial}
          </span>
          <span className="hidden sm:block max-w-[10rem] truncate text-sm">{label}</span>
          <ChevronDownIcon className="size-4 text-muted-foreground" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel className="flex flex-col gap-0.5">
          <span className="truncate">{me.data.display_name?.trim() || "Signed in"}</span>
          <span className="text-xs font-normal text-muted-foreground truncate">
            {me.data.email}
          </span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => go("account")}>
          <UserIcon className="mr-2 size-4" /> Account
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => go("security")}>
          <LockIcon className="mr-2 size-4" /> Two-factor auth
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => go("apikeys")}>
          <KeyRoundIcon className="mr-2 size-4" /> API keys
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={handleLogout} className="text-red-400 focus:text-red-400">
          <LogOutIcon className="mr-2 size-4" /> Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
