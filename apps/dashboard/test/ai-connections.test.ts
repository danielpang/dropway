import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { AIConnections } from "@/components/project-room/ai-connections";
import { EMPTY_AI_PREFERENCES, preferredModel, type ProviderCredential } from "@/lib/project-room/hosted";
vi.mock("@/app/(app)/rooms/hosted-actions", () => ({hostedSetupAction: vi.fn(), revokeCredentialAction: vi.fn(),saveCredentialAction: vi.fn(), validateCredentialAction: vi.fn(),getAIPreferencesAction:vi.fn(),saveAIPreferencesAction:vi.fn()}));
describe("AI setup", () => {
  it("offers local tools without a key, or explicit @dropway requests", () => {
    const html = renderToStaticMarkup(createElement(AIConnections,{onboarding:true,roomId:"project"}));
    expect(html).toContain("Use your own coding tool");expect(html).toContain("Let @dropway propose changes");expect(html).toContain("Skip for now");expect(html).toContain("dropway projects comments project --type text");expect(html).not.toContain("<details");expect(html).not.toContain("subscription");
  });
  it("uses a matching project model over personal settings, never another provider's model",()=>{
    const key:ProviderCredential={id:"key",provider:"openai",scope:"personal",canManage:true,ownerUserId:"user",label:"",lastFour:"1234",validatedAt:null,revokedAt:null,generation:1};
    const personal={...EMPTY_AI_PREFERENCES,provider:"openai",model:"gpt-4.1-mini"};
    expect(preferredModel(key,personal)).toBe("gpt-4.1-mini");
    expect(preferredModel(key,personal,{...EMPTY_AI_PREFERENCES,provider:"openai",model:"gpt-4.1"})).toBe("gpt-4.1");
    expect(preferredModel(key,personal,{...EMPTY_AI_PREFERENCES,provider:"anthropic",model:"claude-sonnet-4-5"})).toBe("gpt-4.1-mini");
  });
});

describe("provider identities",()=>{
 it("recognizes only unambiguous prefixes and maps current OpenRouter IDs",async()=>{
  const {keyProvider,providerModelId,HOSTED_MODELS}=await import("@/lib/project-room/hosted");
  expect(keyProvider("sk-ant-test")).toBe("anthropic");expect(keyProvider("sk-or-v1-test")).toBe("openrouter");expect(keyProvider("sk-proj-test")).toBe("openai");expect(keyProvider("sk-legacy")).toBe(null);
  expect(HOSTED_MODELS).toContain("claude-opus-5-5");expect(HOSTED_MODELS).toContain("claude-fable-5-2");
  expect(providerModelId("anthropic","claude-opus-5-5")).toBe("claude-opus-5-5");expect(providerModelId("openrouter","claude-fable-5-2")).toBe("anthropic/claude-fable-5.2");
 });
});

it("upgrades a retired saved Opus selection and preserves current project overrides",()=>{
 const key:ProviderCredential={id:"key",provider:"anthropic",scope:"personal",canManage:true,ownerUserId:"user",label:"",lastFour:"1234",validatedAt:null,revokedAt:null,generation:1};
 const old={...EMPTY_AI_PREFERENCES,provider:"anthropic",model:"claude-opus-4-1"};
 expect(preferredModel(key,old)).toBe("claude-opus-5-5");
 expect(preferredModel(key,{...old,model:"claude-sonnet-5-5"},old)).toBe("claude-opus-5-5");
 expect(preferredModel(key,old,{...old,model:"claude-fable-5-2"})).toBe("claude-fable-5-2");
});
