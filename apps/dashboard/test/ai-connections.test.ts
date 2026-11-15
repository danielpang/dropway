import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { AIConnections } from "@/components/project-room/ai-connections";
vi.mock("@/app/(app)/rooms/hosted-actions", () => ({
  hostedSetupAction: vi.fn(), revokeCredentialAction: vi.fn(),
  saveCredentialAction: vi.fn(), validateCredentialAction: vi.fn(),
}));
describe("AI connection setup", () => {
  it("exposes provider and key fields immediately instead of a collapsed disclosure", () => {
    const html = renderToStaticMarkup(createElement(AIConnections));
    expect(html).toContain('aria-label="Provider API key"');
    expect(html).toContain('type="password"');
    expect(html).toContain("OpenAI");
    expect(html).toContain("Anthropic");
    expect(html).toContain("OpenRouter");
    expect(html).toContain("Personal · only me");
    expect(html).toContain("Save key");
    expect(html).not.toContain("<details");
  });
  it("does not collect a key before checking secure storage and permissions", () => {
    const html = renderToStaticMarkup(createElement(AIConnections));
    expect(html).toContain('<fieldset disabled=""');
    expect(html).not.toContain('value="team"');
  });
});
