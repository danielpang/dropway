// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import type { HttpClient } from "./client.js";
import { buildManifest, type ManifestFile } from "./manifest.js";

/** A site as returned by the API (the fields the SDK surfaces). */
export interface Site {
  id: string;
  org_id?: string;
  slug: string;
  access_mode?: string;
  current_version_id?: string | null;
  live_url?: string;
  storage_bytes?: number;
}

/** Input to `sites.create`. */
export interface CreateSiteInput {
  slug: string;
  /** "public" | "org_only" at create; other modes are set later via setAccess. */
  accessMode?: "public" | "org_only";
}

/** Input to `sites.deploy`. Provide `files` (in-memory) or `dir` (Node-only). */
export interface DeployInput {
  /** A `{ servedPath: content }` map. Content is a string (UTF-8) or bytes. */
  files?: Record<string, string | Uint8Array>;
  /** A local directory to walk and deploy (Node-only). Mutually exclusive with `files`. */
  dir?: string;
  /** Publish (flip the live pointer) after finalizing. Default true. */
  publish?: boolean;
  /** Max concurrent blob uploads. Default 8. */
  concurrency?: number;
  /** AbortSignal to cancel the whole deploy. */
  signal?: AbortSignal;
}

/** The result of a completed deploy. */
export interface DeployResult {
  versionId: string;
  versionNo?: number;
  filesUploaded: number;
  previewUrl?: string;
  previewExpiresAt?: string;
  liveUrl?: string;
  published: boolean;
  warnings?: string[];
}

interface PrepareResponse {
  missing: string[];
  uploads: Record<string, string>;
}
interface FinalizeResponse {
  version_id: string;
  version_no?: number;
  preview_url?: string;
  preview_expires_at?: string;
  warnings?: string[];
}
interface PublishResponse {
  live_url?: string;
  version_id?: string;
}

/** wireManifest maps the SDK's camelCase manifest to the API's snake_case shape. */
function wireManifest(manifest: ManifestFile[]) {
  return manifest.map((f) => ({
    path: f.path,
    sha256: f.sha256,
    size: f.size,
    content_type: f.contentType,
  }));
}

/**
 * Sites is the ergonomic layer over the site + deployment endpoints. Its headline
 * is `deploy`, a faithful port of the server-proven loop:
 * build manifest → prepare → upload only-missing blobs to presigned URLs →
 * finalize (the API re-hashes every blob and verifies the digest) → publish.
 */
export class Sites {
  constructor(private readonly http: HttpClient) {}

  /** Create a site. Duplicate slug → DropwayError(400); over the site cap → QuotaExceededError. */
  create(input: CreateSiteInput): Promise<Site> {
    const body: Record<string, string> = { slug: input.slug };
    if (input.accessMode) body.access_mode = input.accessMode;
    return this.http.request<Site>("POST", "/v1/sites", { body });
  }

  /** List the org's sites. */
  async list(): Promise<Site[]> {
    const res = await this.http.request<{ sites?: Site[] } | Site[]>(
      "GET",
      "/v1/sites",
    );
    return Array.isArray(res) ? res : (res.sites ?? []);
  }

  /** Get one site by id. */
  get(id: string): Promise<Site> {
    return this.http.request<Site>(
      "GET",
      `/v1/sites/${encodeURIComponent(id)}`,
    );
  }

  /** Set a site's access mode ("public" | "org_only" | "password" | "allowlist"). */
  setAccess(
    id: string,
    input: { accessMode: string; password?: string },
  ): Promise<Site> {
    const body: Record<string, string> = { mode: input.accessMode };
    if (input.password) body.password = input.password;
    return this.http.request<Site>(
      "PUT",
      `/v1/sites/${encodeURIComponent(id)}/access`,
      { body },
    );
  }

  /** Publish (or roll back to) a specific version — flips the live pointer. */
  publish(id: string, input: { versionId: string }): Promise<PublishResponse> {
    return this.http.request<PublishResponse>(
      "POST",
      `/v1/sites/${encodeURIComponent(id)}/publish`,
      { body: { version_id: input.versionId } },
    );
  }

  /**
   * deploy runs the full content-addressed loop for `siteId`. It hashes each file,
   * prepares (learning which blobs are missing + where to PUT them), uploads only
   * the missing blobs directly to object storage (no auth header, no content-type —
   * the presigned URL is the credential), finalizes (the API re-hashes and verifies
   * the digest), and publishes unless `publish: false`.
   */
  async deploy(siteId: string, input: DeployInput): Promise<DeployResult> {
    if (input.files && input.dir) {
      throw new Error("deploy: pass `files` OR `dir`, not both");
    }
    const files =
      input.files ?? (input.dir ? await readDir(input.dir) : undefined);
    if (!files || Object.keys(files).length === 0) {
      throw new Error(
        "deploy: provide `files` or `dir` with at least one file",
      );
    }
    const { manifest, bytesBySha, digest } = buildManifest(files);
    const wire = wireManifest(manifest);
    const base = `/v1/sites/${encodeURIComponent(siteId)}`;

    // 1) prepare
    const prep = await this.http.request<PrepareResponse>(
      "POST",
      `${base}/deployments/prepare`,
      { body: { manifest: wire }, signal: input.signal },
    );

    // 2) upload missing blobs, concurrency-limited.
    const missing = prep.missing ?? [];
    await uploadAll(missing, input.concurrency ?? 8, async (sha) => {
      const url = prep.uploads?.[sha];
      if (!url) throw new Error(`deploy: no upload URL for blob ${sha}`);
      const bytes = bytesBySha.get(sha);
      if (!bytes) throw new Error(`deploy: missing bytes for blob ${sha}`);
      await this.http.putBlob(url, bytes, input.signal);
    });

    // 3) finalize — idempotent by content hash, so it is safe to retry.
    const fin = await this.http.request<FinalizeResponse>(
      "POST",
      `${base}/deployments`,
      {
        body: { manifest: wire, digest },
        idempotent: true,
        signal: input.signal,
      },
    );

    const result: DeployResult = {
      versionId: fin.version_id,
      versionNo: fin.version_no,
      filesUploaded: missing.length,
      previewUrl: fin.preview_url,
      previewExpiresAt: fin.preview_expires_at,
      published: false,
      warnings: fin.warnings,
    };

    if (input.publish === false) return result;

    // 4) publish
    const pub = await this.http.request<PublishResponse>(
      "POST",
      `${base}/publish`,
      {
        body: { version_id: fin.version_id },
        signal: input.signal,
      },
    );
    result.liveUrl = pub.live_url;
    result.published = true;
    return result;
  }
}

/** uploadAll runs `fn` over `items` with at most `concurrency` in flight. */
async function uploadAll(
  items: string[],
  concurrency: number,
  fn: (item: string) => Promise<void>,
): Promise<void> {
  const limit = Math.max(1, concurrency);
  let index = 0;
  async function worker(): Promise<void> {
    while (index < items.length) {
      const item = items[index++];
      if (item === undefined) continue;
      await fn(item);
    }
  }
  await Promise.all(
    Array.from({ length: Math.min(limit, items.length) }, worker),
  );
}

/**
 * readDir walks `dir` and returns a `{ relativePath: bytes }` map. Node-only —
 * `node:fs/promises` and `node:path` are imported lazily so bundlers targeting
 * non-Node runtimes can tree-shake this path away.
 */
async function readDir(dir: string): Promise<Record<string, Uint8Array>> {
  const { readdir, readFile } = await import("node:fs/promises");
  const { join, relative, sep } = await import("node:path");
  const out: Record<string, Uint8Array> = {};
  async function walk(current: string): Promise<void> {
    const entries = await readdir(current, { withFileTypes: true });
    for (const entry of entries) {
      const full = join(current, entry.name);
      if (entry.isDirectory()) {
        await walk(full);
      } else if (entry.isFile()) {
        const rel = relative(dir, full).split(sep).join("/");
        out[rel] = new Uint8Array(await readFile(full));
      }
    }
  }
  await walk(dir);
  return out;
}
