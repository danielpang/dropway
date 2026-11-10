import { beforeEach, describe, expect, it, vi } from "vitest";
import { parseCredential, validHostedRun, type HostedRunInput } from "@/lib/project-room/hosted";

const calls = vi.hoisted(() => ({ setup: vi.fn(), save: vi.fn(), run: vi.fn(), resume: vi.fn(), detail: vi.fn() }));
vi.mock("@/lib/api", () => ({
  ApiError: class extends Error { constructor(public status: number, message: string, public body: unknown) { super(message); } },
  api: { listProviderCredentials: calls.setup, saveProviderCredential: calls.save, runHostedJob: calls.run, resumeHostedJob: calls.resume, getHostedJob: calls.detail },
}));
import { ApiError } from "@/lib/api";
import { getHostedJobAction, hostedSetupAction, resumeHostedJobAction, runHostedJobAction, saveCredentialAction } from "@/app/(app)/rooms/hosted-actions";

const input: HostedRunInput = { roomId: "room", siteId: "site", versionId: "version", credentialRef: "credential", provider: "anthropic", modelId: "claude-sonnet-4-5", prompt: "Revise the caption", idempotencyKey: "request-1", threadId: "thread", budget: { max_tokens: 20000, max_calls: 10, max_seconds: 300, max_output_bytes: 2097152 } };
const job = { id: "job", room_id: "room", site_id: "site", version_id: "version", content_hash: "a".repeat(64), state: "queued", config: { provider: "anthropic", model_id: "claude-sonnet-4-5", harness_id: "pi", harness_version: "1.5.0", prompt: input.prompt, budget: input.budget } };
beforeEach(() => { vi.clearAllMocks(); vi.stubEnv("NEXT_PUBLIC_COLLAB_REVIEW", "1"); vi.stubEnv("NEXT_PUBLIC_COLLAB_EXECUTION", "1"); });

describe("hosted room boundary", () => {
  it("collects no key when execution is disabled, and requires explicit backend readiness", async () => {
    vi.stubEnv("NEXT_PUBLIC_COLLAB_EXECUTION", "0");
    expect(await saveCredentialAction("anthropic", "personal", "secret-key-material")).toMatchObject({ ok: false });
    expect(calls.save).not.toHaveBeenCalled();
    vi.stubEnv("NEXT_PUBLIC_COLLAB_EXECUTION", "1");
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
