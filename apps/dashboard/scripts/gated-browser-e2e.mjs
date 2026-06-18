// Real-browser proof of the GATED (org_only) flow over local http:
//   a signed-in ORG MEMBER opens their org_only site → serve 302 → /authz →
//   (session present) mint → /__authz/callback on the CONTENT host (http://…:8090)
//   → __Host-edge cookie set → serve streams the site → 200.
//
// This is the flow that was dying at ERR_CONNECTION_REFUSED because the callback
// hardcoded https://<host> (→ :443). Everything runs in ONE browser context so the
// page carries the same Better Auth session the API setup established.
import { chromium } from "@playwright/test";
import { createHash } from "node:crypto";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";

const DASH = "http://localhost:3000";
const API = "http://localhost:8080";
const ROOT = "/Users/d_pang/projects/dropway";
const FOLDER = join(ROOT, "examples/synthwave-sunset");
const S = Date.now();
const EMAIL = `gate-${S}@example.com`;
const PASS = "gate-pass-123";
const ORG = `gate${S}`;
const APP = `site${S}`;
const CONTENT = `http://${ORG}--${APP}.localhost:8090/`;

const ctype = (p) =>
  p.endsWith(".html") ? "text/html; charset=utf-8"
  : p.endsWith(".css") ? "text/css; charset=utf-8"
  : "application/octet-stream";

function buildManifest(dir) {
  const files = [];
  const walk = (d) => {
    for (const name of readdirSync(d)) {
      const full = join(d, name);
      if (statSync(full).isDirectory()) walk(full);
      else {
        const data = readFileSync(full);
        files.push({
          path: relative(dir, full).split(sep).join("/"),
          sha256: createHash("sha256").update(data).digest("hex"),
          size: data.length,
          content_type: ctype(name),
          _full: full,
        });
      }
    }
  };
  walk(dir);
  files.sort((a, b) => (a.path < b.path ? -1 : 1));
  const digest = createHash("sha256")
    .update(files.map((f) => `${f.sha256}  ${f.path}\n`).join(""))
    .digest("hex");
  return { files, digest };
}

const browser = await chromium.launch();
const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
const rq = ctx.request; // shares the cookie jar with pages in this context
const J = async (r) => { const t = await r.text(); try { return JSON.parse(t); } catch { return t; } };

// 1) sign up (sets the Better Auth session cookie in this context)
await rq.post(`${DASH}/api/auth/sign-up/email`, { data: { name: "Gate", email: EMAIL, password: PASS } });
// 2) create org with a known slug + set active
const org = await J(await rq.post(`${DASH}/api/auth/organization/create`, { data: { name: `Gate ${S}`, slug: ORG } }));
const orgId = org?.id || org?.organization?.id;
await rq.post(`${DASH}/api/auth/organization/set-active`, { data: { organizationId: orgId } });
// 3) mint a JWT for the API
const { token: JWT } = await J(await rq.get(`${DASH}/api/auth/token`));
if (!JWT) { console.error("no JWT", org); process.exit(1); }

// 4) create an ORG_ONLY site (internal — no external sharing needed)
const H = { authorization: `Bearer ${JWT}`, "content-type": "application/json" };
const site = await J(await rq.post(`${API}/v1/sites`, { headers: H, data: { slug: APP, access_mode: "org_only" } }));
if (!site?.id) { console.error("create-site failed", site); process.exit(1); }

// 5) deploy: prepare → upload missing → finalize → publish
const { files, digest } = buildManifest(FOLDER);
const manifest = files.map(({ _full, ...m }) => m);
const prep = await J(await rq.post(`${API}/v1/sites/${site.id}/deployments/prepare`, { headers: H, data: { manifest } }));
const bysha = Object.fromEntries(files.map((f) => [f.sha256, f]));
for (const sha of prep.missing || []) {
  const f = bysha[sha];
  await rq.fetch(prep.uploads[sha], { method: "PUT", data: readFileSync(f._full), headers: { "content-type": "" } });
}
const fin = await J(await rq.post(`${API}/v1/sites/${site.id}/deployments`, { headers: H, data: { manifest, digest } }));
await rq.post(`${API}/v1/sites/${site.id}/publish`, { headers: H, data: { version_id: fin.version_id } });
console.log(`setup: org=${ORG} app=${APP} site=${site.id} version=${fin.version_id}`);

// 6) THE TEST: open the gated URL in a real page carrying the session cookie.
const page = await ctx.newPage();
const hops = [];
page.on("response", (r) => { if (r.request().isNavigationRequest()) hops.push(`${r.status()} ${r.url()}`); });
let navErr = null;
const resp = await page.goto(CONTENT, { waitUntil: "networkidle", timeout: 30000 }).catch((e) => { navErr = e; return null; });

const finalUrl = page.url();
const status = resp?.status();
const title = await page.title().catch(() => "");
const snippet = (await page.locator("body").innerText().catch(() => "")).slice(0, 80).replace(/\s+/g, " ").trim();
await page.screenshot({ path: "scripts/gated-browser.png" });

const cookies = await ctx.cookies(CONTENT);
const edgeCookie = cookies.find((c) => c.name === "__Host-edge");
console.log(JSON.stringify({ start: CONTENT, navHops: hops, finalUrl, status, title, snippet, navErr: navErr?.message,
  contentHostCookies: cookies.map((c) => c.name), edgeCookieStored: !!edgeCookie }, null, 2));
await browser.close();

const onContentHost = finalUrl.startsWith(`http://${ORG}--${APP}.localhost:8090`);
if (navErr) { console.error(`FAIL: navigation error — ${navErr.message}`); process.exit(1); }
if (status === 200 && onContentHost && /dropway|synthwave|<!doctype|deploy/i.test(snippet + title)) {
  console.log("PASS: signed-in org member loaded the org_only site over http (gated flow completed)");
} else {
  console.error(`FAIL: status=${status} onContentHost=${onContentHost} finalUrl=${finalUrl}`);
  process.exit(1);
}
