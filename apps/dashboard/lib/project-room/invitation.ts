import "server-only";
import { api } from "@/lib/api";
import { object, type ProjectInvitation } from "@/lib/project-room/sharing";

export async function loadProjectInvitation(token: string): Promise<ProjectInvitation | null> {
  if (!/^[A-Za-z0-9_-]{43}$/.test(token)) return null;
  try {
    const value = object(await api.inspectProjectInvitation(token));
    if (typeof value.email !== "string" || typeof value.project_name !== "string" || typeof value.has_account !== "boolean") return null;
    return { email: value.email, projectName: value.project_name, hasAccount: value.has_account };
  } catch { return null; }
}
