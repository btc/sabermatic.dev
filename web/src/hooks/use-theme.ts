import { useContext } from "react";

import { type Theme, ThemeContext } from "@/contexts/theme-context";

export type { Theme };

export function useTheme() {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme must be used within ThemeProvider");
  return ctx;
}
