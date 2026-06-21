// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the connection-capacity detector (lib/db-capacity.ts). This is the
// classifier behind the alertable [db-capacity] log tag, so we pin the exact shapes we
// expect to recognize: the Supabase/Supavisor session-pooler error that took prod down
// (EMAXCONNSESSION), Postgres too_many_connections (SQLSTATE 53300), and node-postgres
// acquire timeouts, including when the signal is nested under `cause`. We also assert
// ordinary errors are NOT misclassified, so the tag stays high-signal.

import { describe, expect, it } from "vitest";

import { connectionCapacityReason } from "@/lib/db-capacity";

describe("connectionCapacityReason", () => {
  it("recognizes the Supavisor session-pooler exhaustion from the production error", () => {
    // Shape mirrors the real pg error: generic XX000 code, signal in the message.
    const err = Object.assign(
      new Error("(EMAXCONNSESSION) max clients reached in session mode - max clients are limited to pool_size: 15"),
      { code: "XX000", severity: "FATAL" },
    );
    expect(connectionCapacityReason(err)).toBe("pooler_session_exhausted");
  });

  it("recognizes Postgres too_many_connections by SQLSTATE 53300", () => {
    expect(connectionCapacityReason(Object.assign(new Error("sorry, too many clients already"), { code: "53300" })))
      .toBe("too_many_connections");
  });

  it("recognizes a node-postgres pool acquire timeout", () => {
    expect(connectionCapacityReason(new Error("timeout exceeded when trying to connect")))
      .toBe("pool_acquire_timeout");
  });

  it("walks the cause chain to find a nested capacity error", () => {
    const inner = Object.assign(new Error("max clients reached in session mode"), { code: "XX000" });
    const outer = Object.assign(new Error("INTERNAL_SERVER_ERROR"), { cause: inner });
    expect(connectionCapacityReason(outer)).toBe("pooler_session_exhausted");
  });

  it("matches plain string messages too (logger forwards message + args)", () => {
    expect(connectionCapacityReason("EMAXCONNSESSION at findSession")).toBe("pooler_session_exhausted");
  });

  it("does not misclassify unrelated errors", () => {
    expect(connectionCapacityReason(new Error("relation \"identity.member\" does not exist"))).toBeNull();
    expect(connectionCapacityReason(null)).toBeNull();
    expect(connectionCapacityReason(undefined)).toBeNull();
    expect(connectionCapacityReason({})).toBeNull();
  });
});
