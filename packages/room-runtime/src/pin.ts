// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { VERSION } from "@earendil-works/pi-coding-agent";

import { fail } from "./errors.js";

/** Package version this spike was read against. `VERSION` must match at runtime. */
export const PINNED_PI_SDK_VERSION = "1.5.0";

export const PI_SDK_PACKAGE = "@earendil-works/pi-coding-agent";

export function assertSdkPin(): void {
  if (VERSION !== PINNED_PI_SDK_VERSION) {
    fail(
      "pi_sdk_pin",
      `expected ${PI_SDK_PACKAGE}@${PINNED_PI_SDK_VERSION} but the installed export is ${VERSION}`,
    );
  }
}

export const ALLOWED_MODELS = {
  anthropic: ["claude-sonnet-4-5", "claude-opus-4-1", "claude-haiku-4-5"],
  openrouter: [
    "anthropic/claude-sonnet-4-5",
    "anthropic/claude-opus-4-1",
    "anthropic/claude-haiku-4-5",
  ],
} as const;

export type ProviderId = keyof typeof ALLOWED_MODELS;

export interface TokenBudget {
  maxTotalTokens: number;
}

/** Pinned before the attempt starts. Later edits do not retarget it. No key material. */
export interface RunPin {
  orgId: string;
  roomId: string;
  siteId: string;
  versionId: string;
  sessionId: string;
  attemptId: string;
  taskId: string;
  initiatorUserId: string;
  credentialRef: string;
  credentialOwnerUserId: string;
  provider: ProviderId;
  modelId: string;
  harnessId: "pi";
  harnessVersion: string;
  budget: TokenBudget;
  baseVersionId: string;
  baseContentHash: string;
}

/** Expiring task/tenant/attempt capability. Not an Anthropic or OpenRouter key. */
export interface GatewayCapability {
  token: string;
  expiresAt: string;
  attemptId: string;
  orgId: string;
  taskId: string;
}

const SECRET_KEYS = new Set([
  "apikey",
  "providerkey",
  "flytoken",
  "flyapitoken",
  "baseurl",
  "authorization",
  "secret",
  "accesstoken",
  "anthropickey",
  "openrouterkey",
]);

export function assertNoSecretFields(value: unknown, path = "input"): void {
  if (!value || typeof value !== "object") return;
  for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
    const norm = key.toLowerCase().replace(/[_-]/g, "");
    if (SECRET_KEYS.has(norm)) {
      fail("secret_field", `${path}.${key} is not accepted by the room worker`);
    }
    if (child && typeof child === "object") assertNoSecretFields(child, `${path}.${key}`);
  }
}

function required(value: string, name: string): void {
  if (typeof value !== "string" || value.trim() === "") fail("pin", `${name} is required`);
}

export function validatePin(pin: RunPin): void {
  for (const name of [
    "orgId",
    "roomId",
    "siteId",
    "versionId",
    "sessionId",
    "attemptId",
    "taskId",
    "initiatorUserId",
    "credentialRef",
    "credentialOwnerUserId",
    "modelId",
    "baseVersionId",
    "baseContentHash",
  ] as const) {
    required(pin[name], name);
  }
  if (pin.credentialOwnerUserId !== pin.initiatorUserId) {
    fail("credential_owner", "the credential owner is the initiating user");
  }
  if (/^sk-/i.test(pin.credentialRef)) {
    fail("credential_ref", "credentialRef must be an opaque id, not a provider key");
  }
  if (pin.harnessId !== "pi") fail("harness", "harness must be pi");
  if (pin.harnessVersion !== PINNED_PI_SDK_VERSION) {
    fail("harness_version", `harness version must be ${PINNED_PI_SDK_VERSION}`);
  }
  const models: readonly string[] = ALLOWED_MODELS[pin.provider] ?? [];
  if (!models.includes(pin.modelId)) {
    fail("model", `${pin.provider}/${pin.modelId} is not in the pinned allowlist`);
  }
  if (!Number.isSafeInteger(pin.budget?.maxTotalTokens) || pin.budget.maxTotalTokens <= 0) {
    fail("budget", "maxTotalTokens must be a positive integer");
  }
}

export function validateCapability(capability: GatewayCapability, pin: RunPin, now: Date): void {
  if (!/^dw_gateway_[A-Za-z0-9_.-]{8,}$/.test(capability.token)) {
    fail("gateway_capability", "gateway capability must be an expiring dw_gateway_ token");
  }
  if (/^sk-/i.test(capability.token)) {
    fail("gateway_capability", "provider keys are not gateway capabilities");
  }
  const expires = Date.parse(capability.expiresAt);
  if (!Number.isFinite(expires) || expires <= now.getTime()) {
    fail("gateway_capability", "gateway capability is expired");
  }
  if (capability.attemptId !== pin.attemptId || capability.orgId !== pin.orgId || capability.taskId !== pin.taskId) {
    fail("gateway_capability", "gateway capability is not bound to this attempt");
  }
}

const BLOCKED_GATEWAY_HOSTS = ["api.anthropic.com", "openrouter.ai", "api.sprites.dev", "api.fly.io"];

export function assertGatewayUrl(raw: string): URL {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    fail("gateway_url", "gateway URL is not absolute");
  }
  if (url.username || url.password) fail("gateway_url", "gateway URL must not carry userinfo");
  if (url.protocol !== "http:" && url.protocol !== "https:") fail("gateway_url", "gateway URL must be http(s)");
  const host = url.hostname.toLowerCase();
  if (
    BLOCKED_GATEWAY_HOSTS.includes(host) ||
    host.endsWith(".anthropic.com") ||
    host.endsWith(".openrouter.ai") ||
    host.endsWith(".fly.io") ||
    host.endsWith(".sprites.app")
  ) {
    fail("gateway_url", "gateway URL must be the Dropway model gateway, not a provider or Fly host");
  }
  return url;
}
