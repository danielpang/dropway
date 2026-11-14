// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import type { RunPin } from "./pin.js";

export interface TranscriptMessage {
  role: "user" | "assistant" | "tool";
  text: string;
}

/**
 * Dropway-owned clarification record. It holds no Sprite handle, no sandbox
 * filesystem, and no provider key. A production store does not exist yet;
 * callers pass their own port. This package does not implement a database.
 */
export interface ClarificationRecord {
  orgId: string;
  sessionId: string;
  question: string;
  closedAttemptId: string;
  pin: {
    provider: RunPin["provider"];
    modelId: string;
    harnessId: "pi";
    harnessVersion: string;
    credentialRef: string;
    credentialOwnerUserId: string;
    initiatorUserId: string;
    roomId: string;
    siteId: string;
    versionId: string;
    baseVersionId: string;
    baseContentHash: string;
    budget: RunPin["budget"];
  };
  transcript: TranscriptMessage[];
}

export interface ClarificationStore {
  save(record: ClarificationRecord): Promise<void>;
  load(key: { orgId: string; sessionId: string }): Promise<ClarificationRecord | null>;
}

export function pinnedFields(pin: RunPin): ClarificationRecord["pin"] {
  return {
    provider: pin.provider,
    modelId: pin.modelId,
    harnessId: pin.harnessId,
    harnessVersion: pin.harnessVersion,
    credentialRef: pin.credentialRef,
    credentialOwnerUserId: pin.credentialOwnerUserId,
    initiatorUserId: pin.initiatorUserId,
    roomId: pin.roomId,
    siteId: pin.siteId,
    versionId: pin.versionId,
    baseVersionId: pin.baseVersionId,
    baseContentHash: pin.baseContentHash,
    budget: { maxTotalTokens: pin.budget.maxTotalTokens },
  };
}
