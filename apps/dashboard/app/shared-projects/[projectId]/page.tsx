import { redirect, notFound } from "next/navigation";
import { getCurrentSession } from "@/lib/session";
import { api } from "@/lib/api";
import { object, parseGuestProject } from "@/lib/project-room/sharing";
import { GuestProjectView } from "@/components/shared-projects/project-view";
export const dynamic = "force-dynamic";
export default async function SharedProjectPage({
  params,
}: {
  params: Promise<{ projectId: string }>;
}) {
  const { projectId } = await params;
  if (!(await getCurrentSession()))
    redirect(
      `/sign-in?callbackURL=${encodeURIComponent(`/shared-projects/${encodeURIComponent(projectId)}`)}`,
    );
  const data = await api
    .sharedProjectRequest(`/${encodeURIComponent(projectId)}`)
    .catch(() => null);
  const project = parseGuestProject(object(data).project);
  if (!project || project.id !== projectId) notFound();
  return <GuestProjectView project={project} />;
}
