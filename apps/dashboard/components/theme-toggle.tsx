"use client";

import * as React from "react";
import { Monitor, Moon, Sun } from "lucide-react";
import { useTheme } from "next-themes";

import { Button } from "@/components/ui/button";
import { Tooltip } from "@/components/ui/tooltip";

/**
 * Small theme toggle. Cycles system -> light -> dark (default is "system", which
 * follows the device's prefers-color-scheme — set in layout.tsx). Renders a stable
 * placeholder until mounted to avoid a hydration mismatch (theme is unknown on the
 * server). The icon reflects the explicit choice; the tooltip names the current
 * mode and the cycle order so the three states (incl. the System "monitor" icon)
 * are self-explanatory.
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

  const currentLabel =
    current === "system"
      ? "System (follows your device)"
      : current === "light"
        ? "Light"
        : "Dark";
  // Until mounted the theme is unknown; show a neutral hint.
  const label = mounted
    ? `Theme: ${currentLabel} — click to switch (System → Light → Dark)`
    : "Switch theme (System → Light → Dark)";

  return (
    <Tooltip label={label}>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        onClick={cycle}
        aria-label={label}
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
    </Tooltip>
  );
}
