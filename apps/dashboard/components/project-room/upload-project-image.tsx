"use client";

import * as React from "react";
import { Upload } from "lucide-react";
import { createProjectImageAction } from "@/app/(app)/rooms/image-actions";
import { loadRoomSitesAction } from "@/app/(app)/rooms/actions";
import { Button } from "@/components/ui/button";
import { deployFolder } from "@/lib/deploy";
import { projectImageFiles } from "@/lib/project-room/image-upload";
import type { RoomSiteOption } from "./room-workspace";

/** One image upload, with no website naming or configuration steps. */
export function UploadProjectImage({ onReady, disabled = false }: { onReady: (image: RoomSiteOption) => void; disabled?: boolean }) {
  const input = React.useRef<HTMLInputElement>(null);
  const pending = React.useRef<{ file: File; operationId: string; siteId?: string; deployed?: boolean } | null>(null);
  const running = React.useRef(false);
  const [busy, setBusy] = React.useState(false);
  const [notice, setNotice] = React.useState("");
  async function upload(file: File) {
    if (running.current || disabled) return;
    running.current = true;
    setBusy(true);
    setNotice("");
    try {
      const files = await projectImageFiles(file);
      if (!pending.current) pending.current = { file, operationId: crypto.randomUUID() };
      const item = pending.current;
      if (!item.siteId) {
        const created = await createProjectImageAction(file.name, item.operationId);
        if (!created.ok) throw new Error(created.message);
        item.siteId = created.siteId;
      }
      if (!item.deployed) {
        const result = await deployFolder({ siteId: item.siteId, files });
        if (!result.ok) throw new Error(result.message);
        item.deployed = true;
      }
      const result = await loadRoomSitesAction();
      if (!result.ok) throw new Error(result.message);
      const image = result.sites.find((site) => site.id === item.siteId);
      if (!image?.currentVersionId || !image.currentContentHash) throw new Error("Your image is saved but still updating. Try again in a moment.");
      onReady(image);
      pending.current = null;
      if (input.current) input.current.value = "";
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "The image could not be uploaded. Try again.");
    } finally {
      running.current = false;
      setBusy(false);
    }
  }
  return <div className="space-y-2">
    <input ref={input} type="file" accept="image/png,image/jpeg,image/webp" aria-label="Upload project image" className="sr-only" disabled={disabled || busy} onChange={(event) => {
      const file = event.target.files?.[0];
      if (file) void upload(file);
    }} />
    <Button type="button" variant="outline" disabled={disabled || busy} onClick={() => pending.current ? void upload(pending.current.file) : input.current?.click()}>
      <Upload className="mr-2 size-4" aria-hidden="true" />{busy ? "Uploading image…" : pending.current ? "Retry image upload" : "Upload image"}
    </Button>
    <p className="text-xs text-muted-foreground">PNG, JPEG or WebP · up to 2 MB. Private to your workspace. Uses one resource from your plan.</p>
    {notice ? <p role="alert" className="text-sm text-destructive">{notice}</p> : null}
  </div>;
}
