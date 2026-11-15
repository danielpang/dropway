export type HostedProvider = "anthropic" | "openrouter" | "openai";
export type CredentialScope = "personal" | "team";
export interface MentionExecution { credential_ref: string; provider: HostedProvider; model_id: string; budget: HostedBudget; idempotency_key: string }
export const hasDropwayMention = (body: string): boolean => /(^|[\s(])@dropway($|[\s,.!?:;)])/i.test(body);
export interface ProviderCredential { scope: CredentialScope; canManage: boolean; ownerUserId: string; id: string; provider: HostedProvider; label: string; lastFour: string; validatedAt: string | null; revokedAt: string | null; generation: number }
export interface HostedBudget { max_tokens: number; max_calls: number; max_seconds: number; max_output_bytes: number }
export interface HostedJob {
  id: string; initiatorUserId: string; roomId: string; siteId: string; versionId: string; contentHash: string; state: string;
  sessionId: string | null; candidateId: string | null; failureCode: string | null; tokensUsed: number; callsUsed: number;
  config: { provider: string; modelId: string; harnessId: string; harnessVersion: string; prompt: string; threadId: string | null; budget: HostedBudget };
}
export interface HostedEvent { id: string; kind: string; message: string; createdAt: string }
export interface HostedRunInput { roomId: string; siteId: string; versionId: string; credentialRef: string; provider: HostedProvider; modelId: string; prompt: string; threadId?: string; idempotencyKey: string; budget: HostedBudget }
const object = (value: unknown): Record<string, unknown> | null => value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
const string = (value: unknown): string => typeof value === "string" ? value : "";
const optional = (value: unknown): string | null => typeof value === "string" && value ? value : null;
export const HOSTED_MODELS = ["claude-sonnet-5-5", "claude-opus-5-5", "claude-fable-5-2", "claude-haiku-5-5", "claude-sonnet-4-5", "claude-haiku-4-5"] as const;
export const OPENAI_MODELS = ["gpt-4.1", "gpt-4.1-mini"] as const;
export function hostedModels(provider: HostedProvider): readonly string[] { return provider === "openai" ? OPENAI_MODELS : HOSTED_MODELS; }
export function parseCredential(value: unknown): ProviderCredential | null {
  const row = object(value);
  if (!row || !string(row.id) || (row.provider !== "anthropic" && row.provider !== "openrouter" && row.provider !== "openai")) return null;
  return { scope: row.scope === "team" ? "team" : "personal", canManage: row.can_manage === true, ownerUserId: string(row.owner_user_id), id: string(row.id), provider: row.provider, label: string(row.label), lastFour: string(row.last_four).slice(-4),
    validatedAt: optional(row.validated_at), revokedAt: optional(row.revoked_at), generation: typeof row.generation === "number" ? row.generation : 0 };
}
export function parseHostedJob(value: unknown): HostedJob | null {
  const row = object(value); const config = object(row?.config); const budget = object(config?.budget);
  if (!row || !config || !string(row.id) || !string(row.room_id) || !string(row.site_id) || !string(row.version_id) || !string(row.state)) return null;
  return { id: string(row.id), initiatorUserId: string(row.initiator_user_id), roomId: string(row.room_id), siteId: string(row.site_id), versionId: string(row.version_id), contentHash: string(row.content_hash), state: string(row.state),
    sessionId: optional(row.session_id), candidateId: optional(row.candidate_id), failureCode: optional(row.failure_code), tokensUsed: Number(row.tokens_used) || 0, callsUsed: Number(row.calls_used) || 0, config: { provider: string(config.provider), modelId: string(config.model_id), harnessId: string(config.harness_id), harnessVersion: string(config.harness_version), prompt: string(config.prompt), threadId: optional(config.thread_id),
      budget: { max_tokens: Number(budget?.max_tokens) || 0, max_calls: Number(budget?.max_calls) || 0, max_seconds: Number(budget?.max_seconds) || 0, max_output_bytes: Number(budget?.max_output_bytes) || 0 } } };
}
export function parseHostedEvents(value: unknown): HostedEvent[] {
  return Array.isArray(value) ? value.slice(0, 200).flatMap((item) => { const row = object(item); return row && string(row.id) ? [{ id: string(row.id), kind: string(row.kind), message: string(row.message), createdAt: string(row.created_at) }] : []; }) : [];
}
export function validHostedRun(input: HostedRunInput): boolean {
  if (!input || typeof input !== "object" || !input.budget || typeof input.budget !== "object"
    || [input.roomId, input.siteId, input.versionId, input.credentialRef, input.idempotencyKey, input.prompt, input.provider, input.modelId].some((value) => typeof value !== "string")) return false;
  const model = input.provider === "openrouter" ? input.modelId.replace(/^anthropic\//, "").replace(/\./g, "-") : input.modelId;
  return Boolean(input.roomId && input.siteId && input.versionId && input.credentialRef && input.idempotencyKey && input.prompt.trim())
    && (input.provider === "anthropic" || input.provider === "openrouter" || input.provider === "openai") && hostedModels(input.provider).includes(model)
    && input.prompt.length <= 16000
    && [[input.budget.max_tokens, 500000], [input.budget.max_calls, 100], [input.budget.max_seconds, 900], [input.budget.max_output_bytes, 10485760]].every(([value, max]) => Number.isInteger(value) && value! > 0 && value! <= max!);
}

export function hostedFailureMessage(code: string | null): string {
  if (code === "budget_exhausted" || code === "time_budget_exhausted") return "The authorized task limit was reached. Review its progress before authorizing another task.";
  if (code === "authorization_changed") return "Source access, membership, the live baseline or credential authorization changed. Refresh and verify them before starting another task.";
  if (code === "provider_model_unavailable") return "This model is no longer available from your provider. Choose a current model in project AI settings.";
  if (code === "provider_authentication") return "The provider rejected this API key or its permissions. Check your AI connection.";
  if (code === "provider_rate_limited") return "The provider is temporarily limiting requests. Please try again shortly.";
  if (code === "provider_rejected") return "The provider rejected the model request. No changes were applied.";
  if (code === "provider_unavailable" || code === "provider_response_invalid") return "The model provider could not complete the request. No changes were applied.";
  if (code === "usage_unverified") return "Provider usage could not be verified. The task stopped without applying a change; check the provider connection before retrying.";
  if (code === "lease_expired") return "The worker stopped responding before its lease expired. Start a new task after worker health is restored.";
  return "The hosted attempt stopped without applying a change. Check the provider connection and worker status before starting a new task.";
}

export interface AIPreferences { mode: "local" | "api"; credential_ref: string; provider: string; model: string }
export const EMPTY_AI_PREFERENCES: AIPreferences = { mode: "local", credential_ref: "", provider: "", model: "" };
export function currentHostedModel(provider: string, model: string): string {
  return (provider === "anthropic" || provider === "openrouter") && model === "claude-opus-4-1" ? "claude-opus-5-5" : model;
}
export function preferredModel(credential: ProviderCredential, personal: AIPreferences, project?: AIPreferences): string {
  const selected = project?.provider === credential.provider && project.model ? project.model : personal.provider === credential.provider ? personal.model : "";
  const model = currentHostedModel(credential.provider, selected);
  return hostedModels(credential.provider).includes(model) ? model : hostedModels(credential.provider)[0]!;
}

/** Only unambiguous provider prefixes are recognized. Never infer generic sk- keys. */
export function keyProvider(key: string): HostedProvider | null {
  if (key.startsWith("sk-ant-")) return "anthropic";
  if (key.startsWith("sk-or-v1-")) return "openrouter";
  if (key.startsWith("sk-proj-") || key.startsWith("sk-svcacct-")) return "openai";
  return null;
}

export function providerModelId(provider: HostedProvider, model: string): string { return provider === "openrouter" ? `anthropic/${model.replace(/-(\d+)-(\d+)$/, "-$1.$2")}` : model; }
