"use client";

import * as React from "react";
import { Button } from "@/components/ui/button";
import { captureStaticPoint, captureStaticText } from "@/lib/collab/static-selection";
import type { StaticSelection, StaticSnapshot } from "@/lib/collab/static-snapshot";

export function StaticReview({ snapshot, onSelect }: { snapshot: StaticSnapshot; onSelect: (selection: StaticSelection | null) => void }) {
  const host = React.useRef<HTMLDivElement>(null);
  const [mode, setMode] = React.useState<"page" | "point" | "text" | "component">("page");
  const modeRef = React.useRef(mode);
  modeRef.current = mode;
  const onSelectRef = React.useRef(onSelect);
  onSelectRef.current = onSelect;
  const [blocked, setBlocked] = React.useState(false);
  const [selected, setSelected] = React.useState("");

  React.useEffect(() => {
    const container = host.current;
    if (!container) return;
    setBlocked(false);
    setSelected("");
    onSelectRef.current(null);
    const frame = document.createElement("iframe");
    frame.title = "Immutable static review";
    frame.className = "h-96 w-full rounded-md border border-border bg-white";
    // This is reconstructed inert markup, not the arbitrary-JS site preview.
    frame.setAttribute("sandbox", "allow-same-origin");
    frame.referrerPolicy = "no-referrer";
    let loaded = false;
    let cleanup: () => void = () => undefined;
    function retire() { cleanup(); frame.remove(); setBlocked(true); onSelectRef.current(null); }
    const onLoad = () => {
      if (loaded) { retire(); return; }
      loaded = true;
      const doc = frame.contentDocument;
      if (!doc || doc.URL !== "about:srcdoc" || doc.documentElement.dataset.reviewSnapshot !== snapshot.snapshotId) { retire(); return; }
      const adopt = (selection: StaticSelection | null) => {
        if (!selection) return;
        setSelected(selection.label);
        onSelectRef.current(selection);
      };
      const onClick = (event: MouseEvent) => {
        event.preventDefault();
        const target = (event.target as Node | null)?.nodeType === 1 ? event.target as Element : null;
        if (modeRef.current === "point") adopt(captureStaticPoint(doc, event));
        if (modeRef.current === "component") {
          const component = target?.closest("[data-review-component]");
          const id = component?.getAttribute("data-review-component");
          if (!id) return;
          doc.querySelectorAll(".dw-selected").forEach((item) => item.classList.remove("dw-selected"));
          component!.classList.add("dw-selected");
          adopt({ kind: "component", label: `Static component ${id}`, anchor: { stable_id: id } });
        }
      };
      const text = () => { if (modeRef.current === "text") adopt(captureStaticText(doc)); };
      const key = (event: KeyboardEvent) => {
        if (event.key === "Escape") { setMode("page"); setSelected(""); onSelectRef.current(null); }
        if (event.key === "Enter" && modeRef.current === "component" && (event.target as Node | null)?.nodeType === 1) {
          (event.target as HTMLElement).click();
        }
        if (modeRef.current === "text") text();
      };
      doc.querySelectorAll("[data-review-component]").forEach((item) => item.setAttribute("tabindex", "0"));
      doc.addEventListener("click", onClick);
      doc.addEventListener("mouseup", text);
      doc.addEventListener("keyup", key);
      cleanup = () => { doc.removeEventListener("click", onClick); doc.removeEventListener("mouseup", text); doc.removeEventListener("keyup", key); };
    };
    frame.addEventListener("load", onLoad);
    frame.srcdoc = snapshot.html;
    container.replaceChildren(frame);
    return () => { cleanup(); frame.removeEventListener("load", onLoad); frame.remove(); };
  }, [snapshot]);

  return <section className="space-y-3" aria-label="Static review selection" onKeyDown={(event) => {
    if (event.key === "Escape") { setMode("page"); setSelected(""); onSelect(null); }
  }}>
    <p className="text-sm text-muted-foreground">Static review of version {snapshot.versionId}. Scripts, original styling and external resources are removed. Points describe this simplified rendition; component IDs are visual targets, not source mappings. {snapshot.omittedAssets ? `${snapshot.omittedAssets} images omitted.` : ""}</p>
    <div className="flex flex-wrap gap-2">
      {(["page", "point", "text", "component"] as const).map((value) => <Button key={value} type="button" size="sm" variant={mode === value ? "default" : "outline"} disabled={blocked}
        aria-pressed={mode === value} onClick={() => { setMode(value); setSelected(""); onSelect(null); }}>{value === "page" ? "Page comment" : value === "point" ? "Place point" : value === "text" ? "Highlight text" : "Select component"}</Button>)}
    </div>
    <p role="status" className="text-sm">{blocked ? "The static frame changed or could not be verified. Reload the snapshot before selecting." : selected || (mode === "text" ? "Select text in the static document." : mode === "component" ? "Click a component, or focus it and press Enter." : mode === "point" ? "Click a point in the static document." : "The comment applies to this whole page.")}</p>
    <div ref={host} />
  </section>;
}
