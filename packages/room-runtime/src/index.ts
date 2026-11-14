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

/**
 * Not wired into services/api. Room execution routes stay disabled.
 * Live Sprite egress, DNS, direct-IP, sleep/wake, and checkpoint checks are release gates.
 */
export const EXECUTION_WIRED = false;

export const LIVE_INTEGRATION_GATES = [
  "real_sprite_not_provisioned",
  "egress_dns_and_direct_ip_not_observed",
  "sleep_wake_and_checkpoint_not_observed",
  "clarification_store_has_no_database_adapter",
  "api_execution_routes_remain_disabled",
  "builtin_bash_wrapper_copies_process_env_and_can_write_a_local_temp_file",
] as const;
