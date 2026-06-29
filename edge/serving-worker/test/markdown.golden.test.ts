// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Golden-fixture parity test. Both this TypeScript suite and the Go suite
// (services/serve/internal/markdown/golden_test.go) render the SHARED Markdown
// fixtures under testdata/markdown/*.md and assert the output matches the committed
// *.html golden. Because both ports check the same goldens, a drift in either
// renderer fails CI — the guard for the cross-language parity the renderer claims.

import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { renderMarkdown } from "../src/markdown";

// testdata/markdown lives at the repo root (shared with the Go suite); this test
// file sits three levels below it (edge/serving-worker/test).
const fixturesDir = fileURLToPath(new URL("../../../testdata/markdown/", import.meta.url));

describe("renderMarkdown — golden fixtures (TS/Go parity)", () => {
  const cases = readdirSync(fixturesDir).filter((f) => f.endsWith(".md"));

  it("has fixtures to check", () => {
    expect(cases.length).toBeGreaterThan(0);
  });

  for (const md of cases) {
    it(`renders ${md} to its golden`, () => {
      const source = readFileSync(`${fixturesDir}${md}`, "utf8");
      const want = readFileSync(`${fixturesDir}${md.slice(0, -3)}.html`, "utf8");
      expect(renderMarkdown(source)).toBe(want);
    });
  }
});
