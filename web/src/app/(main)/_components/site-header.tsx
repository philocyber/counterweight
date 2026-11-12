"use client";

import { useEffect, useState } from "react";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { ChevronRightIcon } from "lucide-react";

import { SidebarTrigger } from "@/components/ui/sidebar";
import { APP_CONFIG } from "@/config/app-config";
import { useCurrentUser } from "@/hooks/use-current-user";
import { useI18n } from "@/i18n";
import { api } from "@/lib/api";
import { findNavEntry } from "@/navigation/sidebar/sidebar-items";

import { AccountSwitcher } from "./sidebar/account-switcher";
import { LayoutControls } from "./sidebar/layout-controls";
import { SearchDialog } from "./sidebar/search-dialog";
import { ThemeSwitcher } from "./sidebar/theme-switcher";
import { UpdateBadge } from "./update-badge";

// Graphite in both colour modes; the `dark` class gives nested controls the dark token set.
export function SiteHeader() {
  "use no memo";
  const { t: swt } = useI18n();

  const currentUser = useCurrentUser();
  const pathname = usePathname();
  const entry = findNavEntry(pathname);
  const [version, setVersion] = useState("");

  useEffect(() => {
    api
      .health()
      .then((h) => setVersion((h.version ?? "").replace(/^v(?=\d)/, "")))
      .catch(() => setVersion(""));
  }, []);

  return (
    <header
      data-console="topbar"
      className="dark sticky top-0 z-50 flex h-(--header-height) w-full shrink-0 items-center gap-3 border-topbar-border border-b bg-topbar px-3 text-topbar-foreground lg:px-4"
    >
      <div className="flex min-w-0 flex-1 items-center gap-2">
        <SidebarTrigger className="text-topbar-muted hover:text-topbar-foreground" />
        <Link prefetch={false} href="/dashboard" className="flex shrink-0 items-center gap-2 rounded-md px-1">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/counterweight.svg" alt="" width={26} height={26} className="shrink-0" />
          <span className="font-semibold text-[17px] lowercase tracking-tight">{APP_CONFIG.name}</span>
        </Link>
        <span aria-hidden="true" className="mx-1 hidden h-5 w-px bg-topbar-border md:block" />
        <nav aria-label="Breadcrumb" className="hidden min-w-0 items-center gap-1.5 text-[13px] md:flex">
          {entry && !entry.group.pinned && (
            <>
              <span className="shrink-0 text-topbar-muted">{entry.group.label}</span>
              <ChevronRightIcon className="size-3.5 shrink-0 text-topbar-muted" />
            </>
          )}
          <span className="truncate font-medium">{entry?.item.title ?? APP_CONFIG.name}</span>
        </nav>
      </div>

      <div className="hidden w-full max-w-md flex-[1.2] justify-center md:flex">
        <SearchDialog />
      </div>

      <div className="flex flex-1 items-center justify-end gap-1.5">
        <Link
          prefetch={false}
          href="/chat"
          className="hidden h-8 shrink-0 items-center gap-2 rounded-full border border-topbar-border bg-white/5 px-3 font-medium text-[13px] transition-colors hover:bg-white/10 sm:inline-flex"
        >
          <span aria-hidden="true" className="size-1.5 rounded-full bg-topbar-accent" />
          {swt("app.askCounterweight")}
        </Link>
        {version && (
          <span className="hidden px-1 font-mono text-[11px] text-topbar-muted tabular-nums xl:inline">
            {swt("interface.m0052")}
            {version}
          </span>
        )}
        <UpdateBadge />
        <LayoutControls />
        <ThemeSwitcher />
        <AccountSwitcher users={[currentUser]} />
      </div>
    </header>
  );
}
