// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// Exercises the remote helper against disposable fixture files, never model
// commands. Production has no child_process import or local execution fallback.
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, symlinkSync, writeFileSync, linkSync, mkdirSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { afterEach, describe, expect, it } from "vitest";
import { SPRITE_GUEST_HELPER } from "../src/sprites-guest.js";
import { artifactHash, verifySpriteArtifact, type SpriteArtifactFile } from "../src/sprites-adapter.js";
const paths: string[] = [];
afterEach(() => { for (const path of paths.splice(0)) rmSync(path, { recursive: true, force: true }); });
function guest() {
  const directory = mkdtempSync(join(tmpdir(), "dropway-remote-fixture-")); paths.push(directory);
  const root = join(directory, "workspace"); mkdirSync(root);
  const helper = SPRITE_GUEST_HELPER.replace("ROOT='/home/sprite/dropway-workspace'", `ROOT=${JSON.stringify(root)}`);
  return { root, directory, run(action: string, input: unknown): unknown {
    return JSON.parse(execFileSync("python3", ["-I", "-S", "-c", helper], { input: JSON.stringify({ action, input }), encoding: "utf8", env: { PATH: process.env.PATH ?? "/usr/bin:/bin" }, stdio: ["pipe", "pipe", "pipe"] }));
  } };
}
function file(path: string, body: Buffer): SpriteArtifactFile { return { path, contentBase64: body.toString("base64"), size: body.length, sha256: createHash("sha256").update(body).digest("hex") }; }
describe("remote workspace helper fixture", () => {
  it("stages binary/text files and exports the Go-compatible immutable digest", () => {
    const g = guest(); const files = [file("index.html", Buffer.from("old text")), file("assets/icon.bin", Buffer.from([0, 255, 1]))];
    g.run("stage", { files }); expect(g.run("read", { path: "index.html" })).toEqual({ text: "old text" });
    g.run("edit", { path: "index.html", oldText: "old", newText: "new" });
    const expected = [file("index.html", Buffer.from("new text")), files[1]!];
    const exported = verifySpriteArtifact(g.run("export", { directory: "." }));
    expect(exported.contentHash).toBe(artifactHash(expected)); expect(exported.files).toHaveLength(2);
  });
  it("rejects symlinks in every path component and hard links without truncating targets", () => {
    const g = guest(); const outside = join(g.directory, "outside"); writeFileSync(outside, "must survive");
    symlinkSync(outside, join(g.root, "link")); linkSync(outside, join(g.root, "hard"));
    mkdirSync(join(g.directory, "outside-dir")); symlinkSync(join(g.directory, "outside-dir"), join(g.root, "dir"));
    for (const path of ["link", "hard", "dir/file", "../outside", "/etc/passwd", "a\\b", ".env"]) {
      expect(() => g.run("write", { path, content: "overwrite" })).toThrow();
      expect(() => g.run("read", { path })).toThrow();
    }
    expect(readFileSync(outside, "utf8")).toBe("must survive");
    expect(() => g.run("export", { directory: "." })).toThrow();
  });
  it("rejects ambiguous edits and tampered staging before export", () => {
    const g = guest(); g.run("write", { path: "index.html", content: "same same" });
    expect(() => g.run("edit", { path: "index.html", oldText: "same", newText: "changed" })).toThrow();
    expect(() => g.run("stage", { files: [{ ...file("x", Buffer.from("data")), sha256: "a".repeat(64) }] })).toThrow();
  });
});
