"use server";
import { api, ApiError } from "@/lib/api";
import { parseCredential, parseHostedEvents, parseHostedJob, validHostedRun, type HostedRunInput, type HostedProvider } from "@/lib/project-room/hosted";
import { collaborationEnabled } from "@/lib/project-room/capabilities";
const closed = () => ({ ok: false as const, message: "Hosted execution is not enabled." });
function failure(error: unknown) {
  // Provider errors and request bodies can contain secrets. Never forward them to the client.
  const status = error instanceof ApiError ? error.status : 0;
  const code = error instanceof ApiError && error.body && typeof error.body === "object" && "error" in error.body ? error.body.error : null;
  if (code === "budget_exhausted") return { ok: false as const, message: "The task limit is exhausted. Review the result and authorize a new bounded task if more work is needed." };
  if (code === "credential_invalid") return { ok: false as const, message: "The provider rejected this key. Replace it with an active personal API key and validate again." };
  return { ok: false as const, message: status === 503 ? "Hosted execution is not configured. Comments and review remain available."
    : status === 403 ? "You do not have permission for this operation, or this credential is not yours."
      : status === 409 ? "The task or its source changed. Refresh before continuing."
        : status === 400 || status === 422 ? "Check the credential, supported model and task limits."
          : "The hosted request did not complete. Refresh its status before retrying." };
}
function row(value: unknown): Record<string, unknown> { return value && typeof value === "object" ? value as Record<string, unknown> : {}; }
export async function hostedSetupAction() {
  if (!(await collaborationEnabled(true))) return closed();
  try { const result = row(await api.listProviderCredentials());
    if (result.execution_enabled !== true || !Array.isArray(result.credentials)) return closed();
    const me = await api.me();
    if (!me.user_id) return closed();
    return { ok: true as const, userId: me.user_id, orgId: me.org_id ?? "", credentials: result.credentials.flatMap((item) => { const parsed = parseCredential(item); return parsed ? [parsed] : []; }) };
  } catch (error) { return failure(error); }
}
export async function saveCredentialAction(provider: HostedProvider, label: string, key: string, id?: string) {
  if (!(await collaborationEnabled(true))) return closed();
  if (typeof key !== "string" || typeof label !== "string" || (id !== undefined && typeof id !== "string") || !["anthropic", "openrouter"].includes(provider) || key.length < 8 || key.length > 4096 || label.length > 120) return { ok: false as const, message: "Enter a supported provider, label and valid API key." };
  try { const parsed = parseCredential(row(await api.saveProviderCredential({ provider, label, api_key: key }, id)).credential);
    return parsed ? { ok: true as const, credential: parsed } : { ok: false as const, message: "The saved credential metadata was not usable." };
  } catch (error) { return failure(error); }
}
export async function validateCredentialAction(id: string) {
  if (!(await collaborationEnabled(true))) return closed();
  try { const result = row(await api.validateProviderCredential(id)); return result.validated === true ? { ok: true as const } : { ok: false as const, message: "This credential could not be validated." }; } catch (error) { return failure(error); }
}
export async function revokeCredentialAction(id: string) {
  if (!(await collaborationEnabled(true))) return closed();
  try { await api.revokeProviderCredential(id); return { ok: true as const }; } catch (error) { return failure(error); }
}
export async function listHostedJobsAction(roomId: string) {
  if (!(await collaborationEnabled(true))) return closed();
  try { const result = row(await api.listHostedJobs(roomId));
    if (!Array.isArray(result.jobs)) return { ok: false as const, message: "The task list was not usable." };
    return { ok: true as const, jobs: result.jobs.flatMap((item) => { const job = parseHostedJob(item); return job?.roomId === roomId ? [job] : []; }) };
  } catch (error) { return failure(error); }
}
export async function getHostedJobAction(roomId: string, jobId: string) {
  if (!(await collaborationEnabled(true))) return closed();
  try { const result = row(await api.getHostedJob(roomId, jobId)); const job = parseHostedJob(result.job);
    if (!job || job.id !== jobId || job.roomId !== roomId) return { ok: false as const, message: "This task is not available in this room." };
    return { ok: true as const, job, events: parseHostedEvents(result.events), question: typeof result.question === "string" ? result.question : null };
  } catch (error) { return failure(error); }
}
export async function runHostedJobAction(input: HostedRunInput) {
  if (!(await collaborationEnabled(true))) return closed();
  if (!validHostedRun(input)) return { ok: false as const, message: "Check the source version, credential, request and bounded task limits." };
  try { const job = parseHostedJob(row(await api.runHostedJob(input.roomId, { site_id: input.siteId, version_id: input.versionId, credential_ref: input.credentialRef, provider: input.provider,
    model_id: input.modelId, prompt: input.prompt, thread_id: input.threadId, idempotency_key: input.idempotencyKey, budget: input.budget })).job);
    return job?.roomId === input.roomId ? { ok: true as const, job } : { ok: false as const, message: "The task receipt was not usable. Refresh before retrying." };
  } catch (error) { return failure(error); }
}
export async function cancelHostedJobAction(roomId: string, jobId: string) {
  if (!(await collaborationEnabled(true))) return closed();
  try { await api.cancelHostedJob(roomId, jobId); return { ok: true as const }; } catch (error) { return failure(error); }
}
export async function resumeHostedJobAction(roomId: string, sessionId: string, answer: string, idempotencyKey: string) {
  if (!(await collaborationEnabled(true))) return closed();
  if (typeof answer !== "string" || typeof idempotencyKey !== "string" || !answer.trim() || answer.length > 16000 || !idempotencyKey) return { ok: false as const, message: "Write an answer before continuing." };
  try { const job = parseHostedJob(row(await api.resumeHostedJob(roomId, sessionId, answer, idempotencyKey)).job);
    return job?.roomId === roomId ? { ok: true as const, job } : { ok: false as const, message: "The continuation receipt was not usable. Refresh before retrying." };
  } catch (error) { return failure(error); }
}
