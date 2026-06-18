#!/usr/bin/env node
// Generate a downloadable PDF of the Terms & Conditions from the single canonical
// source (lib/legal/terms.json), so the PDF can never drift from what the dashboard
// shows. Zero dependencies: emits a minimal, valid PDF using the built-in Courier /
// Courier-Bold fonts (monospaced, so line wrapping is exact without font metrics).
//
//   node scripts/gen-terms-pdf.mjs
//
// Output: public/legal/dropway-terms.pdf. Re-run after editing terms.json.

import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const terms = JSON.parse(
  readFileSync(resolve(here, "../lib/legal/terms.json"), "utf8"),
);

// Page geometry (US Letter, points).
const PAGE_W = 612;
const PAGE_H = 792;
const MARGIN = 56;
const USABLE_W = PAGE_W - MARGIN * 2;
const TOP = PAGE_H - MARGIN;
const BOTTOM = MARGIN;

// Courier is monospaced: each glyph is 0.6em wide.
const charWidth = (size) => size * 0.6;
const charsPerLine = (size) => Math.floor(USABLE_W / charWidth(size));

// Wrap a paragraph to the character budget for its font size (word-aware).
function wrap(text, size) {
  const max = charsPerLine(size);
  const out = [];
  for (const rawLine of String(text).split("\n")) {
    const words = rawLine.split(/\s+/).filter(Boolean);
    let line = "";
    for (const word of words) {
      if (line === "") {
        line = word;
      } else if (line.length + 1 + word.length <= max) {
        line += " " + word;
      } else {
        out.push(line);
        line = word;
      }
      // A single word longer than the line: hard-split it.
      while (line.length > max) {
        out.push(line.slice(0, max));
        line = line.slice(max);
      }
    }
    out.push(line);
  }
  return out;
}

// Build the ordered list of laid-out lines: {text, bold, size}.
const lines = [];
const push = (text, { bold = false, size = 10 } = {}) =>
  wrap(text, size).forEach((t) => lines.push({ text: t, bold, size }));
const blank = (size = 6) => lines.push({ text: "", bold: false, size });

push("Dropway Terms and Conditions", { bold: true, size: 18 });
blank(4);
push(`Version ${terms.version}    Last updated ${terms.updatedLabel}`, {
  size: 9,
});
blank();
push(terms.intro, { size: 10 });
blank();
for (const section of terms.sections) {
  push(section.heading, { bold: true, size: 11 });
  blank(3);
  for (const paragraph of section.body) {
    push(paragraph, { size: 10 });
    blank(4);
  }
  blank(4);
}

// Escape a string for a PDF literal and drop anything outside Latin-1 (the built-in
// fonts' encoding) so the output stays a valid single-byte stream.
const esc = (s) =>
  s
    .replace(/[^\x20-\x7E]/g, "?")
    .replace(/\\/g, "\\\\")
    .replace(/\(/g, "\\(")
    .replace(/\)/g, "\\)");

// Paginate by walking the lines and starting a new page when the next line would
// cross the bottom margin. Each line is absolutely positioned via a text matrix.
const pages = [];
let current = [];
let y = TOP;
for (const line of lines) {
  const lineHeight = Math.round(line.size * 1.4);
  if (y - lineHeight < BOTTOM) {
    pages.push(current);
    current = [];
    y = TOP;
  }
  y -= lineHeight;
  if (line.text !== "") {
    const font = line.bold ? "F2" : "F1";
    current.push(
      `BT /${font} ${line.size} Tf 1 0 0 1 ${MARGIN} ${y} Tm (${esc(line.text)}) Tj ET`,
    );
  }
}
pages.push(current);

// --- Assemble the PDF objects ---------------------------------------------------
const objects = []; // objects[i] is the body of object number i+1
const add = (body) => {
  objects.push(body);
  return objects.length; // 1-based object number
};

const catalogNum = 1;
const pagesNum = 2;
objects.push(""); // reserve #1 (catalog)
objects.push(""); // reserve #2 (pages)

const pageNums = [];
for (const content of pages) {
  const stream = content.join("\n");
  const contentNum = add(
    `<< /Length ${Buffer.byteLength(stream, "latin1")} >>\nstream\n${stream}\nendstream`,
  );
  const pageNum = add(
    `<< /Type /Page /Parent ${pagesNum} 0 R /MediaBox [0 0 ${PAGE_W} ${PAGE_H}] ` +
      `/Resources << /Font << /F1 %F1% 0 R /F2 %F2% 0 R >> >> ` +
      `/Contents ${contentNum} 0 R >>`,
  );
  pageNums.push(pageNum);
}

const f1Num = add(`<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>`);
const f2Num = add(`<< /Type /Font /Subtype /Type1 /BaseFont /Courier-Bold >>`);

// Backfill the font references now that we know their object numbers.
objects[catalogNum - 1] = `<< /Type /Catalog /Pages ${pagesNum} 0 R >>`;
objects[pagesNum - 1] =
  `<< /Type /Pages /Count ${pageNums.length} /Kids [${pageNums.map((n) => `${n} 0 R`).join(" ")}] >>`;
for (let i = 0; i < objects.length; i++) {
  objects[i] = objects[i].replace(/%F1%/g, String(f1Num)).replace(/%F2%/g, String(f2Num));
}

// Serialize with a cross-reference table.
let pdf = "%PDF-1.4\n";
const offsets = [];
for (let i = 0; i < objects.length; i++) {
  offsets.push(Buffer.byteLength(pdf, "latin1"));
  pdf += `${i + 1} 0 obj\n${objects[i]}\nendobj\n`;
}
const xrefStart = Buffer.byteLength(pdf, "latin1");
pdf += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
for (const off of offsets) {
  pdf += `${String(off).padStart(10, "0")} 00000 n \n`;
}
pdf +=
  `trailer\n<< /Size ${objects.length + 1} /Root ${catalogNum} 0 R >>\n` +
  `startxref\n${xrefStart}\n%%EOF`;

const outDir = resolve(here, "../public/legal");
mkdirSync(outDir, { recursive: true });
const outPath = resolve(outDir, "dropway-terms.pdf");
writeFileSync(outPath, Buffer.from(pdf, "latin1"));
console.log(
  `Wrote ${outPath} (${pageNums.length} page(s), ${(Buffer.byteLength(pdf, "latin1") / 1024).toFixed(1)} KB)`,
);
