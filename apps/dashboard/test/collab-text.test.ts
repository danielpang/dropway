// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Text anchors match an exact quote at UTF-16 offsets with prefix and suffix.
// A missing quote, a duplicate that offsets cannot separate, or a quote that
// only exists somewhere else stays detached. Near-miss strings do not attach.

import { describe, expect, it } from "vitest";

import { resolveTextQuote } from "@/lib/collab";

const quote = {
  exact: "hello",
  prefix: "BC ",
  suffix: " suffix",
  start: 6,
  end: 11,
};

describe("resolveTextQuote", () => {
  it("matches the quote at its UTF-16 offsets, counting a surrogate pair as two units", () => {
    const text = "A👍BC hello suffix";
    expect("👍".length).toBe(2);
    expect(text.slice(6, 11)).toBe("hello");
    expect(
      resolveTextQuote(quote, { text }),
    ).toEqual({ status: "anchored", start: 6, end: 11 });
  });

  it("uses the offset to choose one of two exact quotes", () => {
    const text = "BC hello suffix ... BC hello suffix";
    const second = text.lastIndexOf("hello");
    expect(
      resolveTextQuote({ ...quote, start: second, end: second + 5 }, { text }),
    ).toEqual({ status: "anchored", start: second, end: second + 5 });
  });

  it("orphans duplicates when the stored offset matches neither context", () => {
    const text = "BC hello suffix ... BC hello suffix";
    expect(resolveTextQuote({ ...quote, start: 0, end: 5 }, { text })).toEqual({
      status: "ambiguous",
      count: 2,
    });
  });

  it("orphans a missing quote", () => {
    expect(resolveTextQuote(quote, { text: "nothing to see" })).toEqual({
      status: "missing",
    });
  });

  it("does not adopt a unique exact match whose offsets moved", () => {
    const text = "prefix BC hello suffix";
    const moved = text.indexOf("hello");
    expect(moved).not.toBe(quote.start);
    expect(resolveTextQuote(quote, { text })).toEqual({
      status: "stale",
      reason: "offset",
    });
  });

  it("does not fuzzy-match a one-character difference", () => {
    expect(resolveTextQuote(quote, { text: "A👍BC hell0 suffix" })).toEqual({
      status: "missing",
    });
  });

  it("treats an empty quote as invalid", () => {
    expect(
      resolveTextQuote({ ...quote, exact: "", start: 0, end: 0 }, { text: "hello" }),
    ).toEqual({ status: "invalid" });
  });

  it("stays missing when the named block is absent, even if the page contains the quote", () => {
    expect(
      resolveTextQuote(
        { ...quote, blockId: "p-2" },
        { text: null, blockId: "p-2" },
      ),
    ).toEqual({ status: "missing" });
    expect(
      resolveTextQuote(
        { ...quote, blockId: "p-2" },
        { text: "A👍BC hello suffix", blockId: "p-1" },
      ),
    ).toEqual({ status: "missing" });
  });

  it("matches inside the named block only", () => {
    expect(
      resolveTextQuote(
        { ...quote, blockId: "p-1" },
        { text: "A👍BC hello suffix", blockId: "p-1" },
      ),
    ).toEqual({ status: "anchored", start: 6, end: 11 });
  });

  it("stales when the offset slice still equals the quote but the context does not", () => {
    const text = "A👍XX hello suffix";
    expect(text.slice(6, 11)).toBe("hello");
    expect(resolveTextQuote(quote, { text })).toEqual({
      status: "stale",
      reason: "offset",
    });
  });
});
