import { api } from "@/lib/api";

/** Authenticated API evaluates PostHog for the current user and organization. */
export async function collaborationCapabilities() {
  try {
    const value = await api.projectCollaborationCapabilities();
    return { enabled: value?.enabled === true, executionConfigured: value?.execution_configured === true };
  } catch {
    return { enabled: false, executionConfigured: false };
  }
}

export async function collaborationEnabled(execution = false): Promise<boolean> {
  const capability = await collaborationCapabilities();
  return capability.enabled && (!execution || capability.executionConfigured);
}
