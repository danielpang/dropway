import {createElement} from "react";
import {renderToStaticMarkup} from "react-dom/server";
import {expect,it,vi} from "vitest";
import {AICommentStatus,aiStatusText} from "@/components/project-room/ai-comment-status";
import {MentionExecutionControls} from "@/components/project-room/mention-execution";
import type {HostedJob} from "@/lib/project-room/hosted";
vi.mock("@/app/(app)/rooms/hosted-actions",()=>({hostedSetupAction:vi.fn(),getAIPreferencesAction:vi.fn()}));
const job=(state:string,candidateId:string|null=null)=>({id:"job",state,candidateId} as HostedJob);
it("keeps implementation controls out of the composer",()=>{
 const html=renderToStaticMarkup(createElement(MentionExecutionControls,{body:"@dropway make it blue",allowed:true,onChange:()=>{}}));
 expect(html).not.toContain("<select");expect(html).not.toContain("<input");expect(html).not.toContain("<fieldset");expect(html).not.toContain("Maximum");
});
it("shows real lifecycle outcomes and rotates only running copy",()=>{
 expect(aiStatusText(job("queued"))).toContain("queued");expect(aiStatusText(job("running"),0)).not.toBe(aiStatusText(job("running"),1));
 expect(aiStatusText(job("completed","candidate"))).toContain("Nothing has been published");
 expect(aiStatusText(job("failed"))).toContain("couldn’t finish");expect(aiStatusText(job("clarification"))).toContain("information");
 const html=renderToStaticMarkup(createElement(AICommentStatus,{job:job("running"),stale:true,onReview:()=>{},onActivity:()=>{}}));
 expect(html).toContain("can’t refresh");expect(html).not.toContain("animate-spin");
});
