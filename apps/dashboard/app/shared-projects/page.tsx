import Link from "next/link";
import { ArrowRight, Folder } from "lucide-react";
import { redirect } from "next/navigation";
import { getCurrentSession } from "@/lib/session";
import { api } from "@/lib/api";
import { object, parseGuestProject } from "@/lib/project-room/sharing";
export const dynamic = "force-dynamic";
export default async function SharedProjectsPage() {
  if (!(await getCurrentSession()))
    redirect("/sign-in?callbackURL=%2Fshared-projects");
  try {
    const data = object(await api.sharedProjectRequest(""));
    const projects = Array.isArray(data.projects)
      ? data.projects.flatMap((item) => {
          const project = parseGuestProject(item);
          return project ? [project] : [];
        })
      : [];
    return (
      <>
        <h1 className="text-xl font-medium">Projects shared with you</h1>
        <p className="text-sm text-muted-foreground">
          Only accepted invitations for your verified email appear here.
        </p>
        {projects.length ? (
          <ul className="space-y-3">
            {projects.map((project) => (
              <li key={project.id}>
                <Link
                  className="flex items-center gap-3 rounded-lg border border-border bg-card p-4 transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  href={`/shared-projects/${encodeURIComponent(project.id)}`}
                >
                  <Folder className="size-5 shrink-0 text-muted-foreground" aria-hidden />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{project.name || "Shared project"}</span>
                    <span className="mt-1 block text-xs capitalize text-muted-foreground">{project.role}</span>
                  </span>
                  <ArrowRight className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                </Link>
              </li>
            ))}
          </ul>
        ) : (
          <p>
            No accepted projects. Open your invitation email to sign in with the
            invited address and accept.
          </p>
        )}
      </>
    );
  } catch {
    return (
      <>
        <h1 className="text-xl font-medium">Shared projects unavailable</h1>
        <p>Verify your email or ask the project owner to check your access.</p>
      </>
    );
  }
}
