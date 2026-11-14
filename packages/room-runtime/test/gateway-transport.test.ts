// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import { GatewayObserver, withControlPlaneTransport, withGatewayFetch } from "../src/gateway.js";
afterEach(() => vi.unstubAllGlobals());
it("limits model requests to the exact attempt endpoint while isolating trusted SDK transport", async () => {
  const transport = vi.fn(async (_input: Parameters<typeof fetch>[0], _init?: RequestInit) => new Response(null, { status: 204 }));
  vi.stubGlobal("fetch", transport);
  const base = "https://api.example/internal/room-gateway/attempt-1";
  await withGatewayFetch(base, new GatewayObserver(), async () => {
    for (const url of ["https://api.sprites.dev/v1/sprites", "https://api.example/internal/room-attempts/attempt-1/candidate", `${base}/v1/messages?secret=x`, `${base}/v1/messages#fragment`, `${base.replace("attempt-1", "attempt-2")}/v1/messages`]) {
      await expect(fetch(url)).rejects.toThrow("outside the model gateway");
    }
    await withControlPlaneTransport(async () => { await Promise.resolve(); await fetch("https://api.sprites.dev/v1/sprites"); });
    await expect(fetch("https://api.sprites.dev/v1/sprites")).rejects.toThrow();
    await fetch(`${base}/v1/messages`, { redirect: "follow" });
    expect(transport.mock.calls).toHaveLength(2);
    expect(transport.mock.calls[1]?.[1]).toMatchObject({ redirect: "error" });
  });
  expect(globalThis.fetch).toBe(transport);
});
it("restores fetch and transport state after a failed attempt", async () => {
  const transport = vi.fn(async (_input: Parameters<typeof fetch>[0], _init?: RequestInit) => new Response(null, { status: 204 })); vi.stubGlobal("fetch", transport);
  await expect(withGatewayFetch("https://api.example/a", new GatewayObserver(), async () => { throw new Error("cancel"); })).rejects.toThrow("cancel");
  expect(globalThis.fetch).toBe(transport);
  await withGatewayFetch("https://api.example/b", new GatewayObserver(), async () => { await fetch("https://api.example/b/v1/chat/completions"); });
});
