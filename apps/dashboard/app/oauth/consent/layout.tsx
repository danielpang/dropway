import { redirect } from "next/navigation";

import { api } from "@/lib/api";
import { userTwoFactorEnabled } from "@/lib/mfa-server";
import { getCurrentSession } from "@/lib/session";

/**
 * MFA enforcement for the OAuth grant path. The consent page lives OUTSIDE the
 * (app) layout (the OAuth provider redirects here directly), so without this
 * check an unenrolled member of a require_mfa org could complete `dropway
 * login` / MCP connect and walk away with API tokens, bypassing enforcement
 * entirely — tokens are the crown jewels the policy exists to protect.
 *
 * Same posture as the (app) gate: fresh enrollment read (never the 5-minute
 * session cookie cache — an admin reset must not leave a grantable session),
 * fail-soft policy read (an API hiccup can't dead-end every OAuth flow), and
 * the redirect lands in the mandatory setup flow. The pending authorize
 * request is lost, but re-running the connect after enrolling resumes cleanly.
 */
export default async function OAuthConsentLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const session = await getCurrentSession();
  if (session) {
    const policy = await api.getOrgPolicy().catch(() => null);
    if (policy?.require_mfa) {
      const user = session.user as { id: string };
      if (!(await userTwoFactorEnabled(user.id))) {
        redirect("/two-factor-setup");
      }
    }
  }
  return <>{children}</>;
}
