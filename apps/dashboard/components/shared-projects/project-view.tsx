"use client";
import * as React from "react";
import { Button } from "@/components/ui/button";
import {
  sharedThreadsAction,
  sharedRepliesAction,
  sharedCommentAction,
  sharedReplyAction,
  sharedPreviewAction,
  sharedSignoffAction,
} from "@/app/shared-projects/actions";
import type { GuestProject, GuestThread } from "@/lib/project-room/sharing";
import type { StaticSnapshot } from "@/lib/collab/static-snapshot";
export function GuestProjectView({ project }: { project: GuestProject }) {
  const [siteId, setSiteId] = React.useState(project.sites[0]?.id ?? "");
  const site = project.sites.find((item) => item.id === siteId);
  const [page, setPage] = React.useState("/");
  const [body, setBody] = React.useState("");
  const [replies, setReplies] = React.useState<Record<string, string>>({});
  const [threads, setThreads] = React.useState<GuestThread[]>([]);
  const [cursor, setCursor] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState("");
  const [snapshot, setSnapshot] = React.useState<StaticSnapshot | null>(null);
  const [busy, setBusy] = React.useState(false);
  const inFlight = React.useRef(false);
  const request = React.useRef(0);
  const selectedSite = React.useRef(siteId);
  selectedSite.current = siteId;
  React.useEffect(() => {
    let active = true;
    setThreads([]);
    setCursor(null);
    setSnapshot(null);
    setBody("");
    setReplies({});
    request.current++;
    if (siteId)
      void sharedThreadsAction(project.id, siteId).then((result) => {
        if (!active) return;
        if (result.ok) {
          setThreads(result.threads);
          setCursor(result.cursor);
        } else setNotice(result.message);
      });
    return () => {
      active = false;
    };
  }, [project.id, siteId]);
  async function mutate(work: () => Promise<void>) {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    try {
      await work();
    } catch {
      setNotice("The request could not be confirmed. Refresh before retrying.");
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }
  async function preview(version: string, targetPage: string) {
    const id = ++request.current;
    setSnapshot(null);
    const result = await sharedPreviewAction(
      project.id,
      siteId,
      version,
      targetPage,
    );
    if (request.current !== id) return;
    if (result.ok) {
      setSnapshot(result.snapshot);
      setNotice("");
    } else setNotice(result.message);
  }
  const reviewer = project.role === "reviewer";
  return (
    <div className="space-y-6">
      <header className="space-y-2">
        <h1 className="text-xl font-medium">{project.name}</h1>
        <p className="text-sm text-muted-foreground">
          {reviewer
            ? "Reviewer · feedback and version sign-off"
            : "Viewer · read only"}
          . Access is limited to this project. Feedback never starts AI work or
          publishes changes.
        </p>
      </header>
      <p role="status" className="text-sm">
        {notice}
      </p>
      <section className="space-y-3" aria-label="Shared sites">
        <label className="block text-sm">
          Site
          <select
            aria-label="Shared site"
            className="ml-2 rounded border bg-background p-2"
            value={siteId}
            onChange={(event) => setSiteId(event.target.value)}
          >
            {project.sites.map((item) => (
              <option key={item.id} value={item.id}>
                {item.title || item.slug}
              </option>
            ))}
          </select>
        </label>
        {!site ? (
          <p>No sites are currently shared in this project.</p>
        ) : (
          <>
            <label className="block text-sm">
              Page path
              <input
                aria-label="Shared page path"
                className="ml-2 rounded border bg-background p-2"
                value={page}
                onChange={(event) => setPage(event.target.value)}
              />
            </label>
            <Button
              variant="outline"
              disabled={!site.current_version_id}
              onClick={() =>
                site.current_version_id &&
                void preview(site.current_version_id, page)
              }
            >
              Preview current version
            </Button>
            <p className="text-xs text-muted-foreground">
              Preview reconstructs immutable HTML without original scripts,
              styles or external resources. It does not change the site&apos;s
              public access.
            </p>
            {reviewer && site.current_version_id ? (
              <Button
                variant="outline"
                disabled={busy}
                onClick={() =>
                  void mutate(async () => {
                    const result = await sharedSignoffAction(
                      project.id,
                      site.id,
                      site.current_version_id!,
                      site.current_content_hash,
                    );
                    if (selectedSite.current === site.id)
                      setNotice(
                        result.ok
                          ? "Version signed off. This does not approve a candidate or publish changes."
                          : result.message,
                      );
                  })
                }
              >
                Sign off current version
              </Button>
            ) : null}
          </>
        )}
      </section>
      {snapshot ? (
        <section className="space-y-2" aria-label="Immutable preview">
          <p className="text-xs">
            Original version {snapshot.versionId} · {snapshot.page} · hash{" "}
            {snapshot.contentHash}
          </p>
          <iframe
            key={snapshot.snapshotId}
            title="Shared project static preview"
            srcDoc={snapshot.html}
            sandbox=""
            referrerPolicy="no-referrer"
            className="h-[32rem] w-full rounded border bg-white"
          />
          <p className="text-xs">
            {snapshot.omittedAssets} unsupported or unavailable assets omitted.
          </p>
        </section>
      ) : null}
      {reviewer && site?.current_version_id ? (
        <form
          className="space-y-2 rounded border p-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (!body.trim()) return;
            const submittedSite = site.id;
            void mutate(async () => {
              const result = await sharedCommentAction(project.id, {
                site_id: site.id,
                version_id: site.current_version_id!,
                content_hash: site.current_content_hash,
                page,
                body: body.trim(),
              });
              if (selectedSite.current !== submittedSite) return;
              if (result.ok) {
                setThreads((prev) => [result.thread, ...prev]);
                setBody("");
                setNotice("Feedback posted.");
              } else setNotice(result.message);
            });
          }}
        >
          <p className="text-sm">
            Comment on current version {site.current_version_id}, page {page}.
            Historical previews above remain pinned separately.
          </p>
          <textarea
            aria-label="Project feedback"
            required
            maxLength={4000}
            value={body}
            onChange={(event) => setBody(event.target.value)}
            className="min-h-24 w-full rounded border bg-background p-2"
          />
          <p className="text-xs text-muted-foreground">
            {new TextEncoder().encode(body).length} / 4,000 UTF-8 bytes
          </p>
          <Button
            disabled={
              busy ||
              !body.trim() ||
              new TextEncoder().encode(body).length > 4000
            }
          >
            Post feedback
          </Button>
        </form>
      ) : null}
      <section className="space-y-4" aria-label="Project discussion">
        <h2 className="font-medium">Discussion</h2>
        {threads.map((thread) => (
          <article key={thread.id} className="space-y-3 rounded border p-4">
            <p className="text-sm">
              {thread.page} · {thread.status} · {thread.anchor_state}
            </p>
            <p className="text-xs text-muted-foreground">
              Original version {thread.version_id} · {thread.created_at}
            </p>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void preview(thread.version_id, thread.page)}
            >
              View original version
            </Button>
            <ul className="space-y-2">
              {thread.replies.map((reply) => (
                <li key={reply.id}>
                  <p className="whitespace-pre-wrap break-words text-sm">
                    {reply.body}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {reply.created_at}
                  </p>
                </li>
              ))}
            </ul>
            {thread.replies_next_cursor ? (
              <Button
                variant="outline"
                disabled={busy}
                onClick={() =>
                  void mutate(async () => {
                    const targetSite = siteId;
                    const result = await sharedRepliesAction(
                      project.id,
                      thread.id,
                      thread.replies_next_cursor!,
                    );
                    if (selectedSite.current !== targetSite) return;
                    if (result.ok)
                      setThreads((prev) =>
                        prev.map((item) =>
                          item.id === thread.id
                            ? {
                                ...item,
                                replies: [
                                  ...item.replies,
                                  ...result.thread.replies.filter(
                                    (reply) =>
                                      !item.replies.some(
                                        (old) => old.id === reply.id,
                                      ),
                                  ),
                                ],
                                replies_next_cursor:
                                  result.thread.replies_next_cursor,
                              }
                            : item,
                        ),
                      );
                    else setNotice(result.message);
                  })
                }
              >
                More replies
              </Button>
            ) : null}
            {reviewer ? (
              <form
                className="space-y-2"
                onSubmit={(event) => {
                  event.preventDefault();
                  const body = replies[thread.id]?.trim();
                  if (!body) return;
                  const targetSite = siteId;
                  void mutate(async () => {
                    const result = await sharedReplyAction(
                      project.id,
                      thread.id,
                      body,
                    );
                    if (selectedSite.current !== targetSite) return;
                    if (result.ok) {
                      setThreads((prev) =>
                        prev.map((item) =>
                          item.id === thread.id
                            ? {
                                ...item,
                                replies: [...item.replies, result.reply],
                              }
                            : item,
                        ),
                      );
                      setReplies((prev) => ({ ...prev, [thread.id]: "" }));
                    } else setNotice(result.message);
                  });
                }}
              >
                <textarea
                  aria-label={`Reply to ${thread.id}`}
                  required
                  maxLength={4000}
                  className="w-full rounded border bg-background p-2"
                  value={replies[thread.id] ?? ""}
                  onChange={(event) =>
                    setReplies((prev) => ({
                      ...prev,
                      [thread.id]: event.target.value,
                    }))
                  }
                />
                <p className="text-xs text-muted-foreground">
                  {new TextEncoder().encode(replies[thread.id] ?? "").length} /
                  4,000 UTF-8 bytes
                </p>
                <Button
                  disabled={
                    busy ||
                    !replies[thread.id]?.trim() ||
                    new TextEncoder().encode(replies[thread.id] ?? "").length >
                      4000
                  }
                >
                  Reply
                </Button>
              </form>
            ) : null}
          </article>
        ))}
        {cursor ? (
          <Button
            variant="outline"
            disabled={busy}
            onClick={() =>
              void mutate(async () => {
                const targetSite = siteId;
                const result = await sharedThreadsAction(
                  project.id,
                  siteId,
                  cursor,
                );
                if (selectedSite.current !== targetSite) return;
                if (result.ok) {
                  setThreads((prev) => [
                    ...prev,
                    ...result.threads.filter(
                      (item) => !prev.some((old) => old.id === item.id),
                    ),
                  ]);
                  setCursor(result.cursor);
                } else setNotice(result.message);
              })
            }
          >
            More discussions
          </Button>
        ) : null}
      </section>
    </div>
  );
}
