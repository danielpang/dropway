// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Copy for /oauth/error, the page Better Auth sends the browser to when an
// OAuth error cannot be returned to the client (invalid_redirect, missing
// client_id, and the other failures that use onAPIError.errorURL).

export type OAuthErrorPresentation = {
  title: string;
  body: string;
  hint: string;
};

export function firstQueryValue(
  value: string | string[] | undefined,
): string | undefined {
  const values = typeof value === "string" ? [value] : value;
  if (!values) return undefined;
  for (const item of values) {
    if (typeof item !== "string") continue;
    const trimmed = item.trim();
    if (trimmed) return trimmed;
  }
  return undefined;
}

const ERROR_CODE = /^[A-Za-z0-9_-]{1,64}$/;
const ERROR_DESCRIPTION = /^[A-Za-z0-9 .,:'’+_/=()-]{1,200}$/;

/** OAuth error codes we will render. Anything else is omitted so a crafted link cannot put arbitrary text in the heading line. */
export function safeOAuthErrorCode(
  value: string | string[] | undefined,
): string | undefined {
  const raw = firstQueryValue(value);
  if (!raw || !ERROR_CODE.test(raw)) return undefined;
  return raw;
}

/**
 * error_description is reflected from the query string. Keep the short
 * phrases the authorization server sends, and drop HTML, URLs, and control
 * characters so this page cannot be used as a trusted-domain phishing prompt.
 */
export function safeOAuthErrorDescription(
  value: string | string[] | undefined,
): string | undefined {
  const raw = firstQueryValue(value);
  if (!raw || raw.length > 200) return undefined;
  if (/[\u0000-\u001f\u007f]/.test(raw)) return undefined;
  if (raw.includes("://") || raw.includes("<") || raw.includes(">")) return undefined;
  if (!ERROR_DESCRIPTION.test(raw)) return undefined;
  return raw;
}

export function oauthErrorPresentation(
  error: string | undefined,
): OAuthErrorPresentation {
  switch (error) {
    case "invalid_redirect":
      return {
        title: "Callback URL not accepted",
        body: "The callback URL from this connection is not one Dropway has registered for this app.",
        hint: "Start the connection again from the app that sent you here. For ChatGPT, remove the Dropway connector and add it again if it was added before this was fixed.",
      };
    case "invalid_client":
      return {
        title: "This app is not registered",
        body: "Dropway does not recognize the client that started this connection.",
        hint: "Start the connection again from the app that sent you here so it can register with Dropway.",
      };
    case "invalid_scope":
      return {
        title: "Requested permission was not granted",
        body: "The app asked for a permission Dropway does not grant on this connection.",
        hint: "Start the connection again from the app that sent you here.",
      };
    case "access_denied":
      return {
        title: "Access was denied",
        body: "This connection was not approved.",
        hint: "Return to the app that sent you here and try connecting again if you still want to authorize Dropway.",
      };
    case "server_error":
      return {
        title: "Authorization hit an error",
        body: "Dropway could not finish authorizing this app.",
        hint: "Wait a moment and start the connection again from the app that sent you here.",
      };
    default:
      return {
        title: "Authorization didn't finish",
        body: "Dropway could not complete this sign-in or authorization request.",
        hint: "Return to the app that sent you here and try again. You can also go back to your sites.",
      };
  }
}
