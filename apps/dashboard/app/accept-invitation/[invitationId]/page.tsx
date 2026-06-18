import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { headers } from "next/headers";

import { AcceptInvitation } from "@/components/members/accept-invitation";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { auth } from "@/lib/auth";

export const metadata: Metadata = { title: "Join organization" };
export const dynamic = "force-dynamic";

/**
 * Accept-invitation landing (Better Auth Organization plugin). An invited user
 * clicks the email link → here. We require a session first (signing in/up and
 * returning to this same URL), then render the accept/decline control. Better
 * Auth verifies the invitation is for the signed-in user's email and adds the
 * membership; the Go API then sees the new `member` row on its next read.
 */
export default async function AcceptInvitationPage({
  params,
}: {
  params: Promise<{ invitationId: string }>;
}) {
  const { invitationId } = await params;

  const session = await auth.api
    .getSession({ headers: await headers() })
    .catch(() => null);

  if (!session) {
    // Sign in, then come back to this exact invitation link.
    const returnTo = `/accept-invitation/${encodeURIComponent(invitationId)}`;
    redirect(`/sign-in?callbackURL=${encodeURIComponent(returnTo)}`);
  }

  return (
    <Card className="shadow-md">
      <CardHeader className="space-y-1.5 text-center">
        <CardTitle className="text-lg">You&rsquo;ve been invited</CardTitle>
        <CardDescription>
          Join the organization to access its sites. You&rsquo;re signed in as{" "}
          <span className="font-medium text-foreground">
            {session.user.email}
          </span>
          .
        </CardDescription>
      </CardHeader>
      <CardContent>
        <AcceptInvitation invitationId={invitationId} />
      </CardContent>
    </Card>
  );
}
