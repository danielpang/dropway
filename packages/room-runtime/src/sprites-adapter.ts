// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { createHash, randomUUID } from "node:crypto";
import { SpritesClient, type Sprite, type SpriteCommand } from "@fly/sprites";
import {
  assertSameAttempt, SPRITE_PROVIDER, type AttemptSpec, type DestroyReason,
  type RemoteOp, type SandboxHandle, type SandboxLease, type SpriteControlPlane, type ToolOutput,
} from "./control-plane.js";
import { fail, RoomRuntimeError } from "./errors.js";
import { assertEgressPolicy, BUILD_EGRESS_POLICY, policyHash, type NetworkPolicy } from "./policy.js";
import { SPRITE_GUEST_HELPER, SPRITE_WORKSPACE } from "./sprites-guest.js";

export const SPRITES_SDK_VERSION = "0.2.4";
export const ARTIFACT_MAX_BYTES = 10 << 20;
export const ARTIFACT_MAX_FILES = 500;
export const ARTIFACT_MAX_FILE_BYTES = 2 << 20;
const TOOL_MAX_BYTES = 64 << 10;
const EXPORT_MAX_BYTES = 16 << 20;
const MANAGED_LABEL = "dropway-room-attempt-v1";
const LAUNCHER = "import sys; n=int(sys.stdin.buffer.readline()); code=sys.stdin.buffer.read(n); exec(compile(code,'<dropway-remote>','exec'))";

type SDKClient = Pick<SpritesClient, "createSprite" | "getSprite" | "listSprites" | "deleteSprite">;
export type SpriteOperation = "create" | "read" | "write" | "edit" | "bash" | "build" | "stage" | "export";
export interface SpriteArtifactFile { path: string; contentBase64: string; sha256: string; size: number }
export interface SpriteArtifact { files: SpriteArtifactFile[]; contentHash: string }
export interface FlySpriteConfig {
  token: string;
  expectedOrganization: string;
  /** Dedicated deployment prefix. Never use an empty/shared fleet prefix. */
  namePrefix: string;
  leaseMs?: number;
  operationTimeoutMs?: number;
  allowPackageNetwork?: boolean;
  /** Trusted backend authorization/lease check; called afresh for every operation. */
  authorize(spec: AttemptSpec, operation: SpriteOperation, signal: AbortSignal): Promise<void>;
  /** Parent supplies its ALS trusted-control-plane scope, never model fetch scope. */
  transportScope?: <T>(operation: () => Promise<T>) => Promise<T>;
}
interface AttemptRow { spec: AttemptSpec; handle: SandboxHandle; policy: NetworkPolicy; destroyBy: string; busy: boolean }

/** Real remote adapter. Neither guest environment nor tool args contain token. */
export class FlySpriteControlPlane implements SpriteControlPlane {
  private readonly client: SDKClient;
  private readonly rows = new Map<string, AttemptRow>();
  private readonly active = new Map<string, Set<SpriteCommand>>();
  private readonly destroyed = new Set<string>();
  private readonly leaseMs: number;
  private readonly timeoutMs: number;
  private readonly scope: <T>(operation: () => Promise<T>) => Promise<T>;

  constructor(private readonly config: FlySpriteConfig, client?: SDKClient) {
    if (!config.token || !/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$/.test(config.expectedOrganization) ||
        !/^[a-z][a-z0-9-]{2,23}-$/.test(config.namePrefix)) fail("sprite_config", "explicit organization and dedicated Sprite name prefix are required");
    this.leaseMs = config.leaseMs ?? 5 * 60_000;
    this.timeoutMs = config.operationTimeoutMs ?? 60_000;
    if (!Number.isSafeInteger(this.leaseMs) || this.leaseMs < 10_000 || this.leaseMs > 15 * 60_000 ||
        !Number.isSafeInteger(this.timeoutMs) || this.timeoutMs < 100 || this.timeoutMs > this.leaseMs) fail("sprite_config", "invalid bounded Sprite lease or timeout");
    this.client = client ?? new SpritesClient(config.token, { timeout: 30_000 });
    this.scope = config.transportScope ?? (async (operation) => operation());
  }

  async create(spec: AttemptSpec, policy: NetworkPolicy): Promise<SandboxHandle> {
    assertEgressPolicy(policy);
    if (policyHash(policy) === policyHash(BUILD_EGRESS_POLICY) && !this.config.allowPackageNetwork) fail("egress_not_authorized", "package network requires explicit build authorization");
    for (const value of Object.values(spec)) if (typeof value !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(value)) fail("attempt_identity", "invalid attempt identity");
    if ([...this.rows.values()].some((row) => row.spec.attemptId === spec.attemptId)) fail("attempt_exists", "attempt already has a Sprite");
    const signal = AbortSignal.timeout(this.timeoutMs);
    await this.config.authorize(spec, "create", signal);
    const fleet = await this.safe(() => this.client.listSprites({ prefix: this.config.namePrefix, maxResults: 1 }));
    if (fleet.organizationName !== this.config.expectedOrganization) fail("sprite_org", "Sprite token organization does not match the configured organization");
    const name = this.config.namePrefix + randomUUID().replaceAll("-", "");
    const destroyBy = new Date(Date.now() + this.leaseMs).toISOString();
    const handle: SandboxHandle = { provider: SPRITE_PROVIDER, orgId: spec.orgId, attemptId: spec.attemptId, spriteName: name, policyHash: policyHash(policy), urlAuth: "sprite" };
    const labels = labelsFor(handle, destroyBy);
    this.rows.set(name, { spec: structuredClone(spec), handle, policy: structuredClone(policy), destroyBy, busy: false });
    try {
      await this.safe(() => this.client.createSprite(name, {
        urlSettings: { auth: "sprite", privateAccess: "admins" }, labels,
        environment: {}, config: { ramMB: 512, cpus: 1 }, waitForCapacity: false,
      }));
      const sprite = await this.owned(handle);
      await this.safe(() => sprite.updateNetworkPolicy(policy));
      await this.safe(() => sprite.updatePrivilegesPolicy({ profile: "minimal", noNewPrivileges: true }));
      await this.safe(() => sprite.updateResourcesPolicy({ memory: { limitMB: 512, autoscale: false } }));
      await this.checkSecurity(handle);
      const initialized = await this.runRemote(handle, "init", {}, signal, TOOL_MAX_BYTES);
      if (initialized.exitCode !== 0) fail("sprite_init", "remote workspace initialization failed");
      return handle;
    } catch (error) {
      // Includes uncertain-create results: look up and delete only our exact labels.
      try { await this.destroy(handle, "error"); } catch { fail("sprite_cleanup", "Sprite creation failed and cleanup must be retried by the orphan reaper"); }
      throw sanitized(error);
    }
  }

  async readPolicy(handle: SandboxHandle): Promise<NetworkPolicy> {
    const sprite = await this.owned(handle);
    return this.safe(() => sprite.getNetworkPolicy());
  }
  async readUrlAuth(handle: SandboxHandle): Promise<"sprite" | "public"> {
    const sprite = await this.owned(handle);
    return sprite.urlSettings?.auth === "sprite" && sprite.urlSettings.privateAccess === "admins" ? "sprite" : "public";
  }

  async exec(handle: SandboxHandle, op: RemoteOp, signal: AbortSignal): Promise<ToolOutput> {
    assertSameAttempt(handle, op);
    validateOperation(op);
    return this.operation(handle, op.kind, signal, async () => {
      const result = await this.runRemote(handle, op.kind, op.input, signal, TOOL_MAX_BYTES);
      if (result.exitCode !== 0) return { text: "Remote operation failed.", isError: true };
      if (op.kind === "bash" || op.kind === "build") return { text: result.stdout + result.stderr };
      const output: unknown = JSON.parse(result.stdout);
      if (!output || typeof output !== "object" || typeof (output as { text?: unknown }).text !== "string") fail("sprite_output", "invalid remote tool output");
      return { text: (output as { text: string }).text };
    });
  }

  async stage(handle: SandboxHandle, files: SpriteArtifactFile[], signal: AbortSignal): Promise<void> {
    verifySpriteArtifact({ files, contentHash: artifactHash(files) });
    await this.operation(handle, "stage", signal, async () => {
      const result = await this.runRemote(handle, "stage", { files }, signal, TOOL_MAX_BYTES);
      if (result.exitCode !== 0) fail("sprite_stage", "authorized source staging failed");
    });
  }

  async exportArtifact(handle: SandboxHandle, outputDirectory = ".", signal = new AbortController().signal): Promise<SpriteArtifact> {
    if (outputDirectory !== ".") validateSpritePath(outputDirectory);
    return this.operation(handle, "export", signal, async () => {
      const result = await this.runRemote(handle, "export", { directory: outputDirectory }, signal, EXPORT_MAX_BYTES);
      if (result.exitCode !== 0) fail("sprite_export", "candidate artifact export failed");
      // Hashes supplied by a guest are not authoritative; recompute every blob
      // and the full Go-compatible manifest digest outside the sandbox.
      return verifySpriteArtifact(JSON.parse(result.stdout));
    });
  }

  async cancel(handle: SandboxHandle, _reason: string): Promise<void> {
    for (const command of this.active.get(handle.spriteName) ?? []) { command.kill("SIGKILL"); command.close(); }
    const sprite = await this.owned(handle);
    const sessions = await this.safe(() => sprite.listSessions());
    for (const session of sessions) if (session.isActive) {
      await this.safe(async () => { for await (const _event of await sprite.killSession(session.id, "SIGKILL", "2s")) { /* consume acknowledgement */ } });
    }
  }
  async destroy(handle: SandboxHandle, _reason: DestroyReason): Promise<void> {
    if (this.destroyed.has(handle.spriteName)) return;
    try { await this.owned(handle); } catch (error) {
      if (error instanceof RoomRuntimeError && error.code === "sprite_missing") { this.destroyed.add(handle.spriteName); this.rows.delete(handle.spriteName); return; }
      throw error;
    }
    for (const command of this.active.get(handle.spriteName) ?? []) { command.kill("SIGKILL"); command.close(); }
    await this.safe(() => this.client.deleteSprite(handle.spriteName));
    this.active.delete(handle.spriteName); this.rows.delete(handle.spriteName); this.destroyed.add(handle.spriteName);
  }
  async stop(_handle: SandboxHandle): Promise<void> {
    // The official SDK has no explicit VM pause operation. Do not claim that
    // killing a command destroys or hibernates a Sprite. Clarification destroys.
    fail("sprite_stop_unsupported", "use destroy to release a disposable attempt");
  }
  async list(): Promise<SandboxLease[]> {
    const leases: SandboxLease[] = []; let cursor: string | undefined;
    const seen = new Set<string>();
    do {
      const page = await this.safe(() => this.client.listSprites({ prefix: this.config.namePrefix, maxResults: 100, continuationToken: cursor }));
      if (page.organizationName !== this.config.expectedOrganization) fail("sprite_org", "unexpected Sprite list organization");
      for (const sprite of page.sprites) {
        const parsed = parseLabels(sprite.name, sprite.labels ?? [], this.config.namePrefix);
        if (!parsed || sprite.organization !== this.config.expectedOrganization) continue;
        leases.push({ ...parsed, state: sprite.status === "running" ? "running" : "stopped" });
      }
      cursor = page.hasMore ? page.nextContinuationToken : undefined;
      if (page.hasMore && (!cursor || seen.has(cursor))) fail("sprite_pagination", "invalid Sprite inventory continuation");
      if (cursor) seen.add(cursor);
    } while (cursor);
    return leases;
  }

  private async owned(handle: SandboxHandle): Promise<Sprite> {
    if (handle.provider !== SPRITE_PROVIDER || !handle.spriteName.startsWith(this.config.namePrefix)) fail("sprite_scope", "Sprite does not belong to this deployment");
    let sprite: Sprite;
    try { sprite = await this.scope(() => this.client.getSprite(handle.spriteName)); } catch (error) {
      if (error && typeof error === "object" && "statusCode" in error && error.statusCode === 404) fail("sprite_missing", "Sprite no longer exists");
      throw sanitized(error);
    }
    const parsed = parseLabels(sprite.name, sprite.labels, this.config.namePrefix);
    if (sprite.organizationName !== this.config.expectedOrganization || !parsed ||
      parsed.handle.orgId !== handle.orgId || parsed.handle.attemptId !== handle.attemptId || parsed.handle.policyHash !== handle.policyHash) fail("sprite_scope", "Sprite ownership labels do not match this attempt");
    return sprite;
  }
  private async checkSecurity(handle: SandboxHandle): Promise<void> {
    const sprite = await this.owned(handle);
    if (sprite.urlSettings?.auth !== "sprite" || sprite.urlSettings.privateAccess !== "admins") fail("public_url", "Sprite URL must remain private to administrators");
    const policy = await this.safe(() => sprite.getNetworkPolicy()); assertEgressPolicy(policy);
    if (policyHash(policy) !== handle.policyHash) fail("policy_drift", "Sprite egress policy changed");
    const privilege = await this.safe(() => sprite.getPrivilegesPolicy());
    if (privilege.profile !== "minimal" || privilege.noNewPrivileges !== true) fail("privilege_drift", "Sprite privilege policy changed");
    const resources = await this.safe(() => sprite.getResourcesPolicy());
    if (resources.memory?.limitMB !== 512 || resources.memory.autoscale !== false) fail("resource_drift", "Sprite resource cap changed");
  }
  private async operation<T>(handle: SandboxHandle, kind: SpriteOperation, signal: AbortSignal, run: () => Promise<T>): Promise<T> {
    const row = this.rows.get(handle.spriteName);
    if (!row || row.handle.attemptId !== handle.attemptId || row.handle.orgId !== handle.orgId) fail("sprite_scope", "operation requires the live attempt owner");
    if (row.busy) fail("sprite_busy", "another operation is active on this attempt");
    row.busy = true;
    try {
      if (signal.aborted) fail("cancelled", "remote operation cancelled");
      if (Date.now() >= Date.parse(row.destroyBy)) fail("lease_expired", "Sprite attempt lease expired");
      await this.config.authorize(row.spec, kind, signal);
      await this.checkSecurity(handle);
      return await run();
    } catch (error) {
      try { await this.destroy(handle, signal.aborted ? "cancel" : "error"); } catch { fail("sprite_cleanup", "remote operation failed and orphan cleanup is required"); }
      throw sanitized(error);
    } finally { row.busy = false; }
  }
  private async safe<T>(operation: () => Promise<T>): Promise<T> {
    try { return await this.scope(operation); } catch (error) { throw sanitized(error); }
  }
  private async runRemote(handle: SandboxHandle, action: string, input: unknown, signal: AbortSignal, maxBytes: number): Promise<{ stdout: string; stderr: string; exitCode: number }> {
    const sprite = await this.owned(handle);
    const helper = Buffer.from(SPRITE_GUEST_HELPER, "utf8");
    const request = Buffer.from(JSON.stringify({ action, input }), "utf8");
    if (request.length > EXPORT_MAX_BYTES) fail("sprite_input", "remote operation input too large");
    const payload = Buffer.concat([Buffer.from(`${helper.length}\n`), helper, request]);
    return this.scope(() => new Promise((resolve, reject) => {
      if (signal.aborted) { reject(new RoomRuntimeError("cancelled", "remote operation cancelled")); return; }
      const remainingLease = Date.parse(this.rows.get(handle.spriteName)?.destroyBy ?? "") - Date.now();
      if (!Number.isFinite(remainingLease) || remainingLease <= 0) { reject(new RoomRuntimeError("lease_expired", "Sprite attempt lease expired")); return; }
      const command = sprite.spawn("/usr/bin/python3", ["-I", "-S", "-c", LAUNCHER], {
        env: {}, cwd: action === "init" ? "/home/sprite" : SPRITE_WORKSPACE, tty: false,
        detachable: false, maxRunAfterDisconnect: "2s",
      });
      const commands = this.active.get(handle.spriteName) ?? new Set<SpriteCommand>();
      commands.add(command); this.active.set(handle.spriteName, commands);
      const stdout: Buffer[] = [], stderr: Buffer[] = []; let bytes = 0, settled = false;
      const finish = (error?: Error, exitCode = 0) => {
        if (settled) return; settled = true; clearTimeout(timer); signal.removeEventListener("abort", abort);
        commands.delete(command); command.close();
        if (error) reject(error); else resolve({ stdout: Buffer.concat(stdout).toString("utf8"), stderr: Buffer.concat(stderr).toString("utf8"), exitCode });
      };
      const abort = () => { command.kill("SIGKILL"); finish(new RoomRuntimeError("cancelled", "remote operation cancelled")); };
      const timer = setTimeout(() => { command.kill("SIGKILL"); finish(new RoomRuntimeError("timeout", "remote operation timed out")); }, Math.max(1, Math.min(this.timeoutMs, remainingLease)));
      const collect = (chunks: Buffer[], chunk: Buffer | string) => {
        if (settled) return;
        const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk); bytes += buffer.length;
        if (bytes > maxBytes) { command.kill("SIGKILL"); finish(new RoomRuntimeError("output_limit", "remote output limit exceeded")); return; }
        chunks.push(buffer);
      };
      command.stdout.on("data", (chunk: Buffer) => collect(stdout, chunk));
      command.stderr.on("data", (chunk: Buffer) => collect(stderr, chunk));
      command.on("error", () => finish(new RoomRuntimeError("sprite_transport", "remote execution transport failed")));
      command.on("exit", (code: number) => finish(undefined, code));
      command.once("spawn", () => { if (!settled) command.stdin.end(payload); });
      signal.addEventListener("abort", abort, { once: true });
      if (signal.aborted) abort();
    }));
  }
}

function sanitized(error: unknown): RoomRuntimeError {
  return error instanceof RoomRuntimeError ? error : new RoomRuntimeError("sprite_remote", "Sprite control-plane operation failed");
}
function labelsFor(handle: SandboxHandle, destroyBy: string): string[] {
  return [MANAGED_LABEL, `dw-org:${handle.orgId}`, `dw-attempt:${handle.attemptId}`, `dw-policy:${handle.policyHash}`, `dw-expiry:${Date.parse(destroyBy)}`];
}
function parseLabels(name: string, labels: string[], prefix: string): { handle: SandboxHandle; destroyBy: string } | undefined {
  if (!name.startsWith(prefix) || !labels.includes(MANAGED_LABEL)) return;
  const field = (key: string) => { const found = labels.filter((label) => label.startsWith(key + ":")); return found.length === 1 ? found[0]!.slice(key.length + 1) : undefined; };
  const orgId = field("dw-org"), attemptId = field("dw-attempt"), hash = field("dw-policy"), deadline = Number(field("dw-expiry"));
  if (!orgId || !attemptId || !hash || !/^[a-f0-9]{64}$/.test(hash) || !Number.isSafeInteger(deadline) || deadline <= 0) return;
  return { handle: { provider: SPRITE_PROVIDER, orgId, attemptId, spriteName: name, policyHash: hash, urlAuth: "sprite" }, destroyBy: new Date(deadline).toISOString() };
}
export function validateSpritePath(path: string): void {
  if (typeof path !== "string" || !path || Buffer.byteLength(path) > 512 || path.startsWith("/") || path.includes("\\") || /[\x00-\x1f\x7f]/.test(path) ||
    path.split("/").some((part) => ["", ".", "..", ".git", ".ssh", ".aws", ".npmrc", ".netrc"].includes(part) || part.startsWith(".env"))) fail("sprite_path", "path must name an authorized workspace file");
}
function validateOperation(op: RemoteOp): void {
  const fields: Record<string, string[]> = { read: ["path"], write: ["path", "content"], edit: ["path", "oldText", "newText"], bash: ["command"], build: ["command"] };
  const allowed = fields[op.kind];
  if (!allowed || !op.input || Object.keys(op.input).some((key) => !allowed.includes(key)) || allowed.some((key) => typeof op.input[key] !== "string")) fail("sprite_input", "invalid remote operation fields");
  if ("path" in op.input) validateSpritePath(op.input.path as string);
  if ("command" in op.input && (!(op.input.command as string) || Buffer.byteLength(op.input.command as string) > 8192)) fail("sprite_input", "remote command exceeds limit");
  if ("content" in op.input && Buffer.byteLength(op.input.content as string) > ARTIFACT_MAX_FILE_BYTES) fail("sprite_input", "remote file exceeds limit");
}
export function artifactHash(files: Pick<SpriteArtifactFile, "path" | "sha256">[]): string {
  const lines = [...files].sort((a, b) => Buffer.compare(Buffer.from(a.path), Buffer.from(b.path))).map((file) => `${file.sha256}  ${file.path}\n`).join("");
  return createHash("sha256").update(lines).digest("hex");
}
export function verifySpriteArtifact(value: unknown): SpriteArtifact {
  if (!value || typeof value !== "object") fail("sprite_artifact", "invalid exported artifact");
  const artifact = value as SpriteArtifact;
  if (!Array.isArray(artifact.files) || artifact.files.length > ARTIFACT_MAX_FILES || typeof artifact.contentHash !== "string") fail("sprite_artifact", "invalid artifact limits");
  const seen = new Set<string>(); let total = 0;
  for (const file of artifact.files) {
    if (!file || typeof file.path !== "string" || typeof file.contentBase64 !== "string" || !/^[a-f0-9]{64}$/.test(file.sha256) || !Number.isSafeInteger(file.size) || file.size < 0 || file.size > ARTIFACT_MAX_FILE_BYTES) fail("sprite_artifact", "invalid artifact file");
    validateSpritePath(file.path);
    if (seen.has(file.path) || file.contentBase64.length > Math.ceil(ARTIFACT_MAX_FILE_BYTES / 3) * 4) fail("sprite_artifact", "duplicate or oversized artifact file");
    seen.add(file.path);
    const content = Buffer.from(file.contentBase64, "base64"); total += content.length;
    if (content.toString("base64") !== file.contentBase64 || content.length !== file.size || total > ARTIFACT_MAX_BYTES || createHash("sha256").update(content).digest("hex") !== file.sha256) fail("sprite_artifact", "artifact bytes do not match their digest");
  }
  if (artifactHash(artifact.files) !== artifact.contentHash) fail("sprite_artifact", "artifact manifest digest mismatch");
  return structuredClone(artifact);
}
