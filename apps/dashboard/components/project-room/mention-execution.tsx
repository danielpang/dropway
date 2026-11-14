"use client";
import * as React from "react";
import { hostedSetupAction } from "@/app/(app)/rooms/hosted-actions";
import { hostedModels, type MentionExecution, type ProviderCredential } from "@/lib/project-room/hosted";

/** Mounted only for an explicit mention. A mention alone never authorizes a charge. */
export function MentionExecutionControls({ body, allowed, onChange }: { body: string; allowed: boolean; onChange: (value: MentionExecution | undefined) => void }) {
  const [connections, setConnections] = React.useState<ProviderCredential[]>([]);
  const [credentialId, setCredentialId] = React.useState("");
  const [model, setModel] = React.useState("");
  const [tokens, setTokens] = React.useState(20000);
  const [calls, setCalls] = React.useState(10);
  const [requestId, setRequestId] = React.useState("");
  const [notice, setNotice] = React.useState("Checking reusable connections…");
  const callback = React.useRef(onChange); callback.current = onChange;
  const credential = connections.find((item) => item.id === credentialId);
  React.useEffect(() => {
    let active = true;
    if (allowed) void hostedSetupAction().then((result) => {
      if (!active) return;
      if (!result.ok) { setNotice(result.message); return; }
      const available = result.credentials.filter((item) => !item.revokedAt && item.validatedAt);
      setConnections(available); setNotice(available.length ? "Choose the connection charged for this request." : "Save and validate a connection in Hosted revisions first.");
    }).catch(() => { if (active) setNotice("Connections could not be loaded."); });
    return () => { active = false; };
  }, [allowed]);
  React.useEffect(() => { setRequestId(""); }, [body, credentialId, model, tokens, calls, allowed]);
  React.useEffect(() => {
    if (credential && !hostedModels(credential.provider).includes(model)) setModel(hostedModels(credential.provider)[0]!);
  }, [credential, model]);
  React.useEffect(() => {
    callback.current(allowed && requestId && credential && Number.isInteger(tokens) && tokens > 0 && tokens <= 500000 && Number.isInteger(calls) && calls > 0 && calls <= 100 && hostedModels(credential.provider).includes(model) ? {
      credential_ref: credential.id, provider: credential.provider, model_id: credential.provider === "openrouter" ? `anthropic/${model}` : model,
      idempotency_key: requestId, budget: { max_tokens: tokens, max_calls: calls, max_seconds: 300, max_output_bytes: 2097152 },
    } : undefined);
  }, [allowed, requestId, credential, model, tokens, calls]);
  React.useEffect(() => () => callback.current(undefined), []);
  if (!allowed) return <p className="text-sm text-muted-foreground">@dropway needs hosted execution, an owner/editor with site-edit permission, and the current site version. Remove the mention to post an ordinary comment.</p>;
  return <fieldset className="w-full space-y-2 rounded border border-border p-3">
    <legend className="text-sm font-medium">Authorize @dropway revision</legend>
    <p className="text-sm text-muted-foreground">{notice}</p>
    <label className="block text-sm">Connection<select aria-label="Mention connection" className="ml-2 rounded border bg-background p-1" value={credentialId} onChange={(event) => setCredentialId(event.target.value)}><option value="">Choose a connection</option>{connections.map((item) => <option key={item.id} value={item.id}>{item.label || item.provider} · {item.scope} · {item.lastFour}</option>)}</select></label>
    <label className="block text-sm">Model<select aria-label="Mention model" className="ml-2 rounded border bg-background p-1" value={model} onChange={(event) => setModel(event.target.value)}><option value="">Choose a model</option>{credential ? hostedModels(credential.provider).map((item) => <option key={item}>{item}</option>) : null}</select></label>
    <label className="block text-sm">Maximum tokens<input aria-label="Mention maximum tokens" className="ml-2 rounded border bg-background p-1" type="number" min={1} max={500000} value={tokens} onChange={(event) => setTokens(Number(event.target.value))} /></label>
    <label className="block text-sm">Maximum model calls<input aria-label="Mention maximum calls" className="ml-2 rounded border bg-background p-1" type="number" min={1} max={100} value={calls} onChange={(event) => setCalls(Number(event.target.value))} /></label>
    <label className="flex gap-2 text-sm"><input type="checkbox" checked={!!requestId} disabled={!credential || !model} onChange={(event) => setRequestId(event.target.checked ? crypto.randomUUID() : "")} />Posting this comment authorizes paid model and sandbox work using this connection, capped at the selected limits, 5 minutes, and 2 MiB of output. These limits are not a dollar cap. The result requires separate human acceptance or rejection and Apply; it never publishes automatically.</label>
  </fieldset>;
}
