"use client";

import * as React from "react";
import {
  ChevronRight,
  Columns2,
  Folder,
  FolderOpen,
  Globe,
  ImageIcon,
  LayoutGrid,
  Plus,
  Search,
  Settings,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import type { ProjectRoom } from "@/lib/project-room/contract";
import type { RoomSiteOption } from "./room-workspace";

export function RoomDirectory({
  rooms,
  room,
  sites,
  loading,
  error,
  onChooseRoom,
  onOpenSite,
  onCreate,
  onSettings,
  onRetry,
  addSite,
}: {
  rooms: ProjectRoom[];
  room: ProjectRoom | null;
  sites: RoomSiteOption[];
  loading: boolean;
  error?: string;
  onChooseRoom: (id: string) => void;
  onOpenSite: (id: string) => void;
  onCreate: () => void;
  onSettings: () => void;
  onRetry: () => void;
  addSite: React.ReactNode;
}) {
  const [view, setView] = React.useState<"columns" | "cards">("columns");
  const [query, setQuery] = React.useState("");
  React.useEffect(() => setQuery(""), [room?.ID]);
  const matches = (text: string) =>
    text.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase());
  const visibleRooms = room
    ? rooms
    : rooms.filter((item) => matches(item.Name));
  const visibleSites = sites.filter((item) =>
    matches(`${item.label ?? item.slug} ${item.kind ?? "site"}`),
  );
  const roomButton = (item: ProjectRoom, card: boolean) => (
    <button
      key={item.ID}
      type="button"
      onClick={() => onChooseRoom(item.ID)}
      aria-label={`Open room ${item.Name}`}
      aria-current={room?.ID === item.ID ? "true" : undefined}
      className={cn(
        "group min-w-0 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
        card
          ? "rounded-xl border border-border p-5 hover:bg-muted/40"
          : "flex w-full items-center gap-3 rounded-md px-3 py-3",
        room?.ID === item.ID ? "bg-secondary" : "hover:bg-muted/50",
      )}
    >
      <Folder
        className={cn(
          "shrink-0 text-muted-foreground",
          card ? "mb-6 h-9 w-9 stroke-[1.3]" : "h-5 w-5",
        )}
        aria-hidden="true"
      />
      <span className="block min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{item.Name}</span>
        <span className="mt-1 block text-xs capitalize text-muted-foreground">
          {item.Role}
        </span>
      </span>
      {item.Unread > 0 ? (
        <span
          className={cn(
            "rounded bg-muted px-1.5 py-0.5 text-xs tabular-nums",
            card && "mt-3 inline-block",
          )}
        >
          {item.Unread} unread
        </span>
      ) : null}
      {!card ? (
        <ChevronRight
          className="h-4 w-4 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
      ) : null}
    </button>
  );
  const resources = (
    <section
      aria-label={room ? `${room.Name} resources` : "Room contents"}
      className="min-w-0 flex-1"
    >
      {!room ? (
        <div className="flex min-h-80 flex-col items-center justify-center gap-3 p-8 text-center text-muted-foreground">
          <FolderOpen className="h-10 w-10 stroke-[1.2]" aria-hidden="true" />
          <p className="text-sm">
            Choose a room to browse its sites and images.
          </p>
        </div>
      ) : (
        <>
          <header className="flex items-center justify-between gap-3 border-b border-border px-5 py-4">
            <div className="min-w-0">
              <h2 className="truncate text-sm font-semibold">{room.Name}</h2>
              <p className="mt-1 text-xs text-muted-foreground">
                {loading
                  ? "Loading contents…"
                  : `${sites.length} ${sites.length === 1 ? "item" : "items"}`}
              </p>
            </div>
            <Button
              size="icon"
              variant="ghost"
              aria-label="Room settings"
              onClick={onSettings}
            >
              <Settings className="h-4 w-4" aria-hidden="true" />
            </Button>
          </header>
          {error ? (
            <div role="alert" className="p-6 text-sm">
              <p>{error}</p>
              <Button variant="outline" className="mt-3" onClick={onRetry}>
                Retry loading room
              </Button>
            </div>
          ) : loading ? (
            <div
              role="status"
              aria-label="Loading room resources"
              className="space-y-3 p-5"
            >
              {[0, 1, 2].map((i) => (
                <div
                  key={i}
                  className="h-16 animate-pulse rounded-lg bg-muted/50 motion-reduce:animate-none"
                />
              ))}
            </div>
          ) : (
            <>
              <div
                className={
                  view === "cards"
                    ? "grid grid-cols-1 gap-3 p-5 sm:grid-cols-2 xl:grid-cols-3"
                    : "space-y-1 p-2"
                }
              >
                {visibleSites.map((site) => {
                  const Icon = site.kind === "image" ? ImageIcon : Globe;
                  return (
                    <button
                      key={site.id}
                      type="button"
                      aria-label={`Open ${site.kind === "image" ? "image" : "site"} ${site.label ?? site.slug}`}
                      onClick={() => onOpenSite(site.id)}
                      className={cn(
                        "group min-w-0 text-left transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
                        view === "cards"
                          ? "overflow-hidden rounded-lg border border-border"
                          : "flex w-full items-center gap-3 rounded-md px-3 py-3",
                      )}
                    >
                      <span
                        className={cn(
                          "flex shrink-0 items-center justify-center bg-muted/40 text-muted-foreground",
                          view === "cards"
                            ? "h-28 border-b border-border"
                            : "h-10 w-10 rounded-lg",
                        )}
                      >
                        <Icon
                          className={
                            view === "cards"
                              ? "h-9 w-9 stroke-[1.3]"
                              : "h-5 w-5"
                          }
                          aria-hidden="true"
                        />
                      </span>
                      <span
                        className={cn(
                          "block min-w-0 flex-1",
                          view === "cards" && "p-4",
                        )}
                      >
                        <span className="block truncate text-sm font-medium">
                          {site.label ?? site.slug}
                        </span>
                        <span className="mt-1 block text-xs text-muted-foreground">
                          {site.kind === "image" ? "Image" : "Site"} ·{" "}
                          {site.currentVersionId
                            ? "Ready to review"
                            : "Awaiting upload"}
                        </span>
                      </span>
                      {view === "columns" ? (
                        <ChevronRight
                          className="h-4 w-4 shrink-0 text-muted-foreground"
                          aria-hidden="true"
                        />
                      ) : null}
                    </button>
                  );
                })}
              </div>
              {visibleSites.length === 0 ? (
                <p className="p-8 text-center text-sm text-muted-foreground">
                  {query
                    ? "No items match your search."
                    : "This room is empty. Add a site or image to get started."}
                </p>
              ) : null}
              {addSite ? (
                <div className="border-t border-border px-5 py-3">
                  {addSite}
                </div>
              ) : null}
            </>
          )}
        </>
      )}
    </section>
  );
  return (
    <div className="mx-auto max-w-7xl space-y-6 p-4 sm:p-8 lg:p-10">
      <header className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-medium tracking-tight">Rooms</h1>
          <p className="mt-1.5 text-sm text-muted-foreground">
            A place for every project. Sites, images, and feedback together.
          </p>
        </div>
        <Button onClick={onCreate}>
          <Plus className="mr-2 h-4 w-4" aria-hidden="true" />
          New room
        </Button>
      </header>
      <div className="overflow-hidden rounded-xl border border-border bg-card">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
          <nav
            aria-label="Room directory"
            className="flex min-w-0 items-center gap-2 text-sm"
          >
            <button
              type="button"
              onClick={() => onChooseRoom("")}
              className="rounded px-1 py-1 text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              All rooms
            </button>
            {room ? (
              <>
                <ChevronRight
                  className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
                  aria-hidden="true"
                />
                <span
                  className="max-w-48 truncate font-medium"
                  aria-current="page"
                >
                  {room.Name}
                </span>
              </>
            ) : null}
          </nav>
          <div className="flex w-full items-center gap-3 sm:w-auto">
            <div className="relative min-w-0 flex-1">
              <Search
                className="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground"
                aria-hidden="true"
              />
              <Input
                aria-label={room ? "Search room contents" : "Search rooms"}
                placeholder={room ? "Search contents…" : "Search rooms…"}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                className="h-9 pl-8 sm:w-52"
              />
            </div>
            <div
              className="flex shrink-0 rounded-md border border-border p-0.5"
              role="group"
              aria-label="Directory view"
            >
              {(["columns", "cards"] as const).map((value) => (
                <button
                  type="button"
                  key={value}
                  aria-label={`${value === "columns" ? "Columns" : "Cards"} view`}
                  aria-pressed={view === value}
                  onClick={() => setView(value)}
                  className={cn(
                    "rounded px-2 py-1.5 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                    view === value
                      ? "bg-secondary text-foreground"
                      : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  {React.createElement(
                    value === "columns" ? Columns2 : LayoutGrid,
                    { className: "h-4 w-4", "aria-hidden": true },
                  )}
                </button>
              ))}
            </div>
          </div>
        </div>
        <div
          className={
            view === "columns"
              ? "grid min-h-[26rem] md:grid-cols-[17rem_minmax(0,1fr)]"
              : "min-h-[26rem]"
          }
        >
          {view === "columns" || !room ? (
            <section
              aria-label="Room list"
              className={cn(
                view === "columns"
                  ? "border-border p-2 md:border-r"
                  : "grid gap-4 p-5 sm:grid-cols-2 lg:grid-cols-3",
                room && "hidden md:block",
              )}
            >
              {visibleRooms.map((item) => roomButton(item, view === "cards"))}
              {visibleRooms.length === 0 ? (
                <p className="p-6 text-sm text-muted-foreground">
                  {query
                    ? "No rooms match your search."
                    : "No rooms yet. Create your first room to get started."}
                </p>
              ) : null}
            </section>
          ) : null}
          {view === "columns" || room ? (
            <div className={cn("min-w-0", !room && "hidden md:block")}>
              {resources}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}
