"use client";

import * as React from "react";
import Link from "next/link";

import { loadRoomSitesAction } from "@/app/(app)/rooms/actions";
import { DeployDropzone } from "@/components/sites/deploy-dropzone";
import { NewSiteDialog } from "@/components/sites/new-site-dialog";
import { Button } from "@/components/ui/button";
import type { Site } from "@/lib/api";
import type { RoomSiteOption } from "./room-workspace";

/** Create and upload before attaching: the first version uses the normal site
 * workflow, and project creation remains the explicit attachment boundary. */
export function NewProjectSite({
  onReady,
  readOnly = false,
  orgSlug = null,
}: {
  onReady: (site: RoomSiteOption) => void;
  readOnly?: boolean;
  orgSlug?: string | null;
}) {
  const [site, setSite] = React.useState<Site | null>(null);
  const [published, setPublished] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [notice, setNotice] = React.useState("");
  const inFlight = React.useRef(false);

  async function selectCreatedSite() {
    if (!site?.id || !published || inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setNotice("");
    try {
      const result = await loadRoomSitesAction();
      if (!result.ok) {
        setNotice(result.message);
        return;
      }
      const ready = result.sites.find((item) => item.id === site.id);
      if (!ready?.currentVersionId || !ready.currentContentHash) {
        setNotice(
          "The site is still updating. Try adding it again in a moment.",
        );
        return;
      }
      onReady(ready);
      setSite(null);
      setPublished(false);
    } catch {
      setNotice(
        "The site was created, but could not be loaded. Try again to add it to the project.",
      );
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }

  if (!site)
    return (
      <NewSiteDialog
        readOnly={readOnly}
        orgSlug={orgSlug}
        triggerLabel="Create a new site"
        onCreated={(created) => {
          setSite(created);
          setPublished(false);
          setNotice("");
        }}
      />
    );

  return (
    <section
      aria-label="New project site"
      className="space-y-4 rounded-lg border border-border p-5"
    >
      <div className="space-y-1">
        <h3 className="font-medium">Upload {site.slug}</h3>
        <p className="text-sm text-muted-foreground">
          Drop your site folder below. Your site is saved in Sites; after
          uploading, select it for this project.
        </p>
      </div>
      {site.id ? (
        <DeployDropzone
          siteId={site.id}
          isLive={published}
          onDeployed={() => setPublished(true)}
        />
      ) : (
        <p role="alert">
          The new site response was incomplete. Reload Sites to continue.
        </p>
      )}
      {notice ? (
        <p role="status" className="text-sm text-destructive">
          {notice}
        </p>
      ) : null}
      <div className="flex flex-wrap items-center gap-3">
        <Button
          type="button"
          disabled={!published || busy}
          onClick={() => void selectCreatedSite()}
        >
          {busy ? "Loading site…" : "Use this site"}
        </Button>
        {site.id ? (
          <Link
            href={`/sites/${site.id}`}
            className="text-sm text-muted-foreground underline underline-offset-4"
          >
            Open site settings
          </Link>
        ) : null}
      </div>
    </section>
  );
}
