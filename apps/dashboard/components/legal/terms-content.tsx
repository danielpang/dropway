import { TERMS, TERMS_UPDATED_LABEL, TERMS_VERSION } from "@/lib/legal/terms";

/**
 * Renders the Terms & Conditions prose from the canonical data (lib/legal/terms.json).
 * Pure presentation, reused by the signup consent modal. The "Last updated" line is
 * always shown so a reader knows which version they are agreeing to.
 */
export function TermsContent() {
  return (
    <div className="space-y-5 text-sm leading-relaxed text-muted-foreground">
      <p className="text-xs text-muted-foreground">
        Version {TERMS_VERSION} &middot; Last updated {TERMS_UPDATED_LABEL}
      </p>

      <p>{TERMS.intro}</p>

      {TERMS.sections.map((section) => (
        <section key={section.heading} className="space-y-2">
          <h3 className="text-sm font-semibold text-foreground">
            {section.heading}
          </h3>
          {section.body.map((paragraph, i) => (
            <p key={i}>{paragraph}</p>
          ))}
        </section>
      ))}
    </div>
  );
}
