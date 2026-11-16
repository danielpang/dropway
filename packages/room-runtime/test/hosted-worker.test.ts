// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { SPRITE_GUEST_HELPER } from "../src/sprites-guest.js";
import { afterEach, expect, it } from "vitest";
import { executeHostedClaim, type HostedPlane } from "../src/hosted-worker.js";
import { HostedClient, type HostedClaim, type HostedSource } from "../src/hosted-client.js";
import { artifactHash, type SpriteArtifactFile } from "../src/sprites-adapter.js";
import type { SandboxHandle } from "../src/control-plane.js";
import { RoomRuntimeError } from "../src/errors.js";
import { FakePlane } from "./fake-plane.js";
import { FakeGateway } from "./fake-gateway.js";

const gateways: FakeGateway[] = [];
afterEach(async () => { await Promise.all(gateways.splice(0).map(g => g.stop())); });
class Plane extends FakePlane implements HostedPlane {
  staged: { sprite: string; files: SpriteArtifactFile[] }[] = [];
  exportFails = false;
  unchanged = false;
  async stage(h: SandboxHandle, files: SpriteArtifactFile[]) { this.staged.push({ sprite: h.spriteName, files: structuredClone(files) }); }
  async exportArtifact(h: SandboxHandle) {
    expect(this.destroys.some(d => d.sprite === h.spriteName)).toBe(false);
    if (this.exportFails) throw new Error("export failed");
    const files = structuredClone(this.staged.find(s => s.sprite === h.spriteName)!.files);
    if (!this.unchanged) { const file=files[0]!; const data=Buffer.from("<h1>changed</h1>"); file.contentBase64=data.toString("base64");file.size=data.length;file.sha256=createHash("sha256").update(data).digest("hex"); }
    return { files, contentHash: artifactHash(files) };
  }
}
async function stack() {
  const gateway = new FakeGateway(); await gateway.start(); gateways.push(gateway);
  const plane = new Plane();
  const bytes = Buffer.from("<h1>immutable source</h1>");
  const file = { path: "index.html", content_base64: bytes.toString("base64"), content_type: "text/html", size: bytes.length, sha256: createHash("sha256").update(bytes).digest("hex") };
  const source: HostedSource = { files: [file], version_id: "version-1", content_hash: "source-hash", source_content_hash: artifactHash([{ path: file.path, sha256: file.sha256 }]) };
  const claim: HostedClaim = {
    attempt_id: "attempt-1", attempt_token: "attempt-capability", gateway_token: "dw_gateway_attempttoken",
    gateway_base_url: `${gateway.origin}/internal/room-gateway/attempt-1`, deadline: new Date(Date.now()+60_000).toISOString(), lease_expires_at: new Date(Date.now()+30_000).toISOString(),
    job: { id: "task-1", org_id: "org-1", room_id: "room-1", site_id: "site-1", version_id: "version-1", content_hash: "source-hash", initiator_user_id: "user-1", credential_ref: "cred-1", session_id: "session-1", tokens_used: 0,
      config: { provider: "anthropic", model_id: "claude-sonnet-4-5", harness_id: "pi", harness_version: "1.5.0", prompt: "Change the heading", tool_policy: "static-site-v1", budget: { max_tokens: 100, max_calls: 10, max_seconds: 60, max_output_bytes: 1000 } } },
  };
  const calls: { operation: string; body: any }[] = [];
  const client = new HostedClient(gateway.origin, "w".repeat(32), async (input, init) => {
    const operation = String(input).split("/").at(-1)!;
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer attempt-capability");
    expect(init?.redirect).toBe("error");
    if (["candidate", "clarify"].includes(operation)) expect(plane.destroys).toHaveLength(plane.staged.length);
    calls.push({ operation, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    return Response.json(operation === "source" ? source : {});
  });
  return { gateway, plane, source, claim, calls, client, run: () => executeHostedClaim(client, claim, plane, new AbortController().signal) };
}
it("stages verified immutable source and submits only after sandbox destruction", async () => {
  const s = await stack(); s.source.thread = { Anchor: { quote: "review reference sentinel" } }; s.gateway.steps = [{ kind: "text", text: "Proposed change" }];
  expect((await s.run()).status).toBe("completed");
  expect(s.plane.staged[0]?.files[0]?.sha256).toBe(s.source.files[0]?.sha256);
  expect(s.gateway.requests[0]?.body).toContain("review reference sentinel");
  expect(s.gateway.requests[0]?.body).toContain("Untrusted review context");
  const modelRequest = JSON.parse(s.gateway.requests[0]!.body);
  expect(JSON.stringify(modelRequest.system)).toContain("summary in the human Accept/Reject request");
  expect(JSON.stringify(modelRequest.system)).toContain("plain language for a non-technical reviewer");
  expect(s.calls.map(c => c.operation)).toEqual(["source", "events", "heartbeat", "events", "candidate"]);
  expect(s.calls.at(-1)?.body).toMatchObject({ summary: "Proposed change", idempotency_key: "candidate-attempt-1" });
  expect(s.source.files[0]?.content_base64).toBe(s.plane.staged[0]?.files[0]?.contentBase64);
});
it("persists clarification snapshot then resumes the transcript in a fresh sandbox", async () => {
  const s = await stack(); s.plane.unchanged=true; s.gateway.steps = [{ kind: "tool", name: "ask_clarification", input: { question: "Which color?" } }, { kind: "text", text: "Blue proposal" }];
  expect((await s.run()).status).toBe("awaiting_clarification");
  const saved = s.calls.find(c => c.operation === "clarify")!.body;
  expect(saved.question).toBe("Which color?"); expect(saved.files).toHaveLength(1);
  expect(s.calls.some(c => c.operation === "candidate")).toBe(false);
  s.claim.attempt_id = "attempt-2"; s.claim.gateway_base_url = `${s.gateway.origin}/internal/room-gateway/attempt-2`;
  s.claim.transcript = saved.transcript; s.claim.answer = "Use blue"; s.source.files = saved.files; s.source.source_content_hash=artifactHash(saved.files.map((f:any)=>({path:f.path,sha256:createHash("sha256").update(Buffer.from(f.content_base64,"base64")).digest("hex")}))); s.plane.unchanged=false;
  expect((await s.run()).status).toBe("completed");
  expect(s.plane.staged[0]?.sprite).not.toBe(s.plane.staged[1]?.sprite);
  expect(s.plane.staged[1]?.files).toEqual(s.plane.staged[0]?.files);
  expect(s.gateway.requests.at(-1)?.body).toContain("Use blue");
  expect(s.gateway.requests.at(-1)?.body).toContain("Which color?");
});
it("destroys after export failure and never submits a candidate", async () => {
  const s = await stack(); s.plane.exportFails = true;
  await expect(s.run()).rejects.toThrow("export failed");
  expect(s.plane.destroys).toHaveLength(1);
  expect(s.calls.some(c => c.operation === "candidate")).toBe(false);
});
it("rejects source baseline, corrupted bytes and foreign gateway before provisioning", async () => {
  const s = await stack(); s.source.version_id = "other-version";
  await expect(s.run()).rejects.toThrow("baseline mismatch");
  s.source.version_id = s.claim.job.version_id; s.source.files[0]!.sha256 = "wrong";
  await expect(s.run()).rejects.toThrow("integrity mismatch");
  s.claim.gateway_base_url = "https://evil.example/internal/room-gateway/attempt-1";
  await expect(s.run()).rejects.toThrow("trusted API attempt");
  expect(s.plane.staged).toEqual([]); expect(s.gateway.requests).toEqual([]);
});

it("exports a full site larger than the old 2 MiB default, preserving unchanged assets", async () => {
 const s=await stack();
 const files=Array.from({length:4},(_,i)=>{const bytes=Buffer.alloc(1800000,i);return {path:`asset-${i}.bin`,content_base64:bytes.toString("base64"),size:bytes.length,sha256:createHash("sha256").update(bytes).digest("hex"),content_type:"application/octet-stream"};});
 s.source.files.push(...files);s.source.source_content_hash=artifactHash(s.source.files.map(f=>({path:f.path,sha256:f.sha256!})));
 s.claim.job.config.budget.max_output_bytes=10*1024*1024;
 s.gateway.steps=[{kind:"text",text:"Proposed change"}];
 expect((await s.run()).status).toBe("completed");
 const candidate=s.calls.find(c=>c.operation==="candidate");expect(candidate).toBeDefined();
 expect(candidate!.body.files).toHaveLength(5);
 expect(candidate!.body.files[1].content_base64).toBe(files[0]!.content_base64);
});

it("does not invent a clarification when a run changes no files", async()=>{
 const s=await stack();s.plane.unchanged=true;s.gateway.steps=[{kind:"text",text:"Which heading should I remove?"}];
 expect((await s.run()).status).toBe("error");
 expect(s.calls.some(c=>c.operation==="candidate"||c.operation==="clarify")).toBe(false);
 expect(s.calls.at(-1)?.body).toEqual({code:"no_changes"});
 expect(s.calls.find(c=>c.operation==="events"&&c.body.message.includes("Which heading"))).toBeDefined();
});
it("executes the pinned Opus 5.5 model through the SDK without falling back",async()=>{
 const s=await stack();s.claim.job.config.model_id="claude-opus-5-5";s.gateway.steps=[{kind:"tool",name:"read",input:{path:"index.html"}},{kind:"text",text:"Opus proposed change"}];
 const result=await s.run();expect(result.status).toBe("completed");expect(result.text).toBe("Opus proposed change");expect(result.remoteOps).toContain("read");expect(s.gateway.requests).toHaveLength(2);
});

it("lets the model recover from a failed sandbox command without submitting an empty proposal",async()=>{
 const s=await stack();const original=s.plane.exec.bind(s.plane);let first=true;
 s.plane.exec=async(...args)=>{if(first){first=false;return {text:"rg: command not found",isError:true};}return original(...args);};
 s.gateway.steps=[{kind:"tool",name:"bash",input:{command:"rg heading index.html"}},{kind:"tool",name:"read",input:{path:"index.html"}},{kind:"text",text:"Recovered proposal"}];
 const result=await s.run();expect(result.status).toBe("completed");expect(result.text).toBe("Recovered proposal");expect(s.gateway.requests).toHaveLength(3);expect(s.gateway.requests[1]!.body).toContain("command not found");expect(s.calls.some(c=>c.operation==="candidate")).toBe(true);
});
it("does not treat an exceptional tool failure as a completed revision",async()=>{
 const s=await stack();s.plane.exec=async()=>{throw new Error("control plane failure");};s.gateway.steps=[{kind:"tool",name:"read",input:{path:"index.html"}}];
 expect((await s.run()).status).toBe("error");expect(s.calls.some(c=>c.operation==="candidate"||c.operation==="clarify")).toBe(false);
});

it("persists a safe provisioning failure code without raw errors",async()=>{
 const s=await stack();s.plane.create=async()=>{throw new RoomRuntimeError("sprite_remote","private credential-bearing error");};
 const result=await s.run();expect(result.status).toBe("error");expect(result.phase).toBe("provision");expect(s.calls.at(-1)?.body).toEqual({code:"sprite_remote"});expect(JSON.stringify(s.calls)).not.toContain("private credential");expect(s.calls.find(c=>c.operation==="events"&&c.body.kind==="diagnostic")?.body.message).toContain("provision: sprite_remote");
});

it("deletes the selected text through real guest edits and exports a proposal without publishing", async () => {
  const s=await stack();
  const directory=mkdtempSync(join(tmpdir(),"dropway-selected-edit-"));
  const helper=SPRITE_GUEST_HELPER.replace("ROOT='/home/sprite/dropway-workspace'",`ROOT=${JSON.stringify(directory)}`);
  const guest=(action:string,input:unknown)=>JSON.parse(execFileSync("python3",["-I","-S","-c",helper],{input:JSON.stringify({action,input}),encoding:"utf8"}));
  const original='<main><h1>Keep heading</h1><p>Remove this line</p><p>Keep footer</p></main>';
  const bytes=Buffer.from(original);
  s.source.files=[{path:"index.html",content_type:"text/html",content_base64:bytes.toString("base64"),size:bytes.length,sha256:createHash("sha256").update(bytes).digest("hex")}];
  s.source.source_content_hash=artifactHash(s.source.files.map(f=>({path:f.path,sha256:f.sha256!})));
  s.source.thread={Page:"/",AnchorKind:"text",Anchor:{quote:"Remove this line",block_id:"static-v1:node-3",start_offset:0,end_offset:16}};
  s.claim.job.config.prompt="@dropway remove this";
  s.plane.stage=async (handle,files)=>{s.plane.staged.push({sprite:handle.spriteName,files});guest("stage",{files});};
  s.plane.exec=async (_handle,op)=>guest(op.kind,op.input);
  s.plane.exportArtifact=async ()=>guest("export",{directory:"."});
  s.gateway.steps=[
    {kind:"tool",name:"read",input:{path:"index.html"}},
    {kind:"tool",name:"edit",input:{path:"index.html",oldText:"<p>Remove this line</p>",newText:""}},
    {kind:"text",text:"Proposed removing the selected line; heading and footer are unchanged. Awaiting approval."},
  ];
  try {
    expect((await s.run()).status).toBe("completed");
    const request=s.gateway.requests[0]!.body;
    expect(request).toContain("Remove this line");
    expect(request).toContain("selectedHTML");
    const candidate=s.calls.find(c=>c.operation==="candidate")!.body;
    expect(Buffer.from(candidate.files[0].content_base64,"base64").toString()).toBe('<main><h1>Keep heading</h1><p>Keep footer</p></main>');
    expect(s.calls.some(c=>["clarify","fail","publish"].includes(c.operation))).toBe(false);
    expect(s.plane.destroys).toHaveLength(1);
  } finally {rmSync(directory,{recursive:true,force:true});}
});
it.each(["anthropic", "openai", "openrouter"] as const)("passes image pixels through the pinned %s model session", async (provider) => {
  const s=await stack();
  s.claim.job.config.provider=provider;
  s.claim.job.config.model_id=provider==="anthropic"?"claude-opus-5-5":provider==="openrouter"?"anthropic/claude-opus-5.5":"gpt-4.1";
  s.plane.exec=async ()=>({text:"Selected image preview",image:{mimeType:"image/jpeg" as const,data:"/9j/fixture"}});
  s.gateway.steps=[{kind:"tool",name:"read",input:{path:"photo.jpg"}},{kind:"text",text:"Proposed image update for review."}];
  expect((await s.run()).status).toBe("completed");
  const second=s.gateway.requests[1]!.body;
  expect(second).toContain("/9j/fixture");
  expect(second).toContain(provider==="anthropic"?'"type":"image"':'"type":"image_url"');
});
