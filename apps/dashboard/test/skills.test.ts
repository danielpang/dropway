import { describe, expect, it } from "vitest";

import type { DroppedFile } from "@/lib/deploy-manifest";
import {
  MAX_SKILL_FILES,
  hasRootSkillMD,
  precheckSkillFolder,
} from "@/lib/skill-upload";
import { buildZip, crc32 } from "@/lib/zip";

function dropped(path: string, size = 10): DroppedFile {
  // Only .path/.file.size are read by the prechecks; a stub File suffices.
  return { path, file: new File([new Uint8Array(size)], path.split("/").pop() ?? "f") };
}

describe("precheckSkillFolder", () => {
  it("requires a root SKILL.md", () => {
    expect(hasRootSkillMD([dropped("SKILL.md")])).toBe(true);
    expect(hasRootSkillMD([dropped("nested/SKILL.md")])).toBe(false);
    expect(precheckSkillFolder([dropped("readme.md")])).toMatch(/SKILL\.md/);
    expect(precheckSkillFolder([dropped("SKILL.md"), dropped("assets/logo.png")])).toBeNull();
  });

  it("rejects an empty folder", () => {
    expect(precheckSkillFolder([])).toMatch(/no files/);
  });

  it("caps the file count", () => {
    const files = [dropped("SKILL.md")];
    for (let i = 0; i < MAX_SKILL_FILES; i++) files.push(dropped(`f${i}.txt`));
    expect(precheckSkillFolder(files)).toMatch(/at most/);
  });

  it("caps total size at 5 MiB", () => {
    const files = [dropped("SKILL.md"), dropped("big.bin", 6 * 1024 * 1024)];
    expect(precheckSkillFolder(files)).toMatch(/5 MiB/);
  });
});

describe("zip writer", () => {
  it("computes the standard CRC-32", () => {
    // Well-known vector: crc32("123456789") = 0xCBF43926.
    expect(crc32(new TextEncoder().encode("123456789"))).toBe(0xcbf43926);
  });

  it("emits a structurally valid STORE archive", () => {
    const data = new TextEncoder().encode("hello skill");
    const zip = buildZip([{ path: "skill/SKILL.md", data }]);

    const view = new DataView(zip.buffer, zip.byteOffset, zip.byteLength);
    // Local file header signature at 0.
    expect(view.getUint32(0, true)).toBe(0x04034b50);
    // Stored bytes appear verbatim after the 30-byte header + name.
    const nameLen = "skill/SKILL.md".length;
    expect(zip.slice(30 + nameLen, 30 + nameLen + data.length)).toEqual(data);
    // EOCD signature in the trailer (last 22 bytes).
    expect(view.getUint32(zip.length - 22, true)).toBe(0x06054b50);
    // EOCD entry count = 1.
    expect(view.getUint16(zip.length - 22 + 10, true)).toBe(1);
    // Central directory signature where the EOCD says it starts.
    const cenOffset = view.getUint32(zip.length - 22 + 16, true);
    expect(view.getUint32(cenOffset, true)).toBe(0x02014b50);
  });
});
