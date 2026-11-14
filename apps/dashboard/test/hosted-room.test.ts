import { beforeEach, describe, expect, it, vi } from "vitest";
import { parseCredential, validHostedRun, hasDropwayMention, hostedModels, type HostedRunInput } from "@/lib/project-room/hosted";

const calls = vi.hoisted(() => ({ capability: vi.fn(), setup: vi.fn(), save: vi.fn(), run: vi.fn(), resume: vi.fn(), detail: vi.fn() }));
vi.mock("@/lib/api", () => ({
  ApiError: class extends Error { constructor(public status: number, message: string, public body: unknown) { super(message); } },
  api: { projectCollaborationCapabilities: calls.capability, listProviderCredentials: calls.setup, saveProviderCredential: calls.save, runHostedJob: calls.run, resumeHostedJob: calls.resume, getHostedJob: calls.detail },
}));
import { ApiError } from "@/lib/api";
import { getHostedJobAction, hostedSetupAction, resumeHostedJobAction, runHostedJobAction, saveCredentialAction } from "@/app/(app)/rooms/hosted-actions";

const input: HostedRunInput = { roomId: "room", siteId: "site", versionId: "version", credentialRef: "credential", provider: "anthropic", modelId: "claude-sonnet-4-5", prompt: "Revise the caption", idempotencyKey: "request-1", threadId: "thread", budget: { max_tokens: 20000, max_calls: 10, max_seconds: 300, max_output_bytes: 2097152 } };
const job = { id: "job", room_id: "room", site_id: "site", version_id: "version", content_hash: "a".repeat(64), state: "queued", config: { provider: "anthropic", model_id: "claude-sonnet-4-5", harness_id: "pi", harness_version: "1.5.0", prompt: input.prompt, budget: input.budget } };
beforeEach(() => { vi.clearAllMocks(); calls.capability.mockResolvedValue({ enabled: true, execution_configured: true }); });

describe("hosted room boundary", () => {
  it("collects no key when execution is disabled, and requires explicit backend readiness", async () => {
    calls.capability.mockResolvedValue({ enabled: true, execution_configured: false });
    expect(await saveCredentialAction("anthropic", "personal", "secret-key-material")).toMatchObject({ ok: false });
    expect(calls.save).not.toHaveBeenCalled();
    calls.capability.mockResolvedValue({ enabled: true, execution_configured: true });
    calls.setup.mockResolvedValue({ credentials: [], execution_enabled: false });
    expect(await hostedSetupAction()).toMatchObject({ ok: false });
  });
  it("projects only masked credential metadata and never returns a provider error containing the key", async () => {
    const metadata = { id: "credential", provider: "anthropic", label: "Personal", last_four: "1234", validated_at: null, api_key: "must-not-appear", ciphertext: "must-not-appear" };
    expect(JSON.stringify(parseCredential(metadata))).not.toContain("must-not-appear");
    calls.save.mockResolvedValue({ credential: metadata });
    const result = await saveCredentialAction("anthropic", "Personal", "secret-key-material");
    expect(JSON.stringify(result)).not.toContain("must-not-appear");
    calls.save.mockRejectedValue(new ApiError(400, "Provider rejected secret-key-material", { key: "secret-key-material" }));
    expect(JSON.stringify(await saveCredentialAction("anthropic", "Personal", "secret-key-material"))).not.toContain("secret-key-material");
    calls.run.mockRejectedValue(new ApiError(409, "secret-key-material", { error: "budget_exhausted" }));
    expect(await runHostedJobAction(input)).toMatchObject({ ok: false, message: expect.stringContaining("limit is exhausted") });
  });
  it("forwards exact source, owner credential reference, model, limits and idempotency without a key", async () => {
    calls.run.mockResolvedValue({ job });
    expect(await runHostedJobAction(input)).toMatchObject({ ok: true, job: { id: "job", versionId: "version" } });
    expect(calls.run).toHaveBeenCalledWith("room", { site_id: "site", version_id: "version", credential_ref: "credential", provider: "anthropic", model_id: "claude-sonnet-4-5", prompt: input.prompt, thread_id: "thread", idempotency_key: "request-1", budget: input.budget });
    expect(validHostedRun({ ...input, budget: { ...input.budget, max_tokens: 500001 } })).toBe(false);
    expect(validHostedRun({ ...input, modelId: "unrequested-fallback" })).toBe(false);
    expect(validHostedRun({ ...input, prompt: " " })).toBe(false);
    expect(validHostedRun({ ...input, budget: undefined } as unknown as HostedRunInput)).toBe(false);
    expect(validHostedRun({ ...input, modelId: null } as unknown as HostedRunInput)).toBe(false);
  });
  it("binds detail and resume responses to the requested room and does not submit blank answers", async () => {
    calls.detail.mockResolvedValue({ job: { ...job, room_id: "other" }, events: [{ id: "private", message: "secret" }] });
    expect(await getHostedJobAction("room", "job")).toMatchObject({ ok: false });
    expect(await resumeHostedJobAction("room", "session", " ", "idempotency")).toMatchObject({ ok: false });
    expect(calls.resume).not.toHaveBeenCalled();
    calls.resume.mockResolvedValue({ job });
    expect(await resumeHostedJobAction("room", "session", "Use blue", "same-request")).toMatchObject({ ok: true });
    expect(calls.resume).toHaveBeenCalledWith("room", "session", "Use blue", "same-request");
  });
});


describe("reusable provider connections", () => {
  it("retains team management rights without exposing key material", async () => {
    const metadata = { id: "team-key", provider: "openai", scope: "team", can_manage: false, owner_user_id: "admin", last_four: "1234", api_key: "secret-not-returned" };
    expect(parseCredential(metadata)).toMatchObject({ scope: "team", canManage: false, ownerUserId: "admin", provider: "openai" });
    expect(JSON.stringify(parseCredential(metadata))).not.toContain("secret-not-returned");
    calls.save.mockResolvedValue({ credential: { ...metadata, can_manage: true } });
    await saveCredentialAction("openai", "Shared OpenAI", "private-key-material", "team-key", "team");
    expect(calls.save).toHaveBeenCalledWith({ provider: "openai", label: "Shared OpenAI", api_key: "private-key-material", scope: "team" }, "team-key");
  });
  it("validates provider-specific models without silently substituting a provider", () => {
    expect(hostedModels("openai")).toEqual(["gpt-4.1", "gpt-4.1-mini"]);
    expect(validHostedRun({ ...input, provider: "openai", modelId: "gpt-4.1" })).toBe(true);
    expect(validHostedRun({ ...input, provider: "openai", modelId: "gpt-4.1-mini" })).toBe(true);
    expect(validHostedRun({ ...input, provider: "openai", modelId: "claude-sonnet-4-5" })).toBe(false);
    expect(validHostedRun({ ...input, provider: "anthropic", modelId: "gpt-4.1" })).toBe(false);
  });
  it("recognizes explicit standalone mentions without treating emails or other handles as commands", () => {
    for (const text of ["@dropway revise", "Please (@DROPWAY) revise", "Hello @dropway!", "@dropway"]) expect(hasDropwayMention(text)).toBe(true);
    for (const text of ["ordinary comment", "person@dropway.com", "@dropway-helper", "@dropway2", "hello@dropway"]) expect(hasDropwayMention(text)).toBe(false);
  });
});
