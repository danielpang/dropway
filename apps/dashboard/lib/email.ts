import "server-only";

import nodemailer, { type Transporter } from "nodemailer";

import { mailFrom, mailSmtpUrl } from "@/lib/env";

/**
 * Vendor-neutral transactional email seam.
 *
 * Better Auth's auth flows (email verification, password reset, magic link) call
 * `sendEmail` to deliver a link. We send over SMTP, a universal interface, so a
 * self-host can point MAIL_SMTP_URL at anything (their own server, Gmail, SES,
 * Mailgun, Postmark, Resend's SMTP, or the bundled local Mailpit) with no hard
 * dependency on a specific vendor SDK.
 *
 * Degradation, by design (see lib/env.ts mailSmtpUrl + REQUIRE_EMAIL_VERIFICATION):
 *  - MAIL_SMTP_URL UNSET → NO-OP: we log the message at ERROR level (subject +
 *    recipient + the link if present) instead of sending, so a misconfigured deploy
 *    silently dropping every email is actually visible in the logs. A no-email
 *    self-host can still complete sign-up/magic-link by copying the link from the
 *    dashboard logs. (We log even in production: the dockerized dashboard runs as
 *    NODE_ENV=production, and a self-host with no provider has no other way to
 *    recover the link.)
 *  - send FAILS → we log the error and RECOVER (never throw). An SMTP outage must
 *    not break the auth flow with an unhandled rejection; Better Auth treats a
 *    resolved sendEmail as success, and the user can retry. Verification mail that
 *    silently fails is strictly better than a 500 on sign-up.
 */
export type EmailMessage = {
  to: string;
  subject: string;
  /** Plain-text body (always set, the link must survive HTML-stripping clients). */
  text: string;
  /** Optional HTML body; falls back to `text` when absent. */
  html?: string;
  /**
   * Optional Reply-To address. `from` stays the branded sender, but a reply is
   * routed here instead. The contact form sets it to the submitter's email so
   * support can respond to a bug report / feature request with a plain reply.
   */
  replyTo?: string;
};

// Lazily build a single pooled transport, reused across requests (and kept out
// of module init so importing this file never opens a socket when mail is off).
let cached: Transporter | null = null;

function transport(url: string): Transporter {
  if (!cached) {
    cached = nodemailer.createTransport(url);
  }
  return cached;
}

export async function sendEmail(msg: EmailMessage): Promise<void> {
  const url = mailSmtpUrl();

  if (!url) {
    // No provider wired → don't send, but log at ERROR level so a misconfigured
    // deploy is visible (an unset MAIL_SMTP_URL silently dropping every
    // verification / invite / reset mail is the kind of thing nobody notices until
    // a user reports a missing email). Still a no-op, not a throw: a no-email
    // self-host stays usable (the link is recoverable from this log), the error
    // level just makes the cause obvious. Set MAIL_SMTP_URL to actually send.
    // eslint-disable-next-line no-console
    console.error(
      `[email] NOT sent: MAIL_SMTP_URL is unset. to=${msg.to} ` +
        `subject=${JSON.stringify(msg.subject)}\n${msg.text}`,
    );
    return;
  }

  try {
    await transport(url).sendMail({
      from: mailFrom(),
      to: msg.to,
      replyTo: msg.replyTo,
      subject: msg.subject,
      text: msg.text,
      html: msg.html ?? msg.text,
    });
  } catch (err) {
    // Recover, never let a mail failure surface as an auth-flow 500.
    // eslint-disable-next-line no-console
    console.error(`[email] failed to send to ${msg.to}: ${String(err)}`);
  }
}
