// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { createHash } from "node:crypto";

import {
  pinnedFields,
  type ClarificationRecord,
  type ClarificationStore,
  type TranscriptMessage,
} from "./clarification.js";
import {
  assertHandleReady,
  bindAttemptExecutor,
  createGuardedControlPlane,
  type AttemptSpec,
  type CandidateDraft,
  type DestroyReason,
  type SandboxHandle,
  type SpriteControlPlane,
  type ToolOutput,
} from "./control-plane.js";
import { RoomRuntimeError } from "./errors.js";
import { GatewayObserver, withGatewayFetch } from "./gateway.js";
import {
  assertGatewayUrl,
  assertNoSecretFields,
  assertSdkPin,
  validateCapability,
  validatePin,
  type GatewayCapability,
  type RunPin,
} from "./pin.js";
import { ATTEMPT_EGRESS_POLICY } from "./policy.js";
import { openLockedSession, transcriptFrom } from "./session.js";
import { destroyReasonFor, type RunStatus } from "./status.js";
import { sameRemoteTools } from "./tools.js";

export interface WorkerConfig {
  /** Trusted Dropway model gateway. Not taken from the prompt or the run pin. */
  gatewayBaseUrl: string;
}

export interface RunOptions {
  enableClarification?: boolean;
  transcript?: TranscriptMessage[];
  onPrepareSandbox?: (handle: SandboxHandle, signal: AbortSignal) => Promise<void>;
  onBeforeRelease?: (handle: SandboxHandle, result: RunResult, signal: AbortSignal) => Promise<void>;
  onProgress?: (text: string) => void;
  signal?: AbortSignal;
  timeoutMs?: number;
  now?: () => Date;
  /** Test hook. Production runs create a fresh empty directory and delete it. */
  isolationDir?: string;
}

export interface RunResult {
  question?: string;
  status: RunStatus;
  text: string;
  deltas: string[];
  usage: { input: number; output: number; total: number } | null;
  usagePresent: boolean;
  activeTools: string[];
  extensionsLoaded: number;
  modelRequests: number;
  remoteOps: string[];
  drafts: CandidateDraft[];
  spriteName?: string;
  destroyedReason?: DestroyReason;
  transcript: TranscriptMessage[];
  error?: string;
}

export interface ClarifyInput {
  pin: RunPin;
  question: string;
  transcript: TranscriptMessage[];
}

export interface ClarifyResult {
  persisted: boolean;
  destroyed: string[];
  error?: "session_persistence_deferred";
}

/**
 * Dropway-side Pi worker. This is a compatibility spike.
 * It is not mounted on API routes and it does not provision Sprites.
 */
export class RoomWorker {
  private readonly plane: SpriteControlPlane;

  constructor(
    private readonly config: WorkerConfig,
    plane: SpriteControlPlane,
    private readonly store: ClarificationStore,
  ) {
    this.plane = createGuardedControlPlane(plane);
  }

  async run(pin: RunPin, capability: GatewayCapability, prompt: string, options: RunOptions = {}): Promise<RunResult> {
    const now = options.now ?? (() => new Date());
    assertSdkPin();
    assertNoSecretFields(pin);
    assertNoSecretFields(capability);
    validatePin(pin);
    validateCapability(capability, pin, now());
    const gateway = assertGatewayUrl(this.config.gatewayBaseUrl);
    const record = await this.readRecord(pin);
    if (record) assertResume(record, pin);

    const ownedDir = options.isolationDir === undefined;
    const dir = options.isolationDir ?? (await mkdtemp(join(tmpdir(), "dropway-room-")));
    const observer = new GatewayObserver();
    const deltas: string[] = [];
    const drafts: CandidateDraft[] = [];
    const remoteOps: string[] = [];
    let question: string | undefined;
    let status: RunStatus | undefined;
    let handle: SandboxHandle | undefined;
    let session: Awaited<ReturnType<typeof openLockedSession>>["session"] | undefined;
    let unsubscribe = () => {};
    let executor: ReturnType<typeof bindAttemptExecutor> | undefined;
    const abortKind: { current: "timeout" | "cancel" | null } = { current: null };
    const ac = new AbortController();
    const timer = setTimeout(() => {
      abortKind.current = "timeout";
      ac.abort();
    }, options.timeoutMs ?? 60_000);
    const onUserAbort = () => {
      abortKind.current = "cancel";
      ac.abort();
    };
    options.signal?.addEventListener("abort", onUserAbort, { once: true });
    const onAbort = () => {
      status ??= abortKind.current === "timeout" ? "timeout" : "cancelled";
      void session?.abort();
      void executor?.cancel(status).catch(() => undefined);
    };
    ac.signal.addEventListener("abort", onAbort);

    let result: RunResult = emptyResult();
    try {
      if (options.signal?.aborted) {
        status = "cancelled";
        result = emptyResult("cancelled");
        return result;
      }
      handle = await this.plane.create(specOf(pin), ATTEMPT_EGRESS_POLICY);
      try {
        await assertHandleReady(this.plane, handle);
      } catch (error) {
        status = "policy_rejected";
        throw error;
      }
      if (ac.signal.aborted) throw new RoomRuntimeError("cancelled", "attempt cancelled during provisioning");
      await options.onPrepareSandbox?.(handle, ac.signal);
      const bound = bindAttemptExecutor(this.plane, handle);
      executor = {
        ...bound,
        read: (input, signal) => tracked(bound.read(input, signal), "read", remoteOps),
        write: async (input, signal) => {
          const output = await tracked(bound.write(input, signal), "write", remoteOps);
          if (!output.isError) drafts.push(draft(pin, input.path, input.content));
          return output;
        },
        edit: async (input, signal) => {
          const output = await tracked(bound.edit(input, signal), "edit", remoteOps);
          if (!output.isError) drafts.push(draft(pin, input.path, input.newText));
          return output;
        },
        bash: (input, signal) => tracked(bound.bash(input, signal), "bash", remoteOps),
        build: (input, signal) => tracked(bound.build(input, signal), "build", remoteOps),
        cancel: (reason) => bound.cancel(reason),
      };
      const opened = await openLockedSession({
        dir,
        pin,
        capability,
        gatewayOrigin: gateway.origin,
        gatewayBaseUrl: gateway.href.replace(/\/$/, ""),
        transcript: options.transcript ?? record?.transcript ?? [],
        executor,
        observer,
        stop: (next) => {
          status ??= next;
        },
        clarify: options.enableClarification ? (text) => { question = text; } : undefined,
      });
      session = opened.session;
      if (opened.modelFallbackMessage) {
        status = "error";
        throw new RoomRuntimeError("model_fallback", opened.modelFallbackMessage);
      }
      if (opened.extensionsResult.extensions.length !== 0) {
        status = "error";
        throw new RoomRuntimeError("extensions", "Pi loaded an extension");
      }
      const activeTools = opened.session.getActiveToolNames();
      if (!sameRemoteTools(activeTools, options.enableClarification)) {
        status = "error";
        throw new RoomRuntimeError("tools", `active tools were ${activeTools.join(",")}`);
      }
      unsubscribe = opened.session.subscribe((event) => {
        if (event.type === "message_update" && event.assistantMessageEvent.type === "text_delta") {
          deltas.push(event.assistantMessageEvent.delta);
          options.onProgress?.(event.assistantMessageEvent.delta);
        }
      });
      await withGatewayFetch(gateway.href, observer, async () => {
        await opened.session.prompt(prompt);
      });
      status ??= deriveStatus(observer, pin.budget.maxTotalTokens);
      result = {
        ...baseResult(opened.session, observer, activeTools, remoteOps, drafts, deltas, status),
        spriteName: handle.spriteName,
        question,
      };
      return result;
    } catch (error) {
      status ??= error instanceof RoomRuntimeError && (error.code === "egress_unpinned" || error.code === "egress_unrestricted" || error.code === "public_url" || error.code === "policy_drift" || error.code === "egress_defaults" || error.code === "egress_too_wide")
        ? "policy_rejected"
        : "error";
      result = {
        ...emptyResult(status),
        spriteName: handle?.spriteName,
        activeTools: session ? result.activeTools : [],
        extensionsLoaded: 0,
        modelRequests: observer.requests.length,
        remoteOps,
        drafts,
        deltas,
        transcript: session ? transcriptFrom(session.messages) : [],
        text: deltas.join(""),
        error: error instanceof Error ? error.message : "run failed",
        question,
      };
      return result;
    } finally {
      unsubscribe();
      session?.dispose();
      try {
        if (handle) {
          try {
            if (result.status === "completed" || result.status === "awaiting_clarification") {
              await options.onBeforeRelease?.(handle, result, ac.signal);
            }
          } finally {
            const reason = destroyReasonFor(status);
            await this.plane.destroy(handle, reason);
            result.destroyedReason = reason;
          }
        }
      } finally {
        clearTimeout(timer);
        options.signal?.removeEventListener("abort", onUserAbort);
        if (ownedDir) await rm(dir, { recursive: true, force: true });
      }
    }
  }

  async clarify(input: ClarifyInput): Promise<ClarifyResult> {
    assertSdkPin();
    assertNoSecretFields(input);
    validatePin(input.pin);
    const destroyed: string[] = [];
    for (const lease of await this.plane.list()) {
      if (lease.handle.attemptId !== input.pin.attemptId || lease.state === "destroyed") continue;
      await this.plane.destroy(lease.handle, "clarification");
      destroyed.push(lease.handle.spriteName);
    }
    const record: ClarificationRecord = {
      orgId: input.pin.orgId,
      sessionId: input.pin.sessionId,
      question: input.question,
      closedAttemptId: input.pin.attemptId,
      pin: pinnedFields(input.pin),
      transcript: input.transcript,
    };
    try {
      await this.store.save(record);
      return { persisted: true, destroyed };
    } catch {
      return { persisted: false, destroyed, error: "session_persistence_deferred" };
    }
  }

  private async readRecord(pin: RunPin): Promise<ClarificationRecord | null> {
    try {
      return await this.store.load({ orgId: pin.orgId, sessionId: pin.sessionId });
    } catch {
      throw new RoomRuntimeError("session_persistence_deferred", "clarification store is unavailable");
    }
  }
}

function specOf(pin: RunPin): AttemptSpec {
  return {
    orgId: pin.orgId,
    roomId: pin.roomId,
    siteId: pin.siteId,
    versionId: pin.versionId,
    attemptId: pin.attemptId,
    sessionId: pin.sessionId,
    initiatorUserId: pin.initiatorUserId,
    taskId: pin.taskId,
  };
}

function assertResume(record: ClarificationRecord, pin: RunPin): void {
  const next = pinnedFields(pin);
  if (JSON.stringify(next) !== JSON.stringify(record.pin)) {
    throw new RoomRuntimeError("pin_changed", "resume must keep the pinned provider, model, harness, credential, and budget");
  }
  if (pin.attemptId === record.closedAttemptId) {
    throw new RoomRuntimeError("fresh_attempt", "resume requires a fresh attempt");
  }
}

async function tracked(work: Promise<ToolOutput>, kind: string, ops: string[]): Promise<ToolOutput> {
  const output = await work;
  if (!output.isError) ops.push(kind);
  return output;
}

function draft(pin: RunPin, path: string, content: string): CandidateDraft {
  return {
    path,
    contentHash: createHash("sha256").update(content).digest("hex"),
    baseVersionId: pin.baseVersionId,
    baseContentHash: pin.baseContentHash,
  };
}

function deriveStatus(observer: GatewayObserver, maxTotalTokens: number): RunStatus {
  if (observer.turns.length === 0) return "error";
  if (observer.turns.some((turn) => !turn.usagePresent)) return "usage_absent";
  if (observer.spent() > maxTotalTokens) return "budget_exhausted";
  return "completed";
}

function baseResult(
  session: { getLastAssistantText(): string | undefined; messages: readonly unknown[] },
  observer: GatewayObserver,
  activeTools: string[],
  remoteOps: string[],
  drafts: CandidateDraft[],
  deltas: string[],
  status: RunStatus,
): RunResult {
  const usagePresent = observer.turns.every((turn) => turn.usagePresent) && observer.turns.length > 0;
  const input = observer.turns.reduce((sum, turn) => sum + turn.inputTokens, 0);
  const output = observer.turns.reduce((sum, turn) => sum + turn.outputTokens, 0);
  return {
    status,
    text: session.getLastAssistantText() ?? deltas.join(""),
    deltas,
    usage: usagePresent ? { input, output, total: input + output } : null,
    usagePresent,
    activeTools,
    extensionsLoaded: 0,
    modelRequests: observer.requests.length,
    remoteOps,
    drafts,
    transcript: transcriptFrom(session.messages),
  };
}

function emptyResult(status: RunStatus = "error"): RunResult {
  return {
    status,
    text: "",
    deltas: [],
    usage: null,
    usagePresent: false,
    activeTools: [],
    extensionsLoaded: 0,
    modelRequests: 0,
    remoteOps: [],
    drafts: [],
    transcript: [],
  };
}
