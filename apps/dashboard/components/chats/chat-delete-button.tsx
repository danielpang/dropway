"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2, Trash2 } from "lucide-react";

import { deleteChatAction } from "@/app/(app)/chats/actions";
import { Button } from "@/components/ui/button";

/**
 * Delete-the-whole-log control for the chat detail page (creator or admin;
 * mirrors the skill detail delete). Confirms first — deleting removes every
 * message and tears down the served panel.
 */
export function ChatDeleteButton({
  chatId,
  title,
}: {
  chatId: string;
  title: string;
}) {
  const router = useRouter();
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  async function onDelete() {
    if (
      !window.confirm(
        `Delete "${title}" and all of its messages? This also removes it from any site it's shown on.`,
      )
    ) {
      return;
    }
    setError(null);
    setPending(true);
    const result = await deleteChatAction(chatId);
    if (!result.ok) {
      setError(result.message);
      setPending(false);
      return;
    }
    router.push("/chats");
    router.refresh();
  }

  return (
    <div className="flex flex-col items-end gap-1.5">
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={() => void onDelete()}
        disabled={pending}
        title="Delete chat log"
      >
        {pending ? (
          <Loader2 className="size-4 animate-spin" aria-hidden />
        ) : (
          <Trash2 className="size-4" aria-hidden />
        )}
      </Button>
      {error ? (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
}
