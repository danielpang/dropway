// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { writeFileSync, rmSync } from "node:fs";
import { RoomRuntimeError } from "./errors.js";
import { randomUUID } from "node:crypto";
import { pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { reapOrphans } from "./control-plane.js";
import { withControlPlaneTransport } from "./gateway.js";
import { HostedClient, HostedAPIError } from "./hosted-client.js";
import { executeHostedClaim } from "./hosted-worker.js";
import { FlySpriteControlPlane } from "./sprites-adapter.js";

function required(env: NodeJS.ProcessEnv, name: string): string {
  const value = env[name];
  if (!value) throw new Error(`Missing ${name}`);
  return value;
}

export function validateWorkerEnvironment(env: NodeJS.ProcessEnv): void {
  for (const name of ["ROOM_API_PUBLIC_URL", "ROOM_WORKER_TOKEN", "SPRITES_API_TOKEN", "SPRITES_ORGANIZATION", "SPRITES_NAME_PREFIX"]) {
    if (!env[name]) throw new RoomRuntimeError("config_" + name, "Missing worker setting");
  }
  try { new HostedClient(env.ROOM_API_PUBLIC_URL!, "x".repeat(32)); }
  catch { throw new RoomRuntimeError("config_ROOM_API_PUBLIC_URL", "Invalid API origin"); }
  if (env.ROOM_WORKER_TOKEN!.length < 32) throw new RoomRuntimeError("config_ROOM_WORKER_TOKEN", "Token too short");
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$/.test(env.SPRITES_ORGANIZATION!)) throw new RoomRuntimeError("config_SPRITES_ORGANIZATION", "Invalid organization");
  if (!/^[a-z][a-z0-9-]{2,23}-$/.test(env.SPRITES_NAME_PREFIX!)) throw new RoomRuntimeError("config_SPRITES_NAME_PREFIX", "Invalid prefix");
}

export function safeWorkerError(error: unknown): { code: string; status?: number } {
  if (error instanceof HostedAPIError) return { code: "room_api", status: error.status };
  if (error instanceof RoomRuntimeError && /^(config_[A-Z_]+|sprite_[a-z_]+|worker_gateway_origin)$/.test(error.code)) return { code: error.code };
  return { code: "worker_error" };
}

export async function runDaemon(env: NodeJS.ProcessEnv, signal: AbortSignal): Promise<void> {
  rmSync("/tmp/dropway-worker-ready", { force: true });
  validateWorkerEnvironment(env);
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
  let stage = "reaper";
  while (!signal.aborted) {
    try {
      stage = "reaper";
      if (Date.now() - lastReap > 60_000) {
        await reapOrphans(reaper, new Date());
        lastReap = Date.now();
      }
      stage = "claim";
      const claim = await client.claim(workerID, signal);
      writeFileSync("/tmp/dropway-worker-ready", String(Date.now()), { mode: 0o600 });
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
        } catch (error) {
          // Provider errors may contain credentials or user content. Never log them.
          const code = signal.aborted ? "worker_shutdown" : "worker_failed";
          await client.attempt(claim, "fail", { code }, AbortSignal.timeout(10_000)).catch(() => undefined);
          console.error(JSON.stringify({ event: "attempt_failed", attempt_id: claim.attempt_id, code, failure: safeWorkerError(error) }));
        }
        continue;
      }
    } catch (error) {
      rmSync("/tmp/dropway-worker-ready", { force: true });
      if (!signal.aborted) console.error(JSON.stringify({ event: "worker_poll_failed", stage, ...safeWorkerError(error) }));
    }
    await delay(2000, undefined, { signal }).catch(() => undefined);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const controller = new AbortController();
  process.once("SIGTERM", () => controller.abort());
  process.once("SIGINT", () => controller.abort());
  runDaemon(process.env, controller.signal).catch((error) => {
    console.error(JSON.stringify({ event: "worker_start_failed", ...safeWorkerError(error) }));
    process.exitCode = 1;
  });
}
