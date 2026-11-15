"use client";
import * as React from "react";
import {
  hostedSetupAction,
  getAIPreferencesAction,
} from "@/app/(app)/rooms/hosted-actions";
import {
  preferredModel,
  hostedModels,
  type MentionExecution,
  type ProviderCredential,
} from "@/lib/project-room/hosted";

import { Select } from "@/components/ui/select";
import { Input } from "@/components/ui/input";

/** Mounted only for an explicit mention. A mention alone never authorizes a charge. */
export function MentionExecutionControls({
  body,
  allowed,
  roomId,
  onChange,
}: {
  roomId?: string;
  body: string;
  allowed: boolean;
  onChange: (value: MentionExecution | undefined) => void;
}) {
  const [connections, setConnections] = React.useState<ProviderCredential[]>(
    [],
  );
  const [credentialId, setCredentialId] = React.useState("");
  const [model, setModel] = React.useState("");
  const [tokens, setTokens] = React.useState(20000);
  const [calls, setCalls] = React.useState(10);
  const [requestId, setRequestId] = React.useState("");
  const [notice, setNotice] = React.useState("Checking reusable connections…");
  const callback = React.useRef(onChange);
  callback.current = onChange;
  const credential = connections.find((item) => item.id === credentialId);
  React.useEffect(() => {
    let active = true;
    setConnections([]);
    setCredentialId("");
    setRequestId("");
    if (allowed)
      void Promise.all([
        hostedSetupAction(),
        getAIPreferencesAction(),
        roomId ? getAIPreferencesAction(roomId) : Promise.resolve(null),
      ])
        .then(([result, personal, project]) => {
          if (!active) return;
          if (!result.ok || !personal.ok) {
            setNotice("Open AI settings to configure your connection.");
            return;
          }
          const selected = result.credentials.find(
            (item) =>
              item.id === personal.preferences.credential_ref &&
              !item.revokedAt &&
              item.validatedAt,
          );
          if (personal.preferences.mode !== "api" || !selected) {
            setNotice(
              "Add an API key in AI settings to use @dropway. You can also use your own coding tool with MCP or CLI.",
            );
            return;
          }
          setConnections([selected]);
          setCredentialId(selected.id);
          setModel(
            preferredModel(
              selected,
              personal.preferences,
              project?.ok ? project.preferences : undefined,
            ),
          );
          setNotice(
            `${selected.label || selected.provider} · ending ${selected.lastFour}`,
          );
        })
        .catch(() => {
          if (active) setNotice("Connections could not be loaded.");
        });
    return () => {
      active = false;
    };
  }, [allowed, roomId]);
  React.useEffect(() => {
    setRequestId("");
  }, [body, credentialId, model, tokens, calls, allowed, roomId]);
  React.useEffect(() => {
    if (credential && !hostedModels(credential.provider).includes(model))
      setModel(hostedModels(credential.provider)[0]!);
  }, [credential, model]);
  React.useEffect(() => {
    callback.current(
      allowed &&
        requestId &&
        credential &&
        Number.isInteger(tokens) &&
        tokens > 0 &&
        tokens <= 500000 &&
        Number.isInteger(calls) &&
        calls > 0 &&
        calls <= 100 &&
        hostedModels(credential.provider).includes(model)
        ? {
            credential_ref: credential.id,
            provider: credential.provider,
            model_id:
              credential.provider === "openrouter"
                ? `anthropic/${model}`
                : model,
            idempotency_key: requestId,
            budget: {
              max_tokens: tokens,
              max_calls: calls,
              max_seconds: 300,
              max_output_bytes: 2097152,
            },
          }
        : undefined,
    );
  }, [allowed, requestId, credential, model, tokens, calls]);
  React.useEffect(() => () => callback.current(undefined), []);
  if (!allowed) return null;
  return (
    <fieldset className="w-full space-y-2 rounded border border-border p-3">
      <legend className="text-sm font-medium">
        Authorize @dropway revision
      </legend>
      <p className="text-sm text-muted-foreground">{notice}</p>
      <label className="block text-sm">
        Model
        <Select
          aria-label="Mention model"
          className="mt-1"
          value={model}
          onChange={(event) => setModel(event.target.value)}
        >
          <option value="">Choose a model</option>
          {credential
            ? hostedModels(credential.provider).map((item) => (
                <option key={item}>{item}</option>
              ))
            : null}
        </Select>
      </label>
      <label className="block text-sm">
        Maximum tokens
        <Input
          aria-label="Mention maximum tokens"
          className="mt-1"
          type="number"
          min={1}
          max={500000}
          value={tokens}
          onChange={(event) => setTokens(Number(event.target.value))}
        />
      </label>
      <label className="block text-sm">
        Maximum model calls
        <Input
          aria-label="Mention maximum calls"
          className="mt-1"
          type="number"
          min={1}
          max={100}
          value={calls}
          onChange={(event) => setCalls(Number(event.target.value))}
        />
      </label>
      <label className="flex gap-2 text-sm">
        <input
          type="checkbox"
          checked={!!requestId}
          disabled={!credential || !model}
          onChange={(event) =>
            setRequestId(event.target.checked ? crypto.randomUUID() : "")
          }
        />
        Posting this comment authorizes paid model and sandbox work using this
        connection, capped at the selected limits, 5 minutes, and 2 MiB of
        output. These limits are not a dollar cap. The result requires separate
        human acceptance or rejection and Apply; it never publishes
        automatically.
      </label>
    </fieldset>
  );
}
