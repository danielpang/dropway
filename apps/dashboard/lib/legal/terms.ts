import termsData from "./terms.json";

/**
 * The Dropway Terms & Conditions, sourced from the single canonical file
 * (terms.json) so the consent modal, the rendered content, and the generated PDF
 * (scripts/gen-terms-pdf.mjs) never drift apart. Update terms.json (and bump
 * `version` + `updated`), then regenerate the PDF.
 */
export interface TermsSection {
  heading: string;
  body: string[];
}

export interface Terms {
  version: string;
  /** ISO date (YYYY-MM-DD) the Terms were last changed. */
  updated: string;
  /** Human-readable form of `updated`, e.g. "June 18, 2026". */
  updatedLabel: string;
  intro: string;
  sections: TermsSection[];
}

export const TERMS: Terms = termsData;

export const TERMS_VERSION = TERMS.version;
export const TERMS_UPDATED = TERMS.updated;
export const TERMS_UPDATED_LABEL = TERMS.updatedLabel;

/** Public path of the generated, downloadable PDF copy of the Terms. */
export const TERMS_PDF_PATH = "/legal/dropway-terms.pdf";
