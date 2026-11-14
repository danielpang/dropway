// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// Explicit opt-in development gate. This script creates paid remote resources.
import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import { FlySpriteControlPlane, artifactHash } from "../dist/sprites-adapter.js";
import { ATTEMPT_EGRESS_POLICY } from "../dist/policy.js";

if (!process.argv.includes("--run-live") || process.env.DROPWAY_SPRITES_LIVE_APPROVED !== "yes") {
  console.error("Not run. This creates up to two sequential disposable development Sprites. Set DROPWAY_SPRITES_LIVE_APPROVED=yes and pass --run-live only after authorizing the account and spend.");
  process.exit(2);
}
const token = process.env.SPRITES_TOKEN;
const organization = process.env.DROPWAY_SPRITES_ORG;
const prefix = process.env.DROPWAY_SPRITES_PREFIX;
if (!token || !organization || !prefix) throw new Error("SPRITES_TOKEN, DROPWAY_SPRITES_ORG and dedicated DROPWAY_SPRITES_PREFIX are required");
const deadline = Date.now() + 5 * 60_000;
const canary = randomUUID();
process.env.DROPWAY_HOST_ONLY_CANARY = canary;
const plane = new FlySpriteControlPlane({ token, expectedOrganization: organization, namePrefix: prefix,
  leaseMs: 120_000, operationTimeoutMs: 45_000,
  authorize: async () => { assert(Date.now() < deadline, "live test time budget expired"); },
});
const handles = [];
const checks = [];
const controller = new AbortController();
const timer = setTimeout(() => controller.abort(), Math.max(1, deadline - Date.now()));
const baseSpec = { orgId: "live-development-probe", roomId: "probe-room", siteId: "probe-site", versionId: "probe-version", attemptId: randomUUID(), sessionId: randomUUID(), initiatorUserId: "probe-user", taskId: randomUUID() };
function pass(name) { checks.push(name); console.log(`PASS ${name}`); }
const data = Buffer.from("<!doctype html><title>Dropway security gate</title>");
const files = [{ path: "index.html", contentBase64: data.toString("base64"), size: data.length, sha256: createHash("sha256").update(data).digest("hex") }];
try {
  const handle = await plane.create(baseSpec, ATTEMPT_EGRESS_POLICY); handles.push(handle);
  assert.deepEqual(await plane.readPolicy(handle), ATTEMPT_EGRESS_POLICY);
  assert.equal(await plane.readUrlAuth(handle), "sprite"); pass("private URL and deny-all policy readback");
  await plane.stage(handle, files, controller.signal);
  const artifact = await plane.exportArtifact(handle, ".", controller.signal);
  assert.equal(artifact.contentHash, artifactHash(files)); pass("source staging and trusted full-artifact rehash");
  const probe = `python3 -I -S - <<'PY'
import hashlib,json,os,socket
results={}
for domain in ['example.com','api.anthropic.com','api.openai.com','openrouter.ai','registry.npmjs.org']:
    try: socket.getaddrinfo(domain,443); results['dns:'+domain]=False
    except socket.gaierror: results['dns:'+domain]=True
for address,port in [('1.1.1.1',443),('10.0.0.1',80),('169.254.169.254',80)]:
    s=socket.socket();s.settimeout(2)
    try: s.connect((address,port));results['ip:'+address]=False
    except OSError: results['ip:'+address]=True
    finally:s.close()
results['environment']=[{'name':k,'digest':hashlib.sha256(v.encode()).hexdigest()} for k,v in os.environ.items()]
print(json.dumps(results))
PY`;
  const probeOutput = await plane.exec(handle, { kind: "bash", attemptId: handle.attemptId, input: { command: probe } }, controller.signal);
  assert(!probeOutput.isError, "remote network/security probe failed");
  const results = JSON.parse(probeOutput.text);
  for (const [name, blocked] of Object.entries(results)) if (name !== "environment") assert.equal(blocked, true, `${name} unexpectedly allowed`);
  pass("DNS, raw public-IP and private/metadata-IP connections blocked");
  const forbiddenNames = new Set(["SPRITES_TOKEN", "FLY_API_TOKEN", "ANTHROPIC_API_KEY", "OPENROUTER_API_KEY", "DATABASE_URL", "DROPWAY_WORKER_TOKEN", "DROPWAY_HOST_ONLY_CANARY"]);
  const forbiddenDigests = [token, canary].map((value) => createHash("sha256").update(value).digest("hex"));
  for (const item of results.environment) { assert(!forbiddenNames.has(item.name)); assert(!forbiddenDigests.includes(item.digest)); }
  pass("host management credential and canary absent from guest environment");
  // Releasing for clarification must delete, not merely stop the guest.
  await plane.destroy(handle, "clarification");
  assert(!(await plane.list()).some((lease) => lease.handle.spriteName === handle.spriteName));
  pass("clarification cleanup removes first Sprite");
  const resumed = await plane.create({ ...baseSpec, attemptId: randomUUID() }, ATTEMPT_EGRESS_POLICY); handles.push(resumed);
  assert.notEqual(resumed.spriteName, handle.spriteName);
  await plane.stage(resumed, artifact.files, controller.signal);
  assert.equal((await plane.exportArtifact(resumed, ".", controller.signal)).contentHash, artifact.contentHash);
  pass("resume stages authorized artifacts in a fresh Sprite");
  const cancel = new AbortController();
  const running = plane.exec(resumed, { kind: "bash", attemptId: resumed.attemptId, input: { command: "sleep 120" } }, cancel.signal);
  setTimeout(() => cancel.abort(), 1_000);
  await assert.rejects(running, /cancelled/);
  assert(!(await plane.list()).some((lease) => lease.handle.spriteName === resumed.spriteName));
  pass("in-flight cancellation destroys the entire attempt");
  console.log(JSON.stringify({ passed: checks, live: true, billing: "Inspect your Fly account for actual charges; this script does not estimate or cap account billing." }, null, 2));
} finally {
  clearTimeout(timer);
  const cleanup = await Promise.allSettled(handles.map((handle) => plane.destroy(handle, "finish")));
  const remaining = cleanup.filter((result) => result.status === "rejected");
  delete process.env.DROPWAY_HOST_ONLY_CANARY;
  if (remaining.length) {
    console.error("Cleanup needs attention. Use the dedicated Sprite prefix to inspect and destroy the test resources; no credentials are printed.");
    process.exitCode = 1;
  }
}
