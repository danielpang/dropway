import { loadStaticSnapshotAction } from "@/app/(app)/rooms/actions";

// Read-only previews must not wait behind background work in the action queue.
// The existing loader still checks capabilities, room access and artifact hashes.
export async function GET(request: Request) {
  const query = new URL(request.url).searchParams;
  const roomId = query.get("roomId");
  const siteId = query.get("siteId");
  const versionId = query.get("versionId");
  if (!roomId || !siteId || !versionId) {
    return Response.json({ ok: false, message: "Choose a project, site and revision." }, { status: 400 });
  }
  const result = await loadStaticSnapshotAction(roomId, siteId, versionId, query.get("page") ?? "/");
  return Response.json(result, { headers: { "Cache-Control": "private, no-store" } });
}
