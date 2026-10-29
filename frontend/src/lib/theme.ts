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
    const fg = readableForeground(accent.color);
    root.setProperty("--primary-foreground", fg);
    root.setProperty("--ring", accent.color);
    root.setProperty("--sidebar-primary", accent.color);
    root.setProperty("--sidebar-primary-foreground", fg);
  } else {
    root.removeProperty("--primary");
    root.removeProperty("--primary-foreground");
    root.removeProperty("--ring");
    root.removeProperty("--sidebar-primary");
    root.removeProperty("--sidebar-primary-foreground");
  }
  return dark;
}

/** readableForeground picks black or white text for a background hex color by
 * comparing WCAG contrast ratios: fixed white measured ~2.8:1 on the accent
 * colors (orange/cyan/emerald), below the 4.5:1 AA threshold. */
function readableForeground(hex: string): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return "#ffffff";
  const n = parseInt(m[1], 16);
  const lin = (c: number) => {
    const x = c / 255;
    return x <= 0.04045 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4);
  };
  const l = 0.2126 * lin((n >> 16) & 0xff) + 0.7152 * lin((n >> 8) & 0xff) + 0.0722 * lin(n & 0xff);
  const contrastWhite = 1.05 / (l + 0.05);
  const contrastBlack = (l + 0.05) / 0.05;
  return contrastWhite >= contrastBlack ? "#ffffff" : "#0b0f14";
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
