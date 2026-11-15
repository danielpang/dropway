// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { expect, it } from "vitest";
import { validateWorkerEnvironment, safeWorkerError } from "../src/daemon.js";
import { HostedClient, HostedAPIError } from "../src/hosted-client.js";
const valid = {ROOM_API_PUBLIC_URL:"https://api.example",ROOM_WORKER_TOKEN:"x".repeat(32),SPRITES_API_TOKEN:"secret",SPRITES_ORGANIZATION:"test-org",SPRITES_NAME_PREFIX:"room-"};
it("names invalid settings without exposing their values", () => {
  expect(() => validateWorkerEnvironment(valid)).not.toThrow();
  for (const name of Object.keys(valid)) {
    try { validateWorkerEnvironment({...valid,[name]:""}); throw new Error("expected failure"); }
    catch(error) { expect(safeWorkerError(error)).toEqual({code:`config_${name}`}); }
  }
  for (const [name,value] of [["ROOM_API_PUBLIC_URL","https://api.example/path?secret=abc"],["ROOM_WORKER_TOKEN","short"],["SPRITES_ORGANIZATION","bad org"],["SPRITES_NAME_PREFIX","missing-dash"]]) {
    try { validateWorkerEnvironment({...valid,[name!]:value}); throw new Error("expected failure"); }
    catch(error) {expect(safeWorkerError(error)).toEqual({code:`config_${name}`});}
  }
  expect(safeWorkerError(new Error("secret payload"))).toEqual({code:"worker_error"});
  expect(safeWorkerError(new HostedAPIError(401))).toEqual({code:"room_api",status:401});
});

it("identifies a mismatched gateway origin without exposing claim details", () => {
  const client = new HostedClient("https://api.example", "x".repeat(32));
  try { client.validateGateway({gateway_base_url:"https://dashboard.example/internal/room-gateway/test",attempt_id:"test"} as Parameters<HostedClient["validateGateway"]>[0]); }
  catch(error) {expect(safeWorkerError(error)).toEqual({code:"worker_gateway_origin"});return;}
  throw new Error("Expected gateway rejection");
});
