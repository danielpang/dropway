import type { Metadata } from "next";

import { RoomsClosed } from "@/components/project-room/rooms-closed";
import { RoomSession } from "@/components/project-room/room-session";
import { collaborationCapabilities } from "@/lib/project-room/capabilities";
import { ApiError, api, type Site, type Me } from "@/lib/api";
import { betterAuthUrl } from "@/lib/env";
import type { components as SdkComponents } from "@dropway/sdk/schema";
import { canEditRoomSite, parseRoom } from "@/lib/project-room/contract";

export const metadata: Metadata = { title: "Project rooms" };
export const dynamic = "force-dynamic";

export default async function RoomsPage() {
  const capability = await collaborationCapabilities();
  if (!capability.enabled) return <RoomsClosed />;

  try {
    const settings = await api.projectRoomSettings();
    const [rooms, sites, me] = await Promise.all([
      settings.enabled ? api.listProjectRooms() : Promise.resolve([]),
      api.listSites(),
      api.me(),
    ]);
    return (
      <RoomSession
        executionConfigured={capability.executionConfigured}
        orgEnabled={settings.enabled}
        rooms={rooms.flatMap((room) => {
          const parsed = parseRoom(room);
          return parsed ? [parsed] : [];
        })}
        sites={sites.flatMap((site) => siteOption(site, me))}
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

function siteOption(site: Site, me: Me) {
  if (!site.id || !site.slug) return [];
  return [{
    id: site.id,
    canEditContent: canEditRoomSite(site, me),
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
