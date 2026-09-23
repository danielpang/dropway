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
    if (trimmed) return trimmed.length > 300 ? trimmed.slice(0, 300) : trimmed;
  }
  return undefined;
}

export function oauthErrorPresentation(
  error: string | undefined,
): OAuthErrorPresentation {
  switch (error) {
    case "invalid_redirect":
      return {
        title: "Callback URL not accepted",
        body: "Dropway could not match the callback URL this app sent with a callback registered for this connection. ChatGPT's connector uses https://chatgpt.com/connector_platform_oauth_redirect or https://chatgpt.com/connector/oauth/…, and Dropway accepts that callback when this client already registered one on the same ChatGPT host.",
        hint: "Return to ChatGPT and start the connection again. If this connector was added earlier, remove it and add it once more so ChatGPT registers a fresh client.",
      };
    case "invalid_client":
      return {
        title: "This app is not registered",
        body: "Dropway does not recognize the client that started this connection.",
        hint: "Start the connection again from ChatGPT so it can register with Dropway.",
      };
    case "invalid_scope":
      return {
        title: "Requested permission was not granted",
        body: "The app asked for a permission Dropway does not grant on this connection.",
        hint: "Start the connection again from ChatGPT. If it keeps failing, remove the connector and add it again.",
      };
    case "access_denied":
      return {
        title: "Access was denied",
        body: "This connection was not approved.",
        hint: "Return to ChatGPT and try connecting again if you still want to authorize Dropway.",
      };
    case "server_error":
      return {
        title: "Authorization hit an error",
        body: "Dropway could not finish authorizing this app.",
        hint: "Wait a moment and start the connection again from ChatGPT.",
      };
    default:
      return {
        title: "Authorization didn't finish",
        body: "Dropway could not complete this sign-in or authorization request.",
        hint: "Return to the app that sent you here and try again. You can also go back to your sites.",
      };
  }
}
