<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Implementation: download a site

Site download was already a control-plane capability (`GET /v1/sites/{id}/download`,
used by MCP `download_site`). This stage wired it into the dashboard Details tab
and documented the existing read endpoints in OpenAPI.

Earlier pipeline files (`docs/bento/product-investigation.md`, `design.md`,
`engineering-requirements.md`) were not in either checkout. The work follows the
feature brief and the surrounding skill-download pattern rather than a written
plan.

## What shipped

- **Details tab:** a Download card on the site page. Any org member can save the
  current published files as `<slug>.zip`. The button is disabled until the site
  is live.
- **Dashboard client:** typed `listSiteFiles` / `readSiteFile` / `downloadSite`
  against the Go API. The server action returns inline files; the browser zips
  them with the existing STORE zip writer (same path as skill download).
- **Truncation:** the API caps a bulk download at 10 MiB. The action lists the
  live manifest and fetches omitted paths individually so typical sites still
  download complete. Paths that remain over the per-file cap (or fail the
  safety check) are skipped and the UI says so.
- **OpenAPI:** `GET /v1/sites/{id}/files`, `/files/content`, and `/download` are
  now in `services/api/openapi/openapi.yaml`. Dashboard and SDK clients were
  regenerated from that spec.

`dropway-www` was not changed.

## How to verify

1. Open a deployed site → Details → Download. A zip named after the slug should
   contain the live files under a `<slug>/` folder.
2. On a site that has never been deployed, the button stays disabled.
3. Dashboard tests: `pnpm --filter @dropway/dashboard test` (covers complete /
   truncated / unsafe-path cases).
4. Existing API tests: `go test ./services/api/internal/handlers/` (list / read /
   download).
