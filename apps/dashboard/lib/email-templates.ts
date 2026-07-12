/**
 * Transactional email templates (Direction A — centered card).
 *
 * One shared, table-based HTML layout rendered for each of the four emails Better
 * Auth sends (lib/auth.ts): organization invitation, email verification, magic-link
 * sign-in, and password reset. Each builder returns `{ subject, html, text }` which
 * the callbacks hand to `sendEmail` (lib/email.ts) — `html` is the rendered card,
 * `text` is the plain-text fallback that carries the same link for HTML-stripping
 * clients and better deliverability.
 *
 * The markup is deliberately email-grade, NOT app-grade React/CSS:
 *  - tables + inline styles only (Gmail/Outlook strip <style>, flexbox, and external
 *    CSS), a 600px container, and a light color scheme locked via meta tags.
 *  - a VML <v:roundrect> button fallback so the CTA renders as a real button in
 *    Outlook/Windows (which ignores CSS padding/border-radius on <a>).
 *  - a hidden preheader (the inbox preview line) followed by zero-width spacers so
 *    the body text doesn't leak into the preview.
 *  - a visible "paste this link" copy of the action URL, because button clicks fail
 *    in plenty of clients.
 *
 * This module is intentionally dependency-free (no `server-only`, no env, no Node
 * APIs): pure string building, so it's safe to import anywhere, including under the
 * jiti loader that runs the auth config at `better-auth migrate` time. The caller
 * passes `appUrl` (betterAuthUrl()) so the logo resolves to an absolute URL — email
 * images must be absolute, served here from the dashboard's own /public.
 *
 * Copy avoids dashes (em/en/hyphen) by house style. Expiry windows below mirror the
 * Better Auth defaults in effect (no custom `expiresIn` is set in lib/auth.ts): magic
 * link 5 minutes, verification 1 hour, password reset 1 hour, invitation 2 days. If
 * those configs change, update the matching string here.
 */

export type RenderedEmail = {
  subject: string;
  html: string;
  text: string;
};

// Dropway brand tokens (from the design system: emails/index.html :root).
const INK = "#171C29"; // primary text
const INK2 = "#5C636C"; // body text
const INK3 = "#8A9099"; // muted / labels
const INDIGO = "#5647E1"; // brand / CTA
const BORDER = "#E4E7EC";
const BORDER_SOFT = "#EEF0F4";
const MUTED = "#F5F7FA"; // security box fill
const PAGE = "#EDEFF3"; // page background

const SANS =
  "'Geist', -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif";
const MONO =
  "'Geist Mono', ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace";

// Marketing site the logo and footer wordmark link to.
const BRAND_URL = "https://dropway.dev";

// Zero-width spacer that follows the preheader so the real email body doesn't bleed
// into the inbox preview line. U+034F (combining grapheme joiner) + nbsp + zwnj,
// repeated, is the standard trick and renders as nothing.
const PREHEADER_SPACER = "&#847;&zwnj;&nbsp;".repeat(60);

/** Escape a value for safe interpolation into HTML text or an attribute. */
function esc(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

type LayoutInput = {
  /** Inbox preview line (plain text). */
  preheader: string;
  /** Mono uppercase eyebrow, e.g. "Invitation". */
  kicker: string;
  /** Headline (plain text). */
  heading: string;
  /** Body paragraph as safe HTML (callers escape any dynamic values). */
  bodyHtml: string;
  /** CTA button label (plain text). */
  buttonLabel: string;
  /** Action URL (raw; escaped here for both the href and the visible copy). */
  url: string;
  /** Security/expiry note as safe HTML. */
  securityHtml: string;
  /** Footer "you received this because…" line (plain text). */
  footerReason: string;
  /** Absolute base URL of the dashboard, for the logo image. */
  appUrl: string;
};

/** Render the full HTML document for one email (Direction A centered card). */
function renderLayout(i: LayoutInput): string {
  const href = esc(i.url);
  const logo = `${esc(i.appUrl.replace(/\/$/, ""))}/email/dropway-mark.png`;
  return `<!doctype html><html lang="en" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="x-apple-disable-message-reformatting"><meta name="color-scheme" content="light"><meta name="supported-color-schemes" content="light"><title>${esc(i.heading)}</title><!--[if mso]><noscript><xml><o:OfficeDocumentSettings><o:PixelsPerInch>96</o:PixelsPerInch></o:OfficeDocumentSettings></xml></noscript><![endif]--><style>a{text-decoration:none}body,table,td{-webkit-text-size-adjust:100%;-ms-text-size-adjust:100%}img{-ms-interpolation-mode:bicubic}@media only screen and (max-width:620px){.cardpad{padding-left:24px!important;padding-right:24px!important}.h{font-size:23px!important}.bodytext{font-size:15px!important}}</style></head><body style="margin:0;padding:0;background:${PAGE};width:100%;">
<div style="display:none;max-height:0;overflow:hidden;mso-hide:all;font-size:1px;line-height:1px;color:${PAGE};opacity:0;">${esc(i.preheader)}&nbsp;${PREHEADER_SPACER}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:${PAGE};width:100%;"><tr><td align="center" style="padding:40px 14px;">
<!--[if mso | IE]><table role="presentation" width="600" align="center" cellpadding="0" cellspacing="0" border="0"><tr><td><![endif]-->
<table role="presentation" align="center" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;background:#ffffff;border:1px solid ${BORDER};border-radius:16px;overflow:hidden;">
<tr><td class="cardpad" style="padding:40px 48px 0;" align="center"><table role="presentation" cellpadding="0" cellspacing="0" border="0" style="border-collapse:collapse;"><tr><td style="padding-right:11px;vertical-align:middle;"><a href="${BRAND_URL}" style="text-decoration:none;"><img src="${logo}" width="30" height="30" alt="Dropway" style="display:block;border:0;border-radius:6.6px;"></a></td><td style="vertical-align:middle;font-family:${SANS};font-size:19.8px;font-weight:600;letter-spacing:-0.03em;color:${INK};"><a href="${BRAND_URL}" style="color:${INK};text-decoration:none;">Dropway</a></td></tr></table></td></tr>
<tr><td class="cardpad" style="padding:34px 48px 0;" align="center"><p style="margin:0;font-family:${MONO};font-size:12px;letter-spacing:0.16em;text-transform:uppercase;color:${INDIGO};">${esc(i.kicker)}</p></td></tr>
<tr><td class="cardpad" style="padding:12px 48px 0;" align="center"><h1 class="h" style="margin:0;font-family:${SANS};font-size:28px;line-height:1.18;letter-spacing:-0.025em;font-weight:600;color:${INK};">${esc(i.heading)}</h1></td></tr>
<tr><td class="cardpad" style="padding:16px 52px 0;" align="center"><p class="bodytext" style="margin:0;font-family:${SANS};font-size:16px;line-height:1.65;color:${INK2};">${i.bodyHtml}</p></td></tr>
<tr><td class="cardpad" style="padding:32px 48px 0;" align="center"><!--[if mso]><v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" xmlns:w="urn:schemas-microsoft-com:office:word" href="${href}" style="height:48px;v-text-anchor:middle;width:300px;" arcsize="22%" stroke="f" fillcolor="${INDIGO}"><w:anchorlock/><center style="color:#ffffff;font-family:Helvetica,Arial,sans-serif;font-size:15px;font-weight:600;">${esc(i.buttonLabel)}</center></v:roundrect><![endif]--><!--[if !mso]><!-- --><a href="${href}" style="display:inline-block;background:${INDIGO};color:#ffffff;font-family:${SANS};font-size:15px;font-weight:600;line-height:48px;text-decoration:none;border-radius:10px;padding:0 34px;box-shadow:0 1px 2px rgba(23,28,41,0.18);">${esc(i.buttonLabel)}</a><!--<![endif]--></td></tr>
<tr><td class="cardpad" style="padding:26px 48px 0;" align="center"><p style="margin:0 0 7px;font-family:${MONO};font-size:11px;letter-spacing:0.04em;text-transform:uppercase;color:${INK3};text-align:center;">Button not working? Paste this link</p><p style="margin:0;font-family:${MONO};font-size:12px;line-height:1.55;color:${INDIGO};word-break:break-all;text-align:center;"><a href="${href}" style="color:${INDIGO};text-decoration:none;">${href}</a></p></td></tr>
<tr><td class="cardpad" style="padding:32px 48px 0;"><div style="height:1px;background:${BORDER_SOFT};line-height:1px;font-size:0;">&nbsp;</div></td></tr>
<tr><td class="cardpad" style="padding:24px 48px 40px;"><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:${MUTED};border-radius:10px;"><tr><td style="padding:16px 18px;"><p style="margin:0 0 4px;font-family:${MONO};font-size:10px;letter-spacing:0.12em;text-transform:uppercase;color:${INK3};">Security</p><p style="margin:0;font-family:${SANS};font-size:13px;line-height:1.6;color:${INK2};">${i.securityHtml}</p></td></tr></table></td></tr>
</table>
<table role="presentation" align="center" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;"><tr><td class="cardpad" style="padding:28px 48px 8px;" align="center"><p style="margin:0 0 4px;font-family:${SANS};font-size:12px;line-height:1.6;color:${INK3};text-align:center;">${esc(i.footerReason)}</p><p style="margin:0;font-family:${SANS};font-size:12px;line-height:1.6;color:${INK3};text-align:center;"><a href="${BRAND_URL}" style="color:${INK3};text-decoration:none;">Dropway</a></p></td></tr></table>
<!--[if mso | IE]></td></tr></table><![endif]-->
</td></tr></table></body></html>`;
}

type TextInput = {
  heading: string;
  body: string;
  cta: string;
  url: string;
  security: string;
};

/** Render the plain-text fallback for one email. */
function renderText(i: TextInput): string {
  return (
    `${i.heading}\n\n` +
    `${i.body}\n\n` +
    `${i.cta}:\n${i.url}\n\n` +
    `${i.security}\n\n` +
    `Dropway`
  );
}

/** Organization invitation: "you were added to a workspace, accept to join". */
export function invitationEmail(p: {
  url: string;
  appUrl: string;
  orgName: string;
  inviterName?: string;
}): RenderedEmail {
  const org = esc(p.orgName);
  const inviter = p.inviterName?.trim();
  const leadHtml = inviter
    ? `<b style="color:${INK};font-weight:600;">${esc(inviter)}</b> invited you to join the <b style="color:${INK};font-weight:600;">${org}</b> workspace on Dropway. Accept to start shipping folders straight to live URLs.`
    : `You've been invited to join the <b style="color:${INK};font-weight:600;">${org}</b> workspace on Dropway. Accept to start shipping folders straight to live URLs.`;
  const leadText = inviter
    ? `${inviter} invited you to join the ${p.orgName} workspace on Dropway. Accept to start shipping folders straight to live URLs.`
    : `You've been invited to join the ${p.orgName} workspace on Dropway. Accept to start shipping folders straight to live URLs.`;
  const security =
    "This invitation expires in 2 days. If you weren't expecting it, you can safely ignore this email.";
  return {
    subject: `You're invited to join ${p.orgName} on Dropway`,
    html: renderLayout({
      preheader: inviter
        ? `${inviter} invited you to the ${p.orgName} workspace on Dropway.`
        : `You've been invited to the ${p.orgName} workspace on Dropway.`,
      kicker: "Invitation",
      heading: "You're invited to Dropway",
      bodyHtml: leadHtml,
      buttonLabel: "Accept invitation",
      url: p.url,
      securityHtml: security,
      footerReason:
        "You received this email because you were invited to a Dropway workspace.",
      appUrl: p.appUrl,
    }),
    text: renderText({
      heading: "You're invited to Dropway",
      body: leadText,
      cta: "Accept the invitation",
      url: p.url,
      security,
    }),
  };
}

/** Email verification sent on sign-up. */
export function verifyEmail(p: { url: string; appUrl: string }): RenderedEmail {
  const security =
    "This link expires in 1 hour. If you didn't create a Dropway account, you can safely ignore this email.";
  return {
    subject: "Verify your email for Dropway",
    html: renderLayout({
      preheader: "Confirm your email to finish setting up your Dropway account.",
      kicker: "Verify email",
      heading: "Verify your email address",
      bodyHtml:
        "Welcome to Dropway. Confirm this address to activate your account and deploy your first project.",
      buttonLabel: "Verify email",
      url: p.url,
      securityHtml: security,
      footerReason:
        "You received this email because this address was used to sign up for Dropway.",
      appUrl: p.appUrl,
    }),
    text: renderText({
      heading: "Verify your email address",
      body: "Welcome to Dropway. Confirm this address to activate your account and deploy your first project.",
      cta: "Verify your email",
      url: p.url,
      security,
    }),
  };
}

/** Passwordless magic-link sign-in. */
export function magicLinkEmail(p: { url: string; appUrl: string }): RenderedEmail {
  const security =
    "This link expires in 5 minutes and can be used once. If you didn't request it, you can safely ignore this email. Your account stays secure.";
  return {
    subject: "Sign in to Dropway",
    html: renderLayout({
      preheader: "Your link to sign in to Dropway expires in 5 minutes.",
      kicker: "Sign in",
      heading: "Sign in to Dropway",
      bodyHtml:
        "Use the button below to sign in to Dropway. No password needed.",
      buttonLabel: "Sign in to Dropway",
      url: p.url,
      securityHtml: security,
      footerReason:
        "You received this email because a sign in was requested for this address.",
      appUrl: p.appUrl,
    }),
    text: renderText({
      heading: "Sign in to Dropway",
      body: "Use the button below to sign in to Dropway. No password needed.",
      cta: "Sign in to Dropway",
      url: p.url,
      security,
    }),
  };
}

/**
 * Two-factor authentication enabled: a compromise tripwire, not a CTA email. The
 * button just deep-links to the security page so the recipient can review (and,
 * if this wasn't them, disable + rotate credentials).
 */
export function mfaEnabledEmail(p: { appUrl: string }): RenderedEmail {
  const url = `${p.appUrl.replace(/\/$/, "")}/account/security`;
  const security =
    "If you didn't enable two factor authentication, someone else may have access to your account. Review your security settings and change your password right away.";
  const body =
    "Two factor authentication is now active on your Dropway account. From now on, signing in requires a code from your authenticator app. Keep your backup codes somewhere safe: they are the only way back in if you lose your device.";
  return {
    subject: "Two-factor authentication was enabled on your account",
    html: renderLayout({
      preheader: "Two factor authentication is now active on your Dropway account.",
      kicker: "Security update",
      heading: "Two-factor authentication enabled",
      bodyHtml: body,
      buttonLabel: "Review security settings",
      url,
      securityHtml: security,
      footerReason:
        "You received this email because the security settings on your Dropway account changed.",
      appUrl: p.appUrl,
    }),
    text: renderText({
      heading: "Two-factor authentication enabled",
      body,
      cta: "Review your security settings",
      url,
      security,
    }),
  };
}

/** Two-factor authentication disabled: the matching tripwire for the off switch. */
export function mfaDisabledEmail(p: { appUrl: string }): RenderedEmail {
  const url = `${p.appUrl.replace(/\/$/, "")}/account/security`;
  const security =
    "If you didn't disable two factor authentication, someone else may have access to your account. Re-enable it and change your password right away.";
  const body =
    "Two factor authentication was turned off on your Dropway account. Signing in now requires only your usual method. You can re-enable it any time from your security settings.";
  return {
    subject: "Two-factor authentication was disabled on your account",
    html: renderLayout({
      preheader: "Two factor authentication was turned off on your Dropway account.",
      kicker: "Security update",
      heading: "Two-factor authentication disabled",
      bodyHtml: body,
      buttonLabel: "Review security settings",
      url,
      securityHtml: security,
      footerReason:
        "You received this email because the security settings on your Dropway account changed.",
      appUrl: p.appUrl,
    }),
    text: renderText({
      heading: "Two-factor authentication disabled",
      body,
      cta: "Review your security settings",
      url,
      security,
    }),
  };
}

/**
 * Two-factor authentication reset by an org owner/admin (the lockout recovery
 * path): the member re-enrolls at their next sign-in.
 */
export function mfaResetEmail(p: {
  appUrl: string;
  orgName: string;
}): RenderedEmail {
  const url = `${p.appUrl.replace(/\/$/, "")}/account/security`;
  const org = esc(p.orgName);
  const security =
    "If you weren't expecting this, contact your organization's admin. Your password was not changed.";
  const bodyHtml = `An owner or admin of <b style="color:${INK};font-weight:600;">${org}</b> reset two factor authentication on your Dropway account, usually because you lost access to your authenticator. Your existing codes no longer work. Set it up again from your security settings.`;
  const bodyText = `An owner or admin of ${p.orgName} reset two factor authentication on your Dropway account, usually because you lost access to your authenticator. Your existing codes no longer work. Set it up again from your security settings.`;
  return {
    subject: "Two-factor authentication was reset on your account",
    html: renderLayout({
      preheader: "Two factor authentication was reset on your Dropway account.",
      kicker: "Security update",
      heading: "Two-factor authentication was reset",
      bodyHtml,
      buttonLabel: "Set up two-factor again",
      url,
      securityHtml: security,
      footerReason:
        "You received this email because the security settings on your Dropway account changed.",
      appUrl: p.appUrl,
    }),
    text: renderText({
      heading: "Two-factor authentication was reset",
      body: bodyText,
      cta: "Set up two factor again",
      url,
      security,
    }),
  };
}

/** Password reset link. */
export function passwordResetEmail(p: {
  url: string;
  appUrl: string;
}): RenderedEmail {
  const security =
    "This link expires in 1 hour. If you didn't request a reset, your password is unchanged and you can safely ignore this email.";
  return {
    subject: "Reset your Dropway password",
    html: renderLayout({
      preheader: "Reset the password for your Dropway account.",
      kicker: "Reset password",
      heading: "Reset your password",
      bodyHtml:
        "We received a request to reset the password for your Dropway account. Choose a new one using the button below.",
      buttonLabel: "Reset password",
      url: p.url,
      securityHtml: security,
      footerReason:
        "You received this email because a password reset was requested for this address.",
      appUrl: p.appUrl,
    }),
    text: renderText({
      heading: "Reset your password",
      body: "We received a request to reset the password for your Dropway account. Choose a new one using the button below.",
      cta: "Reset your password",
      url: p.url,
      security,
    }),
  };
}
