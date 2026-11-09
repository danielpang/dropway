// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import type { ClarificationRecord, ClarificationStore, GatewayCapability, RunPin } from "../src/index.js";

export function pin(overrides: Partial<RunPin> = {}): RunPin {
  return {
    orgId: "org-1",
    roomId: "room-1",
    siteId: "site-1",
    versionId: "version-1",
    sessionId: "session-1",
    attemptId: "attempt-1",
    taskId: "task-1",
    initiatorUserId: "user-1",
    credentialRef: "cred-user-1",
    credentialOwnerUserId: "user-1",
    provider: "anthropic",
    modelId: "claude-sonnet-4-5",
    harnessId: "pi",
    harnessVersion: "1.5.0",
    budget: { maxTotalTokens: 100 },
    baseVersionId: "base-version",
    baseContentHash: "base-hash",
    ...overrides,
  };
}

export function capability(run: RunPin, overrides: Partial<GatewayCapability> = {}): GatewayCapability {
  return {
    token: "dw_gateway_attempttoken",
    expiresAt: new Date(Date.now() + 60_000).toISOString(),
    attemptId: run.attemptId,
    orgId: run.orgId,
    taskId: run.taskId,
    ...overrides,
  };
}

export class MemoryClarificationStore implements ClarificationStore {
  readonly saved: ClarificationRecord[] = [];
  failSave = false;
  failLoad = false;

  async save(record: ClarificationRecord): Promise<void> {
    if (this.failSave) throw new Error("store unavailable");
    this.saved.push(structuredClone(record));
  }

  async load(key: { orgId: string; sessionId: string }): Promise<ClarificationRecord | null> {
    if (this.failLoad) throw new Error("store unavailable");
    const found = this.saved.filter((record) => record.orgId === key.orgId && record.sessionId === key.sessionId);
    return found.at(-1) ?? null;
  }
}

export function withSentinels(): () => void {
  const previous = {
    ANTHROPIC_API_KEY: process.env.ANTHROPIC_API_KEY,
    ANTHROPIC_BASE_URL: process.env.ANTHROPIC_BASE_URL,
    OPENROUTER_API_KEY: process.env.OPENROUTER_API_KEY,
    FLY_API_TOKEN: process.env.FLY_API_TOKEN,
  };
  process.env.ANTHROPIC_API_KEY = "sk-ant-sentinel";
  process.env.ANTHROPIC_BASE_URL = "http://127.0.0.1:9";
  process.env.OPENROUTER_API_KEY = "sk-or-sentinel";
  process.env.FLY_API_TOKEN = "fly-sentinel-token";
  return () => {
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  };
}

export const SENTINELS = ["sk-ant-sentinel", "sk-or-sentinel", "fly-sentinel-token", "LOCAL_SECRET_SENTINEL", "AGENT_SENTINEL_do_not_leak", "SYSTEM_SENTINEL_do_not_leak"];
