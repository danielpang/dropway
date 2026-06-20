import "server-only";

import { headers } from "next/headers";

import { auth } from "@/lib/auth";
import type { Role } from "@/lib/api";
import { getCurrentSession } from "@/lib/session";

/**
 * Server-side helpers over Better Auth's Organization plugin (`auth.api`).
 *
 * Better Auth owns the membership tables; the dashboard reads
 * + mutates them through the plugin's server API, while the Go API independently
 * re-checks role for any privileged write (the confused-deputy guard). The
 * plugin's `auth.api.*` methods are loosely typed (inferred from endpoints), so
 * we narrow them to the concrete shapes the UI needs at this single boundary.
 */

/** A member of the active org, enriched with the user's identity. */
export interface OrgMember {
  /** The `member` row id, the handle for role/remove mutations. */
  id: string;
  userId: string;
  email: string;
  name: string | null;
  role: Role;
  createdAt: string | null;
}

/** A pending invitation to the active org. */
export interface OrgInvitation {
  id: string;
  email: string;
  role: Role;
  status: string;
  expiresAt: string | null;
}

/** The viewer's own membership in the active org (role gating). */
export interface ActiveOrg {
  organizationId: string;
  name: string | null;
  slug: string | null;
  /** The viewer's user id, so the UI can refuse self-demotion / self-removal. */
  myUserId: string | null;
  myRole: Role;
  members: OrgMember[];
  invitations: OrgInvitation[];
}

function asRole(v: unknown): Role {
  return v === "owner" || v === "admin" ? v : "member";
}

function str(v: unknown): string | null {
  return typeof v === "string" && v.length > 0 ? v : null;
}

/**
 * Load the active organization with its members + pending invitations and the
 * viewer's own role, all in the caller's session context. Returns null when the
 * user has no active org (the (app) layout already routes those to onboarding).
 */
export async function loadActiveOrg(): Promise<ActiveOrg | null> {
  const requestHeaders = await headers();

  // getFullOrganization returns the active org with members (each carrying the
  // joined `user`) and invitations. Shapes are loose → narrow defensively.
  const full = (await auth.api
    .getFullOrganization({ headers: requestHeaders })
    .catch(() => null)) as {
    id?: string;
    name?: string;
    slug?: string;
    members?: Array<{
      id?: string;
      role?: string;
      createdAt?: string | Date;
      userId?: string;
      user?: { id?: string; email?: string; name?: string };
    }>;
    invitations?: Array<{
      id?: string;
      email?: string;
      role?: string;
      status?: string;
      expiresAt?: string | Date;
    }>;
  } | null;

  if (!full?.id) return null;

  // Reuses the request-memoized session (the (app) layout already resolved it),
  // so this page-level org load doesn't re-verify the cookie a second time.
  const session = await getCurrentSession();
  const myUserId = session?.user?.id;

  const members: OrgMember[] = (full.members ?? []).map((m) => ({
    id: m.id ?? "",
    userId: m.userId ?? m.user?.id ?? "",
    email: m.user?.email ?? "",
    name: str(m.user?.name),
    role: asRole(m.role),
    createdAt:
      m.createdAt instanceof Date
        ? m.createdAt.toISOString()
        : str(m.createdAt),
  }));

  const invitations: OrgInvitation[] = (full.invitations ?? [])
    // Only surface still-pending invites; accepted/rejected/cancelled are noise.
    .filter((i) => (i.status ?? "pending") === "pending")
    .map((i) => ({
      id: i.id ?? "",
      email: i.email ?? "",
      role: asRole(i.role),
      status: i.status ?? "pending",
      expiresAt:
        i.expiresAt instanceof Date
          ? i.expiresAt.toISOString()
          : str(i.expiresAt),
    }));

  const mine = members.find((m) => m.userId === myUserId);

  return {
    organizationId: full.id,
    name: str(full.name),
    slug: str(full.slug),
    myUserId: myUserId ?? null,
    myRole: mine?.role ?? "member",
    members,
    invitations,
  };
}

/** True for roles permitted to mutate membership/policy (owner ⊇ admin). */
export function canManage(role: Role): boolean {
  return role === "owner" || role === "admin";
}

/**
 * Resolve ONLY the viewer's role in the active org, a lightweight alternative
 * to loadActiveOrg() for the app shell, which just needs to decide whether to
 * show admin-only nav (e.g. the Audit link). Uses the org plugin's
 * getActiveMember (a single member-row read) and degrades to "member" on any
 * failure so the shell never shows admin affordances by accident (fail closed).
 */
export async function loadActiveRole(): Promise<Role> {
  const requestHeaders = await headers();
  const member = (await auth.api
    .getActiveMember({ headers: requestHeaders })
    .catch(() => null)) as { role?: string } | null;
  return asRole(member?.role);
}
