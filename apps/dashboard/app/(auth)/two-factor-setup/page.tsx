import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { headers } from "next/headers";
import { ShieldCheck } from "lucide-react";

import { MandatoryTwoFactorSetup } from "@/components/account/mandatory-two-factor-setup";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  userHasPasswordCredential,
  userTwoFactorEnabled,
} from "@/lib/mfa-server";
import { getCurrentSession } from "@/lib/session";

export const metadata: Metadata = { title: "Set up two-factor authentication" };
export const dynamic = "force-dynamic";

/**
 * The MANDATORY two-factor setup flow for orgs with require_mfa: the (app)
 * layout redirects unenrolled members here and serves them nothing else until
 * they finish. Lives in the (auth) route group — outside the (app) layout — so
 * the enforcement gate can't redirect-loop.
 *
 * Already-enrolled users (or anyone landing here by URL) bounce straight to the
 * dashboard; the enrollment check is a fresh read, never the cached session.
 */
export default async function TwoFactorSetupPage() {
  const session = await getCurrentSession();
  if (!session) redirect("/sign-in");

  const user = session.user as { id: string; email: string };
  // Independent reads, in parallel: the common case here is an unenrolled
  // member who needs both anyway (the rare already-enrolled visitor wastes
  // one cheap accounts read before bouncing).
  const [enrolled, hasPassword] = await Promise.all([
    userTwoFactorEnabled(user.id),
    userHasPasswordCredential(await headers()),
  ]);
  if (enrolled) redirect("/dashboard");

  return (
    <div className="w-full">
      <Card className="shadow-md">
        <CardHeader className="space-y-1.5">
          <div className="grid size-10 place-items-center rounded-lg bg-primary/10 text-primary">
            <ShieldCheck className="size-5" aria-hidden />
          </div>
          <CardTitle>Set up two-factor authentication</CardTitle>
          <CardDescription>
            Your organization requires two-factor authentication for every
            member. Set it up once for{" "}
            <span className="font-medium text-foreground">{user.email}</span>{" "}
            and you&rsquo;re back in.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <MandatoryTwoFactorSetup requiresPassword={hasPassword} />
        </CardContent>
      </Card>
    </div>
  );
}
