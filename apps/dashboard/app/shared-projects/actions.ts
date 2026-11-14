"use server";
import { cookies } from "next/headers";
import { createHash } from "node:crypto";
import { api, ApiError } from "@/lib/api";
import { betterAuthUrl } from "@/lib/env";
import { sendEmailStrict } from "@/lib/email";
import { projectInvitationEmail } from "@/lib/email-templates";
import { parseRoomDetail } from "@/lib/project-room/contract";
import {
  object,
  parseShare,
  parseGuestProject,
  parseGuestThread,
  parseGuestReply,
  type GuestRole,
} from "@/lib/project-room/sharing";
import {
  prepareStaticSnapshot,
  verifiedRasterDataURL,
} from "@/lib/collab/static-renderer";
import { snapshotPagePath } from "@/lib/collab/static-snapshot";
const enc = encodeURIComponent;
function failure(error: unknown) {
  return {
    ok: false as const,
    message:
      error instanceof ApiError && error.status === 403
        ? "This project is unavailable for this account. Use the invited, verified email address; the owner may also need to enable external sharing."
        : "The request could not be completed. Refresh or ask the project owner to resend your invitation.",
  };
}
export async function listProjectSharesAction(roomId: string) {
  try {
    const data = object(await api.projectShares(roomId));
    return {
      ok: true as const,
      shares: Array.isArray(data.shares)
        ? data.shares.flatMap((item) => {
            const row = parseShare(item);
            return row ? [row] : [];
          })
        : [],
    };
  } catch (error) {
    return failure(error);
  }
}
export async function inviteProjectGuestAction(
  roomId: string,
  email: string,
  role: GuestRole,
) {
  if (
    !email.trim() ||
    email.length > 254 ||
    !["viewer", "reviewer"].includes(role)
  )
    return {
      ok: false as const,
      message: "Enter an email address and guest role.",
    };
  try {
    const room = parseRoomDetail(await api.getProjectRoom(roomId));
    if (!room) throw new Error("Project unavailable.");
    const data = object(
      await api.inviteProjectGuest(roomId, email.trim(), role),
    );
    const share = parseShare(data.share);
    if (
      !share ||
      typeof data.token !== "string" ||
      !/^[A-Za-z0-9_-]{32,128}$/.test(data.token)
    )
      throw new Error("Invalid invite response.");
    const url = new URL("/shared-projects/accept", betterAuthUrl());
    url.hash = new URLSearchParams({ invite: data.token }).toString();
    try {
      await sendEmailStrict({
        to: share.email,
        ...projectInvitationEmail({
          url: url.toString(),
          appUrl: betterAuthUrl(),
          role: share.role,
          expiresAt: share.expires_at,
          projectName: room.room.Name,
        }),
      });
    } catch {
      return {
        ok: true as const,
        share,
        delivered: false,
        message:
          "Invitation saved, but email delivery failed. Check email configuration and resend; no invitation link has been exposed here.",
      };
    }
    return {
      ok: true as const,
      share,
      delivered: true,
      message:
        "Invitation email accepted by the mail server. The recipient must sign in with the invited email and accept.",
    };
  } catch (error) {
    if (error instanceof ApiError && error.status === 409) {
      return {
        ok: false as const,
        message: "Invitation limit reached. Wait at least one minute before resending. An organization can send 50 invitations per hour, with up to 200 active guests per project.",
      };
    }
    return failure(error);
  }
}
export async function revokeProjectGuestAction(
  roomId: string,
  shareId: string,
) {
  try {
    await api.revokeProjectGuest(roomId, shareId);
    return { ok: true as const };
  } catch (error) {
    return failure(error);
  }
}
export async function rememberProjectInvitationAction(token: string) {
  if (!/^[A-Za-z0-9_-]{32,128}$/.test(token)) return { ok: false as const };
  (await cookies()).set("dropway-project-invitation", token, {
    httpOnly: true,
    secure: new URL(betterAuthUrl()).protocol === "https:",
    sameSite: "lax",
    path: "/shared-projects",
    maxAge: 1800,
  });
  return { ok: true as const };
}
export async function acceptSharedProjectAction() {
  try {
    const token =
      (await cookies()).get("dropway-project-invitation")?.value ?? "";
    if (!/^[A-Za-z0-9_-]{32,128}$/.test(token))
      throw new Error("Invalid invitation.");
    const project = parseGuestProject(
      object(await api.sharedProjectRequest("/accept", "POST", { token }))
        .project,
    );
    if (!project) throw new Error("Unavailable project.");
    (await cookies()).delete({
      name: "dropway-project-invitation",
      path: "/shared-projects",
    });
    return { ok: true as const, project };
  } catch (error) {
    if (error instanceof ApiError && [400, 404, 410].includes(error.status))
      (await cookies()).delete({
        name: "dropway-project-invitation",
        path: "/shared-projects",
      });
    return failure(error);
  }
}
export async function sharedThreadsAction(
  projectId: string,
  siteId?: string,
  cursor?: string,
) {
  try {
    const query = new URLSearchParams();
    if (siteId) query.set("site_id", siteId);
    if (cursor) query.set("cursor", cursor);
    const data = object(
      await api.sharedProjectRequest(`/${enc(projectId)}/threads?${query}`),
    );
    return {
      ok: true as const,
      threads: Array.isArray(data.threads)
        ? data.threads.flatMap((item) => {
            const thread = parseGuestThread(item);
            return thread ? [thread] : [];
          })
        : [],
      cursor: typeof data.next_cursor === "string" ? data.next_cursor : null,
    };
  } catch (error) {
    return failure(error);
  }
}
export async function sharedRepliesAction(
  projectId: string,
  threadId: string,
  cursor: string,
) {
  try {
    const thread = parseGuestThread(
      object(
        await api.sharedProjectRequest(
          `/${enc(projectId)}/threads/${enc(threadId)}?cursor=${enc(cursor)}`,
        ),
      ).thread,
    );
    if (!thread) throw new Error("Unavailable thread.");
    return { ok: true as const, thread };
  } catch (error) {
    return failure(error);
  }
}
export async function sharedCommentAction(
  projectId: string,
  input: {
    site_id: string;
    version_id: string;
    content_hash: string;
    page: string;
    body: string;
  },
) {
  if (
    typeof input.body !== "string" ||
    !input.body.trim() ||
    new TextEncoder().encode(input.body).length > 4000
  )
    return {
      ok: false as const,
      message:
        "Feedback must contain 1–4,000 UTF-8 bytes. Shorten the message and try again.",
    };
  try {
    const thread = parseGuestThread(
      object(
        await api.sharedProjectRequest(`/${enc(projectId)}/threads`, "POST", {
          site_id: input.site_id,
          version_id: input.version_id,
          content_hash: input.content_hash,
          page: input.page,
          body: input.body,
          anchor_kind: "page",
          anchor: {},
        }),
      ).thread,
    );
    if (!thread) throw new Error("Unavailable thread.");
    return { ok: true as const, thread };
  } catch (error) {
    return failure(error);
  }
}
export async function sharedReplyAction(
  projectId: string,
  threadId: string,
  body: string,
) {
  if (
    typeof body !== "string" ||
    !body.trim() ||
    new TextEncoder().encode(body).length > 4000
  )
    return {
      ok: false as const,
      message:
        "Replies must contain 1–4,000 UTF-8 bytes. Shorten the message and try again.",
    };
  try {
    const reply = parseGuestReply(
      object(
        await api.sharedProjectRequest(
          `/${enc(projectId)}/threads/${enc(threadId)}/replies`,
          "POST",
          { body },
        ),
      ).reply,
    );
    if (!reply) throw new Error("Unavailable reply.");
    return { ok: true as const, reply };
  } catch (error) {
    return failure(error);
  }
}
export async function sharedPreviewAction(
  projectId: string,
  siteId: string,
  versionId: string,
  page: string,
) {
  try {
    const path = snapshotPagePath(page);
    if (!path) throw new Error("Invalid path.");
    async function file(path: string) {
      const data = object(
        await api.sharedProjectRequest(
          `/${enc(projectId)}/sites/${enc(siteId)}/versions/${enc(versionId)}/files/content?path=${enc(path)}`,
        ),
      );
      if (
        data.site_id !== siteId ||
        data.version_id !== versionId ||
        data.path !== path ||
        typeof data.content_base64 !== "string" ||
        data.content_base64.length > 2800000 ||
        typeof data.content_type !== "string" ||
        typeof data.content_hash !== "string" ||
        !/^[a-f0-9]{64}$/.test(data.content_hash)
      )
        throw new Error("Invalid file.");
      const bytes = Buffer.from(data.content_base64, "base64");
      if (
        bytes.toString("base64") !== data.content_base64 ||
        createHash("sha256").update(bytes).digest("hex") !== data.sha256
      )
        throw new Error("Unverified file.");
      return {
        bytes,
        contentType: data.content_type,
        contentHash: data.content_hash,
      };
    }
    const main = await file(path);
    if (
      !["text/html", "application/xhtml+xml"].includes(
        main.contentType.split(";")[0]!.trim().toLowerCase(),
      )
    )
      throw new Error("HTML required.");
    const prepared = prepareStaticSnapshot(
      new TextDecoder("utf-8", { fatal: true }).decode(main.bytes),
      `/${path}`,
    );
    const images = new Map<string, string>();
    await Promise.all(
      prepared.imagePaths.slice(0, 8).map(async (path) => {
        try {
          const asset = await file(path);
          if (asset.contentHash !== main.contentHash) return;
          const image = verifiedRasterDataURL(asset.bytes, asset.contentType);
          if (image) images.set(path, image);
        } catch {
          /* Missing assets stay omitted. */
        }
      }),
    );
    return {
      ok: true as const,
      snapshot: prepared.render(images, {
        siteId,
        versionId,
        contentHash: main.contentHash,
        page,
      }),
    };
  } catch {
    return {
      ok: false as const,
      message:
        "This immutable HTML preview is unavailable or could not be verified. You can still read the discussion.",
    };
  }
}

export async function sharedSignoffAction(
  projectId: string,
  siteId: string,
  versionId: string,
  contentHash: string,
) {
  try {
    await api.sharedProjectRequest(
      `/${enc(projectId)}/version-signoffs`,
      "POST",
      { site_id: siteId, version_id: versionId, content_hash: contentHash },
    );
    return { ok: true as const };
  } catch (error) {
    return failure(error);
  }
}
