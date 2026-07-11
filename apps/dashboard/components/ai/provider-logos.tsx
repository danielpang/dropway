import {
  siClaude,
  siDeepseek,
  siGooglegemini,
  siHuggingface,
  siMeta,
  siMistralai,
  siOllama,
  siPerplexity,
  siQwen,
  siX,
  type SimpleIcon,
} from "simple-icons";

/**
 * Provider identity for the model picker: the display name, brand color, and
 * (where we have one) the real company logo.
 *
 * Resilience is the point: logos come from a small map keyed by the OpenRouter
 * provider slug, and ANYTHING not in the map falls back to a colored monogram
 * chip. So a brand-new provider OpenRouter adds tomorrow still renders cleanly
 * (its first letter), never breaks the UI, and giving it a real logo later is a
 * one-line map entry. Logos are official brand marks from simple-icons (bundled
 * inline SVG, no network/CSP concerns); providers simple-icons doesn't carry
 * (e.g. OpenAI, which asked to be removed) simply use the monogram.
 */

// providerOf extracts the OpenRouter provider slug (the "<provider>/model" id
// prefix, e.g. "anthropic" from "anthropic/claude-sonnet-4.5").
export function providerOf(id: string): string {
  const slash = id.indexOf("/");
  return slash === -1 ? id : id.slice(0, slash);
}

const providerLabels: Record<string, string> = {
  anthropic: "Anthropic",
  openai: "OpenAI",
  google: "Google",
  "meta-llama": "Meta",
  mistralai: "Mistral",
  deepseek: "DeepSeek",
  "x-ai": "xAI",
  qwen: "Qwen",
  cohere: "Cohere",
  perplexity: "Perplexity",
  amazon: "Amazon",
  microsoft: "Microsoft",
  nvidia: "NVIDIA",
  "hugging-face": "Hugging Face",
  ollama: "Ollama",
};

export function providerLabel(slug: string): string {
  return (
    providerLabels[slug] ??
    slug
      .split(/[-_]/)
      .map((w) => (w ? w.charAt(0).toUpperCase() + w.slice(1) : w))
      .join(" ")
  );
}

// providerIcons maps a provider slug to its official simple-icons mark. Absent
// entries (OpenAI, xAI, Cohere, and any brand-new provider) fall back to the
// monogram. Anthropic uses the Claude mark (its model brand) for a warmer color
// than the near-black Anthropic wordmark; Google uses the Gemini spark.
const providerIcons: Record<string, SimpleIcon> = {
  anthropic: siClaude,
  google: siGooglegemini,
  "meta-llama": siMeta,
  mistralai: siMistralai,
  deepseek: siDeepseek,
  qwen: siQwen,
  perplexity: siPerplexity,
  "x-ai": siX,
  "hugging-face": siHuggingface,
  ollama: siOllama,
};

// Brand-ish fallback accent per provider for the monogram chip; a stable hashed
// hue for anything else so every provider still gets a distinct color.
const fallbackColors: Record<string, string> = {
  openai: "#10A37F",
  anthropic: "#C15F3C",
  cohere: "#39594D",
  amazon: "#FF9900",
  microsoft: "#0078D4",
  nvidia: "#76B900",
};

function fallbackColor(slug: string): string {
  if (fallbackColors[slug]) return fallbackColors[slug];
  let h = 0;
  for (let i = 0; i < slug.length; i++) h = (h * 31 + slug.charCodeAt(i)) % 360;
  return `hsl(${h} 55% 45%)`;
}

/**
 * ProviderMark renders a provider's brand chip: a rounded square holding the
 * real logo when we have one, otherwise the provider's initial. Both share the
 * same shape and sizing so a mixed list reads consistently.
 */
export function ProviderMark({
  provider,
  size = 20,
}: {
  provider: string;
  size?: number;
}) {
  const icon = providerIcons[provider];
  const bg = icon ? `#${icon.hex}` : fallbackColor(provider);
  const px = `${size}px`;
  return (
    <span
      aria-hidden
      className="flex shrink-0 items-center justify-center rounded-md text-white"
      style={{ width: px, height: px, backgroundColor: bg }}
    >
      {icon ? (
        <svg
          viewBox="0 0 24 24"
          width={size * 0.62}
          height={size * 0.62}
          fill="currentColor"
          role="img"
        >
          <path d={icon.path} />
        </svg>
      ) : (
        <span
          className="font-bold leading-none"
          style={{ fontSize: `${Math.round(size * 0.55)}px` }}
        >
          {providerLabel(provider).charAt(0).toUpperCase()}
        </span>
      )}
    </span>
  );
}
