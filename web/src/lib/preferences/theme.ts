import { translate as swt } from "@/i18n/runtime";
export const THEME_MODE_OPTIONS = [
  {
    get label() {
      return swt("english.e012");
    },
    value: "light",
  },
  {
    get label() {
      return swt("english.e014");
    },
    value: "dark",
  },
] as const;

export const THEME_MODE_VALUES = THEME_MODE_OPTIONS.map((o) => o.value);
export type ThemeMode = (typeof THEME_MODE_VALUES)[number];
export type ResolvedThemeMode = "light" | "dark";
