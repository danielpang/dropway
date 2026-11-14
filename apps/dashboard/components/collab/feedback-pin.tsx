// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A pin is a feedback control. It is not an editable source handle.

"use client";

import * as React from "react";

import { feedbackPinA11y } from "@/lib/collab";
import { cn } from "@/lib/utils";

export function FeedbackPin({
  label,
  pressed,
  onOpen,
  className,
  style,
}: {
  label: string;
  pressed: boolean;
  onOpen: () => void;
  className?: string;
  style?: React.CSSProperties;
}) {
  const a11y = feedbackPinA11y(label);
  return (
    <button
      type="button"
      contentEditable={false}
      aria-label={a11y["aria-label"]}
      aria-pressed={pressed}
      data-feedback-only={a11y["data-feedback-only"]}
      onClick={onOpen}
      style={style}
      className={cn(
        "size-4 rounded-full border-2 border-primary bg-primary/40 shadow-sm",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
        className,
      )}
    />
  );
}
