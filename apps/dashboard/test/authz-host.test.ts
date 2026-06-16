// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the /authz host + redirect validation (lib/authz-host.ts). These
// guard genuine open-redirect / token-exfiltration vectors: `host` becomes part
// of `https://<host>/__authz/callback?...` and `next` is the post-auth redirect
// on the content host, so both must be tightly validated before they are trusted.
// This module is fully pure (no imports), so it tests directly under node.

import { describe, expect, it } from "vitest";

import {
  CONTENT_SUFFIX,
  callbackUrl,
  isPlatformContentHost,
  normalizeContentHost,
  safeNextPath,
} from "@/lib/authz-host";

describe("normalizeContentHost", () => {
  it("accepts and lowercases a valid platform content host", () => {
    expect(normalizeContentHost("Acme.DropwayContent.com")).toBe(
      "acme.dropwaycontent.com",
    );
    expect(normalizeContentHost("  deep.sub.dropwaycontent.com  ")).toBe(
      "deep.sub.dropwaycontent.com",
    );
  });

  it("accepts an arbitrary registrable custom domain (Go API is the resolver)", () => {
    expect(normalizeContentHost("docs.example.com")).toBe("docs.example.com");
  });

  it("returns null for nullish / empty input", () => {
    expect(normalizeContentHost(null)).toBeNull();
    expect(normalizeContentHost(undefined)).toBeNull();
    expect(normalizeContentHost("")).toBeNull();
    expect(normalizeContentHost("   ")).toBeNull();
  });

  it("rejects hosts carrying scheme / port / userinfo / path / wildcard / spaces", () => {
    for (const bad of [
      "https://acme.dropwaycontent.com",
      "acme.dropwaycontent.com:8443",
      "user@acme.dropwaycontent.com",
      "acme.dropwaycontent.com/evil",
      "acme.dropwaycontent.com\\evil",
      "*.dropwaycontent.com",
      "ac me.dropwaycontent.com",
      "acme.dropwaycontent.com?x=1",
      "acme.dropwaycontent.com#frag",
      "[::1]",
    ]) {
      expect(normalizeContentHost(bad)).toBeNull();
    }
  });

  it("rejects a single-label host and a leading/trailing-dot / double-dot host", () => {
    expect(normalizeContentHost("localhost")).toBeNull(); // no dot → not multi-label
    expect(normalizeContentHost(".dropwaycontent.com")).toBeNull();
    expect(normalizeContentHost("acme.dropwaycontent.com.")).toBeNull(); // trailing dot
    expect(normalizeContentHost("acme..com")).toBeNull(); // empty label
    expect(normalizeContentHost("-acme.dropwaycontent.com")).toBeNull(); // label starts with -
  });

  it("rejects the bare apex of the content suffix (needs a label in front)", () => {
    // CONTENT_SUFFIX is ".dropwaycontent.com"; its apex must not be a valid host.
    expect(normalizeContentHost(CONTENT_SUFFIX.slice(1))).toBeNull();
  });
});

describe("isPlatformContentHost", () => {
  it("is true for a labelled host under the content suffix", () => {
    expect(isPlatformContentHost("acme.dropwaycontent.com")).toBe(true);
    expect(isPlatformContentHost("a.b.dropwaycontent.com")).toBe(true);
  });

  it("is false for the bare suffix and for off-suffix hosts", () => {
    // The apex (suffix with no label) is not a content host.
    expect(isPlatformContentHost("dropwaycontent.com")).toBe(false);
    expect(isPlatformContentHost("acme.example.com")).toBe(false);
    expect(isPlatformContentHost("evil-dropwaycontent.com")).toBe(false);
  });
});

describe("safeNextPath (same-site path only)", () => {
  it("keeps a same-site absolute path (+query/fragment)", () => {
    expect(safeNextPath("/docs/intro")).toBe("/docs/intro");
    expect(safeNextPath("/a?b=1&c=2")).toBe("/a?b=1&c=2");
    expect(safeNextPath("/")).toBe("/");
    expect(safeNextPath("  /trimmed  ")).toBe("/trimmed");
  });

  it("falls back to / for nullish / empty input", () => {
    expect(safeNextPath(null)).toBe("/");
    expect(safeNextPath(undefined)).toBe("/");
    expect(safeNextPath("")).toBe("/");
  });

  it("rejects absolute, protocol-relative, and backslash-trick targets", () => {
    expect(safeNextPath("https://evil.com")).toBe("/");
    expect(safeNextPath("//evil.com")).toBe("/");
    expect(safeNextPath("/\\evil.com")).toBe("/");
    expect(safeNextPath("relative/path")).toBe("/"); // not absolute
  });

  it("rejects control/whitespace chars (CRLF redirect-splitting)", () => {
    expect(safeNextPath("/a\r\nSet-Cookie: x")).toBe("/");
    expect(safeNextPath("/a\tb")).toBe("/");
    expect(safeNextPath("/ab")).toBe("/"); // DEL
  });
});

describe("callbackUrl", () => {
  it("builds the content-host callback with token + next as encoded query params", () => {
    const u = new URL(callbackUrl("acme.dropwaycontent.com", "tok.en", "/docs?x=1"));
    expect(u.origin).toBe("https://acme.dropwaycontent.com");
    expect(u.pathname).toBe("/__authz/callback");
    expect(u.searchParams.get("token")).toBe("tok.en");
    // URL encoding round-trips the next param (incl. its own query).
    expect(u.searchParams.get("next")).toBe("/docs?x=1");
  });

  it("encodes special characters in the token safely", () => {
    const u = new URL(callbackUrl("h.dropwaycontent.com", "a/b+c=d", "/"));
    expect(u.searchParams.get("token")).toBe("a/b+c=d");
    expect(u.searchParams.get("next")).toBe("/");
  });
});
