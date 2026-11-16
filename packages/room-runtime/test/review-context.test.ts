// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { expect, it } from "vitest";
import { reviewContext } from "../src/review-context.js";
import type { HostedSource } from "../src/hosted-client.js";
function source(html: string, anchor: unknown, page = "/"): HostedSource {
 return { files: [{path: page === "/" ? "index.html" : "about/index.html",content_type:"text/html",content_base64:Buffer.from(html).toString("base64")}], version_id:"v",content_hash:"h",source_content_hash:"h",thread:{Page:page,Anchor:anchor,AnchorKind:"component"} };
}
it("maps generated review IDs to source while ignoring scripts and head nodes", () => {
 const context=JSON.parse(reviewContext(source('<html><head><title>Ignore</title></head><body><script>ignore</script><main><h1>Hello</h1><img src="photo.jpg"></main></body></html>',{stable_id:"static-v1:node-3"})));
 expect(context.sourceExcerpt.targetResolved).toBe(true);
 expect(context.sourceExcerpt.selectedHTML).toBe('<img src="photo.jpg">');
 expect(context.sourceExcerpt.surroundingHTML).toContain('<h1>Hello</h1>');
});
it("uses text block identity and the page, not a quote from a different page", () => {
 const data=source('<main><p data-component-id="intro">Remove <b>this</b> text</p></main>',{block_id:"static-v1:intro",quote:"this"},"/about/");
 data.files.unshift({path:"index.html",content_type:"text/html",content_base64:Buffer.from('<p>this unrelated text</p>').toString('base64')});
 const context=JSON.parse(reviewContext(data));
 expect(context.sourceExcerpt.path).toBe("about/index.html");
 expect(context.sourceExcerpt.selectedHTML).toContain('Remove <b>this</b> text');
 expect(context.anchor.quote).toBe("this");
});
it("preserves the target and newest discussion without truncating JSON", () => {
 const data=source('<p>Target</p>',{quote:'Target'});
 (data.thread as any).Replies=Array.from({length:60},(_,i)=>({Body:`reply-${i} `+'x'.repeat(4000)}));
 const context=JSON.parse(reviewContext(data));
 expect(context.anchor.quote).toBe('Target');
 expect(context.replies[0].body).toContain('reply-0 ');
 expect(context.replies.at(-1).body).toContain('reply-59 ');
 expect(context.historyOmitted).toBe(true);
 expect(context.replies).toHaveLength(9);
});
