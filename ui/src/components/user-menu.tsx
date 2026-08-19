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

  const go = (section: "profile" | "security" | "apikeys") =>
    navigate({ to: "/account", search: { section } });

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        {/* Compact avatar + chevron only. The signed-in name is not repeated
            in the bar — it lives in the dropdown header, which keeps the right
            side of the topbar a fixed width regardless of how long a display
            name is. */}
        <button
          type="button"
          className="flex items-center gap-2 h-8 pl-1 pr-2 rounded-md hover:bg-accent transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          aria-label="Account menu"
        >
          <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-zinc-600 to-zinc-800">
            <UserIcon className="h-3 w-3 text-zinc-300" />
          </span>
          <ChevronDownIcon className="h-3 w-3 text-muted-foreground" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel className="flex flex-col gap-0.5">
          <span className="truncate">{displayLabel(me.data)}</span>
          <span className="text-xs font-normal text-muted-foreground truncate">
            {me.data.email}
          </span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => go("profile")}>
          <UserIcon className="mr-2 size-4" /> Account
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => go("security")}>
          <LockIcon className="mr-2 size-4" /> Two-factor auth
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => go("apikeys")}>
          <KeyRoundIcon className="mr-2 size-4" /> API keys
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={handleLogout} className="text-status-error focus:text-status-error">
          <LogOutIcon className="mr-2 size-4" /> Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
