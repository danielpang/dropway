// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import {
  policyHash,
  type AttemptSpec,
  type NetworkPolicy,
  type RemoteOp,
  type SandboxHandle,
  type SandboxLease,
  type SpriteControlPlane,
  type ToolOutput,
} from "../src/index.js";

export class FakePlane implements SpriteControlPlane {
  readonly ops: RemoteOp[] = [];
  readonly cancels: string[] = [];
  readonly destroys: Array<{ sprite: string; reason: string }> = [];
  readonly stops: string[] = [];
  policyOverride?: NetworkPolicy;
  urlAuth: "sprite" | "public" = "sprite";
  holdExec = false;
  private readonly rows = new Map<string, SandboxLease & { policy: NetworkPolicy }>();
  private readonly execWaiters: Array<() => void> = [];
  private readonly holdRejects: Array<(error: Error) => void> = [];

  async create(spec: AttemptSpec, policy: NetworkPolicy): Promise<SandboxHandle> {
    const existing = [...this.rows.values()].find(
      (row) => row.handle.attemptId === spec.attemptId && row.state !== "destroyed",
    );
    if (existing) return existing.handle;
    const stored = this.policyOverride ?? policy;
    const handle: SandboxHandle = {
      provider: "fly_sprites",
      orgId: spec.orgId,
      attemptId: spec.attemptId,
      spriteName: `sprite-${spec.attemptId}-${this.rows.size}`,
      policyHash: policyHash(stored),
      urlAuth: "sprite",
    };
    this.rows.set(handle.spriteName, {
      handle,
      policy: structuredClone(stored),
      state: "running",
      destroyBy: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
    });
    return handle;
  }

  async readPolicy(handle: SandboxHandle): Promise<NetworkPolicy> {
    return structuredClone(this.must(handle).policy);
  }

  async readUrlAuth(): Promise<"sprite" | "public"> {
    return this.urlAuth;
  }

  async exec(handle: SandboxHandle, op: RemoteOp, signal: AbortSignal): Promise<ToolOutput> {
    this.must(handle);
    this.ops.push(structuredClone(op));
    for (const resolve of this.execWaiters.splice(0)) resolve();
    if (this.holdExec) {
      await new Promise<void>((resolve, reject) => {
        const onAbort = () => reject(new Error("aborted"));
        if (signal.aborted) {
          onAbort();
          return;
        }
        signal.addEventListener("abort", onAbort, { once: true });
        this.holdRejects.push((error) => {
          signal.removeEventListener("abort", onAbort);
          reject(error);
        });
      });
    }
    if (op.kind === "read") return { text: "REMOTE_FILE" };
    if (op.kind === "write" || op.kind === "edit") return { text: "REMOTE_WROTE" };
    return { text: "REMOTE_OK" };
  }

  async cancel(handle: SandboxHandle, reason: string): Promise<void> {
    this.cancels.push(`${handle.spriteName}:${reason}`);
    for (const reject of this.holdRejects.splice(0)) reject(new Error("aborted"));
  }

  async destroy(handle: SandboxHandle, reason: string): Promise<void> {
    const row = this.rows.get(handle.spriteName);
    if (!row || row.state === "destroyed") return;
    row.state = "destroyed";
    this.destroys.push({ sprite: handle.spriteName, reason });
  }

  async stop(handle: SandboxHandle): Promise<void> {
    const row = this.must(handle);
    row.state = "stopped";
    this.stops.push(handle.spriteName);
  }

  async list(): Promise<SandboxLease[]> {
    return [...this.rows.values()].map((row) => ({
      handle: row.handle,
      state: row.state,
      destroyBy: row.destroyBy,
    }));
  }

  onceExec(): Promise<void> {
    if (this.ops.length > 0) return Promise.resolve();
    return new Promise((resolve) => this.execWaiters.push(resolve));
  }

  tamper(handle: SandboxHandle, policy: NetworkPolicy): void {
    this.must(handle).policy = policy;
  }

  setDeadline(handle: SandboxHandle, destroyBy: string): void {
    this.must(handle).destroyBy = destroyBy;
  }

  private must(handle: SandboxHandle): SandboxLease & { policy: NetworkPolicy } {
    const row = this.rows.get(handle.spriteName);
    if (!row) throw new Error(`unknown sprite ${handle.spriteName}`);
    return row;
  }
}
