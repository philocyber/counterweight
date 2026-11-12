"use client";

import { useShallow } from "zustand/react/shallow";

import { Sidebar, SidebarContent } from "@/components/ui/sidebar";
import { useI18n } from "@/i18n";
import { sidebarItems } from "@/navigation/sidebar/sidebar-items";
import { usePreferencesStore } from "@/stores/preferences/preferences-provider";

import { NavMain } from "./nav-main";

// Brand and account controls live in the global top bar.
export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
  "use no memo";
  const { locale: swLocale } = useI18n();
  const { sidebarVariant, sidebarCollapsible, isSynced } = usePreferencesStore(
    useShallow((s) => ({
      sidebarVariant: s.sidebarVariant,
      sidebarCollapsible: s.sidebarCollapsible,
      isSynced: s.isSynced,
    })),
  );

  const variant = isSynced ? sidebarVariant : props.variant;
  const collapsible = isSynced ? sidebarCollapsible : props.collapsible;

  return (
    <Sidebar {...props} variant={variant} collapsible={collapsible}>
      <SidebarContent className="pt-2">
        <NavMain items={sidebarItems.filter((g) => !g.pinned)} />
      </SidebarContent>
      <div className="border-sidebar-border border-t py-1">
        <NavMain items={sidebarItems.filter((g) => g.pinned).map((g) => ({ ...g, label: undefined }))} />
      </div>
    </Sidebar>
  );
}
