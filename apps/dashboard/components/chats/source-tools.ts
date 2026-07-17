/**
 * Display metadata for a chat log's `source_tool`. The API stores it as a
 * free-form string (new tools appear faster than releases), so unknown values
 * render as-is and only the empty string falls back to "Unknown tool".
 */

export const SOURCE_TOOL_LABEL: Record<string, string> = {
  claude_code: "Claude Code",
  chatgpt: "ChatGPT",
  cursor: "Cursor",
  other: "Other",
};

/** The options the import form offers (the API accepts anything). */
export const SOURCE_TOOL_OPTIONS = [
  { value: "claude_code", label: "Claude Code" },
  { value: "chatgpt", label: "ChatGPT" },
  { value: "cursor", label: "Cursor" },
  { value: "other", label: "Other" },
] as const;

/** Human label for a stored source_tool value. */
export function sourceToolLabel(value: string | undefined): string {
  if (!value) return "Unknown tool";
  return SOURCE_TOOL_LABEL[value] ?? value;
}
