import { loadRevisionNumbersAction } from "@/app/(app)/rooms/actions";

export async function GET(request: Request) {
  const siteId = new URL(request.url).searchParams.get("siteId");
  if (!siteId) return Response.json({ ok: false }, { status: 400 });
  const result = await loadRevisionNumbersAction(siteId);
  return Response.json(result, { headers: { "Cache-Control": "private, no-store" } });
}
