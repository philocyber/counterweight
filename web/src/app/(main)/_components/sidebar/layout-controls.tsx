"use client";
import { useEffect, useState } from "react";

import { Settings } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { LOCALE_NATIVE_NAMES, LOCALES, useI18n } from "@/i18n";
import { translate as swt } from "@/i18n/runtime";
import type { ContentLayout, SidebarCollapsible, SidebarVariant } from "@/lib/preferences/layout";
import { applyContentLayout, applySidebarCollapsible, applySidebarVariant } from "@/lib/preferences/layout-utils";
import { PREFERENCE_DEFAULTS } from "@/lib/preferences/preferences-config";
import { persistPreference } from "@/lib/preferences/preferences-storage";
import type { ThemeMode } from "@/lib/preferences/theme";
import { systemThemeMode } from "@/lib/preferences/theme-utils";
import { usePreferencesStore } from "@/stores/preferences/preferences-provider";

export function LayoutControls() {
  "use no memo";
  const { locale: swLocale, setLocale } = useI18n();

  const themeMode = usePreferencesStore((s) => s.themeMode);
  const setThemeMode = usePreferencesStore((s) => s.setThemeMode);
  const [themeOrder, setThemeOrder] = useState<ThemeMode[]>(["light", "dark"]);
  const contentLayout = usePreferencesStore((s) => s.contentLayout);
  const setContentLayout = usePreferencesStore((s) => s.setContentLayout);
  const variant = usePreferencesStore((s) => s.sidebarVariant);
  const setSidebarVariant = usePreferencesStore((s) => s.setSidebarVariant);
  const collapsible = usePreferencesStore((s) => s.sidebarCollapsible);
  const setSidebarCollapsible = usePreferencesStore((s) => s.setSidebarCollapsible);

  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => setThemeOrder(media.matches ? ["dark", "light"] : ["light", "dark"]);
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, []);

  const onThemeModeChange = (mode: ThemeMode | "") => {
    if (!mode) return;
    setThemeMode(mode);
    void persistPreference("theme_mode", mode);
  };

  const onContentLayoutChange = (layout: ContentLayout | "") => {
    if (!layout) return;
    applyContentLayout(layout);
    setContentLayout(layout);
    void persistPreference("content_layout", layout);
  };

  const onSidebarStyleChange = (value: SidebarVariant | "") => {
    if (!value) return;
    setSidebarVariant(value);
    applySidebarVariant(value);
    void persistPreference("sidebar_variant", value);
  };

  const onSidebarCollapseModeChange = (value: SidebarCollapsible | "") => {
    if (!value) return;
    setSidebarCollapsible(value);
    applySidebarCollapsible(value);
    void persistPreference("sidebar_collapsible", value);
  };

  const handleRestore = () => {
    onThemeModeChange(systemThemeMode());
    onContentLayoutChange(PREFERENCE_DEFAULTS.content_layout);
    onSidebarStyleChange(PREFERENCE_DEFAULTS.sidebar_variant);
    onSidebarCollapseModeChange(PREFERENCE_DEFAULTS.sidebar_collapsible);
  };

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={swt("english.e004")}>
          <Settings />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end">
        <div className="flex flex-col gap-5">
          <div className="space-y-1.5">
            <h4 className="font-medium text-sm leading-none">{swt("english.e004")}</h4>
            <p className="text-muted-foreground text-xs">{swt("english.e005")}</p>
          </div>
          <div className="space-y-3 **:data-[slot=toggle-group]:w-full **:data-[slot=toggle-group-item]:flex-1 **:data-[slot=toggle-group-item]:text-xs">
            <div className="space-y-1">
              <Label className="font-medium text-xs">{swt("app.language")}</Label>
              <ToggleGroup
                size="sm"
                spacing={0}
                variant="outline"
                type="single"
                value={swLocale}
                onValueChange={(value) => value && setLocale(value as (typeof LOCALES)[number])}
                aria-label={swt("app.languageSelectAria")}
              >
                {LOCALES.map((loc) => (
                  <ToggleGroupItem key={loc} value={loc}>
                    {LOCALE_NATIVE_NAMES[loc]}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
            </div>

            <div className="space-y-1">
              <Label className="font-medium text-xs">{swt("english.e010")}</Label>
              <ToggleGroup
                size="sm"
                spacing={0}
                variant="outline"
                type="single"
                value={themeMode}
                onValueChange={onThemeModeChange}
              >
                {themeOrder.map((mode) => (
                  <ToggleGroupItem
                    key={mode}
                    value={mode}
                    aria-label={swt(mode === "light" ? "english.e011" : "english.e013")}
                  >
                    {swt(mode === "light" ? "english.e012" : "english.e014")}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
            </div>

            <div className="space-y-1">
              <Label className="font-medium text-xs">{swt("english.e017")}</Label>
              <ToggleGroup
                size="sm"
                spacing={0}
                variant="outline"
                type="single"
                value={contentLayout}
                onValueChange={onContentLayoutChange}
              >
                <ToggleGroupItem value="centered" aria-label={swt("english.e018")}>
                  {swt("english.e019")}
                </ToggleGroupItem>
                <ToggleGroupItem value="full-width" aria-label={swt("english.e020")}>
                  {swt("english.e021")}
                </ToggleGroupItem>
              </ToggleGroup>
            </div>

            <div className="space-y-1">
              <Label className="font-medium text-xs">{swt("english.e027")}</Label>
              <ToggleGroup
                size="sm"
                spacing={0}
                variant="outline"
                type="single"
                value={variant}
                onValueChange={onSidebarStyleChange}
              >
                <ToggleGroupItem value="inset" aria-label={swt("english.e028")}>
                  {swt("english.e029")}
                </ToggleGroupItem>
                <ToggleGroupItem value="sidebar" aria-label={swt("english.e030")}>
                  {swt("english.e031")}
                </ToggleGroupItem>
                <ToggleGroupItem value="floating" aria-label={swt("english.e032")}>
                  {swt("english.e033")}
                </ToggleGroupItem>
              </ToggleGroup>
            </div>

            <div className="space-y-1">
              <Label className="font-medium text-xs">{swt("english.e034")}</Label>
              <ToggleGroup
                size="sm"
                spacing={0}
                variant="outline"
                type="single"
                value={collapsible}
                onValueChange={onSidebarCollapseModeChange}
              >
                <ToggleGroupItem value="icon" aria-label={swt("english.e035")}>
                  {swt("english.e036")}
                </ToggleGroupItem>
                <ToggleGroupItem value="offcanvas" aria-label={swt("english.e037")}>
                  {swt("english.e038")}
                </ToggleGroupItem>
              </ToggleGroup>
            </div>

            <Button type="button" size="sm" variant="outline" className="w-full text-xs" onClick={handleRestore}>
              {swt("english.e039")}
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}
