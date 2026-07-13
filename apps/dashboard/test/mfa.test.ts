import { describe, expect, it } from "vitest";

import {
  mfaDisabledEmail,
  mfaEnabledEmail,
  mfaResetEmail,
} from "@/lib/email-templates";
import { nextPathFrom } from "@/lib/two-factor-gate";

const APP = "https://app.example.com";

describe("MFA notification emails", () => {
  it("enabled email links to the security page and reads as a tripwire", () => {
    const email = mfaEnabledEmail({ appUrl: APP });
    expect(email.subject).toMatch(/enabled/i);
    expect(email.html).toContain(`${APP}/account/security`);
    expect(email.text).toContain(`${APP}/account/security`);
    expect(email.text).toMatch(/if you didn't enable/i);
  });

  it("disabled email warns about re-enabling", () => {
    const email = mfaDisabledEmail({ appUrl: APP });
    expect(email.subject).toMatch(/disabled/i);
    expect(email.html).toContain(`${APP}/account/security`);
    expect(email.text).toMatch(/re-enable/i);
  });

  it("reset email names the org and escapes HTML in it", () => {
    const email = mfaResetEmail({
      appUrl: APP,
      orgName: `<img src=x onerror=alert(1)> & "Co"`,
    });
    expect(email.subject).toMatch(/reset/i);
    // The org name must be escaped in the HTML body (no raw tag injection).
    expect(email.html).not.toContain("<img src=x");
    expect(email.html).toContain("&lt;img src=x");
    // The plain-text part carries it verbatim (no HTML context to escape).
    expect(email.text).toContain(`<img src=x onerror=alert(1)> & "Co"`);
  });

  it("strips a trailing slash from appUrl before building the link", () => {
    const email = mfaEnabledEmail({ appUrl: `${APP}/` });
    expect(email.text).toContain(`${APP}/account/security`);
    expect(email.text).not.toContain(`${APP}//account/security`);
  });
});

describe("two-factor gate next-path sanitizing", () => {
  // Minimal stand-in for the thrown redirect the gate inspects: better-auth's
  // APIError carries `headers` and a truthy `_flag`-free shape; nextPathFrom
  // only reads headers when isAPIError matches, so a non-APIError input must
  // fall back safely.
  it("falls back to /dashboard when there is no redirect to inspect", () => {
    expect(nextPathFrom(undefined)).toBe("/dashboard");
    expect(nextPathFrom(new Error("boom"))).toBe("/dashboard");
    expect(nextPathFrom({ headers: new Headers() })).toBe("/dashboard");
  });
});
