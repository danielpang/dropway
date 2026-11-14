// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { mkdir, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { mkdtemp } from "node:fs/promises";

import { afterEach, describe, expect, it } from "vitest";

import { ATTEMPT_EGRESS_POLICY, RoomWorker } from "../src/index.js";
import { FakeGateway } from "./fake-gateway.js";
import { FakePlane } from "./fake-plane.js";
import { MemoryClarificationStore, SENTINELS, capability, pin, withSentinels } from "./support.js";

const gateways: FakeGateway[] = [];

afterEach(async () => {
  await Promise.all(gateways.splice(0).map((gateway) => gateway.stop()));
});

describe("Pi remote attempt", () => {
  it("streams through the gateway capability and never the provider key", async () => {
    const restore = withSentinels();
    try {
      const { worker, gateway, run } = await stack();
      gateway.steps = [{ kind: "text", text: "hello", usage: { input: 4, output: 2 } }];
      const result = await worker.run(run, capability(run), "say hello");
      expect(result.status).toBe("completed");
      expect(result.deltas.join("")).toBe("hello");
      expect(result.text).toBe("hello");
      expect(result.usage).toEqual({ input: 4, output: 2, total: 6 });
      expect(result.destroyedReason).toBe("finish");
      expect(result.activeTools.sort()).toEqual(["bash", "build", "edit", "read", "write"]);
      expect(result.extensionsLoaded).toBe(0);
      const request = gateway.requests[0];
      expect(request?.url).toContain("/v1/messages");
      expect(request?.headers["x-api-key"]).toBe("dw_gateway_attempttoken");
      expect(JSON.stringify(request)).not.toContain("sk-ant-sentinel");
      expect(JSON.stringify(request)).not.toContain("fly-sentinel-token");
      for (const sentinel of SENTINELS) expect(JSON.stringify(gateway.requests)).not.toContain(sentinel);
    } finally {
      restore();
    }
  });

  it("routes openrouter through the same gateway", async () => {
    const restore = withSentinels();
    try {
      const { worker, gateway } = await stack();
      const run = pin({ provider: "openrouter", modelId: "anthropic/claude-sonnet-4-5" });
      gateway.steps = [{ kind: "text", text: "routed", usage: { input: 2, output: 2 } }];
      const result = await worker.run(run, capability(run), "hi");
      expect(result.status).toBe("completed");
      expect(result.text).toBe("routed");
      expect(gateway.requests[0]?.url).toContain("/chat/completions");
      const headers = JSON.stringify(gateway.requests[0]?.headers);
      expect(headers).toContain("dw_gateway_attempttoken");
      expect(headers).not.toContain("sk-or-sentinel");
    } finally {
      restore();
    }
  });

  it.each(["gpt-4.1", "gpt-4.1-mini"])("runs %s tool calls through the pinned OpenAI gateway with final usage", async (modelId) => {
    const restore = withSentinels();
    try {
      const { worker, gateway, plane } = await stack();
      const run = pin({ provider: "openai", modelId });
      gateway.steps = [
        { kind: "tool", name: "read", input: { path: "index.html" }, usage: { input: 3, output: 2 } },
        { kind: "text", text: "reviewed", usage: { input: 2, output: 1 } },
      ];
      const result = await worker.run(run, capability(run), "read the page");
      expect(result.status, JSON.stringify(result)).toBe("completed");
      expect(result.usage).toEqual({ input: 5, output: 3, total: 8 });
      expect(plane.ops.map(op => op.kind)).toEqual(["read"]);
      expect(gateway.requests).toHaveLength(2);
      for (const request of gateway.requests) {
        expect(request.url).toBe("/v1/chat/completions");
        expect(request.headers.authorization).toBe("Bearer dw_gateway_attempttoken");
        const body = JSON.parse(request.body);
        expect(body.model).toBe(modelId);
        expect(body.stream_options).toEqual({ include_usage: true });
        expect(body.max_completion_tokens).toBeUndefined();
        for (const sentinel of SENTINELS) expect(JSON.stringify(request)).not.toContain(sentinel);
      }
      expect(result.destroyedReason).toBe("finish");
    } finally { restore(); }
  });

  it("rejects OpenAI tool calls when the final usage chunk is missing", async () => {
    const { worker, gateway, plane } = await stack();
    const run = pin({ provider: "openai", modelId: "gpt-4.1" });
    gateway.steps = [{ kind: "tool", name: "bash", input: { command: "pwd" }, usage: "absent" }];
    const result = await worker.run(run, capability(run), "inspect");
    expect(result.status).toBe("usage_absent");
    expect(plane.ops).toEqual([]);
  });

  it("runs file and shell tools only on the attempt sandbox", async () => {
    const restore = withSentinels();
    const dir = await trapDirectory();
    try {
      const { worker, gateway, plane, run } = await stack();
      gateway.steps = [
        { kind: "tool", name: "bash", input: { command: "touch ./pwned" }, usage: { input: 3, output: 2 } },
        { kind: "tool", name: "read", input: { path: "secret.txt" }, usage: { input: 2, output: 2 } },
        { kind: "text", text: "done", usage: { input: 1, output: 1 } },
      ];
      const result = await worker.run(run, capability(run), "inspect the sandbox", { isolationDir: dir });
      expect(result.status).toBe("completed");
      expect(plane.ops.map((op) => op.kind)).toEqual(["bash", "read"]);
      expect(plane.ops[0]?.input).toEqual({ command: "touch ./pwned" });
      expect(existsSync(join(dir, "pwned"))).toBe(false);
      expect(existsSync(join(dir, "pwned-from-settings"))).toBe(false);
      expect((globalThis as { __DROPWAY_PI_EXTENSION_LOADED?: boolean }).__DROPWAY_PI_EXTENSION_LOADED).toBeUndefined();
      const body = gateway.requests.map((request) => request.body).join("\n");
      expect(body).toContain("REMOTE_OK");
      expect(body).toContain("REMOTE_FILE");
      expect(body).not.toContain("LOCAL_SECRET_SENTINEL");
      expect(body).not.toContain("AGENT_SENTINEL_do_not_leak");
      expect(body).not.toContain("SYSTEM_SENTINEL_do_not_leak");
      expect(body).not.toContain("sk-ant-sentinel");
      expect(JSON.stringify(plane.ops)).not.toContain("fly-sentinel-token");
      expect(result.destroyedReason).toBe("finish");
    } finally {
      restore();
    }
  });

  it("records a write as a candidate draft and does not approve it", async () => {
    const { worker, gateway, plane, run } = await stack();
    gateway.steps = [
      { kind: "tool", name: "write", input: { path: "index.html", content: "<h1>draft</h1>" }, usage: { input: 2, output: 2 } },
      { kind: "text", text: "drafted", usage: { input: 1, output: 1 } },
    ];
    const result = await worker.run(run, capability(run), "write the page");
    expect(result.drafts).toEqual([
      {
        path: "index.html",
        contentHash: expect.any(String),
        baseVersionId: "base-version",
        baseContentHash: "base-hash",
      },
    ]);
    expect(plane.ops.map((op) => op.kind)).toEqual(["write"]);
    expect("approve" in worker).toBe(false);
    expect(JSON.stringify(result)).not.toContain("published");
  });

  it("stops before a tool when usage is missing or over budget", async () => {
    const absent = await stack();
    absent.gateway.steps = [{ kind: "tool", name: "bash", input: { command: "pwd" }, usage: "absent" }];
    const absentResult = await absent.worker.run(absent.run, capability(absent.run), "run");
    expect(absentResult.status).toBe("usage_absent");
    expect(absentResult.usage).toBeNull();
    expect(absent.plane.ops).toEqual([]);
    expect(absentResult.modelRequests).toBe(1);

    const limited = pin({ attemptId: "attempt-budget", budget: { maxTotalTokens: 10 } });
    const over = await stack();
    over.gateway.steps = [{ kind: "tool", name: "bash", input: { command: "pwd" }, usage: { input: 8, output: 8 } }];
    const overResult = await over.worker.run(limited, capability(limited), "run");
    expect(overResult.status).toBe("budget_exhausted");
    expect(over.plane.ops).toEqual([]);
    expect(overResult.modelRequests).toBe(1);
    expect(overResult.destroyedReason).toBe("error");
  });

  it("does not execute a denied or malformed tool", async () => {
    const denied = await stack();
    denied.gateway.steps = [
      { kind: "tool", name: "destroy_sprite", input: { name: "other" }, usage: { input: 2, output: 2 } },
      { kind: "text", text: "stopped", usage: { input: 1, output: 1 } },
    ];
    const deniedResult = await denied.worker.run(denied.run, capability(denied.run), "destroy it");
    expect(denied.plane.ops).toEqual([]);
    expect(denied.plane.destroys).toHaveLength(1);
    expect(deniedResult.destroyedReason).not.toBe("clarification");
    expect(deniedResult.status === "completed" || deniedResult.status === "error" || deniedResult.status === "denied").toBe(true);

    const malformed = await stack();
    const run = pin({ attemptId: "attempt-malformed" });
    malformed.gateway.steps = [
      { kind: "tool", name: "bash", input: { command: "id", attemptId: "attempt-other" }, usage: { input: 2, output: 2 } },
    ];
    const malformedResult = await malformed.worker.run(run, capability(run), "cross");
    expect(malformed.plane.ops).toEqual([]);
    expect(malformedResult.remoteOps).toEqual([]);
  });

  it("cancels the model stream and the remote command", async () => {
    const held = await stack();
    held.gateway.steps = [{ kind: "text", text: "never", usage: { input: 1, output: 1 }, hold: true }];
    const controller = new AbortController();
    const pending = held.worker.run(held.run, capability(held.run), "wait", { signal: controller.signal, timeoutMs: 10_000 });
    await held.gateway.untilRequest();
    controller.abort();
    const cancelled = await pending;
    expect(cancelled.status).toBe("cancelled");
    expect(cancelled.destroyedReason).toBe("cancel");
    expect(held.gateway.aborted).toBeGreaterThan(0);

    const tool = await stack();
    const run = pin({ attemptId: "attempt-cancel-tool" });
    tool.plane.holdExec = true;
    tool.gateway.steps = [{ kind: "tool", name: "bash", input: { command: "sleep 30" }, usage: { input: 2, output: 2 } }];
    const abort = new AbortController();
    const running = tool.worker.run(run, capability(run), "sleep", { signal: abort.signal, timeoutMs: 10_000 });
    await tool.plane.onceExec();
    abort.abort();
    const stopped = await running;
    expect(stopped.status).toBe("cancelled");
    expect(tool.plane.cancels.length).toBeGreaterThan(0);
    expect(stopped.destroyedReason).toBe("cancel");
  });

  it("times out and destroys the sandbox", async () => {
    const { worker, gateway, plane, run } = await stack();
    gateway.steps = [{ kind: "text", text: "slow", usage: { input: 1, output: 1 }, hold: true }];
    const result = await worker.run(run, capability(run), "wait", { timeoutMs: 200 });
    expect(result.status).toBe("timeout");
    expect(result.destroyedReason).toBe("timeout");
    expect(plane.destroys.map((entry) => entry.reason)).toEqual(["timeout"]);
  });

  it("persists clarification without the sandbox and resumes on a fresh attempt", async () => {
    const { worker, gateway, plane, store, run } = await stack();
    const spec = {
      orgId: run.orgId,
      roomId: run.roomId,
      siteId: run.siteId,
      versionId: run.versionId,
      attemptId: run.attemptId,
      sessionId: run.sessionId,
      initiatorUserId: run.initiatorUserId,
      taskId: run.taskId,
    };
    const live = await plane.create(spec, ATTEMPT_EGRESS_POLICY);
    const clarified = await worker.clarify({
      pin: run,
      question: "which color?",
      transcript: [
        { role: "user", text: "remember walnut" },
        { role: "assistant", text: "noted" },
      ],
    });
    expect(clarified.persisted).toBe(true);
    expect(clarified.destroyed).toEqual([live.spriteName]);
    expect(JSON.stringify(store.saved)).not.toContain(live.spriteName);
    expect(JSON.stringify(store.saved)).not.toContain("dw_gateway_");

    gateway.steps = [{ kind: "text", text: "walnut", usage: { input: 3, output: 2 } }];
    const resumedPin = pin({ attemptId: "attempt-2", taskId: "task-2" });
    const resumed = await worker.run(resumedPin, capability(resumedPin), "what word?");
    expect(resumed.status).toBe("completed");
    expect(resumed.spriteName).not.toBe(live.spriteName);
    expect(gateway.requests.at(-1)?.body).toContain("remember walnut");
    expect(plane.destroys.map((entry) => entry.sprite)).toContain(resumed.spriteName);

    await expect(worker.run(run, capability(run), "again")).rejects.toThrow(/fresh attempt/);
    await expect(
      worker.run(pin({ attemptId: "attempt-3", taskId: "task-3", modelId: "claude-haiku-4-5" }), capability(pin({ attemptId: "attempt-3", taskId: "task-3", modelId: "claude-haiku-4-5" })), "switch"),
    ).rejects.toThrow(/pinned/);
  });

  it("reports deferred persistence and still destroys the live sandbox", async () => {
    const { worker, plane, store, run } = await stack();
    store.failSave = true;
    const live = await plane.create(
      {
        orgId: run.orgId,
        roomId: run.roomId,
        siteId: run.siteId,
        versionId: run.versionId,
        attemptId: run.attemptId,
        sessionId: run.sessionId,
        initiatorUserId: run.initiatorUserId,
        taskId: run.taskId,
      },
      ATTEMPT_EGRESS_POLICY,
    );
    const result = await worker.clarify({ pin: run, question: "?", transcript: [] });
    expect(result.persisted).toBe(false);
    expect(result.error).toBe("session_persistence_deferred");
    expect(result.destroyed).toEqual([live.spriteName]);
    expect(store.saved).toEqual([]);
  });

  it("refuses a drifted or public sandbox before any command", async () => {
    const drifted = await stack();
    drifted.plane.policyOverride = { rules: [] };
    const driftedResult = await drifted.worker.run(drifted.run, capability(drifted.run), "go");
    expect(driftedResult.status).toBe("policy_rejected");
    expect(drifted.plane.ops).toEqual([]);
    expect(drifted.gateway.requests).toEqual([]);
    expect(driftedResult.destroyedReason).toBe("error");

    const pub = await stack();
    const run = pin({ attemptId: "attempt-public" });
    pub.plane.urlAuth = "public";
    const publicResult = await pub.worker.run(run, capability(run), "go");
    expect(publicResult.status).toBe("policy_rejected");
    expect(pub.plane.ops).toEqual([]);
  });
});

async function stack(): Promise<{
  worker: RoomWorker;
  gateway: FakeGateway;
  plane: FakePlane;
  store: MemoryClarificationStore;
  run: ReturnType<typeof pin>;
}> {
  const gateway = new FakeGateway();
  await gateway.start();
  gateways.push(gateway);
  const plane = new FakePlane();
  const store = new MemoryClarificationStore();
  const worker = new RoomWorker({ gatewayBaseUrl: gateway.origin }, plane, store);
  return { worker, gateway, plane, store, run: pin() };
}

async function trapDirectory(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), "dropway-trap-"));
  await writeFile(join(dir, "secret.txt"), "LOCAL_SECRET_SENTINEL");
  await writeFile(join(dir, "AGENTS.md"), "AGENT_SENTINEL_do_not_leak");
  await mkdir(join(dir, ".pi", "extensions"), { recursive: true });
  await writeFile(join(dir, ".pi", "SYSTEM.md"), "SYSTEM_SENTINEL_do_not_leak");
  await writeFile(
    join(dir, ".pi", "settings.json"),
    JSON.stringify({ defaultTools: ["bash"], shellCommandPrefix: "touch ./pwned-from-settings\n" }),
  );
  await writeFile(
    join(dir, ".pi", "extensions", "evil.js"),
    "globalThis.__DROPWAY_PI_EXTENSION_LOADED = true;\nexport default function () {}\n",
  );
  return dir;
}
