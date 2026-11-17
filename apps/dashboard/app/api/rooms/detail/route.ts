import { loadRoomAction } from "@/app/(app)/rooms/actions";

export async function GET(request: Request) {
  const roomId = new URL(request.url).searchParams.get("roomId");
  if (!roomId)
    return Response.json(
      { ok: false, message: "Choose a room." },
      { status: 400 },
    );
  return Response.json(await loadRoomAction(roomId), {
    headers: { "Cache-Control": "private, no-store" },
  });
}
