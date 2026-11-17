import { cookies } from "next/headers";
import { getCurrentSession } from "@/lib/session";
import { loadProjectInvitation } from "@/lib/project-room/invitation";
import { AcceptProjectInvitation } from "@/components/shared-projects/accept-invitation";
export const dynamic = "force-dynamic";
export default async function AcceptInvitationPage() {
  const token = (await cookies()).get("dropway-project-invitation")?.value;
  const [session, lookup] = await Promise.all([
    getCurrentSession(),
    token ? loadProjectInvitation(token) : Promise.resolve(null),
  ]);
  return (
    <AcceptProjectInvitation
      pendingInvitation={Boolean(token)}
      lookup={lookup}
      email={session?.user.email ?? null}
      verified={session?.user.emailVerified === true}
    />
  );
}
