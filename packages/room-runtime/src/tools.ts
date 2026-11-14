// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import type { AgentToolResult, ToolDefinition } from "@earendil-works/pi-coding-agent";
import { defineTool } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

import type { AttemptExecutor } from "./control-plane.js";
import type { GatewayObserver } from "./gateway.js";
import type { RunStatus } from "./status.js";

export const REMOTE_TOOL_NAMES = ["read", "write", "edit", "bash", "build"] as const;

const FORBIDDEN_ARG_KEYS = new Set([
  "attemptId",
  "attempt",
  "sprite",
  "spriteName",
  "handle",
  "policy",
  "token",
  "apiKey",
  "flyToken",
  "cwd",
  "env",
]);

export interface ToolGuard {
  executor: AttemptExecutor;
  observer: GatewayObserver;
  maxTotalTokens: number;
  stop(status: RunStatus): void;
}

/**
 * Custom tools only. The built-in bash wrapper is not used: it copies
 * `process.env` into the execution options and can write a local temp file
 * when output is truncated. These handlers do not read the host filesystem
 * and do not spawn a host process.
 */
export function remoteTools(guard: ToolGuard): ToolDefinition[] {
  const run = async (
    signal: AbortSignal | undefined,
    op: () => Promise<{ text: string; isError?: boolean }>,
  ): Promise<AgentToolResult> => {
    const decision = decisionFor(guard);
    if (decision) {
      guard.observer.blockFurtherModelCalls = true;
      guard.stop(decision);
      return {
        content: [{ type: "text", text: decision }],
        details: { denied: true },
        isError: true,
        terminate: true,
      };
    }
    try {
      const output = await op();
      return {
        content: [{ type: "text", text: output.text }],
        details: {},
        isError: output.isError === true,
        terminate: output.isError === true,
      };
    } catch (error) {
      const aborted = signal?.aborted === true || (error instanceof Error && error.message === "aborted");
      if (aborted) guard.stop("cancelled");
      return {
        content: [{ type: "text", text: aborted ? "cancelled" : "remote tool failed" }],
        details: {},
        isError: true,
        terminate: true,
      };
    }
  };

  return [
    defineTool({
      name: "read",
      label: "Read",
      description: "Read a file in the attempt sandbox.",
      parameters: Type.Object({ path: Type.String() }),
      prepareArguments: (args) => checkedArguments<{ path: string }>(args, ["path"]),
      execute: (_id, params, signal) =>
        run(signal, () => guard.executor.read({ path: params.path }, liveSignal(signal))),
    }),
    defineTool({
      name: "write",
      label: "Write",
      description: "Write a file in the attempt sandbox. The result is a candidate draft, not a publish.",
      parameters: Type.Object({ path: Type.String(), content: Type.String() }),
      prepareArguments: (args) => checkedArguments<{ path: string; content: string }>(args, ["path", "content"]),
      execute: (_id, params, signal) =>
        run(signal, () =>
          guard.executor.write({ path: params.path, content: params.content }, liveSignal(signal)),
        ),
    }),
    defineTool({
      name: "edit",
      label: "Edit",
      description: "Replace text in a sandbox file. The result is a candidate draft, not a publish.",
      parameters: Type.Object({
        path: Type.String(),
        oldText: Type.String(),
        newText: Type.String(),
      }),
      prepareArguments: (args) =>
        checkedArguments<{ path: string; oldText: string; newText: string }>(args, ["path", "oldText", "newText"]),
      execute: (_id, params, signal) =>
        run(signal, () =>
          guard.executor.edit(
            { path: params.path, oldText: params.oldText, newText: params.newText },
            liveSignal(signal),
          ),
        ),
    }),
    defineTool({
      name: "bash",
      label: "Bash",
      description: "Run a shell command in the attempt sandbox.",
      parameters: Type.Object({ command: Type.String() }),
      prepareArguments: (args) => checkedArguments<{ command: string }>(args, ["command"]),
      execute: (_id, params, signal) =>
        run(signal, () => guard.executor.bash({ command: params.command }, liveSignal(signal))),
    }),
    defineTool({
      name: "build",
      label: "Build",
      description: "Run a build command in the attempt sandbox.",
      parameters: Type.Object({ command: Type.String() }),
      prepareArguments: (args) => checkedArguments<{ command: string }>(args, ["command"]),
      execute: (_id, params, signal) =>
        run(signal, () => guard.executor.build({ command: params.command }, liveSignal(signal))),
    }),
  ];
}

function decisionFor(guard: ToolGuard): RunStatus | undefined {
  const turn = guard.observer.latest();
  if (!turn || !turn.usagePresent) return "usage_absent";
  if (guard.observer.spent() > guard.maxTotalTokens) return "budget_exhausted";
  return undefined;
}

function liveSignal(signal: AbortSignal | undefined): AbortSignal {
  return signal ?? new AbortController().signal;
}

function checkedArguments<T>(args: unknown, fields: string[]): T {
  if (!args || typeof args !== "object" || Array.isArray(args)) {
    throw new Error("malformed tool arguments");
  }
  const record = args as Record<string, unknown>;
  for (const key of Object.keys(record)) {
    if (FORBIDDEN_ARG_KEYS.has(key)) throw new Error(`denied tool argument ${key}`);
  }
  const out: Record<string, string> = {};
  for (const field of fields) {
    const value = record[field];
    if (typeof value !== "string" || value.length === 0) throw new Error(`malformed tool argument ${field}`);
    out[field] = value;
  }
  return out as T;
}

export function sameRemoteTools(names: readonly string[]): boolean {
  const got = [...names].sort();
  const expected = [...REMOTE_TOOL_NAMES].sort();
  return got.length === expected.length && got.every((name, index) => name === expected[index]);
}
