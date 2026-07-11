"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Check, ChevronsUpDown, Search } from "lucide-react";

import { ProviderMark, providerLabel, providerOf } from "@/components/ai/provider-logos";
import type { AiModel } from "@/lib/api";
import { cn } from "@/lib/utils";

/**
 * A command-palette style model picker: a compact trigger that opens a
 * searchable popover of models grouped by provider. Each provider shows its real
 * logo (falling back to a colored monogram for providers we don't have a mark
 * for), and each model shows its context window and a cost tier so the choice is
 * informed at a glance. See provider-logos.tsx for the logo/fallback mapping.
 *
 * Self-contained: no popover/command dependency. Keyboard-navigable (up/down to
 * move, enter to pick, escape to close, typing filters), closes on click-outside
 * or blur, and respects the app's neutral palette + focus-visible ring.
 *
 * Copy note: no em or en dashes in user-facing text.
 */

interface ModelPickerProps {
  models: AiModel[];
  value: string;
  onChange: (id: string) => void;
  disabled?: boolean;
}

export function ModelPicker({ models, value, onChange, disabled }: ModelPickerProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [activeIdx, setActiveIdx] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const selected = useMemo(() => models.find((m) => m.id === value), [models, value]);

  // Flat, filtered, provider-grouped list. `flat` is the keyboard-nav order;
  // `groups` drives rendering (a provider header before its models).
  const { groups, flat } = useMemo(() => filterAndGroup(models, query), [models, query]);

  // Close on outside click.
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  // Focus the search + reset state when opening.
  useEffect(() => {
    if (open) {
      setQuery("");
      const idx = Math.max(0, flat.findIndex((m) => m.id === value));
      setActiveIdx(idx);
      // Defer focus to after the popover paints.
      requestAnimationFrame(() => inputRef.current?.focus());
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // Keep the active row in view as the user arrows through.
  useEffect(() => {
    if (!open) return;
    const el = listRef.current?.querySelector<HTMLElement>(`[data-idx="${activeIdx}"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [activeIdx, open]);

  const commit = (id: string) => {
    onChange(id);
    setOpen(false);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActiveIdx((i) => Math.min(flat.length - 1, i + 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActiveIdx((i) => Math.max(0, i - 1));
    } else if (e.key === "Enter") {
      e.preventDefault();
      const m = flat[activeIdx];
      if (m) commit(m.id);
    } else if (e.key === "Escape") {
      e.preventDefault();
      setOpen(false);
    }
  };

  const selectedProvider = providerOf(value);

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="listbox"
        aria-expanded={open}
        className={cn(
          "flex items-center gap-2 rounded-lg border bg-background py-1.5 pl-1.5 pr-2 text-xs font-medium",
          "transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          "disabled:cursor-not-allowed disabled:opacity-60",
        )}
      >
        <ProviderMark provider={selectedProvider} />
        <span className="max-w-[11rem] truncate text-left">
          {selected ? modelName(selected) : "Select a model"}
        </span>
        <ChevronsUpDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      </button>

      {open && (
        <div
          role="listbox"
          className={cn(
            "absolute right-0 z-50 mt-2 w-[22rem] max-w-[calc(100vw-2rem)] overflow-hidden rounded-xl border bg-popover shadow-xl",
            "animate-popover-in",
          )}
          onKeyDown={onKeyDown}
        >
          <div className="flex items-center gap-2 border-b px-3 py-2.5">
            <Search className="h-4 w-4 shrink-0 text-muted-foreground" />
            <input
              ref={inputRef}
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setActiveIdx(0);
              }}
              placeholder="Search models or providers"
              className="w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
            />
          </div>

          <div ref={listRef} className="max-h-[22rem] overflow-y-auto py-1">
            {flat.length === 0 && (
              <p className="px-3 py-6 text-center text-sm text-muted-foreground">
                No models match &ldquo;{query}&rdquo;.
              </p>
            )}
            {groups.map((g) => (
              <div key={g.provider} className="mb-1">
                <div className="sticky top-0 z-10 flex items-center gap-2 bg-popover/95 px-3 py-1.5 backdrop-blur">
                  <ProviderMark provider={g.provider} />
                  <span className="text-[0.7rem] font-semibold uppercase tracking-wide text-muted-foreground">
                    {providerLabel(g.provider)}
                  </span>
                  <span className="text-[0.7rem] text-muted-foreground/60">{g.models.length}</span>
                </div>
                {g.models.map((m) => {
                  const idx = flat.findIndex((x) => x.id === m.id);
                  const active = idx === activeIdx;
                  const isSelected = m.id === value;
                  return (
                    <button
                      key={m.id}
                      type="button"
                      data-idx={idx}
                      role="option"
                      aria-selected={isSelected}
                      onMouseEnter={() => setActiveIdx(idx)}
                      onClick={() => commit(m.id)}
                      className={cn(
                        "flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm",
                        active ? "bg-accent" : "bg-transparent",
                      )}
                    >
                      <Check
                        className={cn(
                          "h-4 w-4 shrink-0",
                          isSelected ? "text-primary opacity-100" : "opacity-0",
                        )}
                      />
                      <span className="flex-1 truncate">{modelName(m)}</span>
                      {contextLabel(m) && (
                        <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[0.65rem] font-medium text-muted-foreground">
                          {contextLabel(m)}
                        </span>
                      )}
                      <CostTier model={m} />
                    </button>
                  );
                })}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// ---- model labels + metadata ----------------------------------------------

function modelName(m: AiModel): string {
  if (m.name) {
    const colon = m.name.indexOf(": ");
    return colon === -1 ? m.name : m.name.slice(colon + 2);
  }
  const slash = m.id.indexOf("/");
  return slash === -1 ? m.id : m.id.slice(slash + 1);
}

// contextLabel renders a context window as "128K" / "1M".
function contextLabel(m: AiModel): string {
  const n = m.context_length;
  if (!n || n <= 0) return "";
  if (n >= 1_000_000) return `${Math.round(n / 100_000) / 10}M`.replace(".0M", "M");
  if (n >= 1000) return `${Math.round(n / 1000)}K`;
  return `${n}`;
}

// CostTier renders a three-dot indicator of relative output price. Thresholds are
// USD per token (OpenRouter's unit): free, budget, mid, premium.
function CostTier({ model }: { model: AiModel }) {
  const price = parseFloat(model.pricing?.completion ?? "");
  let filled = 0;
  let title = "Cost unknown";
  if (!Number.isNaN(price)) {
    if (price <= 0) {
      filled = 0;
      title = "Free";
    } else if (price < 0.000002) {
      filled = 1;
      title = "Budget";
    } else if (price < 0.00001) {
      filled = 2;
      title = "Mid";
    } else {
      filled = 3;
      title = "Premium";
    }
  }
  return (
    <span className="flex shrink-0 items-center gap-0.5" title={title} aria-label={title}>
      {[0, 1, 2].map((i) => (
        <span
          key={i}
          className={cn(
            "h-1.5 w-1.5 rounded-full",
            i < filled ? "bg-primary" : "bg-muted-foreground/25",
          )}
        />
      ))}
    </span>
  );
}

// ---- search + grouping ------------------------------------------------------

interface ProviderGroup {
  provider: string;
  models: AiModel[];
}

function filterAndGroup(
  models: AiModel[],
  query: string,
): { groups: ProviderGroup[]; flat: AiModel[] } {
  const q = query.trim().toLowerCase();
  const matches = (m: AiModel) => {
    if (!q) return true;
    const p = providerOf(m.id);
    return (
      m.id.toLowerCase().includes(q) ||
      (m.name ?? "").toLowerCase().includes(q) ||
      providerLabel(p).toLowerCase().includes(q)
    );
  };

  const byProvider = new Map<string, AiModel[]>();
  for (const m of models) {
    if (!matches(m)) continue;
    const key = providerOf(m.id);
    const list = byProvider.get(key);
    if (list) list.push(m);
    else byProvider.set(key, [m]);
  }
  for (const list of byProvider.values()) {
    list.sort((a, b) => modelName(a).localeCompare(modelName(b)));
  }

  const groups = [...byProvider.keys()]
    .sort((a, b) => providerLabel(a).localeCompare(providerLabel(b)))
    .map((provider) => ({ provider, models: byProvider.get(provider)! }));

  const flat = groups.flatMap((g) => g.models);
  return { groups, flat };
}
