"use client";
import type { ReactNode } from "react";
import * as React from "react";

import { AppSidebar } from "@/app/(main)/_components/sidebar/app-sidebar";
import { StarPrompt } from "@/components/star-prompt";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { useI18n } from "@/i18n";
import { auth } from "@/lib/auth";
import { getClientCookie } from "@/lib/cookie.client";
import {
  SIDEBAR_COLLAPSIBLE_VALUES,
  SIDEBAR_VARIANT_VALUES,
  type SidebarCollapsible,
  type SidebarVariant,
} from "@/lib/preferences/layout";
import { PREFERENCE_DEFAULTS } from "@/lib/preferences/preferences-config";
import { cn } from "@/lib/utils";

import { MainContent } from "./_components/main-content";
import { SiteHeader } from "./_components/site-header";

// Reads a layout-critical preference from the browser cookie (falls back to the
// default during static-export prerender where document is unavailable). Kept
// fully client-side so the app can be statically exported — no Server Actions /
// next/headers.
function readPref<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  if (typeof document === "undefined") return fallback;
  const value = getClientCookie(key);
  return value && (allowed as readonly string[]).includes(value) ? (value as T) : fallback;
}

export default function Layout({ children }: Readonly<{ children: ReactNode }>) {
  "use no memo";
  const { locale: swLocale } = useI18n();

  // Client-side auth gate — replaces the Next proxy/middleware that static export
  // disables. No token → bounce to /login; render nothing until confirmed so no
  // protected UI (or its API calls) flashes for a logged-out visitor.
  const [authed, setAuthed] = React.useState(false);
  React.useEffect(() => {
    if (auth.getToken()) {
      setAuthed(true);
    } else {
      // When the client guard finds no login, also clear the cookie. Otherwise proxy.ts sees its presence
      // and redirects /login back to the app, while this guard redirects back again,
      // creating an infinite redirect and blank screen when cookie and localStorage disagree.
      auth.clearToken();
      window.location.href = "/login";
    }
  }, []);

  const defaultOpen = typeof document === "undefined" ? true : getClientCookie("sidebar_state") !== "false";
  const variant = readPref<SidebarVariant>(
    "sidebar_variant",
    SIDEBAR_VARIANT_VALUES,
    PREFERENCE_DEFAULTS.sidebar_variant,
  );
  const collapsible = readPref<SidebarCollapsible>(
    "sidebar_collapsible",
    SIDEBAR_COLLAPSIBLE_VALUES,
    PREFERENCE_DEFAULTS.sidebar_collapsible,
  );

  if (!authed) return null;

  return (
    <SidebarProvider
      defaultOpen={defaultOpen}
      className="flex-col"
      style={
        {
          "--sidebar-width": "calc(var(--spacing) * 60)",
        } as React.CSSProperties
      }
    >
      <SiteHeader />
      <div className="flex min-h-0 flex-1">
        <AppSidebar
          variant={variant}
          collapsible={collapsible}
          className="top-(--header-height) h-[calc(100svh-var(--header-height))]!"
        />
        <SidebarInset
          className={cn(
            "[html[data-content-layout=centered]_&>*]:mx-auto",
            "[html[data-content-layout=centered]_&>*]:w-full",
            "[html[data-content-layout=centered]_&>*]:max-w-screen-2xl",
            "peer-data-[variant=inset]:border",
            "min-h-[calc(100svh-var(--header-height))] min-w-0 overflow-x-clip",
          )}
        >
          <MainContent>{children}</MainContent>
        </SidebarInset>
      </div>
      <StarPrompt />
    </SidebarProvider>
  );
}
