"use client";
import { Moon, Sun } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/i18n";
import { persistPreference } from "@/lib/preferences/preferences-storage";
import { usePreferencesStore } from "@/stores/preferences/preferences-provider";

export function ThemeSwitcher() {
  "use no memo";
  const { t } = useI18n();

  const themeMode = usePreferencesStore((s) => s.themeMode);
  const setThemeMode = usePreferencesStore((s) => s.setThemeMode);
  const nextTheme = themeMode === "dark" ? "light" : "dark";

  const switchTheme = () => {
    setThemeMode(nextTheme);
    void persistPreference("theme_mode", nextTheme);
  };

  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={switchTheme}
      aria-label={t("app.cycleTheme", {
        theme: t(nextTheme === "light" ? "english.e012" : "english.e014"),
      })}
    >
      {themeMode === "dark" ? <Sun /> : <Moon />}
    </Button>
  );
}
