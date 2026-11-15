"use client";
import * as React from "react";
import { CheckCircle2, Trash2 } from "lucide-react";
import { Tooltip } from "@/components/ui/tooltip";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { hostedSetupAction, revokeCredentialAction, saveCredentialAction, validateCredentialAction } from "@/app/(app)/rooms/hosted-actions";
import type { HostedProvider, ProviderCredential, CredentialScope } from "@/lib/project-room/hosted";

export function AIConnections() {
  const [ready, setReady] = React.useState(false);
  const [credentials, setCredentials] = React.useState<ProviderCredential[]>([]);
  const [notice, setNotice] = React.useState("Checking secure key storage…");
  const [provider, setProvider] = React.useState<HostedProvider>("openai");
  const [scope, setScope] = React.useState<CredentialScope>("personal");
  const [canManageTeam, setCanManageTeam] = React.useState(false);
  const [replaceId, setReplaceId] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const busyRef = React.useRef(false);
  React.useEffect(() => {
    let active = true;
    void hostedSetupAction().then((result) => {
      if (!active) return;
      if (!result.ok) { setNotice("AI setup is unavailable. The workspace administrator needs to configure secure AI execution before you can save a key."); return; }
      setReady(true); setCanManageTeam(result.canManageTeam); setCredentials(result.credentials); setNotice("");
    }).catch(() => { if (active) setNotice("Could not load AI connections. Refresh to try again."); });
    return () => { active = false; };
  }, []);
  async function reloadCredentials() {
    const result = await hostedSetupAction();
    if (result.ok) { setCredentials(result.credentials); setCanManageTeam(result.canManageTeam); }
    else { setReady(false); setCredentials([]); setNotice(result.message); }
  }
  async function mutate(work: () => Promise<void>) {
    if (busyRef.current) return;
    busyRef.current = true; setBusy(true);
    try { await work(); } catch { setNotice("The connection could not be updated. Please try again."); }
    finally { busyRef.current = false; setBusy(false); }
  }
  return <div className="space-y-5">
    {notice ? <p role="status" className="rounded-lg border border-border bg-muted/20 p-3 text-sm text-muted-foreground">{notice}</p> : null}
      <section className="space-y-5" aria-label="AI connections">
        <p className="text-sm text-muted-foreground">Connections can be reused across every project in this organization. Personal connections are usable only by their owner. Team connections are usable by authorized editors and managed by organization admins. Saved keys are never revealed.</p>
        <h3 className="text-sm font-semibold">Saved connections</h3>
        {credentials.length === 0 ? <p className="text-sm text-muted-foreground">No API keys added yet. Add your first connection below.</p> : null}
        <ul className="space-y-2">{credentials.map((item) => <li key={item.id} className="flex flex-wrap items-center gap-2 text-sm">
          <span>{item.label || item.provider} · {item.provider} · {item.scope === "team" ? "Team" : "Personal"} · ending {item.lastFour} · {item.revokedAt ? "Revoked" : item.validatedAt ? "Validated" : "Not validated"}</span>
          <Tooltip label="Validate connection" align="end"><Button aria-label="Validate connection" type="button" size="sm" variant="outline" disabled={busy || !item.canManage || !!item.revokedAt} onClick={() => void mutate(async () => { const result = await validateCredentialAction(item.id); setNotice(result.ok ? "Credential validated." : result.message); await reloadCredentials(); })}><CheckCircle2 className="h-4 w-4" aria-hidden="true" /></Button></Tooltip>
          <Tooltip label="Revoke connection" align="end"><Button aria-label="Revoke connection" type="button" size="sm" variant="outline" disabled={busy || !item.canManage || !!item.revokedAt} onClick={() => void mutate(async () => { const result = await revokeCredentialAction(item.id); setNotice(result.ok ? "Credential revoked. New model calls cannot use it." : result.message); await reloadCredentials(); })}><Trash2 className="h-4 w-4" aria-hidden="true" /></Button></Tooltip>
        </li>)}</ul>
        <form className="grid gap-4 rounded-xl border border-border bg-background p-5 sm:grid-cols-2" onSubmit={(event) => {
          event.preventDefault(); if (!ready || busyRef.current) return;
          const form = event.currentTarget; const data = new FormData(form); const key = String(data.get("api-key") ?? ""); const label = String(data.get("label") ?? "");
          form.reset(); // Clear plaintext from the DOM before awaiting any network request.
          void mutate(async () => { const result = await saveCredentialAction(provider, label, key, replaceId || undefined, scope);
            if (!result.ok) { setNotice(result.message); return; } setNotice("Credential saved. Validate it before running a task."); await reloadCredentials(); });
        }}>
          <h3 className="text-sm font-semibold sm:col-span-2">Add or replace an API key</h3>
          <fieldset disabled={!ready || busy} className="contents">
          <label className="text-sm">Connection<select aria-label="Connection to replace" value={replaceId} onChange={(event) => { setReplaceId(event.target.value); const existing = credentials.find((item) => item.id === event.target.value); if (existing) { setProvider(existing.provider); setScope(existing.scope); } }} className="mt-1 block w-full rounded-md border border-border bg-background px-2 py-2 text-xs"><option value="">New connection</option>{credentials.filter((item) => item.canManage && !item.revokedAt).map((item) => <option key={item.id} value={item.id}>Replace {item.label || item.provider} · {item.scope} ({item.lastFour})</option>)}</select></label>
          <label className="text-sm">Provider<select aria-label="Credential provider" value={provider} disabled={!!replaceId} onChange={(event) => setProvider(event.target.value as HostedProvider)} className="mt-1 block w-full rounded-md border border-border bg-background px-2 py-2 text-xs"><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="openrouter">OpenRouter</option></select></label>
          <label className="text-sm">Access<select aria-label="Connection access" disabled={!!replaceId} value={scope} onChange={(event) => setScope(event.target.value as CredentialScope)} className="mt-1 block w-full rounded-md border border-border bg-background px-2 py-2 text-xs"><option value="personal">Personal · only me</option>{canManageTeam ? <option value="team">Team · all projects in this organization</option> : null}</select></label>
          <label className="text-sm">Connection name<Input name="label" aria-label="Connection label" maxLength={120} placeholder="e.g. Team OpenAI" className="mt-2" /></label>
          <label className="text-sm sm:col-span-2">API key<Input name="api-key" aria-label="Provider API key" type="password" autoComplete="new-password" minLength={8} maxLength={4096} required placeholder="Paste your provider API key" className="mt-2" /></label>
          <Button type="submit" className="w-fit" disabled={!ready || busy}>{replaceId ? "Replace key" : "Save key"}</Button>
          </fieldset>
        </form>
      </section>
  </div>;
}
