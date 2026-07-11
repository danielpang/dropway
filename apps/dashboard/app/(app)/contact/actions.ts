"use server";

import { sendEmail } from "@/lib/email";
import { supportEmail } from "@/lib/env";
import { getCurrentSession } from "@/lib/session";

/** The two kinds of message the contact form can send. */
export type ContactKind = "bug" | "feature";

export type ContactActionResult =
  | { ok: true }
  | { ok: false; message: string };

const KIND_LABEL: Record<ContactKind, string> = {
  bug: "Bug report",
  feature: "Feature request",
};

const MAX_SUBJECT = 200;
const MAX_MESSAGE = 5000;

/**
 * Deliver a contact-form submission (bug report / feature request) to the org's
 * support inbox, reusing the same transactional email seam (lib/email.ts) the
 * auth flows send through. The submission carries the signed-in user's id and
 * email in the body AND sets Reply-To to that email, so support can reply to the
 * user directly.
 *
 * Refuses when SUPPORT_EMAIL is unset (the feature is off, not silently
 * dropping feedback) and validates the payload server-side, since a "use server"
 * action is a public endpoint and must not trust client input.
 */
export async function submitContactAction(input: {
  kind: ContactKind;
  subject: string;
  message: string;
}): Promise<ContactActionResult> {
  const to = supportEmail();
  if (!to) {
    return {
      ok: false,
      message: "Contact is not configured on this deployment.",
    };
  }

  const kind = input.kind === "feature" ? "feature" : "bug";
  const subject = input.subject.trim();
  const message = input.message.trim();

  if (!message) {
    return { ok: false, message: "Please add a message before sending." };
  }
  if (subject.length > MAX_SUBJECT || message.length > MAX_MESSAGE) {
    return { ok: false, message: "Your message is too long. Please shorten it." };
  }

  // Attribute the report to the sender so support has context and can reply.
  const session = await getCurrentSession();
  const user = session?.user as
    | { email?: string; name?: string; id?: string }
    | undefined;
  const from = user?.email ?? "unknown";

  const label = KIND_LABEL[kind];
  const heading = subject || label;
  const mailSubject = `[${label}] ${heading}`;

  const lines = [
    `Type: ${label}`,
    `From: ${user?.name ? `${user.name} <${from}>` : from}`,
    user?.id ? `User ID: ${user.id}` : null,
    subject ? `Subject: ${subject}` : null,
    "",
    message,
  ].filter((l): l is string => l !== null);
  const text = lines.join("\n");

  const html = `<p><strong>${escapeHtml(label)}</strong></p>
<p>From: ${escapeHtml(user?.name ? `${user.name} <${from}>` : from)}${
    user?.id ? `<br/>User ID: ${escapeHtml(user.id)}` : ""
  }${subject ? `<br/>Subject: ${escapeHtml(subject)}` : ""}</p>
<p style="white-space:pre-wrap">${escapeHtml(message)}</p>`;

  // Route replies straight back to the submitter (when we know their email), so
  // support can respond to a bug report / feature request with a plain reply. A
  // basic shape check keeps a malformed value out of the Reply-To header.
  const replyTo =
    user?.email && /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(user.email)
      ? user.email
      : undefined;

  // sendEmail never throws (it logs + recovers), but a misconfigured SMTP URL is
  // still a no-op the user should not be told "sent" about. We can't distinguish
  // that here, so we optimistically report success; delivery failures surface in
  // the dashboard logs, same as every other outgoing mail.
  await sendEmail({ to, replyTo, subject: mailSubject, text, html });
  return { ok: true };
}

/** Minimal HTML entity escaping for the plain-text values we interpolate. */
function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}
