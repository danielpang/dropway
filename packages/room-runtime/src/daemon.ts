// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { randomUUID } from "node:crypto";
import { pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { reapOrphans } from "./control-plane.js";
import { withControlPlaneTransport } from "./gateway.js";
import { HostedClient } from "./hosted-client.js";
import { executeHostedClaim } from "./hosted-worker.js";
import { FlySpriteControlPlane } from "./sprites-adapter.js";

function required(env: NodeJS.ProcessEnv, name: string): string {
  const value = env[name];
  if (!value) throw new Error(`Missing ${name}`);
  return value;
}

export async function runDaemon(env: NodeJS.ProcessEnv, signal: AbortSignal): Promise<void> {
  const client = new HostedClient(required(env, "ROOM_API_PUBLIC_URL"), required(env, "ROOM_WORKER_TOKEN"));
  const config = {
    token: required(env, "SPRITES_API_TOKEN"),
    expectedOrganization: required(env, "SPRITES_ORGANIZATION"),
    namePrefix: required(env, "SPRITES_NAME_PREFIX"),
    leaseMs: 900_000,
    transportScope: withControlPlaneTransport,
  };
  const reaper = new FlySpriteControlPlane({ ...config, authorize: async () => { throw new Error("Reaper cannot execute attempts"); } });
  const workerID = `room-${randomUUID()}`;
  let lastReap = 0;
  while (!signal.aborted) {
    try {
      if (Date.now() - lastReap > 60_000) {
        await reapOrphans(reaper, new Date());
        lastReap = Date.now();
      }
      const claim = await client.claim(workerID, signal);
      if (claim) {
        const plane = new FlySpriteControlPlane({ ...config, authorize: async (spec, _operation, live) => {
          const job = claim.job;
          if (spec.orgId !== job.org_id || spec.roomId !== job.room_id || spec.siteId !== job.site_id ||
              spec.versionId !== job.version_id || spec.initiatorUserId !== job.initiator_user_id ||
              spec.attemptId !== claim.attempt_id || spec.taskId !== job.id) throw new Error("Attempt authorization binding mismatch");
          await client.attempt(claim, "heartbeat", {}, AbortSignal.any([live, signal]));
        } });
        try {
          const result = await executeHostedClaim(client, claim, plane, signal);
          console.info(JSON.stringify({ event: "attempt_finished", attempt_id: claim.attempt_id, status: result.status }));
        } catch {
          // Provider errors may contain credentials or user content. Never log them.
          const code = signal.aborted ? "worker_shutdown" : "worker_failed";
          await client.attempt(claim, "fail", { code }, AbortSignal.timeout(10_000)).catch(() => undefined);
          console.error(JSON.stringify({ event: "attempt_failed", attempt_id: claim.attempt_id, code }));
        }
        continue;
      }
    } catch {
      if (!signal.aborted) console.error(JSON.stringify({ event: "worker_poll_failed" }));
    }
    await delay(2000, undefined, { signal }).catch(() => undefined);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const controller = new AbortController();
  process.once("SIGTERM", () => controller.abort());
  process.once("SIGINT", () => controller.abort());
  runDaemon(process.env, controller.signal).catch(() => {
    console.error("Room worker failed to start; check required environment configuration.");
    process.exitCode = 1;
  });
}
