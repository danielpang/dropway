// SPDX-License-Identifier: FSL-1.1-Apache-2.0

export type { ClarificationRecord, ClarificationStore, TranscriptMessage } from "./clarification.js";
export {
  assertHandleReady,
  assertSameAttempt,
  bindAttemptExecutor,
  createGuardedControlPlane,
  reapOrphans,
  SPRITE_PROVIDER,
} from "./control-plane.js";
export type {
  AttemptExecutor,
  AttemptSpec,
  CandidateDraft,
  DestroyReason,
  RemoteKind,
  RemoteOp,
  SandboxHandle,
  SandboxLease,
  SpriteControlPlane,
  ToolOutput,
} from "./control-plane.js";
export { RoomRuntimeError } from "./errors.js";
export {
  ALLOWED_MODELS,
  PINNED_PI_SDK_VERSION,
  PI_SDK_PACKAGE,
  assertGatewayUrl,
  assertNoSecretFields,
  assertSdkPin,
  validateCapability,
  validatePin,
} from "./pin.js";
export type { GatewayCapability, ProviderId, RunPin, TokenBudget } from "./pin.js";
export { ATTEMPT_EGRESS_POLICY, assertEgressPolicy, policyHash } from "./policy.js";
export type { NetworkPolicy, NetworkPolicyRule } from "./policy.js";
export { SYSTEM_PROMPT } from "./session.js";
export type { RunStatus } from "./status.js";
export { REMOTE_TOOL_NAMES } from "./tools.js";
export { RoomWorker } from "./worker.js";
export type { ClarifyInput, ClarifyResult, RunOptions, RunResult, WorkerConfig } from "./worker.js";

/** Execution is wired behind explicit API, dashboard and worker feature flags. */
export const EXECUTION_WIRED = true;
export const LIVE_INTEGRATION_GATES = [
  "development_sprite_and_provider_verification_required",
  "egress_dns_and_direct_ip_live_verification_required",
  "restart_orphan_cleanup_live_verification_required",
] as const;
export { FlySpriteControlPlane, artifactHash, verifySpriteArtifact, SPRITES_SDK_VERSION } from "./sprites-adapter.js";
export type { FlySpriteConfig, SpriteArtifact, SpriteArtifactFile } from "./sprites-adapter.js";
export { HostedClient } from "./hosted-client.js";
export { executeHostedClaim } from "./hosted-worker.js";
