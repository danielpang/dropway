"use client";

import * as React from "react";
import { ThemeProvider as NextThemesProvider } from "next-themes";

/**
 * Thin wrapper so the (client) provider can be mounted from the server-rendered
 * root layout. Configured in layout.tsx with attribute="class",
 * defaultTheme="system" and enableSystem — the theme follows the device's
 * prefers-color-scheme automatically, with an optional manual override.
 */
export function ThemeProvider({
  children,
  ...props
}: React.ComponentProps<typeof NextThemesProvider>) {
  return <NextThemesProvider {...props}>{children}</NextThemesProvider>;
}
