"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import {
  Ban,
  CircleCheck,
  Loader2,
  Pencil,
  Pin,
  PinOff,
  Plus,
  Search,
  Trash2,
} from "lucide-react";

import {
  createMemoryAction,
  deleteMemoryAction,
  patchMemoryAction,
} from "@/app/(app)/settings/memory/actions";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import type { Memory, MemoryKind } from "@/lib/api";

/** The kinds a memory can carry (also the filter chips). */
const KINDS: MemoryKind[] = [
  "fact",
  "preference",
  "style",
  "correction",
  "manual",
];

const SOURCE_LABELS: Record<string, string> = {
  ai_session: "AI builder session",
  chat_log: "Shared chat",
  site_version: "Site deploy",
  manual: "Added manually",
};

const TEXTAREA_CLASS =
  "flex w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50";

export interface MemoryFilters {
  q: string;
  kind: string;
  pinned: boolean;
  disabled: boolean;
}

/**
 * The company-memory management view: search + kind/pinned/disabled filters
 * (URL-driven, like the skills library), one Card row per memory with
 * pin/disable/edit/delete (admin-only — the Go API re-checks on every write),
 * and a member-allowed "Add memory" form. The server page already resolved the
 * org flag; this view only renders when memory is enabled.
 */
export function MemoryView(props: {
  memories: Memory[];
  canManage: boolean;
  filters: MemoryFilters;
  count: number;
  /** The org's memory cap; 0 = unlimited. */
  max: number;
  loadError: string | null;
}) {
  const { memories, canManage, filters, count, max, loadError } = props;
  const router = useRouter();
  const [query, setQuery] = React.useState(filters.q);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState<string | null>(null); // key of the in-flight action
  const [adding, setAdding] = React.useState(false);

  const hasFilters =
    Boolean(filters.q) || Boolean(filters.kind) || filters.pinned || filters.disabled;

  const navigate = (next: Partial<MemoryFilters>) => {
    const merged = { ...filters, ...next };
    const params = new URLSearchParams();
    if (merged.q) params.set("q", merged.q);
    if (merged.kind) params.set("kind", merged.kind);
    if (merged.pinned) params.set("pinned", "true");
    if (merged.disabled) params.set("disabled", "true");
    const qs = params.toString();
    router.push(`/settings/memory${qs ? `?${qs}` : ""}`);
  };

  const run = async (
    key: string,
    fn: () => Promise<{ ok: boolean; message?: string }>,
  ) => {
    setBusy(key);
    setError(null);
    setNotice(null);
    const res = await fn();
    if (!res.ok) setError(res.message ?? "Something went wrong.");
    setBusy(null);
    router.refresh();
  };

  return (
    <div className="space-y-6">
      {/* Search + filter chips + add. */}
      <div className="flex flex-wrap items-center gap-2">
        <form
          className="relative"
          onSubmit={(e) => {
            e.preventDefault();
            navigate({ q: query });
          }}
        >
          <Search className="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search memories…"
            className="w-56 pl-8"
            aria-label="Search memories"
          />
        </form>
        <Button
          variant={!filters.kind && !filters.pinned ? "secondary" : "ghost"}
          size="sm"
          onClick={() => navigate({ kind: "", pinned: false })}
        >
          All
        </Button>
        {KINDS.map((kind) => (
          <Button
            key={kind}
            variant={filters.kind === kind ? "secondary" : "ghost"}
            size="sm"
            onClick={() =>
              navigate({ kind: filters.kind === kind ? "" : kind })
            }
          >
            {kind}
          </Button>
        ))}
        <Button
          variant={filters.pinned ? "secondary" : "ghost"}
          size="sm"
          onClick={() => navigate({ pinned: !filters.pinned })}
          title="Only pinned memories (always recalled)"
        >
          <Pin className="mr-1 h-3.5 w-3.5" /> Pinned
        </Button>
        <Button
          variant={filters.disabled ? "secondary" : "ghost"}
          size="sm"
          onClick={() => navigate({ disabled: !filters.disabled })}
          title="Include memories that are kept but never recalled"
        >
          Show disabled
        </Button>
        <span className="flex-1" />
        <Button size="sm" onClick={() => setAdding((v) => !v)}>
          <Plus className="mr-1 h-4 w-4" /> Add memory
        </Button>
      </div>

      {/* Usage line. */}
      <p className="text-xs text-muted-foreground">
        {count === 1 ? "1 memory stored" : `${count} memories stored`}
        {max > 0 && <> of {max}</>}
      </p>

      {error && (
        <p
          role="alert"
          className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {error}
        </p>
      )}
      {notice && (
        <p
          role="status"
          className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700 dark:text-emerald-400"
        >
          {notice}
        </p>
      )}

      {adding && (
        <AddMemoryForm
          busy={busy === "create"}
          onCancel={() => setAdding(false)}
          onSubmit={(content, kind) =>
            run("create", async () => {
              const res = await createMemoryAction({ content, kind });
              if (!res.ok) return res;
              setAdding(false);
              setNotice(
                res.created
                  ? "Memory saved."
                  : "Already remembered — an equivalent memory exists.",
              );
              return { ok: true };
            })
          }
        />
      )}

      {loadError && (
        <Card className="p-6 text-sm text-muted-foreground">
          Couldn&apos;t load memories: {loadError}
        </Card>
      )}

      {/* The list. */}
      {memories.length === 0 && !loadError ? (
        hasFilters ? (
          <Card className="p-10 text-center text-sm text-muted-foreground">
            No memories match your filters.
          </Card>
        ) : (
          <Card className="space-y-3 p-10 text-center text-sm text-muted-foreground">
            <p className="font-medium text-foreground">Nothing remembered yet</p>
            <p className="mx-auto max-w-md">
              Dropway learns durable facts about your organization from AI
              builds, shared chats, sites and skills — your brand voice, your
              stack, your preferences — and recalls them in future work. You can
              also add memories by hand.
            </p>
            <div>
              <Button size="sm" onClick={() => setAdding(true)}>
                <Plus className="mr-1 h-4 w-4" /> Add the first memory
              </Button>
            </div>
          </Card>
        )
      ) : (
        <div className="space-y-3">
          {memories.map((memory) => (
            <MemoryRow
              key={memory.id}
              memory={memory}
              canManage={canManage}
              busy={busy}
              run={run}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ---- Add form ---------------------------------------------------------------

function AddMemoryForm(props: {
  busy: boolean;
  onCancel: () => void;
  onSubmit: (content: string, kind: MemoryKind) => void;
}) {
  const { busy, onCancel, onSubmit } = props;
  const [content, setContent] = React.useState("");
  const [kind, setKind] = React.useState<MemoryKind>("manual");

  return (
    <Card className="p-4">
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (!content.trim()) return;
          onSubmit(content, kind);
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="memory-content">What should Dropway remember?</Label>
          <textarea
            id="memory-content"
            value={content}
            rows={3}
            autoFocus
            placeholder="e.g. Our brand voice is friendly but concise; headlines never use exclamation marks."
            onChange={(e) => setContent(e.target.value)}
            className={TEXTAREA_CLASS}
          />
        </div>
        <div className="flex flex-wrap items-end gap-2">
          <div className="w-44 space-y-2">
            <Label htmlFor="memory-kind">Kind</Label>
            <Select
              id="memory-kind"
              value={kind}
              onChange={(e) => setKind(e.target.value as MemoryKind)}
            >
              {KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </Select>
          </div>
          <span className="flex-1" />
          <Button type="button" variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
          <Button type="submit" disabled={busy || !content.trim()}>
            {busy && <Loader2 className="mr-1 h-4 w-4 animate-spin" />}
            Save memory
          </Button>
        </div>
      </form>
    </Card>
  );
}

// ---- One memory row ---------------------------------------------------------

const TRUNCATE_AT = 280;

function MemoryRow(props: {
  memory: Memory;
  canManage: boolean;
  busy: string | null;
  run: (
    key: string,
    fn: () => Promise<{ ok: boolean; message?: string }>,
  ) => Promise<void>;
}) {
  const { memory, canManage, busy, run } = props;
  const [expanded, setExpanded] = React.useState(false);
  const [editing, setEditing] = React.useState(false);
  const [draft, setDraft] = React.useState(memory.content);
  const [draftKind, setDraftKind] = React.useState(memory.kind);

  const long = memory.content.length > TRUNCATE_AT;
  const shown =
    long && !expanded ? `${memory.content.slice(0, TRUNCATE_AT)}…` : memory.content;

  const adminTitle = canManage
    ? undefined
    : "Only owners and admins can manage memories.";

  const startEdit = () => {
    setDraft(memory.content);
    setDraftKind(memory.kind);
    setEditing(true);
  };

  return (
    <Card
      className={`space-y-2 p-4 ${memory.disabled ? "opacity-60" : ""}`}
    >
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="secondary">{memory.kind}</Badge>
        {memory.pinned && (
          <Badge variant="success">
            <Pin className="h-3 w-3" /> pinned
          </Badge>
        )}
        {memory.disabled && <Badge variant="muted">disabled</Badge>}
        {memory.distance != null && (
          <Badge variant="outline" title="Vector distance to your search">
            {memory.distance.toFixed(3)}
          </Badge>
        )}
        <span className="flex-1" />
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="sm"
            disabled={!canManage || busy === `pin:${memory.id}`}
            title={adminTitle ?? (memory.pinned ? "Unpin (recall by relevance)" : "Pin (always recalled)")}
            onClick={() =>
              void run(`pin:${memory.id}`, () =>
                patchMemoryAction({ id: memory.id, pinned: !memory.pinned }),
              )
            }
          >
            {memory.pinned ? (
              <PinOff className="h-4 w-4" />
            ) : (
              <Pin className="h-4 w-4" />
            )}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={!canManage || busy === `disable:${memory.id}`}
            title={adminTitle ?? (memory.disabled ? "Enable (recall again)" : "Disable (keep but never recall)")}
            onClick={() =>
              void run(`disable:${memory.id}`, () =>
                patchMemoryAction({ id: memory.id, disabled: !memory.disabled }),
              )
            }
          >
            {memory.disabled ? (
              <CircleCheck className="h-4 w-4" />
            ) : (
              <Ban className="h-4 w-4" />
            )}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={!canManage || editing}
            title={adminTitle ?? "Edit"}
            onClick={startEdit}
          >
            <Pencil className="h-4 w-4" />
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={!canManage || busy === `rm:${memory.id}`}
            title={adminTitle ?? "Delete"}
            onClick={() => {
              if (
                !window.confirm(
                  "Delete this memory for the whole org? This can't be undone.",
                )
              )
                return;
              void run(`rm:${memory.id}`, () =>
                deleteMemoryAction({ id: memory.id }),
              );
            }}
          >
            {busy === `rm:${memory.id}` ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Trash2 className="h-4 w-4" />
            )}
          </Button>
        </div>
      </div>

      {editing ? (
        <form
          className="space-y-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (!draft.trim()) return;
            void run(`edit:${memory.id}`, async () => {
              const res = await patchMemoryAction({
                id: memory.id,
                content: draft,
                kind: draftKind,
              });
              if (res.ok) setEditing(false);
              return res;
            });
          }}
        >
          <textarea
            value={draft}
            rows={4}
            autoFocus
            aria-label="Memory content"
            onChange={(e) => setDraft(e.target.value)}
            className={TEXTAREA_CLASS}
          />
          <div className="flex items-center gap-2">
            <Select
              value={draftKind}
              aria-label="Memory kind"
              className="h-9 w-40"
              onChange={(e) => setDraftKind(e.target.value)}
            >
              {(KINDS as string[]).includes(memory.kind) ? null : (
                <option value={memory.kind}>{memory.kind}</option>
              )}
              {KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </Select>
            <span className="flex-1" />
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setEditing(false)}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              size="sm"
              disabled={busy === `edit:${memory.id}` || !draft.trim()}
            >
              {busy === `edit:${memory.id}` && (
                <Loader2 className="mr-1 h-4 w-4 animate-spin" />
              )}
              Save
            </Button>
          </div>
        </form>
      ) : (
        <>
          <p className="whitespace-pre-wrap text-sm text-foreground">{shown}</p>
          {long && (
            <button
              type="button"
              className="text-xs font-medium text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
              onClick={() => setExpanded((v) => !v)}
            >
              {expanded ? "Show less" : "Show more"}
            </button>
          )}
        </>
      )}

      <p className="text-xs text-muted-foreground">
        {SOURCE_LABELS[memory.source_kind] ?? memory.source_kind}
        {memory.source_tool && <> · via {memory.source_tool}</>}
        {memory.updated_at && <> · updated {formatDate(memory.updated_at)}</>}
        {memory.last_used_at && (
          <> · last recalled {formatDate(memory.last_used_at)}</>
        )}
      </p>
    </Card>
  );
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}
