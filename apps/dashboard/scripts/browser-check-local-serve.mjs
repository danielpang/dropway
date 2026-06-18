// Real-browser proof that a deployed site is reachable on local/self-host via
// its org-namespaced `*.localhost:8090` URL — exercising the EXACT port-bearing
// Host header a browser sends (`<org>--<app>.localhost:8090`), which the curl
// E2E (port-less Host) never exercised. Drives bundled Chromium directly.
import { chromium } from "@playwright/test";

const url = process.argv[2] || "http://acme1781565175--blog1781565175.localhost:8090/";

const browser = await chromium.launch();
const page = await browser.newPage();
const reqHosts = [];
page.on("request", (r) => {
  try {
    reqHosts.push(new URL(r.url()).host);
  } catch {}
});

const resp = await page.goto(url, { waitUntil: "networkidle", timeout: 20000 });
const status = resp?.status();
const title = await page.title();
const bodyLen = (await page.content()).length;
const h1 = await page.locator("h1, header, body").first().innerText().catch(() => "");

console.log(JSON.stringify({
  url,
  topNavHost: reqHosts[0],          // proves the browser sent <org>--<app>.localhost:8090
  status,                            // want 200
  title,
  bodyLen,
  bodySnippet: h1.slice(0, 80).replace(/\s+/g, " ").trim(),
}, null, 2));

await page.screenshot({ path: "scripts/local-serve-browser.png" });
await browser.close();

if (status !== 200) {
  console.error(`FAIL: expected 200, got ${status}`);
  process.exit(1);
}
console.log("PASS: real browser loaded the local org-namespaced site with HTTP 200");
