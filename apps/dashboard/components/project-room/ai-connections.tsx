"use client";
import * as React from "react";
import Link from "next/link";
import {
  Check,
  Eye,
  EyeOff,
  KeyRound,
  Pencil,
  Terminal,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Tooltip } from "@/components/ui/tooltip";
import {
  hostedSetupAction,
  revokeCredentialAction,
  saveCredentialAction,
  validateCredentialAction,
  getAIPreferencesAction,
  saveAIPreferencesAction,
} from "@/app/(app)/rooms/hosted-actions";
import {
  EMPTY_AI_PREFERENCES,
  keyProvider,
  hostedModels,
  type HostedProvider,
  type ProviderCredential,
  type AIPreferences,
} from "@/lib/project-room/hosted";

const providerNames = {
  anthropic: "Anthropic",
  openai: "OpenAI",
  openrouter: "OpenRouter",
};
const demoCredential: ProviderCredential = {
  id: "preview-key",
  provider: "openai",
  label: "My OpenAI key",
  scope: "personal",
  ownerUserId: "demo-owner",
  canManage: true,
  lastFour: "7X2K",
  validatedAt: "2026-11-14",
  revokedAt: null,
  generation: 1,
};

export function AIConnections({
  roomId,
  canManageProject = false,
  onboarding = false,
  onDone,
  demo = false,
}: {
  roomId?: string;
  canManageProject?: boolean;
  onboarding?: boolean;
  onDone?: () => void;
  demo?: boolean;
}) {
  const [showKey, setShowKey] = React.useState(false);
  const [ready, setReady] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [credentials, setCredentials] = React.useState<ProviderCredential[]>(
    [],
  );
  const [preferences, setPreferences] =
    React.useState<AIPreferences>(EMPTY_AI_PREFERENCES);
  const [mode, setMode] = React.useState<"local" | "api">("local");
  const [provider, setProvider] = React.useState<HostedProvider>("openai");
  const [model, setModel] = React.useState(hostedModels("openai")[0]!);
  const [existingId, setExistingId] = React.useState("");
  const [override, setOverride] = React.useState("");
  const [notice, setNotice] = React.useState("");
  const [editing, setEditing] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const busyRef = React.useRef(false);
  const current = credentials.find(
    (item) => item.id === preferences.credential_ref && !item.revokedAt,
  );
  React.useEffect(() => {
    let active = true;
    async function load() {
      if (demo) {
        setCredentials([demoCredential]);
        setPreferences({
          mode: "api",
          credential_ref: demoCredential.id,
          provider: "openai",
          model: "gpt-4.1",
        });
        setMode(onboarding ? "local" : "api");
        setReady(true);
        setLoaded(true);
        return;
      }
      const [setup, personal, project] = await Promise.all([
        hostedSetupAction(),
        getAIPreferencesAction(),
        roomId ? getAIPreferencesAction(roomId) : Promise.resolve(null),
      ]);
      if (!active) return;
      if (setup.ok) {
        setCredentials(setup.credentials);
        setReady(true);
      }
      if (personal.ok) {
        setPreferences(personal.preferences);
        setMode(personal.preferences.mode);
        if (
          ["openai", "anthropic", "openrouter"].includes(
            personal.preferences.provider,
          )
        )
          setProvider(personal.preferences.provider as HostedProvider);
        if (personal.preferences.model) setModel(personal.preferences.model);
      } else
        setNotice(
          "Could not load your AI preferences. Refresh before making changes.",
        );
      if (project?.ok && project.preferences.model)
        setOverride(
          `${project.preferences.provider}:${project.preferences.model}`,
        );
      setLoaded(personal.ok);
    }
    void load().catch(() => {
      if (active)
        setNotice("Could not load AI settings. Refresh to try again.");
    });
    return () => {
      active = false;
    };
  }, [roomId, demo, onboarding]);
  async function mutate(work: () => Promise<void>) {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true);
    setNotice("");
    try {
      await work();
    } catch {
      setNotice("The settings could not be saved. Try again.");
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  }
  async function savePreferences(next: AIPreferences) {
    if (!demo) {
      const result = await saveAIPreferencesAction(next);
      if (!result.ok) {
        setNotice(result.message);
        return false;
      }
    }
    setPreferences(next);
    setNotice(
      demo
        ? "Preview saved. No real API key or model is used."
        : "AI settings saved.",
    );
    return true;
  }
  return (
    <div className="space-y-6">
      <div
        className="grid gap-3 sm:grid-cols-2"
        role="group"
        aria-label="How to work with AI"
      >
        {(
          [
            {
              value: "local",
              title: "Use your own coding tool",
              description: "Claude, Codex or Pi with Dropway MCP or CLI.",
              Icon: Terminal,
            },
            {
              value: "api",
              title: "Let @dropway propose changes",
              description:
                "Connect an API key and request changes in comments.",
              Icon: KeyRound,
            },
          ] as const
        ).map(({ value, title, description, Icon }) => (
          <button
            key={value}
            type="button"
            aria-pressed={mode === value}
            onClick={() => setMode(value)}
            className={`flex items-start gap-3 rounded-xl border p-4 text-left transition-colors ${mode === value ? "border-foreground bg-secondary/40" : "border-border hover:bg-muted/40"}`}
          >
            <Icon className="mt-0.5 size-5 shrink-0" aria-hidden />
            <span>
              <span className="block text-sm font-medium">{title}</span>
              <span className="mt-1 block text-xs leading-relaxed text-muted-foreground">
                {description}
              </span>
            </span>
            {mode === value ? (
              <Check className="ml-auto size-4 shrink-0" aria-hidden />
            ) : null}
          </button>
        ))}
      </div>
      {notice ? (
        <p role="status" className="text-sm text-muted-foreground">
          {notice}
        </p>
      ) : null}
      {mode === "local" ? (
        <section className="space-y-4 rounded-xl border border-border p-5">
          <h3 className="font-medium">Bring your own workflow</h3>
          <p className="text-sm leading-relaxed text-muted-foreground">
            Use Claude, Codex or Pi locally to read feedback and update sites or
            images through Dropway MCP or CLI. No LLM API key is needed in
            Dropway. Ordinary comments do not start AI work.
          </p>
          <div className="flex gap-3">
            <Button asChild variant="outline">
              <Link href="/mcp">Connect MCP</Link>
            </Button>
            <Button asChild variant="outline">
              <Link href="/cli">Set up CLI</Link>
            </Button>
          </div>
          <div className="rounded-lg bg-muted/50 p-3 text-xs">
            <p className="mb-2 text-muted-foreground">
              Pull feedback after signing in with <code>dropway login</code>:
            </p>
            <code className="block whitespace-pre-wrap break-all">
              dropway projects comments {roomId || "<project-id>"} --type text
            </code>
            <p className="mt-2 text-muted-foreground">
              Filter with --version, --since, --until and --type (text,
              component, image, point or page). MCP exposes the same filters
              through list_room_threads; get_comment_thread returns replies.
            </p>
          </div>
          <Button
            disabled={busy || !loaded}
            onClick={() =>
              void mutate(async () => {
                if (await savePreferences({ ...preferences, mode: "local" }))
                  onDone?.();
              })
            }
          >
            {onboarding ? "Continue with my own tool" : "Use local tools"}
          </Button>
        </section>
      ) : (
        <section className="space-y-5">
          <div>
            <h3 className="text-sm font-semibold">Your AI connection</h3>
            <p className="mt-1 text-sm text-muted-foreground">
              One active connection, reused across your projects in this
              organization. Manage the same connection in{" "}
              <Link
                href="/settings/ai"
                className="underline underline-offset-4"
              >
                personal AI settings
              </Link>
              . Keys are encrypted and never displayed.
            </p>
          </div>
          {!current && credentials.some((item) => !item.revokedAt) ? (
            <div className="flex flex-wrap items-end gap-3">
              <label className="min-w-60 flex-1 space-y-2 text-sm">
                <span>Use a saved key</span>
                <Select
                  aria-label="Saved API key"
                  value={existingId}
                  onChange={(event) => setExistingId(event.target.value)}
                >
                  <option value="">Choose a saved key</option>
                  {credentials
                    .filter((item) => !item.revokedAt)
                    .map((item) => (
                      <option key={item.id} value={item.id}>
                        {providerNames[item.provider]} · {item.scope} · ••••{" "}
                        {item.lastFour}
                      </option>
                    ))}
                </Select>
              </label>
              <Button
                variant="outline"
                disabled={!existingId || busy || !loaded}
                onClick={() =>
                  void mutate(async () => {
                    const key = credentials.find(
                      (item) => item.id === existingId,
                    );
                    if (!key) return;
                    const nextModel = hostedModels(key.provider)[0]!;
                    if (
                      await savePreferences({
                        mode: "api",
                        credential_ref: key.id,
                        provider: key.provider,
                        model: nextModel,
                      })
                    ) {
                      setProvider(key.provider);
                      setModel(nextModel);
                    }
                  })
                }
              >
                Use this key
              </Button>
            </div>
          ) : null}
          {current && !editing ? (
            <div className="rounded-xl border border-border p-5">
              <div className="flex items-start justify-between gap-3">
                <div className="flex gap-3">
                  <div className="flex size-10 items-center justify-center rounded-lg bg-secondary">
                    <KeyRound className="size-5" aria-hidden />
                  </div>
                  <div>
                    <p className="text-sm font-medium">
                      {providerNames[current.provider]}
                    </p>
                    <p className="mt-1 text-xs text-muted-foreground">
                      •••• {current.lastFour} ·{" "}
                      {current.scope === "personal" ? "Personal" : "Team"} ·{" "}
                      {current.validatedAt ? "Connected" : "Needs validation"}
                    </p>
                  </div>
                </div>
                <div className="flex gap-1">
                  <Tooltip label="Edit connection">
                    <Button
                      size="icon"
                      variant="ghost"
                      aria-label="Edit connection"
                      disabled={busy}
                      onClick={() => {
                        setProvider(current.provider);
                        setEditing(true);
                      }}
                    >
                      <Pencil className="size-4" />
                    </Button>
                  </Tooltip>
                  {current.canManage ? (
                    <Tooltip label="Disconnect key">
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label="Disconnect key"
                        disabled={busy}
                        onClick={() =>
                          void mutate(async () => {
                            const result = demo
                              ? { ok: true }
                              : await revokeCredentialAction(current.id);
                            if (!result.ok) {
                              setNotice("Could not disconnect this key.");
                              return;
                            }
                            setCredentials((items) =>
                              items.filter((item) => item.id !== current.id),
                            );
                            await savePreferences({ ...EMPTY_AI_PREFERENCES });
                          })
                        }
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    </Tooltip>
                  ) : null}
                </div>
              </div>
              {!current.validatedAt ? (
                <Button
                  variant="outline"
                  className="mt-4"
                  disabled={busy || !current.canManage}
                  onClick={() =>
                    void mutate(async () => {
                      const result = demo
                        ? { ok: true }
                        : await validateCredentialAction(current.id);
                      if (result.ok) {
                        setCredentials((items) =>
                          items.map((item) =>
                            item.id === current.id
                              ? {
                                  ...item,
                                  validatedAt: new Date().toISOString(),
                                }
                              : item,
                          ),
                        );
                        setNotice("Connection validated.");
                      } else
                        setNotice(
                          "Validation failed. Check or replace your API key.",
                        );
                    })
                  }
                >
                  Validate key
                </Button>
              ) : null}
            </div>
          ) : (
            <form
              className="space-y-4 rounded-xl border border-border p-5"
              onSubmit={(event) => {
                event.preventDefault();
                if (!ready || busyRef.current) return;
                const form = event.currentTarget;
                const key = String(
                  new FormData(form).get("api-key") || "",
                ).trim();
                const selectedProvider = keyProvider(key) ?? provider;
                const selectedModel =
                  selectedProvider === provider &&
                  hostedModels(selectedProvider).includes(model)
                    ? model
                    : hostedModels(selectedProvider)[0]!;
                setProvider(selectedProvider);
                setModel(selectedModel);
                setShowKey(false);
                form.reset();
                void mutate(async () => {
                  if (demo) {
                    const previewCredential: ProviderCredential = {
                      ...demoCredential,
                      provider: selectedProvider,
                      label: `My ${providerNames[selectedProvider]} key`,
                      lastFour: "DEMO",
                    };
                    setCredentials([previewCredential]);
                    await savePreferences({
                      mode: "api",
                      credential_ref: previewCredential.id,
                      provider: selectedProvider,
                      model: selectedModel,
                    });
                    setEditing(false);
                    return;
                  }
                  const result = await saveCredentialAction(
                    selectedProvider,
                    `My ${providerNames[selectedProvider]} key`,
                    key,
                    current?.provider === selectedProvider &&
                      current.scope === "personal"
                      ? current.id
                      : undefined,
                    "personal",
                  );
                  if (!result.ok) {
                    setNotice(result.message);
                    return;
                  }
                  setCredentials((items) => [
                    result.credential,
                    ...items.filter((item) => item.id !== result.credential.id),
                  ]);
                  if (
                    !(await savePreferences({
                      mode: "api",
                      credential_ref: result.credential.id,
                      provider: result.credential.provider,
                      model: selectedModel,
                    }))
                  )
                    return;
                  const validated = await validateCredentialAction(
                    result.credential.id,
                  );
                  if (validated.ok)
                    setCredentials((items) =>
                      items.map((item) =>
                        item.id === result.credential.id
                          ? { ...item, validatedAt: new Date().toISOString() }
                          : item,
                      ),
                    );
                  setNotice(
                    validated.ok
                      ? "Connected. Tag @dropway in a comment to propose a change."
                      : "Key saved, but validation failed. Check or replace it before starting AI work.",
                  );
                  setEditing(false);
                });
              }}
            >
              {!ready ? (
                <p className="text-sm text-muted-foreground">
                  Secure AI execution is unavailable. You can still use MCP or
                  CLI.
                </p>
              ) : null}
              <fieldset
                disabled={!ready || busy}
                className="grid gap-4 sm:grid-cols-2"
              >
                <label className="space-y-2 text-sm">
                  <span>Provider</span>
                  <Select
                    aria-label="Credential provider"
                    value={provider}
                    onChange={(event) => {
                      const next = event.target.value as HostedProvider;
                      setProvider(next);
                      setModel(hostedModels(next)[0]!);
                    }}
                  >
                    {Object.entries(providerNames).map(([value, label]) => (
                      <option key={value} value={value}>
                        {label}
                      </option>
                    ))}
                  </Select>
                </label>
                <label className="space-y-2 text-sm">
                  <span>Default model</span>
                  <Select
                    aria-label="Personal default model"
                    value={model}
                    onChange={(event) => setModel(event.target.value)}
                  >
                    {hostedModels(provider).map((value) => (
                      <option key={value}>{value}</option>
                    ))}
                  </Select>
                </label>
                <label className="space-y-2 text-sm sm:col-span-2">
                  <span>API key</span>
                  <div className="relative">
                    <Input
                      name="api-key"
                      aria-label="Provider API key"
                      type={showKey ? "text" : "password"}
                      className="pr-11"
                      onChange={(event) => {
                        const detected = keyProvider(event.target.value);
                        if (detected && detected !== provider) {
                          setProvider(detected);
                          setModel(hostedModels(detected)[0]!);
                        }
                      }}
                      autoComplete="new-password"
                      minLength={8}
                      maxLength={4096}
                      required
                      placeholder="Paste your API key"
                    />
                    <button
                      type="button"
                      aria-label={showKey ? "Hide API key" : "Show API key"}
                      aria-pressed={showKey}
                      title={showKey ? "Hide API key" : "Show API key"}
                      onClick={() => setShowKey((value) => !value)}
                      className="absolute right-1 top-1 flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    >
                      {showKey ? (
                        <EyeOff className="size-4" aria-hidden />
                      ) : (
                        <Eye className="size-4" aria-hidden />
                      )}
                    </button>
                  </div>
                </label>
                <div className="flex gap-2 sm:col-span-2">
                  <Button type="submit">
                    {busy ? "Connecting…" : "Save and connect"}
                  </Button>
                  {current ? (
                    <Button
                      type="button"
                      variant="ghost"
                      onClick={() => {
                        setEditing(false);
                        setProvider(current.provider);
                        setModel(
                          hostedModels(current.provider).includes(
                            preferences.model,
                          )
                            ? preferences.model
                            : hostedModels(current.provider)[0]!,
                        );
                      }}
                    >
                      Cancel
                    </Button>
                  ) : null}
                </div>
              </fieldset>
            </form>
          )}
          {current && !editing ? (
            <div className="flex flex-wrap items-end gap-3">
              <label className="min-w-60 flex-1 space-y-2 text-sm">
                <span>Personal default model</span>
                <Select
                  aria-label="Personal default model"
                  value={model}
                  onChange={(event) => setModel(event.target.value)}
                >
                  {hostedModels(current.provider).map((value) => (
                    <option key={value}>{value}</option>
                  ))}
                </Select>
              </label>
              <Button
                disabled={busy || !loaded}
                onClick={() =>
                  void mutate(async () => {
                    if (
                      await savePreferences({
                        mode: "api",
                        credential_ref: current.id,
                        provider: current.provider,
                        model,
                      })
                    )
                      onDone?.();
                  })
                }
              >
                {onboarding ? "Continue" : "Save default"}
              </Button>
            </div>
          ) : null}
          <p className="text-xs leading-relaxed text-muted-foreground">
            Tag @dropway in a comment to request a change using your saved
            settings. Dropway manages task limits automatically. Ordinary
            comments do not trigger AI. Review the proposed changes, accept or
            reject them, then apply approved changes to publish.
          </p>
        </section>
      )}
      {roomId ? (
        <section className="space-y-3 border-t border-border pt-5">
          <h3 className="text-sm font-semibold">Project model</h3>
          <p className="text-sm text-muted-foreground">
            A project model overrides personal settings for the matching
            provider. Other providers keep their personal default. Only project
            owners can change this.
          </p>
          <div className="flex flex-wrap items-end gap-3">
            <div className="min-w-60 flex-1">
              <Select
                aria-label="Project model override"
                value={override}
                disabled={!canManageProject || busy || !loaded}
                onChange={(event) => setOverride(event.target.value)}
              >
                <option value="">Use personal default</option>
                {Object.entries(providerNames).map(([value, label]) => (
                  <optgroup key={value} label={label}>
                    {hostedModels(value as HostedProvider).map((item) => (
                      <option key={item} value={`${value}:${item}`}>
                        {item}
                      </option>
                    ))}
                  </optgroup>
                ))}
              </Select>
            </div>
            {canManageProject ? (
              <Button
                variant="outline"
                disabled={busy || !loaded}
                onClick={() =>
                  void mutate(async () => {
                    const [p = "", m = ""] = override.split(":");
                    const result = demo
                      ? { ok: true }
                      : await saveAIPreferencesAction(
                          { ...EMPTY_AI_PREFERENCES, provider: p, model: m },
                          roomId,
                        );
                    setNotice(
                      result.ok
                        ? "Project model saved."
                        : "Could not save the project model.",
                    );
                  })
                }
              >
                Save project model
              </Button>
            ) : null}
          </div>
        </section>
      ) : null}
      {onboarding ? (
        <Button type="button" variant="ghost" onClick={onDone}>
          Skip for now
        </Button>
      ) : null}
    </div>
  );
}
