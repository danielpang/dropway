// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { errorForResponse, type ApiErrorBody } from "./errors.js";

/** The hosted Dropway control-plane API. Override via `baseUrl` for self-host. */
export const DEFAULT_BASE_URL = "https://api.dropway.dev";

/** Options for constructing a Dropway client. */
export interface DropwayOptions {
  /**
   * The org-scoped API key (`dw_live_...`). Defaults to `process.env.DROPWAY_API_KEY`
   * so CI just sets the env var. Throws at construction if neither is provided.
   */
  apiKey?: string;
  /** API base URL. Defaults to the hosted API; set this for self-host. */
  baseUrl?: string;
  /** Override the fetch implementation (defaults to the global `fetch`). */
  fetch?: typeof fetch;
  /** Max retry attempts for idempotent requests (GET, blob PUT, finalize). Default 3. */
  maxRetries?: number;
  /** Per-request timeout in milliseconds. Default 30000. */
  timeoutMs?: number;
}

/** Options for a single request. */
export interface RequestOptions {
  /** JSON body (serialized automatically). */
  body?: unknown;
  /** Query parameters. */
  query?: Record<string, string | number | boolean | undefined>;
  /**
   * Whether this request is safe to retry on 5xx / network error. GETs default to
   * true; writes default to false. The deploy loop opts finalize in explicitly
   * (idempotent by content hash).
   */
  idempotent?: boolean;
  /** Extra headers. */
  headers?: Record<string, string>;
  /** AbortSignal to cancel the request. */
  signal?: AbortSignal;
}

/**
 * HttpClient is the shared transport: it attaches the API key, serializes JSON,
 * maps non-2xx responses to typed DropwayErrors, and retries idempotent requests
 * with jittered backoff. It is the base the resource namespaces (e.g. `sites`)
 * build on, and backs the low-level `request` escape hatch that reaches any `/v1`
 * endpoint the generated types describe.
 */
export class HttpClient {
  readonly baseUrl: string;
  private readonly apiKey: string;
  private readonly fetchImpl: typeof fetch;
  private readonly maxRetries: number;
  private readonly timeoutMs: number;

  constructor(opts: DropwayOptions = {}) {
    const apiKey = opts.apiKey ?? process.env.DROPWAY_API_KEY;
    if (!apiKey) {
      throw new Error(
        "Dropway: no API key. Pass { apiKey } or set the DROPWAY_API_KEY environment variable.",
      );
    }
    this.apiKey = apiKey;
    this.baseUrl = (opts.baseUrl ?? DEFAULT_BASE_URL).replace(/\/+$/, "");
    const f = opts.fetch ?? globalThis.fetch;
    if (!f) {
      throw new Error(
        "Dropway: no fetch implementation. Use Node 18+ or pass { fetch }.",
      );
    }
    this.fetchImpl = f;
    this.maxRetries = Math.max(0, opts.maxRetries ?? 3);
    this.timeoutMs = opts.timeoutMs ?? 30_000;
  }

  /**
   * request performs a JSON API call against `path` (e.g. "/v1/sites"), returning
   * the parsed response body as T. This is also the full-spec escape hatch: any
   * `/v1` endpoint is reachable through it, typed by the caller. Non-2xx → a typed
   * DropwayError; 204 → undefined.
   */
  async request<T = unknown>(
    method: string,
    path: string,
    opts: RequestOptions = {},
  ): Promise<T> {
    const url = this.buildUrl(path, opts.query);
    const idempotent = opts.idempotent ?? method.toUpperCase() === "GET";
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.apiKey}`,
      Accept: "application/json",
      ...opts.headers,
    };
    let bodyInit: string | undefined;
    if (opts.body !== undefined) {
      headers["Content-Type"] = "application/json";
      bodyInit = JSON.stringify(opts.body);
    }

    const res = await this.fetchWithRetry(
      url,
      { method, headers, body: bodyInit, signal: opts.signal },
      idempotent,
    );

    if (res.status === 204) return undefined as T;
    const json = res.text ? safeJson(res.text) : null;

    if (!statusOk(res.status)) {
      throw errorForResponse(
        res.status,
        (json as ApiErrorBody | null) ?? null,
        parseRetryAfter(res.headers.get("retry-after")),
      );
    }
    return (json as T) ?? (undefined as T);
  }

  /**
   * putBlob uploads raw bytes to a presigned object-store URL. No Authorization and
   * no Content-Type — the presigned URL is the credential and neither is part of the
   * SigV4 signature (matching the dashboard/CLI). Retries as an idempotent PUT.
   */
  async putBlob(
    url: string,
    data: Uint8Array,
    signal?: AbortSignal,
  ): Promise<void> {
    const res = await this.fetchWithRetry(
      url,
      { method: "PUT", body: data, signal },
      true,
    );
    if (!statusOk(res.status)) {
      throw errorForResponse(res.status, null);
    }
  }

  private buildUrl(path: string, query?: RequestOptions["query"]): string {
    const url = new URL(path.startsWith("http") ? path : this.baseUrl + path);
    if (query) {
      for (const [k, v] of Object.entries(query)) {
        if (v !== undefined) url.searchParams.set(k, String(v));
      }
    }
    return url.toString();
  }

  /**
   * fetchWithRetry issues the request and reads the FULL body under a single
   * timeout that covers both headers and body (a stalled body must not hang past
   * timeoutMs), returning a small snapshot. Idempotent requests retry on transient
   * 5xx / 429 / network error with jittered backoff. An already-aborted external
   * signal short-circuits before the first fetch.
   */
  private async fetchWithRetry(
    url: string,
    init: RequestInit,
    idempotent: boolean,
  ): Promise<ResponseSnapshot> {
    let lastErr: unknown;
    for (let attempt = 0; ; attempt++) {
      const external = init.signal;
      // Honor a signal that is ALREADY aborted (addEventListener would never fire).
      if (external?.aborted) {
        throw (
          external.reason ??
          new DOMException("The operation was aborted.", "AbortError")
        );
      }
      const controller = new AbortController();
      const onAbort = () => controller.abort();
      external?.addEventListener("abort", onAbort, { once: true });
      // One deadline for the whole exchange — headers AND body read below.
      const timer = setTimeout(() => controller.abort(), this.timeoutMs);
      try {
        const res = await this.fetchImpl(url, {
          ...init,
          signal: controller.signal,
        });
        const retryable = res.status >= 500 || res.status === 429;
        if (idempotent && attempt < this.maxRetries && retryable) {
          // Drain/close the body so the connection can be reused, then back off.
          await res.body?.cancel().catch(() => {});
          await sleep(backoffMs(attempt, res.headers.get("retry-after")));
          continue;
        }
        // Read the body while the timeout is still armed (a slow body aborts too).
        const text = res.status === 204 ? "" : await res.text();
        return { status: res.status, headers: res.headers, text };
      } catch (err) {
        lastErr = err;
        if (external?.aborted) throw err; // caller cancelled → don't retry
        if (idempotent && attempt < this.maxRetries) {
          await sleep(backoffMs(attempt, null));
          continue;
        }
        throw err;
      } finally {
        clearTimeout(timer);
        external?.removeEventListener("abort", onAbort);
      }
    }
    // unreachable; the loop returns or throws.
    throw lastErr;
  }
}

/** A read-out response: status + headers + the fully-read body text. */
interface ResponseSnapshot {
  status: number;
  headers: Headers;
  text: string;
}

function statusOk(status: number): boolean {
  return status >= 200 && status < 300;
}

function safeJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;
  const n = Number(value);
  return Number.isFinite(n) ? n : undefined;
}

/** backoffMs is exponential with jitter, honoring a Retry-After when the server sends one. */
function backoffMs(attempt: number, retryAfter: string | null): number {
  const ra = retryAfter ? Number(retryAfter) : NaN;
  if (Number.isFinite(ra)) return Math.min(ra * 1000, 30_000);
  const base = Math.min(1000 * 2 ** attempt, 15_000);
  return base / 2 + Math.floor((base / 2) * pseudoRandom());
}

// A small non-crypto jitter source; Math.random is fine for backoff spreading.
function pseudoRandom(): number {
  return Math.random();
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
