// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { InMemoryCredentialStore, type Model } from "@earendil-works/pi-ai";
import { ModelRuntime } from "@earendil-works/pi-coding-agent";

import { fail } from "./errors.js";
import type { GatewayCapability, ProviderId, RunPin } from "./pin.js";

export interface TurnObservation {
  usagePresent: boolean;
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
}

export interface CapturedRequest {
  url: string;
  headers: Record<string, string>;
  body: string;
}

/**
 * Watches the gateway stream Pi actually consumes.
 * Pi turns a missing Anthropic usage object into zeros (`input_tokens || 0`),
 * so absence has to be recorded here, before that coercion.
 */
export class GatewayObserver {
  readonly requests: CapturedRequest[] = [];
  readonly turns: TurnObservation[] = [];
  blockFurtherModelCalls = false;
  private current: TurnObservation = blankTurn();
  private turnOpen = false;

  beginTurn(): void {
    this.current = blankTurn();
    this.turnOpen = true;
  }

  note(payload: unknown): void {
    if (!this.turnOpen || !payload || typeof payload !== "object") return;
    const event = payload as Record<string, unknown>;
    if (event.type === "message_start") {
      const message = event.message as Record<string, unknown> | undefined;
      this.noteUsage(message?.usage);
    } else if (event.type === "message_delta") {
      this.noteUsage(event.usage);
    } else if ("choices" in event || "usage" in event) {
      this.noteUsage(event.usage);
    }
    const choice = Array.isArray(event.choices) ? (event.choices[0] as { finish_reason?: string } | undefined) : undefined;
    if (event.type === "message_stop" || choice?.finish_reason) this.finishTurn();
  }

  finishTurn(): void {
    if (!this.turnOpen) return;
    this.turnOpen = false;
    this.turns.push(this.current);
    this.current = blankTurn();
  }

  latest(): TurnObservation | undefined {
    return this.turns[this.turns.length - 1];
  }

  spent(): number {
    return this.turns.reduce((sum, turn) => sum + (turn.usagePresent ? turn.totalTokens : 0), 0);
  }

  private noteUsage(usage: unknown): void {
    if (!usage || typeof usage !== "object") return;
    const record = usage as Record<string, unknown>;
    const input = numberField(record, "input_tokens") ?? numberField(record, "prompt_tokens");
    const output = numberField(record, "output_tokens") ?? numberField(record, "completion_tokens");
    if (input === undefined && output === undefined) return;
    this.current.usagePresent = true;
    this.current.inputTokens = input ?? this.current.inputTokens;
    this.current.outputTokens = output ?? this.current.outputTokens;
    this.current.totalTokens = this.current.inputTokens + this.current.outputTokens;
  }
}

function blankTurn(): TurnObservation {
  return { usagePresent: false, inputTokens: 0, outputTokens: 0, totalTokens: 0 };
}

function numberField(record: Record<string, unknown>, key: string): number | undefined {
  const value = record[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

export function pinnedModel(
  pin: RunPin,
  gatewayBaseUrl: string,
): Model<"anthropic-messages"> | Model<"openai-completions"> {
  const shared = {
    id: pin.modelId,
    name: pin.modelId,
    reasoning: false as const,
    input: ["text"] as ("text" | "image")[],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 200_000,
    maxTokens: 8192,
  };
  if (pin.provider === "openrouter") {
    const base = gatewayBaseUrl.replace(/\/$/, "");
    return {
      ...shared,
      api: "openai-completions",
      provider: "openrouter",
      baseUrl: `${base}/v1`,
      compat: { supportsStore: false },
    };
  }
  return {
    ...shared,
    api: "anthropic-messages",
    provider: "anthropic",
    baseUrl: gatewayBaseUrl.replace(/\/$/, ""),
  };
}

export async function createGatewayRuntime(pin: RunPin, capability: GatewayCapability): Promise<ModelRuntime> {
  const runtime = await ModelRuntime.create({
    credentials: new InMemoryCredentialStore(),
    modelsPath: null,
    allowModelNetwork: false,
    refreshOnCreate: false,
  });
  await runtime.setRuntimeApiKey(pin.provider, capability.token);
  return runtime;
}

/**
 * Replace global fetch for one attempt. Requests to any origin other than the
 * pinned gateway throw. The stream is parsed in line so usage is known before
 * Pi starts tool calls.
 */
export async function withGatewayFetch(
  origin: string,
  observer: GatewayObserver,
  fn: () => Promise<void>,
): Promise<void> {
  const previous = globalThis.fetch;
  globalThis.fetch = (async (input: Parameters<typeof fetch>[0], init?: Parameters<typeof fetch>[1]) => {
    if (observer.blockFurtherModelCalls) {
      fail("model_blocked", "further model calls are blocked for this attempt");
    }
    const url = requestUrl(input);
    let parsed: URL;
    try {
      parsed = new URL(url);
    } catch {
      fail("gateway_fetch", `refusing non-absolute request ${url}`);
    }
    if (parsed.origin !== origin) {
      fail("gateway_fetch", `refusing request outside the model gateway: ${parsed.origin}`);
    }
    const headers = headersOf(init?.headers);
    const body = typeof init?.body === "string" ? init.body : init?.body instanceof Uint8Array ? new TextDecoder().decode(init.body) : "";
    observer.requests.push({ url: parsed.pathname + parsed.search, headers, body });
    const response = await previous(input, init);
    return tapResponse(response, observer);
  }) as typeof fetch;
  try {
    await fn();
  } finally {
    globalThis.fetch = previous;
  }
}

function requestUrl(input: Parameters<typeof fetch>[0]): string {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.toString();
  return input.url;
}

function headersOf(headers: Parameters<typeof fetch>[1] extends infer Init ? Init extends { headers?: infer Headers } ? Headers : undefined : undefined): Record<string, string> {
  const out: Record<string, string> = {};
  if (!headers) return out;
  const wrapped = new Headers(headers);
  wrapped.forEach((value, key) => {
    out[key.toLowerCase()] = value;
  });
  return out;
}

function tapResponse(response: Response, observer: GatewayObserver): Response {
  if (!response.body) return response;
  observer.beginTurn();
  const decoder = new TextDecoder();
  let buffer = "";
  const transform = new TransformStream<Uint8Array, Uint8Array>({
    transform(chunk, controller) {
      buffer += decoder.decode(chunk, { stream: true });
      buffer = drainEvents(buffer, observer);
      controller.enqueue(chunk);
    },
    flush() {
      buffer += decoder.decode();
      drainEvents(`${buffer}\n\n`, observer);
      observer.finishTurn();
    },
  });
  return new Response(response.body.pipeThrough(transform), {
    status: response.status,
    statusText: response.statusText,
    headers: response.headers,
  });
}

function drainEvents(buffer: string, observer: GatewayObserver): string {
  const parts = buffer.split(/\n\n/);
  const rest = parts.pop() ?? "";
  for (const part of parts) {
    const data = part
      .split("\n")
      .filter((line) => line.startsWith("data:"))
      .map((line) => line.slice(5).trim())
      .join("");
    if (!data || data === "[DONE]") continue;
    try {
      observer.note(JSON.parse(data) as unknown);
    } catch {
      // Non-JSON SSE frames are ignored. Pi still sees the original bytes.
    }
  }
  return rest;
}
