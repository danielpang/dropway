import type { SnapshotLoader, SnapshotResult } from "./preview-cache";

export const loadRoomPreview: SnapshotLoader = async (roomId, siteId, versionId, page) => {
  const query = new URLSearchParams({ roomId, siteId, versionId, page });
  const response = await fetch(`/api/rooms/preview?${query}`, { cache: "no-store" });
  if (!response.ok) throw new Error("Preview request failed");
  return response.json() as Promise<SnapshotResult>;
};

export async function loadRoomRevisions(siteId: string, signal: AbortSignal): Promise<
  { ok: true; versions: { id: string; number: number }[] } | { ok: false }
> {
  const response = await fetch(`/api/rooms/revisions?${new URLSearchParams({ siteId })}`, { cache: "no-store", signal });
  if (!response.ok) throw new Error("Revisions request failed");
  return response.json();
}
