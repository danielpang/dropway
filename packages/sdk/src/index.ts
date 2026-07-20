// SPDX-License-Identifier: FSL-1.1-Apache-2.0

/**
 * @dropway/sdk — the official TypeScript SDK for Dropway.
 *
 * Authenticate with an org-scoped API key (`dw_live_...`), then create and deploy
 * sites over the `/v1` control plane — no browser, no interactive OAuth. The key is
 * read from the constructor or, if omitted, the `DROPWAY_API_KEY` environment
 * variable, so CI just sets one env var.
 *
 *   import { Dropway } from "@dropway/sdk";
 *   const dw = new Dropway();                       // reads DROPWAY_API_KEY
 *   const site = await dw.sites.create({ slug: "launch" });
 *   const { liveUrl } = await dw.sites.deploy(site.id, {
 *     files: { "index.html": "<h1>hi</h1>" },
 *   });
 *
 * Two layers: the ergonomic `sites` namespace (the curated deploy loop), and the
 * low-level `request` escape hatch that reaches any `/v1` endpoint the generated
 * types describe (import `paths` / `components` from "@dropway/sdk/schema" once
 * generated, or annotate `request<T>()` yourself).
 */

import { HttpClient, type DropwayOptions } from "./client.js";
import { Sites } from "./sites.js";

export class Dropway {
  /** The shared transport (auth, retries, error mapping). */
  readonly http: HttpClient;
  /** The site + deployment ergonomic layer. */
  readonly sites: Sites;

  constructor(options: DropwayOptions = {}) {
    this.http = new HttpClient(options);
    this.sites = new Sites(this.http);
  }

  /**
   * request is the full-spec escape hatch: call any `/v1` endpoint directly, typed
   * by the caller. Non-2xx → a typed DropwayError.
   *
   *   const keys = await dw.request<{ keys: unknown[] }>("GET", "/v1/api-keys");
   */
  request<T = unknown>(
    method: string,
    path: string,
    opts?: Parameters<HttpClient["request"]>[2],
  ): Promise<T> {
    return this.http.request<T>(method, path, opts);
  }
}

export {
  HttpClient,
  DEFAULT_BASE_URL,
  type DropwayOptions,
  type RequestOptions,
} from "./client.js";
export {
  Sites,
  type Site,
  type CreateSiteInput,
  type DeployInput,
  type DeployResult,
} from "./sites.js";
export {
  DropwayError,
  AuthError,
  ForbiddenError,
  NotFoundError,
  QuotaExceededError,
  RateLimitError,
  type ApiErrorBody,
} from "./errors.js";
export {
  digest,
  sha256Hex,
  contentTypeForPath,
  buildManifest,
  type ManifestFile,
} from "./manifest.js";
