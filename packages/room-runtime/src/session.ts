// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import {
  createAgentSession,
  createExtensionRuntime,
  SessionManager,
  SettingsManager,
  type ResourceLoader,
} from "@earendil-works/pi-coding-agent";

import type { TranscriptMessage } from "./clarification.js";
import type { AttemptExecutor } from "./control-plane.js";
import { createGatewayRuntime, pinnedModel, type GatewayObserver } from "./gateway.js";
import type { GatewayCapability, RunPin } from "./pin.js";
import { remoteTools, type ToolGuard } from "./tools.js";
import type { RunStatus } from "./status.js";

export const SYSTEM_PROMPT =
  "You are a Dropway review-room worker. File, shell, and build tools run only in the attempt sandbox. Do not use a local filesystem or a local shell.";

/**
 * Host discovery is not consulted. Extensions, skills, prompts, themes, and
 * AGENTS.md stay empty even if those files exist in cwd or agentDir.
 */
class SealedResourceLoader implements ResourceLoader {
  getExtensions() {
    return { extensions: [], errors: [], warnings: [], runtime: createExtensionRuntime() };
  }
  getSkills() {
    return { skills: [], diagnostics: [] };
  }
  getPrompts() {
    return { prompts: [], diagnostics: [] };
  }
  getThemes() {
    return { themes: [], diagnostics: [] };
  }
  getAgentsFiles() {
    return { agentsFiles: [] };
  }
  getSystemPrompt(): string {
    return SYSTEM_PROMPT;
  }
  getSystemPromptSource() {
    return undefined;
  }
  getAppendSystemPrompt() {
    return [];
  }
  getAppendSystemPromptSources() {
    return [];
  }
  extendResources(): void {
    throw new Error("resource autoload is disabled");
  }
  async reload(): Promise<void> {}
}

export async function openLockedSession(input: {
  dir: string;
  pin: RunPin;
  capability: GatewayCapability;
  gatewayOrigin: string;
  gatewayBaseUrl?: string;
  transcript: TranscriptMessage[];
  executor: AttemptExecutor;
  observer: GatewayObserver;
  stop(status: RunStatus): void;
  clarify?: (question: string) => void;
}) {
  const settings = SettingsManager.inMemory();
  settings.setRetryEnabled(false);
  settings.setCompactionEnabled(false);
  settings.setCacheWarmingMode("off");
  const model = pinnedModel(input.pin, input.gatewayBaseUrl ?? input.gatewayOrigin);
  const sessionManager = SessionManager.inMemory(input.dir);
  seedTranscript(sessionManager, input.transcript, model);
  const guard: ToolGuard = {
    executor: input.executor,
    observer: input.observer,
    maxTotalTokens: input.pin.budget.maxTotalTokens,
    stop: input.stop,
    clarify: input.clarify,
  };
  const modelRuntime = await createGatewayRuntime(input.pin, input.capability);
  const customTools = remoteTools(guard);
  const { session, extensionsResult, modelFallbackMessage } = await createAgentSession({
    cwd: input.dir,
    agentDir: input.dir,
    model,
    thinkingLevel: "off",
    modelRuntime,
    noTools: "all",
    tools: customTools.map((tool) => tool.name),
    customTools,
    resourceLoader: new SealedResourceLoader(),
    sessionManager,
    settingsManager: settings,
  });
  return { session, extensionsResult, modelFallbackMessage };
}

function seedTranscript(
  manager: SessionManager,
  transcript: TranscriptMessage[],
  model: ReturnType<typeof pinnedModel>,
): void {
  for (const message of transcript) {
    if (message.role === "tool") continue;
    if (message.role === "user") {
      manager.appendMessage({ role: "user", content: message.text, timestamp: Date.now() });
      continue;
    }
    manager.appendMessage({
      role: "assistant",
      content: [{ type: "text", text: message.text }],
      api: model.api,
      provider: model.provider,
      model: model.id,
      usage: {
        input: 0,
        output: 0,
        cacheRead: 0,
        cacheWrite: 0,
        totalTokens: 0,
        cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
      },
      stopReason: "stop",
      timestamp: Date.now(),
    });
  }
}

export function transcriptFrom(messages: readonly unknown[]): TranscriptMessage[] {
  return messages.flatMap((message) => {
    if (!message || typeof message !== "object" || !("role" in message)) return [];
    const role = (message as { role?: unknown }).role;
    if (role !== "user" && role !== "assistant" && role !== "toolResult") return [];
    const content = "content" in message ? (message as { content?: unknown }).content : undefined;
    return [{ role: role === "toolResult" ? "tool" : role, text: textOf(content) }];
  });
}

function textOf(content: unknown): string {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content
    .map((part) => {
      if (!part || typeof part !== "object") return "";
      const record = part as { type?: string; text?: string };
      return record.type === "text" && typeof record.text === "string" ? record.text : "";
    })
    .filter((part) => part.length > 0)
    .join("");
}
