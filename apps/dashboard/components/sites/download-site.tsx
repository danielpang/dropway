"use client";

import * as React from "react";
import { Download, Loader2 } from "lucide-react";

import { downloadSiteAction } from "@/app/(app)/sites/[id]/actions";
import { Button } from "@/components/ui/button";
import { decodeSiteFiles } from "@/lib/site-download";
import { buildZip } from "@/lib/zip";

/**
 * Details-tab "download this site" control. Fetches the live version's files
 * through a server action (JWT stays server-side), zips them in the browser
 * the same way skill download does, and saves `<slug>.zip`. Disabled until the
 * site has a published version — there is nothing to fetch before then.
 */
export function DownloadSite({
  siteId,
  slug,
  isLive,
}: {
  siteId: string;
  slug: string;
  isLive: boolean;
}) {
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  const download = async () => {
    if (!isLive || busy) return;
    setBusy(true);
    setError(null);
    setNotice(null);
    const res = await downloadSiteAction(siteId);
    if (!res.ok) {
      setError(res.message);
      setBusy(false);
      return;
    }
    const skippedFromAction = res.skipped ?? [];
    const { entries, skipped: unsafe } = decodeSiteFiles(
      res.download.files ?? [],
      `${slug}/`,
    );
    const skipped = [...skippedFromAction, ...unsafe];
    if (!entries.length) {
      setError(
        skipped.length
          ? "None of this site's files could be included in the download."
          : "This site has no files to download.",
      );
      setBusy(false);
      return;
    }
    const blob = new Blob([new Uint8Array(buildZip(entries))], {
      type: "application/zip",
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${slug}.zip`;
    a.click();
    URL.revokeObjectURL(url);
    if (skipped.length === 1) {
      setNotice("Downloaded, but 1 file was too large or unsafe to include.");
    } else if (skipped.length > 1) {
      setNotice(
        `Downloaded, but ${skipped.length} files were too large or unsafe to include.`,
      );
    } else if (res.download.truncated) {
      setNotice("Downloaded, but some files could not be included.");
    }
    setBusy(false);
  };

  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="space-y-1">
        <p className="text-sm font-medium text-foreground">Download files</p>
        <p className="text-sm text-muted-foreground">
          {isLive
            ? "Save this site's current published files as a zip."
            : "Deploy a version before you can download this site's files."}
        </p>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
        {notice ? <p className="text-sm text-muted-foreground">{notice}</p> : null}
      </div>
      <Button
        variant="outline"
        onClick={() => void download()}
        disabled={!isLive || busy}
        className="shrink-0"
      >
        {busy ? (
          <Loader2 className="size-4 animate-spin" aria-hidden />
        ) : (
          <Download className="size-4" aria-hidden />
        )}
        Download
      </Button>
    </div>
  );
}
