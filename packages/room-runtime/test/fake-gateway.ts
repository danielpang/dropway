// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";

export interface GatewayUsage {
  input: number;
  output: number;
}

export type GatewayStep =
  | { kind: "text"; text: string; usage?: GatewayUsage | "absent"; hold?: boolean }
  | { kind: "tool"; name: string; input: unknown; usage?: GatewayUsage | "absent" };

export interface CapturedGatewayRequest {
  url: string;
  headers: Record<string, string>;
  body: string;
}

export class FakeGateway {
  readonly requests: CapturedGatewayRequest[] = [];
  steps: GatewayStep[] = [];
  aborted = 0;
  origin = "";
  private server: Server | undefined;
  private index = 0;
  private readonly arrived: Array<() => void> = [];

  async start(): Promise<void> {
    this.server = createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on("data", (chunk: Buffer) => chunks.push(chunk));
      req.on("end", () => {
        const headers: Record<string, string> = {};
        for (const [key, value] of Object.entries(req.headers)) {
          headers[key.toLowerCase()] = Array.isArray(value) ? value.join(",") : String(value ?? "");
        }
        this.requests.push({
          url: req.url ?? "",
          headers,
          body: Buffer.concat(chunks).toString("utf8"),
        });
        for (const resolve of this.arrived.splice(0)) resolve();
        const step = this.steps[this.index++] ?? {
          kind: "text" as const,
          text: "no-more-script",
          usage: { input: 1, output: 1 },
        };
        this.respond(req, res, step);
      });
    });
    await new Promise<void>((resolve) => this.server?.listen(0, "127.0.0.1", resolve));
    const address = this.server.address();
    if (!address || typeof address === "string") throw new Error("gateway did not bind");
    this.origin = `http://127.0.0.1:${address.port}`;
  }

  async stop(): Promise<void> {
    const server = this.server;
    if (!server) return;
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }

  untilRequest(): Promise<void> {
    if (this.requests.length > 0) return Promise.resolve();
    return new Promise((resolve) => this.arrived.push(resolve));
  }

  private respond(req: IncomingMessage, res: ServerResponse, step: GatewayStep): void {
    if (step.kind === "text" && step.hold) {
      res.writeHead(200, { "content-type": "text/event-stream" });
      req.on("close", () => {
        this.aborted += 1;
      });
      return;
    }
    const openAI = (req.url ?? "").includes("/chat/completions");
    const body = openAI ? openAIEvents(step) : anthropicEvents(step);
    res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
    res.end(body);
  }
}

function usageOf(step: GatewayStep): GatewayUsage | "absent" {
  return step.usage ?? { input: 1, output: 1 };
}

function anthropicEvents(step: GatewayStep): string {
  const usage = usageOf(step);
  const startUsage = usage === "absent" ? {} : { input_tokens: usage.input, output_tokens: 1 };
  const deltaUsage = usage === "absent" ? undefined : { output_tokens: usage.output };
  const blocks =
    step.kind === "tool"
      ? [
          {
            type: "content_block_start",
            index: 0,
            content_block: { type: "tool_use", id: "toolu_test", name: step.name, input: {} },
          },
          {
            type: "content_block_delta",
            index: 0,
            delta: { type: "input_json_delta", partial_json: JSON.stringify(step.input) },
          },
          { type: "content_block_stop", index: 0 },
        ]
      : textBlocks(step.text);
  const events = [
    {
      type: "message_start",
      message: {
        id: "msg_test",
        type: "message",
        role: "assistant",
        content: [],
        model: "claude-sonnet-4-5",
        stop_reason: null,
        stop_sequence: null,
        usage: startUsage,
      },
    },
    ...blocks,
    {
      type: "message_delta",
      delta: { stop_reason: step.kind === "tool" ? "tool_use" : "end_turn", stop_sequence: null },
      ...(deltaUsage ? { usage: deltaUsage } : {}),
    },
    { type: "message_stop" },
  ];
  return events
    .map((event) => `event: ${(event as { type: string }).type}\ndata: ${JSON.stringify(event)}\n\n`)
    .join("");
}

function textBlocks(text: string): unknown[] {
  const midpoint = Math.max(1, Math.ceil(text.length / 2));
  const parts = text.length <= 1 ? [text] : [text.slice(0, midpoint), text.slice(midpoint)];
  return [
    { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } },
    ...parts.map((part) => ({
      type: "content_block_delta",
      index: 0,
      delta: { type: "text_delta", text: part },
    })),
    { type: "content_block_stop", index: 0 },
  ];
}

function openAIEvents(step: GatewayStep): string {
  const usage = usageOf(step);
  const text = step.kind === "text" ? step.text : "";
  const chunks = [
    {
      id: "chatcmpl_test",
      object: "chat.completion.chunk",
      model: "anthropic/claude-sonnet-4-5",
      choices: [{ index: 0, delta: { role: "assistant", content: text }, finish_reason: null }],
    },
    {
      id: "chatcmpl_test",
      object: "chat.completion.chunk",
      model: "anthropic/claude-sonnet-4-5",
      choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
      ...(usage === "absent" ? {} : { usage: { prompt_tokens: usage.input, completion_tokens: usage.output, total_tokens: usage.input + usage.output } }),
    },
  ];
  return `${chunks.map((chunk) => `data: ${JSON.stringify(chunk)}\n\n`).join("")}data: [DONE]\n\n`;
}
