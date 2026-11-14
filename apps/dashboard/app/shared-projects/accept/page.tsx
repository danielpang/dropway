import { cookies } from "next/headers";
import { getCurrentSession } from "@/lib/session";
import { AcceptProjectInvitation } from "@/components/shared-projects/accept-invitation";
export const dynamic = "force-dynamic";
export default async function AcceptInvitationPage() {
  const session = await getCurrentSession();
  return (
    <AcceptProjectInvitation
      pendingInvitation={Boolean(
        (await cookies()).get("dropway-project-invitation")?.value,
      )}
      email={session?.user.email ?? null}
      verified={session?.user.emailVerified === true}
    />
  );
}
