// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import type { TranscriptMessage } from "./clarification.js";
import type { ProviderId } from "./pin.js";

export interface HostedJob {
  id: string; org_id: string; room_id: string; site_id: string; version_id: string;
  content_hash: string; initiator_user_id: string; credential_ref: string;
  session_id: string | null; tokens_used: number;
  config: {
    provider: ProviderId; model_id: string; harness_id: "pi"; harness_version: string;
    prompt: string; tool_policy: string;
    budget: { max_tokens: number; max_calls: number; max_seconds: number; max_output_bytes: number };
  };
}
export interface HostedClaim {
  job: HostedJob; attempt_id: string; attempt_token: string; gateway_token: string;
  gateway_base_url: string; lease_expires_at: string; deadline: string;
  transcript?: TranscriptMessage[]; answer?: string;
}
export interface HostedFile { path: string; content_base64: string; content_type: string; sha256?: string; size?: number }
export interface HostedSource { files: HostedFile[]; version_id: string; content_hash: string; source_content_hash: string; thread?: unknown }

export class HostedAPIError extends Error {
  constructor(readonly status: number) { super(`Room API request failed (${status})`); }
}

/** Captured transport bypasses only the model's global guard, not authentication. */
export class HostedClient {
  readonly origin: string;
  private readonly transport: typeof fetch;
  constructor(baseURL: string, private readonly workerToken: string, transport: typeof fetch = globalThis.fetch.bind(globalThis)) {
    const url = new URL(baseURL);
    if (url.username || url.password || url.search || url.hash || (url.pathname !== "/" && url.pathname !== "")) throw new Error("ROOM_API_PUBLIC_URL must be an origin");
    if (url.protocol !== "https:" && !(url.protocol === "http:" && ["127.0.0.1", "localhost", "[::1]"].includes(url.hostname))) throw new Error("Room API must use HTTPS or loopback HTTP");
    if (workerToken.length < 32) throw new Error("ROOM_WORKER_TOKEN must be at least 32 characters");
    this.origin = url.origin;
    this.transport = transport;
  }
  async claim(workerID: string, signal: AbortSignal): Promise<HostedClaim | null> {
    return this.request<HostedClaim>("POST", "/internal/room-worker/claims", this.workerToken, { worker_id: workerID }, signal);
  }
  async attempt<T = unknown>(claim: HostedClaim, operation: string, body: unknown, signal: AbortSignal): Promise<T> {
    if (!["heartbeat", "source", "candidate", "clarify", "events", "fail"].includes(operation)) throw new Error("Unknown attempt operation");
    const result = await this.request<T>(operation === "source" ? "GET" : "POST", `/internal/room-attempts/${encodeURIComponent(claim.attempt_id)}/${operation}`, claim.attempt_token, body, signal);
    return result as T;
  }
  validateGateway(claim: HostedClaim): void {
    const url = new URL(claim.gateway_base_url);
    if (url.origin !== this.origin || url.pathname !== `/internal/room-gateway/${claim.attempt_id}` || url.search || url.hash || url.username || url.password) throw new Error("Gateway claim is outside the trusted API attempt");
  }
  private async request<T>(method: string, path: string, token: string, body: unknown, signal: AbortSignal): Promise<T | null> {
    const response = await this.transport(`${this.origin}${path}`, {
      method, redirect: "error", signal: AbortSignal.any([signal, AbortSignal.timeout(30_000)]),
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: method === "GET" ? undefined : JSON.stringify(body),
    });
    if (!response.ok) { await response.body?.cancel(); throw new HostedAPIError(response.status); }
    if (response.status === 204) return null;
    const reader = response.body?.getReader();
    if (!reader) throw new Error("Room API response was empty");
    const chunks: Uint8Array[] = []; let bytes = 0;
    for (;;) {
      const next = await reader.read(); if (next.done) break;
      bytes += next.value.byteLength;
      if (bytes > 16 * 1024 * 1024) { await reader.cancel(); throw new Error("Room API response exceeds limit"); }
      chunks.push(next.value);
    }
    return JSON.parse(Buffer.concat(chunks).toString("utf8")) as T;
  }
}
