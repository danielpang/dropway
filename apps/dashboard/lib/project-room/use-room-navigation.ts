"use client";

import * as React from "react";
import { useSearchParams } from "next/navigation";

export interface RoomLocation {
  roomId: string;
  siteId: string;
  workspace: boolean;
  create?: boolean;
}

/** Native history keeps directory navigation instant and supports Back/Forward.
 * Next's search params also update when the header links back to /rooms. */
export function useRoomNavigation() {
  const query = useSearchParams();
  const [threadLink, setThreadLink] = React.useState(false);
  React.useEffect(() => {
    const read = () =>
      setThreadLink(window.location.hash.startsWith("#thread-"));
    read();
    window.addEventListener("hashchange", read);
    window.addEventListener("popstate", read);
    return () => {
      window.removeEventListener("hashchange", read);
      window.removeEventListener("popstate", read);
    };
  }, [query]);
  const roomId = query.get("room") ?? "";
  const siteId = query.get("site") ?? "";
  const location: RoomLocation = {
    roomId,
    siteId,
    create: query.get("view") === "create",
    workspace: Boolean(
      roomId && (siteId || threadLink || query.get("view") === "workspace"),
    ),
  };
  const navigate = React.useCallback((next: RoomLocation) => {
    const url = new URL(window.location.href);
    for (const key of ["room", "site", "view"]) url.searchParams.delete(key);
    if (next.roomId) url.searchParams.set("room", next.roomId);
    if (next.siteId) url.searchParams.set("site", next.siteId);
    if (next.create) url.searchParams.set("view", "create");
    else if (next.workspace && !next.siteId)
      url.searchParams.set("view", "workspace");
    url.hash = "";
    setThreadLink(false);
    if (url.href !== window.location.href)
      window.history.pushState(null, "", url);
  }, []);
  return { location, navigate };
}
