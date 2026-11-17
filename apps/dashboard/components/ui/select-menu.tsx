"use client";

import * as React from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";

export interface SelectMenuOption {
  value: string;
  label: string;
  disabled?: boolean;
  group?: string;
}

/** A select-only combobox. Focus stays on the trigger while navigating its list. */
export function SelectMenu({
  label,
  value,
  options,
  onValueChange,
  placeholder = "Select an option",
  variant = "outline",
  align = "start",
  disabled = false,
  className,
  triggerProps,
  onValuePreview,
}: {
  onValuePreview?: (value: string) => void;
  label?: string;
  value: string;
  options: SelectMenuOption[];
  onValueChange: (value: string) => void;
  placeholder?: string;
  variant?: "outline" | "ghost";
  align?: "start" | "end";
  disabled?: boolean;
  className?: string;
  triggerProps?: React.ButtonHTMLAttributes<HTMLButtonElement>;
}) {
  const id = React.useId();
  const trigger = React.useRef<HTMLButtonElement>(null);
  const menu = React.useRef<HTMLDivElement>(null);
  const search = React.useRef({ text: "", time: 0 });
  const [open, setOpen] = React.useState(false);
  const [active, setActive] = React.useState(0);
  const [position, setPosition] = React.useState<React.CSSProperties>({});
  const selected = options.findIndex((option) => option.value === value);
  const activeIndex = Math.min(active, options.length - 1);
  const enabled = options.flatMap((option, index) => option.disabled ? [] : [index]);
  const first = enabled[0] ?? -1;
  const last = enabled.at(-1) ?? -1;

  function show(index = selected >= 0 && !options[selected]?.disabled ? selected : first) {
    search.current = { text: "", time: 0 };
    setActive(index);
    setOpen(true);
  }

  function choose(index: number) {
    const option = options[index];
    if (!option || option.disabled) return;
    setOpen(false);
    trigger.current?.focus();
    if (option.value !== value) onValueChange(option.value);
  }

  React.useLayoutEffect(() => {
    if (!open) return;
    function positionMenu() {
      const rect = trigger.current?.getBoundingClientRect();
      if (!rect) return;
      const width = Math.min(Math.max(rect.width, 224), 384, window.innerWidth - 16);
      const below = window.innerHeight - rect.bottom - 14;
      const above = rect.top - 14;
      const upwards = below < 240 && above > below;
      setPosition({
        width,
        left: Math.max(8, Math.min(
          align === "end" ? rect.right - width : rect.left,
          window.innerWidth - width - 8,
        )),
        top: upwards ? undefined : rect.bottom + 6,
        bottom: upwards ? window.innerHeight - rect.top + 6 : undefined,
        maxHeight: Math.max(0, Math.min(360, upwards ? above : below)),
      });
    }
    function dismiss(event: PointerEvent) {
      const target = event.target as Node;
      if (!trigger.current?.contains(target) && !menu.current?.contains(target)) {
        setOpen(false);
      }
    }
    const close = () => setOpen(false);
    positionMenu();
    window.addEventListener("resize", positionMenu);
    window.addEventListener("scroll", positionMenu, true);
    window.addEventListener("blur", close);
    document.addEventListener("pointerdown", dismiss);
    return () => {
      window.removeEventListener("resize", positionMenu);
      window.removeEventListener("scroll", positionMenu, true);
      window.removeEventListener("blur", close);
      document.removeEventListener("pointerdown", dismiss);
    };
  }, [open, align]);

  React.useEffect(() => {
    if (open) document.getElementById(`${id}-${activeIndex}`)?.scrollIntoView({ block: "nearest" });
  }, [open, activeIndex, id]);

  const previewCallback = React.useRef(onValuePreview);
  previewCallback.current = onValuePreview;
  const activeValue = options[activeIndex]?.disabled ? undefined : options[activeIndex]?.value;
  React.useEffect(() => {
    if (!open || activeValue === undefined) return;
    const timer = setTimeout(() => previewCallback.current?.(activeValue), 100);
    return () => clearTimeout(timer);
  }, [open, activeValue]);

  function onKeyDown(event: React.KeyboardEvent<HTMLButtonElement>) {
    switch (event.key) {
      case "ArrowDown":
      case "ArrowUp":
        event.preventDefault();
        if (!open) show(selected >= 0 && !options[selected]?.disabled ? selected : event.key === "ArrowUp" ? last : first);
        else setActive(event.key === "ArrowDown"
          ? enabled.find((index) => index > activeIndex) ?? last
          : [...enabled].reverse().find((index) => index < activeIndex) ?? first);
        return;
      case "Home":
      case "End":
        event.preventDefault();
        if (!open) show(event.key === "Home" ? first : last);
        else setActive(event.key === "Home" ? first : last);
        return;
      case "Enter":
      case " ":
        if (event.key === " " && search.current.text && Date.now() - search.current.time < 700) break;
        event.preventDefault();
        if (open) choose(activeIndex);
        else show();
        return;
      case "Escape":
        if (open) {
          event.preventDefault();
          event.stopPropagation();
          setOpen(false);
        }
        return;
      case "Tab":
        setOpen(false);
        return;
    }
    if (event.key.length !== 1 || event.ctrlKey || event.metaKey || event.altKey) return;
    event.preventDefault();
    const now = Date.now();
    const text = (now - search.current.time < 700 ? search.current.text : "") + event.key.toLocaleLowerCase();
    const query = [...text].every((letter) => letter === text[0]) ? text[0]! : text;
    const start = open ? activeIndex : selected;
    const match = Array.from({ length: options.length }, (_, offset) =>
      (Math.max(start, 0) + offset + (query.length === 1 ? 1 : 0)) % options.length,
    ).find((index) => !options[index]!.disabled && options[index]!.label.toLocaleLowerCase().startsWith(query));
    if (!open) show(match ?? first);
    else if (match !== undefined) setActive(match);
    search.current = { text, time: now };
  }

  return (
    <>
      <button
        {...triggerProps}
        ref={trigger}
        type="button"
        role="combobox"
        aria-label={label}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        aria-activedescendant={open && activeIndex >= 0 ? `${id}-${activeIndex}` : undefined}
        disabled={disabled}
        onClick={() => open ? setOpen(false) : show()}
        onKeyDown={onKeyDown}
        onBlur={(event) => { setOpen(false); triggerProps?.onBlur?.(event); }}
        className={cn(
          "inline-flex h-9 min-w-0 max-w-full items-center justify-between gap-3 rounded-md border px-3 text-left text-sm transition-colors",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
          variant === "outline" ? "border-input bg-background hover:bg-muted/60" : "border-transparent px-2 hover:bg-muted/60",
          open && "bg-muted/60",
          className,
        )}
      >
        <span className={cn("truncate", selected < 0 && "text-muted-foreground")}>
          {options[selected]?.label ?? placeholder}
        </span>
        <ChevronDown aria-hidden className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-180")} />
      </button>
      {open && createPortal(
        <div
          ref={menu}
          style={position}
          className="fixed z-50 flex flex-col overflow-hidden rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-xl"
          onMouseDown={(event) => event.preventDefault()}
        >
          {label && <div className="shrink-0 px-2.5 pb-2 pt-1.5 text-xs font-medium text-muted-foreground">{label}</div>}
          <div id={id} role="listbox" aria-label={label} className="min-h-0 overflow-y-auto overscroll-contain">
            {options.map((option, index) => (
              <React.Fragment key={option.value}>
              {option.group && option.group !== options[index - 1]?.group && (
                <div role="presentation" className="px-2.5 pb-1 pt-3 text-xs font-medium text-muted-foreground">{option.group}</div>
              )}
              <div
                id={`${id}-${index}`}
                role="option"
                aria-label={option.group ? `${option.group}: ${option.label}` : undefined}
                aria-selected={option.value === value}
                aria-disabled={option.disabled || undefined}
                onPointerMove={() => !option.disabled && setActive(index)}
                onClick={() => choose(index)}
                className={cn(
                  "flex min-h-9 cursor-pointer items-center gap-3 rounded-md px-2.5 py-2 text-sm transition-colors",
                  index === activeIndex && "bg-accent text-accent-foreground",
                  option.value === value && "font-medium",
                  option.disabled && "cursor-not-allowed opacity-40",
                )}
              >
                <span className="min-w-0 flex-1 break-words">{option.label}</span>
                <Check aria-hidden className={cn("size-3.5 shrink-0", option.value !== value && "invisible")} />
              </div>
              </React.Fragment>
            ))}
          </div>
          {options.length === 0 && <p className="px-2.5 py-3 text-sm text-muted-foreground">No options available</p>}
        </div>,
        document.body,
      )}
    </>
  );
}
