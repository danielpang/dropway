"use client";
import * as React from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cancelHostedJobAction, getHostedJobAction, hostedSetupAction, listHostedJobsAction, resumeHostedJobAction, revokeCredentialAction, runHostedJobAction, saveCredentialAction, validateCredentialAction } from "@/app/(app)/rooms/hosted-actions";
import { HOSTED_MODELS, hostedFailureMessage, type HostedEvent, type HostedJob, type HostedProvider, type ProviderCredential } from "@/lib/project-room/hosted";

export function HostedExecution({ roomId, siteId, versionId, canRun, threads, historicalThreadCount = 0, onCandidateReady }: {
  roomId: string; siteId: string; versionId: string; canRun: boolean;
  threads: { id: string; page: string }[]; historicalThreadCount?: number; onCandidateReady: () => void;
}) {
  const [userId, setUserId] = React.useState("");
  const defaultsKey = React.useRef("");
  const [ready, setReady] = React.useState(false);
  const [credentials, setCredentials] = React.useState<ProviderCredential[]>([]);
  const [notice, setNotice] = React.useState("Checking hosted execution availability…");
  const [provider, setProvider] = React.useState<HostedProvider>("anthropic");
  const [replaceId, setReplaceId] = React.useState("");
  const [credentialId, setCredentialId] = React.useState("");
  const [model, setModel] = React.useState<string>(HOSTED_MODELS[0]);
  const [prompt, setPrompt] = React.useState("");
  const [threadId, setThreadId] = React.useState("");
  const [maxTokens, setMaxTokens] = React.useState(20000);
  const [maxCalls, setMaxCalls] = React.useState(10);
  const [authorized, setAuthorized] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const busyRef = React.useRef(false);
  const runKey = React.useRef("");
  const answerKey = React.useRef("");
  const [jobs, setJobs] = React.useState<HostedJob[]>([]);
  const [jobId, setJobId] = React.useState("");
  const [detail, setDetail] = React.useState<{ job: HostedJob; events: HostedEvent[]; question: string | null } | null>(null);
  const [answer, setAnswer] = React.useState("");
  const refreshRef = React.useRef(onCandidateReady);
  refreshRef.current = onCandidateReady;
  const seenCandidates = React.useRef(new Set<string>());
  const canContinue = Boolean(detail && canRun && detail.job.siteId === siteId && detail.job.initiatorUserId === userId);
  const credential = credentials.find((item) => item.id === credentialId && !item.revokedAt) ?? null;

  React.useEffect(() => {
    try { const saved = localStorage.getItem("dropway-hosted-model-v1"); if (saved && HOSTED_MODELS.includes(saved as typeof HOSTED_MODELS[number])) setModel(saved); } catch { /* optional preference */ }
  }, []);
  React.useEffect(() => { runKey.current = ""; setAuthorized(false); }, [siteId, versionId, credentialId, model, prompt, threadId, maxTokens, maxCalls]);
  React.useEffect(() => { answerKey.current = ""; }, [answer, detail?.job.sessionId]);
  React.useEffect(() => { if (threadId && !threads.some((item) => item.id === threadId)) setThreadId(""); }, [threads, threadId]);
  React.useEffect(() => {
    let active = true;
    void hostedSetupAction().then((result) => {
      if (!active) return;
      if (!result.ok) { setNotice(result.message); return; }
      setReady(true); setUserId(result.userId); setCredentials(result.credentials); setNotice("Your credential is used only for tasks you explicitly authorize.");
      setCredentialId(result.credentials.find((item) => !item.revokedAt)?.id ?? "");
      defaultsKey.current = `dropway-hosted-defaults-v1:${result.orgId}:${result.userId}`;
      try {
        const saved = JSON.parse(localStorage.getItem(defaultsKey.current) ?? "null") as { credentialId?: string; model?: string; maxTokens?: number; maxCalls?: number } | null;
        if (saved?.credentialId && result.credentials.some((item) => item.id === saved.credentialId && !item.revokedAt)) setCredentialId(saved.credentialId);
        if (saved?.model && HOSTED_MODELS.includes(saved.model as typeof HOSTED_MODELS[number])) setModel(saved.model);
        if (Number.isInteger(saved?.maxTokens) && saved!.maxTokens! > 0 && saved!.maxTokens! <= 500000) setMaxTokens(saved!.maxTokens!);
        if (Number.isInteger(saved?.maxCalls) && saved!.maxCalls! > 0 && saved!.maxCalls! <= 100) setMaxCalls(saved!.maxCalls!);
      } catch { /* Preferences contain no keys and are optional. */ }
    }).catch(() => { if (active) setNotice("Hosted execution is unavailable. Comments and review remain available."); });
    return () => { active = false; };
  }, []);
  React.useEffect(() => {
    if (!ready) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const refresh = async () => {
      try {
        const result = await listHostedJobsAction(roomId);
        if (!active) return;
        if (!result.ok) { setJobs([]); setNotice(result.message); return; }
        setJobs(result.jobs);
        if (!jobId && result.jobs[0]) setJobId(result.jobs[0].id);
      } catch { if (active) setNotice("Task status could not be refreshed."); }
      finally { if (active) timer = setTimeout(() => void refresh(), 5000); }
    };
    void refresh();
    return () => { active = false; clearTimeout(timer); };
  }, [ready, roomId, jobId]);
  React.useEffect(() => {
    if (!ready || !jobId) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    setDetail(null);
    const refresh = async () => {
      try {
        const result = await getHostedJobAction(roomId, jobId);
        if (!active) return;
        if (!result.ok) { setDetail(null); setNotice(result.message); return; }
        setDetail(result);
        if (result.job.candidateId && !seenCandidates.current.has(result.job.candidateId)) {
          seenCandidates.current.add(result.job.candidateId); refreshRef.current();
        }
      } catch { if (active) setNotice("Task details could not be refreshed."); }
      finally { if (active) timer = setTimeout(() => void refresh(), 5000); }
    };
    void refresh();
    return () => { active = false; clearTimeout(timer); };
  }, [ready, roomId, jobId]);

  async function mutate(work: () => Promise<void>) {
    if (busyRef.current) return;
    busyRef.current = true; setBusy(true);
    try { await work(); } catch { setNotice("The request was interrupted. Refresh status before retrying; task retries reuse the same request identity."); }
    finally { busyRef.current = false; setBusy(false); }
  }
  async function reloadCredentials() {
    const result = await hostedSetupAction();
    if (result.ok) setCredentials(result.credentials); else { setReady(false); setCredentials([]); setNotice(result.message); }
  }

  return <section className="space-y-4" aria-label="Hosted execution">
    <h2 className="text-sm font-medium">Hosted revisions</h2>
    <p role="status" className="text-sm text-muted-foreground">{notice}</p>
    {!ready ? <p className="text-sm text-muted-foreground">No provider key is collected until the encrypted credential store and hosted worker are available.</p> : <>
      <details className="space-y-3 rounded-md border border-border p-3">
        <summary className="cursor-pointer text-sm font-medium">Your provider connections</summary>
        <p className="text-sm text-muted-foreground">Personal keys are write-only after saving. They are never shared with room members or the execution sandbox.</p>
        <ul className="space-y-2">{credentials.map((item) => <li key={item.id} className="flex flex-wrap items-center gap-2 text-sm">
          <span>{item.label || item.provider} · {item.provider} · ending {item.lastFour} · {item.revokedAt ? "Revoked" : item.validatedAt ? "Validated" : "Not validated"}</span>
          <Button type="button" size="sm" variant="outline" disabled={busy || !!item.revokedAt} onClick={() => void mutate(async () => { const result = await validateCredentialAction(item.id); setNotice(result.ok ? "Credential validated." : result.message); await reloadCredentials(); })}>Validate</Button>
          <Button type="button" size="sm" variant="outline" disabled={busy || !!item.revokedAt} onClick={() => void mutate(async () => { const result = await revokeCredentialAction(item.id); setNotice(result.ok ? "Credential revoked. New model calls cannot use it." : result.message); await reloadCredentials(); })}>Revoke</Button>
        </li>)}</ul>
        <form className="grid gap-2" onSubmit={(event) => {
          event.preventDefault(); if (busyRef.current) return;
          const form = event.currentTarget; const data = new FormData(form); const key = String(data.get("api-key") ?? ""); const label = String(data.get("label") ?? "");
          form.reset(); // Clear plaintext from the DOM before awaiting any network request.
          void mutate(async () => { const result = await saveCredentialAction(provider, label, key, replaceId || undefined);
            if (!result.ok) { setNotice(result.message); return; } setNotice("Credential saved. Validate it before running a task."); setCredentialId(result.credential.id); await reloadCredentials(); });
        }}>
          <label className="text-sm">Connection<select aria-label="Connection to replace" value={replaceId} onChange={(event) => { setReplaceId(event.target.value); const existing = credentials.find((item) => item.id === event.target.value); if (existing) setProvider(existing.provider); }} className="ml-2 rounded border bg-background p-1"><option value="">New connection</option>{credentials.filter((item) => !item.revokedAt).map((item) => <option key={item.id} value={item.id}>Replace {item.label || item.provider} ({item.lastFour})</option>)}</select></label>
          <label className="text-sm">Provider<select aria-label="Credential provider" value={provider} disabled={!!replaceId} onChange={(event) => setProvider(event.target.value as HostedProvider)} className="ml-2 rounded border bg-background p-1"><option value="anthropic">Anthropic</option><option value="openrouter">OpenRouter</option></select></label>
          <Input name="label" aria-label="Connection label" maxLength={120} placeholder="Personal key" />
          <Input name="api-key" aria-label="Provider API key" type="password" autoComplete="new-password" minLength={8} maxLength={4096} required />
          <Button type="submit" disabled={busy}>{replaceId ? "Replace key" : "Save key"}</Button>
        </form>
      </details>
      <form className="space-y-3 rounded-md border border-border p-3" onSubmit={(event) => {
        event.preventDefault(); if (!authorized || !canRun || !credential || busyRef.current) return;
        const key = runKey.current || (runKey.current = crypto.randomUUID());
        void mutate(async () => {
          const result = await runHostedJobAction({ roomId, siteId, versionId, credentialRef: credential.id, provider: credential.provider,
            modelId: credential.provider === "openrouter" ? `anthropic/${model}` : model, prompt, threadId: threadId || undefined, idempotencyKey: key,
            budget: { max_tokens: maxTokens, max_calls: maxCalls, max_seconds: 300, max_output_bytes: 2097152 } });
          if (!result.ok) { setNotice(result.message); return; }
          setJobId(result.job.id); setJobs((prev) => [result.job, ...prev.filter((item) => item.id !== result.job.id)]); setNotice("Task queued. It can propose a candidate, but cannot publish the live site.");
          runKey.current = ""; setAuthorized(false);
          try { localStorage.setItem(defaultsKey.current, JSON.stringify({ credentialId: credential.id, model, maxTokens, maxCalls })); } catch { /* optional preference */ }
        });
      }}>
        <p className="text-sm">Source: existing uploaded files for site {siteId}, immutable version {versionId}. Preset: static-site-v1. Harness: Pi 1.5.0.</p>
        <label className="block text-sm">Your credential<select aria-label="Run credential" value={credentialId} onChange={(event) => setCredentialId(event.target.value)} className="ml-2 rounded border bg-background p-1"><option value="">Choose a connection</option>{credentials.filter((item) => !item.revokedAt).map((item) => <option key={item.id} value={item.id}>{item.label || item.provider} ({item.lastFour})</option>)}</select></label>
        <label className="block text-sm">Model<select aria-label="Run model" value={model} onChange={(event) => setModel(event.target.value)} className="ml-2 rounded border bg-background p-1">{HOSTED_MODELS.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
        <label className="block text-sm">Related thread<select aria-label="Run thread" value={threadId} onChange={(event) => setThreadId(event.target.value)} className="ml-2 rounded border bg-background p-1"><option value="">No thread</option>{threads.map((item) => <option key={item.id} value={item.id}>{item.page} · {item.id}</option>)}</select></label>
        {historicalThreadCount > 0 ? <p className="text-xs text-muted-foreground">Historical or unmatched threads are not run targets. Review their original context, then create a thread on this source version for a new task. Reanchoring preserves the original version and does not silently move feedback to newer files.</p> : null}
        <textarea aria-label="Revision request" value={prompt} onChange={(event) => setPrompt(event.target.value)} maxLength={16000} required className="min-h-24 w-full rounded border bg-background p-2 text-sm" placeholder="Describe the revision to propose. Comments alone never start a task." />
        <div className="flex flex-wrap gap-3"><label className="text-sm">Maximum tokens<Input aria-label="Maximum task tokens" type="number" min={1} max={500000} value={maxTokens} onChange={(event) => setMaxTokens(Number(event.target.value))} /></label><label className="text-sm">Maximum model calls<Input aria-label="Maximum model calls" type="number" min={1} max={100} value={maxCalls} onChange={(event) => setMaxCalls(Number(event.target.value))} /></label></div>
        <p className="text-xs text-muted-foreground">This task is limited to 5 minutes and 2 MiB of output. Your provider charges for model usage; these are token and call limits, not a dollar estimate. Clarification resumes retain the task configuration.</p>
        <label className="flex items-start gap-2 text-sm"><input type="checkbox" checked={authorized} onChange={(event) => setAuthorized(event.target.checked)} />I authorize my own credential for this bounded request and its clarification resumes. Publishing still requires a separate human approval.</label>
        <Button type="submit" disabled={busy || !canRun || !credential?.validatedAt || !authorized || !prompt.trim() || !siteId || !versionId}>Run hosted revision</Button>
        {!canRun ? <p className="text-sm text-muted-foreground">An owner or editor also needs content-edit permission for this site.</p> : null}
      </form>
      <div className="space-y-2">
        <h3 className="text-sm font-medium">Tasks</h3>
        <ul className="space-y-1">{jobs.map((job) => <li key={job.id}><button type="button" className="text-left text-sm underline" aria-pressed={jobId === job.id} onClick={() => setJobId(job.id)}>{job.state} · {job.config.prompt.slice(0, 120)}</button></li>)}</ul>
        {detail ? <div className="space-y-2 rounded border border-border p-3">
          <p className="text-sm">{detail.job.state} · {detail.job.config.provider} / {detail.job.config.modelId} · {detail.job.config.harnessId} {detail.job.config.harnessVersion}</p>
          <p className="text-xs text-muted-foreground">Accounted token budget: {detail.job.tokensUsed} / {detail.job.config.budget.max_tokens}. Model calls: {detail.job.callsUsed} / {detail.job.config.budget.max_calls}. In-flight calls reserve tokens until usage is verified.</p>
          {detail.job.state === "failed" ? <p role="status" className="text-sm">{hostedFailureMessage(detail.job.failureCode)}</p> : null}
          <ul className="space-y-1 text-sm">{detail.events.map((item) => <li key={item.id}><span className="text-muted-foreground">{item.createdAt} · {item.kind}</span><p className="whitespace-pre-wrap">{item.message}</p></li>)}</ul>
          {detail.question ? <p className="whitespace-pre-wrap text-sm">{detail.question}</p> : null}
          {detail.question && detail.job.sessionId && canContinue ? <form className="space-y-2" onSubmit={(event) => { event.preventDefault(); const sessionId = detail.job.sessionId; if (!sessionId || !answer.trim()) return;
            const key = answerKey.current || (answerKey.current = crypto.randomUUID());
            void mutate(async () => { const result = await resumeHostedJobAction(roomId, sessionId, answer, key); if (!result.ok) { setNotice(result.message); return; } setJobId(result.job.id); setAnswer(""); setNotice("Answer recorded. A fresh attempt will continue with the same task configuration."); });
          }}><textarea aria-label="Clarification answer" value={answer} maxLength={16000} onChange={(event) => setAnswer(event.target.value)} className="min-h-20 w-full rounded border bg-background p-2 text-sm" /><Button type="submit" disabled={busy || !answer.trim()}>Answer and continue</Button><p className="text-xs text-muted-foreground">The execution sandbox is released while waiting. This answer is not approval to publish.</p></form> : null}
          {detail.question && !canContinue ? <p className="text-sm text-muted-foreground">Waiting for the initiating editor to answer this task on its original site.</p> : null}
          {detail.job.candidateId ? <Button type="button" variant="outline" onClick={onCandidateReady}>Refresh proposed changes for human review</Button> : null}
          {canContinue && !["completed", "failed", "cancelled", "candidate_ready"].includes(detail.job.state) ? <Button type="button" variant="outline" disabled={busy} onClick={() => void mutate(async () => { const result = await cancelHostedJobAction(roomId, detail.job.id); setNotice(result.ok ? "Cancellation recorded. No candidate is approved by cancelling." : result.message); })}>Cancel task</Button> : null}
        </div> : null}
      </div>
    </>}
  </section>;
}
