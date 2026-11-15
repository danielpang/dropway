"use client";
import * as React from "react";
import {
  hostedSetupAction,
  getAIPreferencesAction,
} from "@/app/(app)/rooms/hosted-actions";
import {
  preferredModel,
  providerModelId,
  type MentionExecution,
} from "@/lib/project-room/hosted";

/** Preparing a mention never starts a task. Sending the comment is the request. */
export function MentionExecutionControls({
  body,
  allowed,
  roomId,
  onChange,
}: {
  body: string;
  allowed: boolean;
  roomId?: string;
  onChange: (value: MentionExecution | undefined) => void;
}) {
  const [configuration, setConfiguration] =
    React.useState<Omit<MentionExecution, "idempotency_key">>();
  const [notice, setNotice] = React.useState("Preparing @dropway…");
  const callback = React.useRef(onChange);
  callback.current = onChange;
  React.useEffect(() => {
    let active = true;
    setConfiguration(undefined);
    callback.current(undefined);
    if (!allowed) {
      setNotice(
        "@dropway is unavailable here. Check project AI settings and use the current revision.",
      );
      return;
    }
    setNotice("Preparing @dropway…");
    void Promise.all([
      hostedSetupAction(),
      getAIPreferencesAction(),
      roomId ? getAIPreferencesAction(roomId) : Promise.resolve(null),
    ])
      .then(([setup, personal, project]) => {
        if (!active) return;
        if (!setup.ok || !personal.ok || (roomId && !project?.ok)) {
          setNotice(
            "Could not load AI settings. Try again or open project settings.",
          );
          return;
        }
        const key = setup.credentials.find(
          (item) =>
            item.id === personal.preferences.credential_ref &&
            !item.revokedAt &&
            item.validatedAt,
        );
        if (personal.preferences.mode !== "api" || !key) {
          setNotice(
            "Connect an API key in project AI settings to use @dropway.",
          );
          return;
        }
        const model = preferredModel(
          key,
          personal.preferences,
          project?.ok ? project.preferences : undefined,
        );
        setConfiguration({
          credential_ref: key.id,
          provider: key.provider,
          model_id: providerModelId(key.provider, model),
          budget: {
            max_tokens: 100000,
            max_calls: 20,
            max_seconds: 300,
            max_output_bytes: 10 * 1024 * 1024,
          },
        });
        setNotice("");
      })
      .catch(() => {
        if (active) setNotice("Could not load AI settings. Please try again.");
      });
    return () => {
      active = false;
      callback.current(undefined);
    };
  }, [allowed, roomId]);
  React.useEffect(() => {
    callback.current(
      allowed && configuration && body.trim()
        ? { ...configuration, idempotency_key: crypto.randomUUID() }
        : undefined,
    );
  }, [allowed, configuration, body]);
  if (!allowed) return null;
  return notice ? (
    <p role="status" className="text-xs text-muted-foreground">
      {notice}
    </p>
  ) : null;
}
