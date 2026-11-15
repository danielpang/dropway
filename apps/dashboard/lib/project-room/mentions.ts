export interface MentionOption {
  id: string;
  label: string;
  text: string;
  detail: string;
}

export function mentionOptions(members: { userId: string; role: string; name?: string }[]): MentionOption[] {
  return [
    { id: "dropway", label: "Dropway", text: "@dropway", detail: "AI assistant" },
    ...Array.from(new Map(members.map((member) => [member.userId, member])).values()).map((member) => {
      const name = member.name?.trim() || member.userId;
      // The assistant's handle is reserved; a teammate's name must not start AI work.
      const handle = name.replace(/\s+/g, " ").replace(/@/g, "");
      return { id: `member:${member.userId}`, label: name, text: `@${/^dropway(?:$|[\s,.!?:;)])/i.test(handle) ? `member-${handle}` : handle}`, detail: member.role };
    }),
  ];
}

/** Only a standalone @ token at the caret opens the picker, never an email. */
export function activeMention(value: string, start: number, end = start) {
  if (start !== end) return null;
  const match = /(^|[\s(])@([^\s@]*)$/.exec(value.slice(0, start));
  return match ? { start: start - match[2]!.length - 1, end: start, query: match[2]! } : null;
}

export function filterMentions(options: MentionOption[], query: string) {
  const search = query.toLocaleLowerCase();
  return options.filter((option) => `${option.label} ${option.text}`.toLocaleLowerCase().includes(search));
}

export function insertMention(value: string, range: { start: number; end: number }, text: string) {
  const suffix = value.slice(range.end);
  const inserted = text + (/^\s/.test(suffix) ? "" : " ");
  return { value: value.slice(0, range.start) + inserted + suffix, caret: range.start + inserted.length };
}
