// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { EventEmitter } from "node:events";
import { PassThrough, Writable } from "node:stream";
import { createHash } from "node:crypto";
import { Sprite, type SpriteCommand, type SpawnOptions } from "@fly/sprites";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FlySpriteControlPlane, artifactHash, verifySpriteArtifact, type SpriteArtifactFile } from "../src/sprites-adapter.js";
import { ATTEMPT_EGRESS_POLICY, BUILD_EGRESS_POLICY } from "../src/policy.js";
import type { AttemptSpec } from "../src/control-plane.js";

const spec: AttemptSpec = { orgId: "org1", roomId: "room1", siteId: "site1", versionId: "version1", attemptId: "attempt1", sessionId: "session1", initiatorUserId: "user1", taskId: "task1" };
const file = (path = "index.html", content = "hello"): SpriteArtifactFile => ({ path, contentBase64: Buffer.from(content).toString("base64"), sha256: createHash("sha256").update(content).digest("hex"), size: Buffer.byteLength(content) });

function harness(options: { organization?: string; denyAuthorize?: string; holdAction?: string; output?: string; drift?: boolean; timeout?: number } = {}) {
  const requests: { path: string; method: string; body: unknown }[] = [];
  const rows = new Map<string, Record<string, unknown>>();
  const commands: { command: EventEmitter; path: string; args: string[]; options: SpawnOptions; action?: string; input?: unknown; killed: boolean; closed: boolean }[] = [];
  const authorizations: string[] = [];
  let policy: unknown = ATTEMPT_EGRESS_POLICY, privileges: unknown, resources: unknown;
  const organization = options.organization ?? "development";
  vi.stubGlobal("fetch", vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input));
    const path = url.pathname; const method = init?.method ?? "GET";
    const body: unknown = init?.body ? JSON.parse(init.body as string) : undefined;
    requests.push({ path, method, body });
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer manager-secret");
    if (path === "/v1/sprites" && method === "GET") return Response.json({ name: organization, sprites: [...rows.values()], has_more: false });
    if (path === "/v1/sprites" && method === "POST") {
      const create = body as Record<string, unknown>;
      const row = { ...create, organization, status: "running", created_at: new Date().toISOString() };
      rows.set(create.name as string, row); return Response.json(row);
    }
    const name = path.split("/")[3]!;
    if (path.endsWith("/policy/network")) {
      if (method === "POST") { policy = body; return new Response(null, { status: 204 }); }
      return Response.json(options.drift ? { rules: [] } : policy);
    }
    if (path.endsWith("/policy/privileges")) {
      if (method === "POST") { privileges = body; return new Response(null, { status: 204 }); }
      return Response.json(privileges);
    }
    if (path.endsWith("/policy/resources")) {
      if (method === "POST") { resources = body; return new Response(null, { status: 204 }); }
      return Response.json(resources);
    }
    if (method === "DELETE") { rows.delete(name); return new Response(null, { status: 204 }); }
    const row = rows.get(name);
    return row ? Response.json(row) : Response.json({ error: "not_found" }, { status: 404 });
  }));
  vi.spyOn(Sprite.prototype, "spawn").mockImplementation(function (path, args = [], spawnOptions = {}) {
    const command = new EventEmitter() as EventEmitter & { stdin: Writable; stdout: PassThrough; stderr: PassThrough; close(): void; kill(): void };
    const row = { command, path, args, options: spawnOptions, killed: false, closed: false } as (typeof commands)[number]; commands.push(row);
    command.stdout = new PassThrough(); command.stderr = new PassThrough();
    let input = Buffer.alloc(0);
    command.stdin = new Writable({ write(chunk, _encoding, callback) { input = Buffer.concat([input, chunk]); callback(); }, final(callback) {
      const split = input.indexOf(10); const count = Number(input.subarray(0, split).toString());
      expect(input.subarray(split + 1, split + 1 + count).toString()).toContain("workspace operation rejected");
      const request = JSON.parse(input.subarray(split + 1 + count).toString()) as { action: string; input: unknown };
      row.action = request.action; row.input = request.input;
      callback();
      if (request.action === options.holdAction) return;
      queueMicrotask(() => {
        if (row.closed) return;
        const output = request.action === "export" ? JSON.stringify({ files: [file()], contentHash: artifactHash([file()]) }) :
          request.action === "read" || request.action === "write" || request.action === "edit" ? JSON.stringify({ text: "remote result" }) : request.action === "bash" || request.action === "build" ? options.output ?? "remote shell result" : JSON.stringify({ ok: true });
        command.stdout.emit("data", Buffer.from(output)); command.emit("exit", 0);
      });
    } });
    command.kill = () => { row.killed = true; };
    command.close = () => { row.closed = true; };
    queueMicrotask(() => command.emit("spawn"));
    return command as unknown as SpriteCommand;
  });
  const plane = new FlySpriteControlPlane({ token: "manager-secret", expectedOrganization: "development", namePrefix: "dw-test-", leaseMs: 10_000, operationTimeoutMs: options.timeout ?? 1_000,
    authorize: async (_spec, operation) => { authorizations.push(operation); if (operation === options.denyAuthorize) throw new Error("private upstream credential details"); },
  });
  return { plane, rows, requests, commands, authorizations };
}
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("official Sprite SDK adapter", () => {
  it("creates private scoped resources, checks policy before exec, stages and verifies full artifacts", async () => {
    const h = harness(); const handle = await h.plane.create(spec, ATTEMPT_EGRESS_POLICY);
    const create = h.requests.find((r) => r.method === "POST" && r.path === "/v1/sprites")!.body as Record<string, unknown>;
    expect(create.url_settings).toEqual({ auth: "sprite", private_access: "admins" });
    expect(create.environment).toEqual({}); expect(create.labels).toContain("dropway-room-attempt-v1");
    await h.plane.stage(handle, [file()], new AbortController().signal);
    const result = await h.plane.exportArtifact(handle);
    expect(result).toEqual({ files: [file()], contentHash: artifactHash([file()]) });
    expect(h.authorizations).toEqual(["create", "stage", "export"]);
    for (const command of h.commands) {
      expect(command.path).toBe("/usr/bin/python3"); expect(command.options.env).toEqual({});
      expect(command.options.maxRunAfterDisconnect).toBe("2s");
      expect(JSON.stringify(command.args) + JSON.stringify(command.input)).not.toContain("manager-secret");
      expect(command.closed).toBe(true);
    }
    const lease = (await h.plane.list())[0]!; expect(lease.handle).toEqual(handle);
    await h.plane.destroy(handle, "finish"); await h.plane.destroy(handle, "finish");
    expect(h.rows.size).toBe(0); expect(h.requests.filter((r) => r.method === "DELETE")).toHaveLength(1);
  });
  it("denies wrong organization before creating paid resources", async () => {
    const h = harness({ organization: "production" });
    await expect(h.plane.create(spec, ATTEMPT_EGRESS_POLICY)).rejects.toThrow(/organization/);
    expect(h.requests.some((r) => r.method === "POST")).toBe(false);
  });
  it("destroys when readback is unrestricted and never executes guest code", async () => {
    const h = harness({ drift: true });
    await expect(h.plane.create(spec, ATTEMPT_EGRESS_POLICY)).rejects.toThrow(/unrestricted/);
    expect(h.commands).toHaveLength(0); expect(h.rows.size).toBe(0);
  });
  it("does not permit package downloads without explicit build scope", async () => {
    const h = harness(); await expect(h.plane.create(spec, BUILD_EGRESS_POLICY)).rejects.toThrow(/explicit/);
    expect(h.requests).toHaveLength(0);
  });
  it("reauthorizes before each remote operation and sanitizes upstream errors", async () => {
    const h = harness({ denyAuthorize: "write" }); const handle = await h.plane.create(spec, ATTEMPT_EGRESS_POLICY);
    await expect(h.plane.exec(handle, { kind: "write", attemptId: spec.attemptId, input: { path: "x", content: "data" } }, new AbortController().signal)).rejects.toThrow("Sprite control-plane operation failed");
    expect(h.commands).toHaveLength(1); expect(h.rows.size).toBe(0);
  });
  it("fails closed on path escapes, injected environments and cross-attempt calls", async () => {
    const h = harness(); const handle = await h.plane.create(spec, ATTEMPT_EGRESS_POLICY);
    for (const path of ["../x", "/etc/passwd", "a//b", ".env", "a\\b"]) await expect(h.plane.exec(handle, { kind: "read", attemptId: spec.attemptId, input: { path } }, new AbortController().signal)).rejects.toThrow(/workspace/);
    await expect(h.plane.exec(handle, { kind: "bash", attemptId: spec.attemptId, input: { command: "id", env: { TOKEN: "x" } } }, new AbortController().signal)).rejects.toThrow(/fields/);
    await expect(h.plane.exec(handle, { kind: "bash", attemptId: "other", input: { command: "id" } }, new AbortController().signal)).rejects.toThrow(/attempt/);
    expect(h.commands).toHaveLength(1);
  });
  it("bounds output and destroys timed-out or aborted attempts", async () => {
    let h = harness({ output: "x".repeat(70_000) }); let handle = await h.plane.create(spec, ATTEMPT_EGRESS_POLICY);
    await expect(h.plane.exec(handle, { kind: "bash", attemptId: spec.attemptId, input: { command: "overflow" } }, new AbortController().signal)).rejects.toThrow(/output limit/);
    expect(h.rows.size).toBe(0);
    vi.restoreAllMocks(); vi.unstubAllGlobals(); h = harness({ holdAction: "bash", timeout: 100 }); handle = await h.plane.create(spec, ATTEMPT_EGRESS_POLICY);
    await expect(h.plane.exec(handle, { kind: "bash", attemptId: spec.attemptId, input: { command: "hang" } }, new AbortController().signal)).rejects.toThrow(/timed out/);
    expect(h.rows.size).toBe(0);
    vi.restoreAllMocks(); vi.unstubAllGlobals(); h = harness({ holdAction: "bash" }); handle = await h.plane.create(spec, ATTEMPT_EGRESS_POLICY);
    const controller = new AbortController();
    const pending = h.plane.exec(handle, { kind: "bash", attemptId: spec.attemptId, input: { command: "hang" } }, controller.signal);
    setTimeout(() => controller.abort(), 10);
    await expect(pending).rejects.toThrow(/cancelled/); expect(h.rows.size).toBe(0);
  });
  it("never inventories or destroys unrelated resources", async () => {
    const h = harness(); const handle = await h.plane.create(spec, ATTEMPT_EGRESS_POLICY);
    h.rows.set("dw-test-unrelated", { name: "dw-test-unrelated", organization: "development", labels: [], status: "running" });
    expect(await h.plane.list()).toHaveLength(1);
    await expect(h.plane.destroy({ ...handle, spriteName: "dw-test-unrelated" }, "orphan")).rejects.toThrow(/ownership/);
    expect(h.rows.has("dw-test-unrelated")).toBe(true);
  });
});

describe("untrusted artifact export", () => {
  it("recomputes byte hashes, lengths and canonical manifest outside the guest", () => {
    const original = { files: [file()], contentHash: artifactHash([file()]) };
    expect(verifySpriteArtifact(original)).toEqual(original);
    expect(() => verifySpriteArtifact({ ...original, files: [{ ...file(), sha256: "a".repeat(64) }] })).toThrow(/digest/);
    expect(() => verifySpriteArtifact({ ...original, files: [{ ...file(), size: 0 }] })).toThrow(/digest/);
    expect(() => verifySpriteArtifact({ ...original, files: [file(), file()] })).toThrow(/duplicate/);
    expect(() => verifySpriteArtifact({ ...original, contentHash: "a".repeat(64) })).toThrow(/manifest/);
    expect(() => verifySpriteArtifact({ ...original, files: [file("../escape")] })).toThrow(/workspace/);
  });
});
