// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Exact text quotes. Offsets are JavaScript UTF-16 code units. Prefix and
// suffix disambiguate a quote; they never fuzzy-match a different string.
// A quote that has moved is stale until someone explicitly reanchors it.

export interface TextQuote {
  exact: string;
  prefix: string;
  suffix: string;
  start: number;
  end: number;
  blockId?: string;
}

export interface TextScope {
  text: string | null;
  blockId?: string;
}

export type TextQuoteResolution =
  | { status: "anchored"; start: number; end: number }
  | { status: "missing" }
  | { status: "ambiguous"; count: number }
  | { status: "stale"; reason: "offset" }
  | { status: "invalid" };

function matchesAt(
  text: string,
  index: number,
  exact: string,
  prefix: string,
  suffix: string,
): boolean {
  if (index < 0 || index + exact.length > text.length) return false;
  if (text.slice(index, index + exact.length) !== exact) return false;
  if (prefix) {
    if (index < prefix.length) return false;
    if (text.slice(index - prefix.length, index) !== prefix) return false;
  }
  if (suffix) {
    const after = index + exact.length;
    if (text.slice(after, after + suffix.length) !== suffix) return false;
  }
  return true;
}

function findContextMatches(text: string, quote: TextQuote): number[] {
  const hits: number[] = [];
  let from = 0;
  while (from < text.length) {
    const index = text.indexOf(quote.exact, from);
    if (index < 0) break;
    if (matchesAt(text, index, quote.exact, quote.prefix, quote.suffix)) hits.push(index);
    from = index + 1;
  }
  return hits;
}

export function resolveTextQuote(quote: TextQuote, scope: TextScope): TextQuoteResolution {
  if (quote.exact.length === 0) return { status: "invalid" };
  if (quote.blockId !== undefined && (scope.text === null || scope.blockId !== quote.blockId)) {
    return { status: "missing" };
  }
  if (scope.text === null) return { status: "missing" };
  const text = scope.text;

  const offsetUsable =
    Number.isInteger(quote.start) &&
    Number.isInteger(quote.end) &&
    quote.start >= 0 &&
    quote.end - quote.start === quote.exact.length;
  const offsetExact = offsetUsable && text.slice(quote.start, quote.end) === quote.exact;
  if (
    offsetExact &&
    matchesAt(text, quote.start, quote.exact, quote.prefix, quote.suffix)
  ) {
    return { status: "anchored", start: quote.start, end: quote.end };
  }
  if (offsetExact) return { status: "stale", reason: "offset" };

  const hits = findContextMatches(text, quote);
  if (hits.length === 0) return { status: "missing" };
  if (hits.length > 1) return { status: "ambiguous", count: hits.length };
  return { status: "stale", reason: "offset" };
}
