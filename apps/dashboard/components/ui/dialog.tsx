"use client";

import * as React from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";

import { cn } from "@/lib/utils";

/**
 * Minimal, dependency-free modal dialog (no @radix-ui/react-dialog in the
 * dashboard's dependency set). Controlled via `open`/`onOpenChange`. Handles the
 * accessibility basics: role=dialog + aria-modal, Escape to close, backdrop
 * click to close, focus moved into the panel on open and restored on close, and
 * body scroll lock while open. Styling reuses the popover/card tokens so it
 * matches the rest of the surface in both themes.
 */

interface DialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Accessible title id wiring is handled by DialogTitle below. */
  children: React.ReactNode;
  /** Optional aria-label when there is no visible DialogTitle. */
  label?: string;
  /** Extra classes for the panel, e.g. a wider `max-w-*` override (default max-w-md). */
  className?: string;
}

const TitleIdContext = React.createContext<string | undefined>(undefined);

export function Dialog({
  open,
  onOpenChange,
  children,
  label,
  className,
}: DialogProps) {
  const [mounted, setMounted] = React.useState(false);
  const panelRef = React.useRef<HTMLDivElement>(null);
  const titleId = React.useId();
  const previouslyFocused = React.useRef<HTMLElement | null>(null);

  // Keep the latest onOpenChange in a ref so the focus-management effect below can
  // depend ONLY on `open`. Parents routinely pass an inline onOpenChange (a fresh
  // function identity every render); if it were in that effect's deps, every parent
  // re-render, e.g. each keystroke in a field INSIDE the dialog, would tear down
  // and re-run the effect, which restores focus to the trigger and then moves it to
  // the panel's first focusable, yanking focus out of the input on every character.
  // Reading onOpenChange through a ref decouples the effect from its identity.
  const onOpenChangeRef = React.useRef(onOpenChange);
  React.useEffect(() => {
    onOpenChangeRef.current = onOpenChange;
  });

  React.useEffect(() => setMounted(true), []);

  // Escape to close + body scroll lock + focus management. Depends ONLY on `open`,
  // so it runs when the dialog opens/closes, never on an unrelated re-render, and
  // therefore never steals focus from a field the user is typing into.
  React.useEffect(() => {
    if (!open) return;

    previouslyFocused.current = document.activeElement as HTMLElement | null;
    const { overflow } = document.body.style;
    document.body.style.overflow = "hidden";

    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") onOpenChangeRef.current(false);
    }
    document.addEventListener("keydown", onKeyDown);

    // Move focus into the panel (first focusable, else the panel itself).
    const focusable = panelRef.current?.querySelector<HTMLElement>(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
    );
    (focusable ?? panelRef.current)?.focus();

    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.body.style.overflow = overflow;
      previouslyFocused.current?.focus?.();
    };
  }, [open]);

  if (!mounted || !open) return null;

  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-foreground/40 backdrop-blur-sm"
        aria-hidden
        onClick={() => onOpenChange(false)}
      />
      {/* Panel */}
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={label ? undefined : titleId}
        aria-label={label}
        tabIndex={-1}
        className={cn(
          "relative z-10 w-full max-w-md rounded-lg border border-border bg-popover text-popover-foreground shadow-lg",
          "focus-visible:outline-none",
          "animate-fade-in",
          className,
        )}
      >
        <button
          type="button"
          onClick={() => onOpenChange(false)}
          aria-label="Close"
          className="absolute right-4 top-4 rounded-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        >
          <X className="size-4" aria-hidden />
        </button>
        <TitleIdContext.Provider value={titleId}>
          {children}
        </TitleIdContext.Provider>
      </div>
    </div>,
    document.body,
  );
}

export function DialogHeader({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("flex flex-col space-y-1.5 p-6 pb-2", className)}
      {...props}
    />
  );
}

export function DialogTitle({
  className,
  ...props
}: React.HTMLAttributes<HTMLHeadingElement>) {
  const id = React.useContext(TitleIdContext);
  return (
    <h2
      id={id}
      className={cn("text-lg font-semibold tracking-tight", className)}
      {...props}
    />
  );
}

export function DialogDescription({
  className,
  ...props
}: React.HTMLAttributes<HTMLParagraphElement>) {
  return (
    <p className={cn("text-sm text-muted-foreground", className)} {...props} />
  );
}

export function DialogBody({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("px-6 py-2", className)} {...props} />;
}

export function DialogFooter({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "flex flex-col-reverse gap-2 p-6 pt-4 sm:flex-row sm:justify-end",
        className,
      )}
      {...props}
    />
  );
}
