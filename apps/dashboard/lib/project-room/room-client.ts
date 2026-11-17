import type {
  loadRoomAction,
  loadRoomDiscussionAction,
} from "@/app/(app)/rooms/actions";

async function read<T>(section: string, roomId: string): Promise<T> {
  const response = await fetch(
    `/api/rooms/${section}?${new URLSearchParams({ roomId })}`,
    { cache: "no-store" },
  );
  if (!response.ok) throw new Error("Room request failed");
  return response.json() as Promise<T>;
}

// Read-only room loads must not serialize behind mutations in Next's action queue.
export const loadRoomDetail = (roomId: string) =>
  read<Awaited<ReturnType<typeof loadRoomAction>>>("detail", roomId);
export const loadRoomDiscussion = (roomId: string) =>
  read<Awaited<ReturnType<typeof loadRoomDiscussionAction>>>(
    "discussion",
    roomId,
  );
