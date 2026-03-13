"use client";

import { useRef, useCallback } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { WolfLogo } from "@/components/wolf-logo";
import {
  LayoutDashboardIcon,
  PackageIcon,
  BugIcon,
  PawPrintIcon,
  SettingsIcon,
} from "lucide-react";
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarFooter,
  useSidebar,
} from "@/components/ui/sidebar";

const navItems = [
  { title: "Dashboard", href: "/", icon: LayoutDashboardIcon },
  { title: "Collections", href: "/collections", icon: PackageIcon },
  { title: "Findings", href: "/findings", icon: BugIcon },
  { title: "Wolf", href: "/wolf", icon: PawPrintIcon },
];

const bottomItems = [
  { title: "Settings", href: "/settings", icon: SettingsIcon },
];

export function AppSidebar() {
  const pathname = usePathname();
  const { state, setOpen } = useSidebar();
  const hoverIntentRef = useRef(false);

  function isActive(href: string) {
    if (href === "/") return pathname === "/";
    if (href === "/wolf") {
      return pathname.startsWith("/wolf") || pathname.startsWith("/fixes") || pathname.startsWith("/loops");
    }
    return pathname.startsWith(href);
  }

  const handleMouseEnter = useCallback(() => {
    if (state === "collapsed") {
      hoverIntentRef.current = true;
      setOpen(true);
    }
  }, [state, setOpen]);

  const handleMouseLeave = useCallback(() => {
    if (hoverIntentRef.current) {
      hoverIntentRef.current = false;
      setOpen(false);
    }
  }, [setOpen]);

  return (
    <Sidebar
      collapsible="icon"
      onMouseEnter={handleMouseEnter}
      onMouseLeave={handleMouseLeave}
    >
      <SidebarHeader className="p-4">
        <Link href="/" className="flex items-center gap-3">
          <WolfLogo size={40} className="shrink-0 text-primary" />
          <div className="overflow-hidden group-data-[collapsible=icon]:hidden leading-tight">
            <span className="text-2xl font-bold tracking-tight whitespace-nowrap font-brand block">
              The Wolf
            </span>
            <span className="text-[10px] text-muted-foreground block -mt-1">
              by AlphaBravo
            </span>
          </div>
        </Link>
        <p className="text-xs text-muted-foreground mt-1 group-data-[collapsible=icon]:hidden">
          Multi Tool Code Analysis Engine
        </p>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Navigation</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {navItems.map((item) => (
                <SidebarMenuItem key={item.href}>
                  <SidebarMenuButton
                    asChild
                    isActive={isActive(item.href)}
                    tooltip={item.title}
                  >
                    <Link href={item.href}>
                      <item.icon className="h-4 w-4" />
                      <span>{item.title}</span>
                    </Link>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu>
          {bottomItems.map((item) => (
            <SidebarMenuItem key={item.href}>
              <SidebarMenuButton
                asChild
                isActive={isActive(item.href)}
                tooltip={item.title}
              >
                <Link href={item.href}>
                  <item.icon className="h-4 w-4" />
                  <span>{item.title}</span>
                </Link>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
        <div className="px-4 pb-3 group-data-[collapsible=icon]:hidden">
          <a
            href="https://alphabravo.io"
            target="_blank"
            rel="noopener noreferrer"
            className="text-[10px] text-muted-foreground hover:text-foreground transition-colors"
          >
            alphabravo.io
          </a>
        </div>
      </SidebarFooter>
    </Sidebar>
  );
}
