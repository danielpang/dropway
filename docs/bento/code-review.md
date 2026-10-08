<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Code review: download a site

Reviewed `feature/add-ability-to-download-a-site-be4f48df` against the feature
brief (download a site's files; put the control on the site Details tab) and
`docs/bento/implementation.md`. Earlier pipeline files
(`product-investigation.md`, `design.md`, `engineering-requirements.md`) are
not in either checkout.

`dropway-www` has no commits on this branch; nothing to review there.

**Verdict: approve.** The change does what was asked, the new behavior is
covered by tests, I did not find a bug that would fail a typical download, and
the new code follows the existing skill-download pattern clearly enough.

Verification run on this review: `pnpm --filter @dropway/dashboard test` (27
files, 223 tests passed), `pnpm --filter @dropway/dashboard typecheck`,
`go test ./services/api/internal/handlers/` (ok).

## 1. Does what the requirements said

Yes.

- Details tab of a site (`apps/dashboard/app/(app)/sites/[id]/page.tsx`
  lines 311–326) renders a Download card with `DownloadSite`.
- The button is disabled until `site.current_version_id` is set
  (`download-site.tsx` line 96; `page.tsx` line 155), matching "nothing to
  fetch before a deploy."
- Clicking it calls `GET /v1/sites/{id}/download` through a server action,
  zips the live files in the browser, and saves `<slug>.zip`.
- Any org member can use it; that matches the existing API (RLS-scoped reads
  already used by MCP `download_site`). This PR does not add new endpoints, it
  documents the ones that already existed in OpenAPI.
- A changelog entry (`apps/dashboard/lib/changelog.ts` lines 35–48) announces
  the UI.

Not in scope, and correctly left alone: the Access/settings page, MCP, and
`dropway-www`.

## 2. Covered by tests

Yes for the new dashboard logic; the Go handlers were already tested and were
not changed.

| Behavior | Test |
| --- | --- |
| Empty site id never hits the API | `test/site-download-actions.test.ts` |
| Complete bulk payload returned as-is | same |
| Truncated payload filled via list + per-file read | same |
| Per-file fetch failure listed in `skipped` | same |
| Unpublished site 400 mapped to copy | same |
| Decode utf8/base64 under `<slug>/` | `test/site-download.test.ts` |
| Traversal / absolute paths skipped | same |

What is not covered, and does not need to block:

- The inner catch in `downloadSiteAction` (`actions.ts` lines 50–52) that
  keeps a partial zip when `listSiteFiles` itself fails. The UI still shows
  "some files could not be included" because `truncated` stays true.
- The client component (`download-site.tsx`). Same as skill download: vitest
  here is node-only and does not mount RSC/client UI.

## 3. Bugs

None found that would break the feature as specified.

Checked and discarded:

- Path traversal: `decodeSiteFiles` (`lib/site-download.ts` lines 84–86)
  reuses `isSafeSkillPath`; tested.
- Authz: download still goes through the existing JWT + Go tenant checks; the
  dashboard does not read object storage itself.
- Truncation: omitted files are fetched one path at a time; files over the
  10 MiB per-read cap are skipped and the user is told.

Non-blocking observations (would not fail a typical site):

- `completeTruncatedSiteDownload` (`lib/site-download.ts` lines 54–64) fans
  out `Promise.all` over every omitted path with no concurrency cap. A site
  with hundreds of files past the 10 MiB bulk budget will issue that many
  simultaneous API reads from one server action. Skill-folder download does
  the same. Fine for typical static sites; a huge tree could time out and
  then fall through to the partial-zip catch.
- `download()` in `download-site.tsx` (lines 30–75) has no `try/finally`. If
  `atob` or `buildZip` threw, `busy` would stay true and the button would
  stay disabled. Skill download has the same shape; the Go encoder makes
  invalid base64 unlikely.

## 4. Clarity

Clear enough for the next person.

- Client-safe helpers live in `lib/site-download.ts` with an explicit note
  that `lib/api.ts` is `server-only`. Duplicate `SiteFilePayload` types there
  are intentional, not drift.
- The action comment explains why truncated payloads are completed instead of
  shipping a 10 MiB stub.
- UI copy is plain and matches the disabled/live states.

Nit, not blocking: `packages/sdk/src/generated/schema.ts` is a wholesale
reformat (thousands of lines) on top of the three new operations. The
dashboard generated client is a small additive diff. The SDK file is generated
and the types are right; it just makes the PR noisier than it needs to be.
