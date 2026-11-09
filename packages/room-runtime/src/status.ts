// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import type { DestroyReason } from "./control-plane.js";

export type RunStatus =
  | "completed"
  | "budget_exhausted"
  | "usage_absent"
  | "cancelled"
  | "timeout"
  | "error"
  | "denied"
  | "policy_rejected";

export function destroyReasonFor(status: RunStatus | undefined): DestroyReason {
  if (status === "completed") return "finish";
  if (status === "cancelled") return "cancel";
  if (status === "timeout") return "timeout";
  return "error";
}
