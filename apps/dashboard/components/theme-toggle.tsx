"use client";

import * as React from "react";
import { Monitor, Moon, Sun } from "lucide-react";
import { useTheme } from "next-themes";

import { Button } from "@/components/ui/button";

/**
 * Small theme toggle. Cycles system -> light -> dark. Renders a stable
 * placeholder until mounted to avoid a hydration mismatch (theme is unknown on
 * the server). The icon reflects the explicit choice; "system" follows the OS.
 */
export function ThemeToggle() {
  const { theme, setTheme } = useTheme();
  const [mounted, setMounted] = React.useState(false);

  React.useEffect(() => setMounted(true), []);

  const order = ["system", "light", "dark"] as const;
  const current = (theme ?? "system") as (typeof order)[number];

  function cycle() {
    // order is a non-empty const tuple, so the modulo index is always valid;
    // the non-null assertion satisfies `noUncheckedIndexedAccess`.
    const next = order[(order.indexOf(current) + 1) % order.length]!;
    setTheme(next);
  }

  const label = `Theme: ${current}. Click to change.`;

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      onClick={cycle}
      aria-label={label}
      title={label}
    >
      {!mounted ? (
        <Monitor aria-hidden />
      ) : current === "light" ? (
        <Sun aria-hidden />
      ) : current === "dark" ? (
        <Moon aria-hidden />
      ) : (
        <Monitor aria-hidden />
      )}
    </Button>
  );
}
