import type { StaticSnapshot } from "@/lib/collab/static-snapshot";

export type SnapshotResult = { ok: true; snapshot: StaticSnapshot } | { ok: false; message: string };
export type SnapshotLoader = (roomId: string, siteId: string, versionId: string, page: string) => Promise<SnapshotResult>;

/** Short-lived, bounded memory owned by one mounted workspace, never shared across users. */
export function createPreviewCache(loader: SnapshotLoader) {
  const cached = new Map<string, { snapshot: StaticSnapshot; expires: number; bytes: number }>();
  const pending = new Map<string, Promise<SnapshotResult>>();
  const keyFor = (args: Parameters<SnapshotLoader>) => JSON.stringify(args);
  const ttl = 60_000;
  const maxBytes = 12 * 1024 * 1024;
  let bytes = 0;
  function remove(key: string) {
    bytes -= cached.get(key)?.bytes ?? 0;
    cached.delete(key);
  }
  function peek(...args: Parameters<SnapshotLoader>) {
    const key = keyFor(args);
    const entry = cached.get(key);
    if (!entry) return null;
    if (entry.expires <= Date.now()) { remove(key); return null; }
    cached.delete(key);
    cached.set(key, entry);
    return entry.snapshot;
  }
  function load(...args: Parameters<SnapshotLoader>): Promise<SnapshotResult> {
    const hit = peek(...args);
    if (hit) return Promise.resolve({ ok: true, snapshot: hit });
    const key = keyFor(args);
    const inFlight = pending.get(key);
    if (inFlight) return inFlight;
    const request = loader(...args).then((result) => {
      if (result.ok) {
        const snapshot = result.snapshot;
        if (snapshot.siteId !== args[1] || snapshot.versionId !== args[2] || snapshot.page !== args[3]) {
          return { ok: false as const, message: "The requested revision could not be verified." };
        }
        const size = snapshot.html.length * 2;
        if (size <= maxBytes) {
          remove(key);
          cached.set(key, { snapshot, expires: Date.now() + ttl, bytes: size });
          bytes += size;
          while (cached.size > 6 || bytes > maxBytes) remove(cached.keys().next().value!);
        }
      }
      return result;
    }).finally(() => { pending.delete(key); });
    pending.set(key, request);
    return request;
  }
  return { peek, load };
}
