import { createStore } from "zustand/vanilla";

import type { ContentLayout, SidebarCollapsible, SidebarVariant } from "@/lib/preferences/layout";
import { PREFERENCE_DEFAULTS } from "@/lib/preferences/preferences-config";
import type { ResolvedThemeMode, ThemeMode } from "@/lib/preferences/theme";

export type PreferencesState = {
  themeMode: ThemeMode;
  resolvedThemeMode: ResolvedThemeMode;
  contentLayout: ContentLayout;
  sidebarVariant: SidebarVariant;
  sidebarCollapsible: SidebarCollapsible;
  setThemeMode: (mode: ThemeMode) => void;
  setResolvedThemeMode: (mode: ResolvedThemeMode) => void;
  setContentLayout: (layout: ContentLayout) => void;
  setSidebarVariant: (variant: SidebarVariant) => void;
  setSidebarCollapsible: (mode: SidebarCollapsible) => void;
  isSynced: boolean;
  setIsSynced: (val: boolean) => void;
};

export const createPreferencesStore = (init?: Partial<PreferencesState>) =>
  createStore<PreferencesState>()((set) => ({
    themeMode: init?.themeMode ?? PREFERENCE_DEFAULTS.theme_mode,
    resolvedThemeMode: init?.resolvedThemeMode ?? "light",
    contentLayout: init?.contentLayout ?? PREFERENCE_DEFAULTS.content_layout,
    sidebarVariant: init?.sidebarVariant ?? PREFERENCE_DEFAULTS.sidebar_variant,
    sidebarCollapsible: init?.sidebarCollapsible ?? PREFERENCE_DEFAULTS.sidebar_collapsible,
    setThemeMode: (mode) => set({ themeMode: mode }),
    setResolvedThemeMode: (mode) => set({ resolvedThemeMode: mode }),
    setContentLayout: (layout) => set({ contentLayout: layout }),
    setSidebarVariant: (variant) => set({ sidebarVariant: variant }),
    setSidebarCollapsible: (mode) => set({ sidebarCollapsible: mode }),
    isSynced: init?.isSynced ?? false,
    setIsSynced: (val) => set({ isSynced: val }),
  }));
