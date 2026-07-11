// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for submitContactAction (contact/actions.ts). The contact form is a
// public "use server" endpoint, so the properties under test are: it refuses when
// SUPPORT_EMAIL is unset (feature off, not a silent drop), it validates the
// payload before sending, and when it does send it routes to the configured
// support inbox with the signed-in user's identity stamped on the message.
//
// The action is a "use server" module; under node that directive is an inert
// top-of-file string. We mock its three dependencies so the logic runs isolated.

import { afterEach, describe, expect, it, vi } from "vitest";

const { sendEmail, supportEmail, getCurrentSession } = vi.hoisted(() => ({
  sendEmail: vi.fn(),
  supportEmail: vi.fn(),
  getCurrentSession: vi.fn(),
}));

vi.mock("@/lib/email", () => ({ sendEmail }));
vi.mock("@/lib/env", () => ({ supportEmail }));
vi.mock("@/lib/session", () => ({ getCurrentSession }));

import { submitContactAction } from "@/app/(app)/contact/actions";

afterEach(() => {
  vi.clearAllMocks();
});

describe("submitContactAction", () => {
  it("refuses when SUPPORT_EMAIL is unset, without sending", async () => {
    supportEmail.mockReturnValue(undefined);

    const res = await submitContactAction({
      kind: "bug",
      subject: "x",
      message: "y",
    });

    expect(res.ok).toBe(false);
    expect(sendEmail).not.toHaveBeenCalled();
  });

  it("rejects an empty message before sending", async () => {
    supportEmail.mockReturnValue("support@example.com");
    getCurrentSession.mockResolvedValue({ user: { email: "u@example.com" } });

    const res = await submitContactAction({
      kind: "bug",
      subject: "has subject",
      message: "   ",
    });

    expect(res.ok).toBe(false);
    expect(sendEmail).not.toHaveBeenCalled();
  });

  it("rejects an over-long message before sending", async () => {
    supportEmail.mockReturnValue("support@example.com");
    getCurrentSession.mockResolvedValue({ user: { email: "u@example.com" } });

    const res = await submitContactAction({
      kind: "feature",
      subject: "",
      message: "z".repeat(5001),
    });

    expect(res.ok).toBe(false);
    expect(sendEmail).not.toHaveBeenCalled();
  });

  it("sends a bug report to the support inbox with the sender stamped", async () => {
    supportEmail.mockReturnValue("support@example.com");
    getCurrentSession.mockResolvedValue({
      user: { email: "dev@example.com", name: "Dev", id: "user-1" },
    });
    sendEmail.mockResolvedValue(undefined);

    const res = await submitContactAction({
      kind: "bug",
      subject: "Deploys fail",
      message: "It 500s on upload",
    });

    expect(res).toEqual({ ok: true });
    expect(sendEmail).toHaveBeenCalledTimes(1);
    const msg = sendEmail.mock.calls[0]![0];
    expect(msg.to).toBe("support@example.com");
    expect(msg.subject).toBe("[Bug report] Deploys fail");
    // Reply-To routes support's reply straight back to the submitter.
    expect(msg.replyTo).toBe("dev@example.com");
    // The sender's identity and the body are carried in the text part.
    expect(msg.text).toContain("dev@example.com");
    expect(msg.text).toContain("user-1");
    expect(msg.text).toContain("It 500s on upload");
  });

  it("omits Reply-To when the session email is missing or malformed", async () => {
    supportEmail.mockReturnValue("support@example.com");
    getCurrentSession.mockResolvedValue({ user: { id: "user-2" } });
    sendEmail.mockResolvedValue(undefined);

    await submitContactAction({
      kind: "bug",
      subject: "No email on session",
      message: "still send it",
    });

    const msg = sendEmail.mock.calls[0]![0];
    expect(msg.replyTo).toBeUndefined();
  });

  it("labels a feature request and falls back to the label when no subject", async () => {
    supportEmail.mockReturnValue("support@example.com");
    getCurrentSession.mockResolvedValue({ user: { email: "pm@example.com" } });
    sendEmail.mockResolvedValue(undefined);

    const res = await submitContactAction({
      kind: "feature",
      subject: "",
      message: "Add dark mode",
    });

    expect(res).toEqual({ ok: true });
    const msg = sendEmail.mock.calls[0]![0];
    expect(msg.subject).toBe("[Feature request] Feature request");
    expect(msg.text).toContain("Add dark mode");
  });

  it("escapes HTML in the rendered body to prevent injection", async () => {
    supportEmail.mockReturnValue("support@example.com");
    getCurrentSession.mockResolvedValue({ user: { email: "x@example.com" } });
    sendEmail.mockResolvedValue(undefined);

    await submitContactAction({
      kind: "bug",
      subject: "<script>",
      message: "<img src=x onerror=alert(1)>",
    });

    const msg = sendEmail.mock.calls[0]![0];
    expect(msg.html).not.toContain("<script>");
    expect(msg.html).not.toContain("<img");
    expect(msg.html).toContain("&lt;script&gt;");
  });
});
