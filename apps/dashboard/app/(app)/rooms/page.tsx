import type { Metadata } from "next";

import { RoomsClosed } from "@/components/project-room/rooms-closed";
import { RoomSession } from "@/components/project-room/room-session";
import { isCollabReviewEnabled } from "@/lib/collab";
import { ApiError, api, type Site } from "@/lib/api";
import { betterAuthUrl } from "@/lib/env";
import type { components as SdkComponents } from "@dropway/sdk/schema";
import { parseRoom } from "@/lib/project-room/contract";

export const metadata: Metadata = { title: "Project rooms" };
export const dynamic = "force-dynamic";

export default async function RoomsPage() {
  const on = isCollabReviewEnabled({
    NEXT_PUBLIC_COLLAB_REVIEW: process.env.NEXT_PUBLIC_COLLAB_REVIEW,
  });
  if (!on) return <RoomsClosed />;

  try {
    const [settings, rooms, sites] = await Promise.all([
      api.projectRoomSettings(),
      api.listProjectRooms(),
      api.listSites(),
    ]);
    return (
      <RoomSession
        orgEnabled={settings.enabled}
        rooms={rooms.flatMap((room) => {
          const parsed = parseRoom(room);
          return parsed ? [parsed] : [];
        })}
        sites={sites.flatMap(siteOption)}
        parentOrigin={betterAuthUrl()}
      />
    );
  } catch (error) {
    const message = error instanceof ApiError ? error.message : "The room API could not be read.";
    return (
      <RoomSession
        orgEnabled={false}
        rooms={[]}
        sites={[]}
        parentOrigin={betterAuthUrl()}
        loadError={message}
      />
    );
  }
}

function siteOption(site: Site) {
  if (!site.id || !site.slug) return [];
  return [{
    id: site.id,
    slug: site.slug,
    liveUrl: site.live_url ?? null,
    currentVersionId: site.current_version_id ?? null,
    currentContentHash: liveContentHash(site),
  }];
}

function liveContentHash(site: Site): string | null {
  const hash = (site as Site & Pick<SdkComponents["schemas"]["Site"], "current_content_hash">).current_content_hash;
  return typeof hash === "string" && hash.length > 0 ? hash : null;
}
