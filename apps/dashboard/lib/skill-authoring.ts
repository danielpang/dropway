/**
 * Helpers for authoring a skill in-browser (the write-a-skill editor). The
 * authored SKILL.md + any extra files are turned into the same DroppedFile[]
 * shape the drag-and-drop upload uses, so authoring reuses the entire
 * create → prepare → upload → finalize pipeline (lib/skill-upload.ts) unchanged.
 */

import { isSafeSkillPath } from "@/lib/skills-shared";
import type { DroppedFile } from "@/lib/deploy-manifest";

/** One additional file the author adds alongside SKILL.md. */
export interface AuthoredFile {
  path: string;
  content: string;
}

/** A starter SKILL.md scaffold shown in a fresh editor. */
export function skillTemplate(name: string): string {
  const slug = name.trim() || "my-skill";
  return `---
name: ${slug}
description: One sentence on what this skill does and when to use it.
---

# ${name.trim() || "My skill"}

Describe when an assistant should use this skill, then give the steps or rules
it should follow.

## Steps

1. First, …
2. Then, …
`;
}

export interface ComposeResult {
  files: DroppedFile[];
  error: string | null;
}

/**
 * Build the upload file set from the editor state. SKILL.md is always first.
 * Returns a client-safe error for an empty body, a bad extra-file path, or a
 * duplicate path (the server re-validates too). Uses the browser File API, so
 * this only runs client-side.
 */
export function composeSkillFiles(skillMd: string, extras: AuthoredFile[]): ComposeResult {
  if (!skillMd.trim()) {
    return { files: [], error: "The SKILL.md can't be empty." };
  }
  const seen = new Set<string>(["SKILL.md"]);
  const files: DroppedFile[] = [
    { path: "SKILL.md", file: new File([skillMd], "SKILL.md", { type: "text/markdown" }) },
  ];
  for (const extra of extras) {
    const path = extra.path.trim();
    if (!path) continue; // ignore blank rows
    if (path === "SKILL.md" || !isSafeSkillPath(path)) {
      return { files: [], error: `Invalid file path: "${extra.path}".` };
    }
    if (seen.has(path)) {
      return { files: [], error: `Duplicate file path: "${path}".` };
    }
    seen.add(path);
    const name = path.split("/").pop() || path;
    files.push({ path, file: new File([extra.content], name, { type: "text/plain" }) });
  }
  return { files, error: null };
}
