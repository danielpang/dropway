"use client";

import * as React from "react";
import { Bot, UserRound } from "lucide-react";
import { activeMention, filterMentions, insertMention, type MentionOption } from "@/lib/project-room/mentions";

type Props = Omit<React.TextareaHTMLAttributes<HTMLTextAreaElement>, "value" | "onChange"> & {
  value: string;
  onValueChange: (value: string) => void;
  options: MentionOption[];
};

export function MentionTextarea({ value, onValueChange, options, onKeyDown, onSelect, onBlur, onInput, ...props }: Props) {
  const ref = React.useRef<HTMLTextAreaElement>(null);
  const id = React.useId();
  const selection = React.useRef("");
  const pendingCaret = React.useRef<number | null>(null);
  const [range, setRange] = React.useState<ReturnType<typeof activeMention>>(null);
  const [selected, setSelected] = React.useState(0);
  const matches = range ? filterMentions(options, range.query) : [];
  const open = !props.disabled && matches.length > 0;
  const index = Math.min(selected, Math.max(0, matches.length - 1));
  function update(input: HTMLTextAreaElement) {
    const signature = JSON.stringify([input.value, input.selectionStart, input.selectionEnd]);
    if (signature === selection.current) return;
    selection.current = signature;
    const next = activeMention(input.value, input.selectionStart, input.selectionEnd);
    if (next?.start !== range?.start || next?.end !== range?.end || next?.query !== range?.query) {
      setRange(next);
      setSelected(0);
    }
  }
  React.useLayoutEffect(() => {
    if (!ref.current) return;
    ref.current.style.height = "auto";
    ref.current.style.height = `${Math.min(160, ref.current.scrollHeight)}px`;
    if (pendingCaret.current !== null) {
      ref.current.focus();
      ref.current.setSelectionRange(pendingCaret.current, pendingCaret.current);
      selection.current = JSON.stringify([value, pendingCaret.current, pendingCaret.current]);
      pendingCaret.current = null;
    }
    if (!value) setRange(null);
  }, [value]);
  React.useEffect(() => {
    if (open) document.getElementById(`${id}-${index}`)?.scrollIntoView({ block: "nearest" });
  }, [id, index, open]);
  function choose(option: MentionOption) {
    if (!range) return;
    const next = insertMention(value, range, option.text);
    pendingCaret.current = next.caret;
    onValueChange(next.value);
    setRange(null);
  }
  return <div className="relative min-w-0">
    {open ? <div className="absolute bottom-full left-0 z-50 mb-2 w-full min-w-48 rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-xl">
      <div className="px-2 py-1 text-[11px] font-medium text-muted-foreground">Mention</div>
      <ul id={id} role="listbox" aria-label="Mention suggestions" className="max-h-48 overflow-y-auto">
        {matches.map((option, i) => <li id={`${id}-${i}`} key={option.id} role="option" aria-selected={i === index}
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => choose(option)}
          onMouseMove={() => setSelected(i)}
          className={`flex cursor-pointer items-center gap-2 rounded-md px-2 py-2 text-sm ${i === index ? "bg-accent text-accent-foreground" : ""}`}>
          {option.id === "dropway" ? <Bot className="h-4 w-4 shrink-0" aria-hidden="true" /> : <UserRound className="h-4 w-4 shrink-0" aria-hidden="true" />}
          <span className="min-w-0 flex-1 truncate">{option.label}</span>
          <span className="text-[10px] text-muted-foreground">{option.detail}</span>
        </li>)}
      </ul>
    </div> : null}
    <textarea {...props} ref={ref} value={value} aria-autocomplete="list" aria-controls={open ? id : undefined}
      aria-activedescendant={open ? `${id}-${index}` : undefined}
      onChange={(event) => { onValueChange(event.target.value); update(event.target); }}
      onSelect={(event) => { update(event.currentTarget); onSelect?.(event); }}
      onBlur={(event) => { setRange(null); onBlur?.(event); }}
      onInput={onInput}
      onKeyDown={(event) => {
        if (open && !event.nativeEvent.isComposing) {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault();
            setSelected((index + (event.key === "ArrowDown" ? 1 : -1) + matches.length) % matches.length);
            return;
          }
          if (event.key === "Enter" || event.key === "Tab") { event.preventDefault(); choose(matches[index]!); return; }
          if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setRange(null); return; }
        }
        onKeyDown?.(event);
      }} />
  </div>;
}
