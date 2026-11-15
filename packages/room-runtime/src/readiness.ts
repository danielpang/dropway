// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { statSync } from "node:fs";
import { setTimeout } from "node:timers/promises";
// A task is bounded to five minutes; allow it to finish before the next poll.
let ready = false;
for (let attempt = 0; attempt < 30; attempt++) {
  try { ready = Date.now() - statSync("/tmp/dropway-worker-ready").mtimeMs < 360_000; } catch { /* not ready */ }
  if (ready) break;
  await setTimeout(2000);
}
console.log(ready ? "Worker queue polling is ready" : "Worker has not successfully polled the queue; inspect worker_start_failed/worker_poll_failed logs");
process.exitCode = ready ? 0 : 1;
