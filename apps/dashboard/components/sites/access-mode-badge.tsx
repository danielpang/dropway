import { Globe, Lock, Mail, Users } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import type { AccessMode } from "@/lib/api";

/** Icon + label for each site access mode (Phase 1 serves `public` only). */
const ACCESS: Record<
  AccessMode,
  { label: string; icon: typeof Globe }
> = {
  public: { label: "Public", icon: Globe },
  password: { label: "Password", icon: Lock },
  allowlist: { label: "Allowlist", icon: Mail },
  org_only: { label: "Org only", icon: Users },
};

/** Renders a site's access mode as a token-driven pill with a matching icon. */
export function AccessModeBadge({ mode }: { mode: AccessMode | undefined }) {
  const meta = (mode && ACCESS[mode]) || ACCESS.public;
  const Icon = meta.icon;
  return (
    <Badge variant="muted">
      <Icon className="size-3" aria-hidden />
      {meta.label}
    </Badge>
  );
}
