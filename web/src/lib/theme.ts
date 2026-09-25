// Theme: "auto" follows prefers-color-scheme; "light" / "dark" override it (data-theme on <html>).
export type ThemeMode = "auto" | "light" | "dark";

export const THEME_LABEL: Record<ThemeMode, string> = { auto: "خودکار", light: "روشن", dark: "تیره" };

export function nextMode(m: ThemeMode): ThemeMode {
  return m === "auto" ? "light" : m === "light" ? "dark" : "auto";
}

export function readMode(): ThemeMode {
  try {
    const t = localStorage.getItem("theme");
    return t === "light" || t === "dark" ? t : "auto";
  } catch {
    return "auto";
  }
}

export function applyMode(m: ThemeMode) {
  const el = document.documentElement;
  if (m === "auto") el.removeAttribute("data-theme");
  else el.setAttribute("data-theme", m);
  try {
    if (m === "auto") localStorage.removeItem("theme");
    else localStorage.setItem("theme", m);
  } catch {
    /* private mode: the choice lasts for this page only */
  }
}
