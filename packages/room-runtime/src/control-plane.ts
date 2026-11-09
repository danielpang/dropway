// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { fail } from "./errors.js";
import { assertEgressPolicy, policyHash, type NetworkPolicy } from "./policy.js";

export const SPRITE_PROVIDER = "fly_sprites" as const;

export type DestroyReason = "finish" | "error" | "timeout" | "clarification" | "cancel" | "orphan";

export type RemoteKind = "read" | "write" | "edit" | "bash" | "build";

/** One fresh disposable sandbox. No provider key and no Fly token. */
export interface AttemptSpec {
  orgId: string;
  roomId: string;
  siteId: string;
  versionId: string;
  attemptId: string;
  sessionId: string;
  initiatorUserId: string;
  taskId: string;
}

export interface SandboxHandle {
  provider: typeof SPRITE_PROVIDER;
  orgId: string;
  attemptId: string;
  spriteName: string;
  policyHash: string;
  urlAuth: "sprite";
}

export interface RemoteOp {
  kind: RemoteKind;
  attemptId: string;
  input: Record<string, unknown>;
}

export interface CandidateDraft {
  path: string;
  contentHash: string;
  baseVersionId: string;
  baseContentHash: string;
}

export interface ToolOutput {
  text: string;
  isError?: boolean;
  candidate?: CandidateDraft;
}

export interface SandboxLease {
  handle: SandboxHandle;
  state: "running" | "stopped" | "destroyed";
  /** ISO time after which an unreaped sandbox is an orphan. Stop does not delete it. */
  destroyBy: string;
}

/**
 * Trusted control plane. Holds Fly credentials in a future adapter.
 * This interface has no approve, publish, or deploy method.
 * Model tools never receive this object.
 */
export interface SpriteControlPlane {
  create(spec: AttemptSpec, policy: NetworkPolicy): Promise<SandboxHandle>;
  readPolicy(handle: SandboxHandle): Promise<NetworkPolicy>;
  readUrlAuth(handle: SandboxHandle): Promise<"sprite" | "public">;
  exec(handle: SandboxHandle, op: RemoteOp, signal: AbortSignal): Promise<ToolOutput>;
  cancel(handle: SandboxHandle, reason: string): Promise<void>;
  destroy(handle: SandboxHandle, reason: DestroyReason): Promise<void>;
  /** Pause is not deletion. The guest filesystem remains until destroy. */
  stop(handle: SandboxHandle): Promise<void>;
  list(): Promise<SandboxLease[]>;
}

/** File, shell, and build only, bound to one attempt. */
export interface AttemptExecutor {
  readonly attemptId: string;
  read(input: { path: string }, signal: AbortSignal): Promise<ToolOutput>;
  write(input: { path: string; content: string }, signal: AbortSignal): Promise<ToolOutput>;
  edit(input: { path: string; oldText: string; newText: string }, signal: AbortSignal): Promise<ToolOutput>;
  bash(input: { command: string }, signal: AbortSignal): Promise<ToolOutput>;
  build(input: { command: string }, signal: AbortSignal): Promise<ToolOutput>;
  cancel(reason: string): Promise<void>;
}

export function assertSameAttempt(handle: SandboxHandle, op: RemoteOp): void {
  if (op.attemptId !== handle.attemptId) {
    fail("cross_attempt", "remote op is not bound to this attempt");
  }
}

export function createGuardedControlPlane(inner: SpriteControlPlane): SpriteControlPlane {
  return {
    create: (spec, policy) => {
      assertEgressPolicy(policy);
      return inner.create(spec, policy);
    },
    readPolicy: (handle) => inner.readPolicy(handle),
    readUrlAuth: (handle) => inner.readUrlAuth(handle),
    async exec(handle, op, signal) {
      assertSameAttempt(handle, op);
      const policy = await inner.readPolicy(handle);
      assertEgressPolicy(policy);
      if (policyHash(policy) !== handle.policyHash) {
        fail("policy_drift", "Sprite policy changed after the attempt was opened");
      }
      const auth = await inner.readUrlAuth(handle);
      if (auth !== "sprite") fail("public_url", "Sprite URL auth must stay private");
      return inner.exec(handle, op, signal);
    },
    cancel: (handle, reason) => inner.cancel(handle, reason),
    destroy: (handle, reason) => inner.destroy(handle, reason),
    stop: (handle) => inner.stop(handle),
    list: () => inner.list(),
  };
}

export async function assertHandleReady(plane: SpriteControlPlane, handle: SandboxHandle): Promise<void> {
  const policy = await plane.readPolicy(handle);
  assertEgressPolicy(policy);
  if (policyHash(policy) !== handle.policyHash) {
    fail("policy_drift", "Sprite policy does not match the handle");
  }
  const auth = await plane.readUrlAuth(handle);
  if (auth !== "sprite") fail("public_url", "Sprite URL auth must stay private");
}

/**
 * Destroy sandboxes whose deadline has passed. Stopped guests are still live
 * until this runs. Already-destroyed leases are skipped, so cleanup is idempotent.
 */
export async function reapOrphans(plane: SpriteControlPlane, now: Date): Promise<string[]> {
  const destroyed: string[] = [];
  for (const lease of await plane.list()) {
    if (lease.state === "destroyed") continue;
    if (Date.parse(lease.destroyBy) > now.getTime()) continue;
    await plane.destroy(lease.handle, "orphan");
    destroyed.push(lease.handle.spriteName);
  }
  return destroyed;
}

export function bindAttemptExecutor(plane: SpriteControlPlane, handle: SandboxHandle): AttemptExecutor {
  const call = (kind: RemoteOp["kind"], input: Record<string, unknown>, signal: AbortSignal) =>
    plane.exec(handle, { kind, attemptId: handle.attemptId, input }, signal);

  return {
    attemptId: handle.attemptId,
    read: (input, signal) => call("read", { path: input.path }, signal),
    write: (input, signal) => call("write", { path: input.path, content: input.content }, signal),
    edit: (input, signal) =>
      call("edit", { path: input.path, oldText: input.oldText, newText: input.newText }, signal),
    bash: (input, signal) => call("bash", { command: input.command }, signal),
    build: (input, signal) => call("build", { command: input.command }, signal),
    cancel: (reason) => plane.cancel(handle, reason),
  };
}
