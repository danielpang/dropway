// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Parent-side comment draft. Run and Accept stay out of this form: a comment
// never starts an agent.

"use client";

import * as React from "react";

import { Button } from "@/components/ui/button";
import { commentControls } from "@/lib/collab";

export function CommentDraftForm({
  value,
  onChange,
  onSubmit,
  onDraftKeyDown,
}: {
  value: string;
  onChange: (value: string) => void;
  onSubmit: () => void;
  onDraftKeyDown: (event: React.KeyboardEvent<HTMLTextAreaElement>) => void;
}) {
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
      className="space-y-2"
    >
      <label className="block space-y-1 text-sm">
        <span className="font-medium text-foreground">Comment on this version</span>
        <textarea
          aria-label="Comment on this version"
          value={value}
          rows={3}
          maxLength={4000}
          onChange={(event) => onChange(event.target.value)}
          onKeyDown={onDraftKeyDown}
          placeholder="Write a comment about this version"
          className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        />
      </label>
      {commentControls().map((control) =>
        control === "comment" ? (
          <Button key={control} type="submit" size="sm" disabled={value.trim().length === 0}>
            Add comment
          </Button>
        ) : null,
      )}
    </form>
  );
}
