import "server-only";
import { api, ApiError } from "@/lib/api";
import { object, type ProjectInvitationLookup } from "@/lib/project-room/sharing";

export async function loadProjectInvitation(token: string): Promise<ProjectInvitationLookup> {
  if (!/^[A-Za-z0-9_-]{43}$/.test(token)) return { status: "unavailable" };
  try {
    const value = object(await api.inspectProjectInvitation(token));
    if (typeof value.email !== "string" || typeof value.project_name !== "string" || typeof value.has_account !== "boolean") return { status: "temporary-error" };
    return { status: "available", invitation: { email: value.email, projectName: value.project_name, hasAccount: value.has_account } };
  } catch (error) {
    // Only the lookup contract's explicit rejection is definitive. In particular,
    // a 404 can mean this endpoint has not been deployed yet.
    if (error instanceof ApiError && error.status === 403 && object(error.body).error === "forbidden") return { status: "unavailable" };
    return { status: "temporary-error" };
  }
}
