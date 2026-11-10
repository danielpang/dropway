// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

import {
  ATTEMPT_EGRESS_POLICY,
  EXECUTION_WIRED,
  RoomRuntimeError,
  assertEgressPolicy,
  assertNoSecretFields,
  assertSameAttempt,
  createGuardedControlPlane,
  policyHash,
  reapOrphans,
  validateCapability,
  validatePin,
} from "../src/index.js";
import { FakePlane } from "./fake-plane.js";
import { capability, pin } from "./support.js";

describe("egress policy", () => {
  it("rejects an empty ruleset because Sprites treat that as unrestricted", () => {
    expect(() => assertEgressPolicy({ rules: [] })).toThrow(RoomRuntimeError);
  });

  it("rejects the development defaults preset and provider hosts", () => {
    expect(() => assertEgressPolicy({ rules: [{ include: "defaults" }] })).toThrow(/defaults/);
    expect(() =>
      assertEgressPolicy({
        rules: [
          { domain: "*", action: "deny" },
          { domain: "api.anthropic.com", action: "allow" },
        ],
      }),
    ).toThrow(/api\.anthropic\.com/);
  });

  it("accepts only the pinned deny-all allowlist", () => {
    expect(() => assertEgressPolicy(ATTEMPT_EGRESS_POLICY)).not.toThrow();
    expect(policyHash(ATTEMPT_EGRESS_POLICY)).toBe(policyHash(structuredClone(ATTEMPT_EGRESS_POLICY)));
    expect(() =>
      assertEgressPolicy({
        rules: [...ATTEMPT_EGRESS_POLICY.rules, { domain: "example.com", action: "allow" }],
      }),
    ).toThrow(/pinned/);
  });
});

describe("control plane", () => {
  it("does not treat stop as deletion and reaps orphans idempotently", async () => {
    const plane = new FakePlane();
    const handle = await plane.create(
      {
        orgId: "org-1",
        roomId: "room-1",
        siteId: "site-1",
        versionId: "version-1",
        attemptId: "attempt-1",
        sessionId: "session-1",
        initiatorUserId: "user-1",
        taskId: "task-1",
      },
      ATTEMPT_EGRESS_POLICY,
    );
    await plane.stop(handle);
    expect(plane.destroys).toEqual([]);
    expect((await plane.list()).map((lease) => lease.state)).toEqual(["stopped"]);
    expect(await reapOrphans(plane, new Date())).toEqual([]);
    plane.setDeadline(handle, new Date(Date.now() - 1000).toISOString());
    expect(await reapOrphans(plane, new Date())).toEqual([handle.spriteName]);
    await plane.destroy(handle, "orphan");
    expect(plane.destroys).toEqual([{ sprite: handle.spriteName, reason: "orphan" }]);
    expect(await reapOrphans(plane, new Date())).toEqual([]);
  });

  it("denies a remote op that names a different attempt", async () => {
    const inner = new FakePlane();
    const plane = createGuardedControlPlane(inner);
    const handle = await plane.create(
      {
        orgId: "org-1",
        roomId: "room-1",
        siteId: "site-1",
        versionId: "version-1",
        attemptId: "attempt-a",
        sessionId: "session-1",
        initiatorUserId: "user-1",
        taskId: "task-1",
      },
      ATTEMPT_EGRESS_POLICY,
    );
    expect(() => assertSameAttempt(handle, { kind: "bash", attemptId: "attempt-b", input: { command: "id" } })).toThrow(
      /attempt/,
    );
    await expect(
      plane.exec(handle, { kind: "bash", attemptId: "attempt-b", input: { command: "id" } }, new AbortController().signal),
    ).rejects.toThrow(/attempt/);
    expect(inner.ops).toEqual([]);
  });
});

describe("pin", () => {
  it("keeps the credential on the initiating user and rejects secret fields", () => {
    expect(() => validatePin(pin({ credentialOwnerUserId: "room-owner" }))).toThrow(/initiating user/);
    expect(() => validatePin(pin({ harnessVersion: "0.0.0" }))).toThrow(/1\.5\.0/);
    expect(() => validatePin(pin({ modelId: "claude-unpinned" }))).toThrow(/allowlist/);
    const expired = pin();
    expect(() => validateCapability(capability(expired, { expiresAt: new Date(Date.now() - 1000).toISOString() }), expired, new Date())).toThrow(
      /expired/,
    );
    expect(() => assertNoSecretFields({ apiKey: "sk-ant-secret", nested: { flyToken: "x" } })).toThrow(/not accepted/);
    expect(EXECUTION_WIRED).toBe(true);
  });
});

describe("package boundary", () => {
  it("uses the SDK boundary without a host shell or candidate approval", async () => {
    const root = join(process.cwd(), "src");
    const files = await sourceFiles(root);
    const source = (await Promise.all(files.map((file) => readFile(file, "utf8")))).join("\n");
    expect(source).not.toContain("https://api.sprites.dev");
    expect(source).not.toMatch(/from ["']node:child_process["']/);
    expect(source).not.toMatch(/from ["']child_process["']/);
    expect(source).not.toMatch(/\bapprove\s*\(/);
    expect(source).not.toContain("FLY_API_TOKEN");
  });
});

async function sourceFiles(dir: string): Promise<string[]> {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = await Promise.all(
    entries.map(async (entry) => {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) return sourceFiles(path);
      return entry.name.endsWith(".ts") ? [path] : [];
    }),
  );
  return files.flat();
}
