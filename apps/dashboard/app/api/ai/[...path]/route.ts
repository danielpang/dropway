import { NextRequest } from "next/server";

import { mintApiToken } from "@/lib/api";
import { API_URL } from "@/lib/env";

/**
 * SSE / JSON proxy for the AI builder's Go API routes.
 *
 * Server actions can't stream, and the browser never holds the Better Auth API
 * JWT (it's minted server-side per request). This route handler bridges the gap:
 * it authenticates the dashboard session, mints the same short-lived JWT the
 * typed client uses, and pipes the upstream response straight through unbuffered
 * so the builder chat streams live (text/event-stream) without the JSON path.
 *
 * It forwards to /v1/ai/<path> only, so it can never be used to reach an
 * arbitrary API route.
 */

export const dynamic = "force-dynamic";

// A builder turn streams SSE through this route for minutes. Without an explicit
// maxDuration, Vercel's default function limit can cut the stream short the same
// way the Go server's WriteTimeout used to. 300s is the Fluid Compute ceiling on
// the Hobby plan; on Pro this can go up to 800 to cover the API's full 10-minute
// turn deadline.
export const maxDuration = 300;

async function proxy(req: NextRequest, path: string[]): Promise<Response> {
  const token = await mintApiToken();
  if (!token) {
    return new Response(JSON.stringify({ error: "unauthorized" }), {
      status: 401,
      headers: { "Content-Type": "application/json" },
    });
  }

  const suffix = path.join("/");
  const search = req.nextUrl.search;
  const upstreamURL = `${API_URL}/v1/ai/${suffix}${search}`;

  const headers: Record<string, string> = {
    Authorization: `Bearer ${token}`,
    Accept: req.headers.get("accept") ?? "application/json",
  };
  const lastEventId = req.headers.get("last-event-id");
  if (lastEventId) headers["Last-Event-ID"] = lastEventId;

  const method = req.method;
  const hasBody = method !== "GET" && method !== "HEAD";
  if (hasBody) headers["Content-Type"] = "application/json";

  const upstream = await fetch(upstreamURL, {
    method,
    headers,
    body: hasBody ? await req.text() : undefined,
    // Never cache AI responses; stream them through.
    cache: "no-store",
    // @ts-expect-error - duplex is required by Node fetch for streamed bodies.
    duplex: "half",
  });

  // Pipe the upstream body through unbuffered. For SSE this keeps the stream
  // live; for JSON it's a straight pass-through.
  const responseHeaders = new Headers();
  const contentType = upstream.headers.get("content-type");
  if (contentType) responseHeaders.set("Content-Type", contentType);
  responseHeaders.set("Cache-Control", "no-store");
  responseHeaders.set("X-Accel-Buffering", "no");

  return new Response(upstream.body, {
    status: upstream.status,
    headers: responseHeaders,
  });
}

export async function GET(
  req: NextRequest,
  ctx: { params: Promise<{ path: string[] }> },
) {
  const { path } = await ctx.params;
  return proxy(req, path);
}

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ path: string[] }> },
) {
  const { path } = await ctx.params;
  return proxy(req, path);
}

export async function DELETE(
  req: NextRequest,
  ctx: { params: Promise<{ path: string[] }> },
) {
  const { path } = await ctx.params;
  return proxy(req, path);
}
