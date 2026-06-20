"use client";

import * as React from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";

import { cn } from "@/lib/utils";

/**
 * Side drawer (slide-over), modeled on the dependency-free Dialog: controlled via
 * `open`/`onOpenChange`, role=dialog + aria-modal, Escape + backdrop to close,
 * focus moved into the panel on open and restored on close, body scroll lock.
 * Slides in from the right edge, full-height — used for the "change plan" panel.
 */

interface SheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  children: React.ReactNode;
  /** Accessible label when there's no visible SheetTitle. */
  label?: string;
  /** Panel width override (default max-w-2xl). */
  className?: string;
}

const TitleIdContext = React.createContext<string | undefined>(undefined);

export function Sheet({
  open,
  onOpenChange,
  children,
  label,
  className,
}: SheetProps) {
  const [mounted, setMounted] = React.useState(false);
  const panelRef = React.useRef<HTMLDivElement>(null);
  const titleId = React.useId();
  const previouslyFocused = React.useRef<HTMLElement | null>(null);

  // See Dialog for why onOpenChange is read through a ref (keeps the focus effect
  // depending only on `open`, so typing inside the panel doesn't yank focus).
  const onOpenChangeRef = React.useRef(onOpenChange);
  React.useEffect(() => {
    onOpenChangeRef.current = onOpenChange;
  });

  React.useEffect(() => setMounted(true), []);

  React.useEffect(() => {
    if (!open) return;

    previouslyFocused.current = document.activeElement as HTMLElement | null;
    const { overflow } = document.body.style;
    document.body.style.overflow = "hidden";

    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") onOpenChangeRef.current(false);
    }
    document.addEventListener("keydown", onKeyDown);

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
    <div className="fixed inset-0 z-50">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-foreground/40 backdrop-blur-sm"
        aria-hidden
        onClick={() => onOpenChange(false)}
      />
      {/* Panel: full-height, pinned to the right, slides in. */}
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={label ? undefined : titleId}
        aria-label={label}
        tabIndex={-1}
        className={cn(
          "absolute inset-y-0 right-0 flex w-full max-w-2xl flex-col border-l border-border bg-popover text-popover-foreground shadow-xl",
          "focus-visible:outline-none animate-slide-in-right",
          className,
        )}
      >
        <button
          type="button"
          onClick={() => onOpenChange(false)}
          aria-label="Close"
          className="absolute right-4 top-4 z-10 rounded-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
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

export function SheetHeader({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "flex flex-col space-y-1 border-b border-border p-6 pr-12",
        className,
      )}
      {...props}
    />
  );
}

export function SheetTitle({
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

export function SheetBody({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div className={cn("flex-1 overflow-y-auto p-6", className)} {...props} />
  );
}
