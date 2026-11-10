// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { createHash } from "node:crypto";

import { fail } from "./errors.js";

/**
 * Sprites egress is opt-in and read-only inside the guest.
 * `{ "rules": [] }` means unrestricted, so this plan is never empty.
 * `include: "defaults"` is rejected: those defaults allow package and model APIs.
 * Model and Fly hosts stay denied. The sprite does not call the model gateway.
 *
 * Specific allow rules beat the `*` deny. Docs:
 * https://docs.fly.io/sprites/concepts/networking/
 */
export const ATTEMPT_EGRESS_POLICY: NetworkPolicy = {rules: [{domain: "*", action: "deny"}]};

/** Only an explicitly authorized build may opt into package downloads. */
export const BUILD_EGRESS_POLICY: NetworkPolicy = {
  rules: [
    { domain: "*", action: "deny" },
    { domain: "registry.npmjs.org", action: "allow" },
    { domain: "nodejs.org", action: "allow" },
    { domain: "*.nodejs.org", action: "allow" },
  ],
};

export interface NetworkPolicyRule {
  domain?: string;
  action?: "allow" | "deny";
  include?: string;
}

export interface NetworkPolicy {
  rules: NetworkPolicyRule[];
}

const BLOCKED_ALLOW = new Set([
  "*",
  "api.anthropic.com",
  "*.anthropic.com",
  "openrouter.ai",
  "*.openrouter.ai",
  "api.sprites.dev",
  "*.sprites.dev",
  "api.fly.io",
  "*.fly.io",
]);

export function policyHash(policy: NetworkPolicy): string {
  return createHash("sha256").update(JSON.stringify(policy.rules.map((rule) => ({domain: rule.domain, action: rule.action, include: rule.include})))).digest("hex");
}

export function assertEgressPolicy(policy: NetworkPolicy): void {
  if (!policy || !Array.isArray(policy.rules) || policy.rules.length === 0) {
    fail("egress_unrestricted", "an empty Sprite network policy is unrestricted");
  }
  for (const rule of policy.rules) {
    if (rule.include) {
      fail("egress_defaults", `Sprite policy must not include preset ${rule.include}`);
    }
    if (rule.action === "allow" && rule.domain && BLOCKED_ALLOW.has(rule.domain)) {
      fail("egress_too_wide", `Sprite policy must not allow ${rule.domain}`);
    }
  }
  if (policyHash(policy) !== policyHash(ATTEMPT_EGRESS_POLICY) && policyHash(policy) !== policyHash(BUILD_EGRESS_POLICY)) {
    fail("egress_unpinned", "Sprite policy is not the pinned deny-all allowlist");
  }
}
