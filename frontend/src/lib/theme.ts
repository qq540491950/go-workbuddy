// Theme application: mode (light/dark/system), accent color, font scale.
import { Config, type Settings } from "@/lib/api";

export interface ThemePrefs {
  themeMode: string;
  accent: string;
  fontScale: string;
}

export const ACCENTS: Record<string, { label: string; color: string }> = {
  blue: { label: "默认蓝", color: "" }, // empty = theme default
  violet: { label: "紫罗兰", color: "#8b5cf6" },
  emerald: { label: "祖母绿", color: "#10b981" },
  orange: { label: "暖橙", color: "#f97316" },
  rose: { label: "玫红", color: "#f43f5e" },
  cyan: { label: "青蓝", color: "#06b6d4" },
};

export const FONT_SCALES: Record<string, { label: string; root: string }> = {
  sm: { label: "小", root: "93.75%" },
  md: { label: "中", root: "100%" },
  lg: { label: "大", root: "112.5%" },
};

const media = window.matchMedia("(prefers-color-scheme: dark)");

/** applyTheme applies mode/accent/font-scale to the document. */
export function applyTheme(prefs: ThemePrefs): boolean {
  const dark =
    prefs.themeMode === "dark" || (prefs.themeMode === "system" && media.matches);
  document.documentElement.classList.toggle("dark", dark);
  document.documentElement.style.fontSize = (FONT_SCALES[prefs.fontScale] ?? FONT_SCALES.md).root;

  const accent = ACCENTS[prefs.accent] ?? ACCENTS.blue;
  const root = document.documentElement.style;
  if (accent.color) {
    root.setProperty("--primary", accent.color);
    root.setProperty("--primary-foreground", "#ffffff");
    root.setProperty("--ring", accent.color);
    root.setProperty("--sidebar-primary", accent.color);
    root.setProperty("--sidebar-primary-foreground", "#ffffff");
  } else {
    root.removeProperty("--primary");
    root.removeProperty("--primary-foreground");
    root.removeProperty("--ring");
    root.removeProperty("--sidebar-primary");
    root.removeProperty("--sidebar-primary-foreground");
  }
  return dark;
}

/** cacheTheme writes prefs to localStorage for instant boot application. */
export function cacheTheme(s: Settings) {
  localStorage.setItem(
    "wb-theme-prefs",
    JSON.stringify({ themeMode: s.themeMode, accent: s.accent, fontScale: s.fontScale }),
  );
}

/** cachedTheme reads the boot cache (falls back to dark defaults). */
export function cachedTheme(): ThemePrefs {
  try {
    const raw = localStorage.getItem("wb-theme-prefs");
    if (raw) return JSON.parse(raw) as ThemePrefs;
  } catch { /* ignore */ }
  return { themeMode: "dark", accent: "blue", fontScale: "md" };
}

/** syncThemeFromBackend loads settings from the backend, caches and applies them. */
export async function syncThemeFromBackend(): Promise<boolean> {
  try {
    const cfg = await Config.GetConfig();
    const s = cfg.settings;
    cacheTheme(s);
    return applyTheme(cachedTheme());
  } catch {
    return applyTheme(cachedTheme());
  }
}

/** onSystemThemeChange re-applies when the OS appearance changes. */
export function onSystemThemeChange(prefs: ThemePrefs, fn: (dark: boolean) => void): () => void {
  const handler = () => {
    if (prefs.themeMode === "system") fn(applyTheme(prefs));
  };
  media.addEventListener("change", handler);
  return () => media.removeEventListener("change", handler);
}
