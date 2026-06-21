"use client";

import { cn } from "@/lib/utils";

/**
 * A burst pipe with water dripping from the crack — the error-page motif.
 * Pure SVG + CSS so it renders server-side (always visible) and animates with
 * CSS (no canvas/physics that could silently no-op). Droplets fall from the
 * crack on a staggered loop into a rippling puddle. Decorative (aria-hidden);
 * the surrounding copy carries the meaning. Under prefers-reduced-motion the
 * loops effectively freeze, leaving a static burst pipe + a couple of drops.
 */

// Teardrop path with its tip at (cx, cyTop) and a rounded body below.
function drop(cx: number, cyTop: number): string {
  return (
    `M${cx},${cyTop} ` +
    `C${cx + 2.4},${cyTop + 3.6} ${cx + 4.6},${cyTop + 5.6} ${cx + 4.6},${cyTop + 8.2} ` +
    `a4.6 4.6 0 1 1 -9.2 0 ` +
    `C${cx - 4.6},${cyTop + 5.6} ${cx - 2.4},${cyTop + 3.6} ${cx},${cyTop} Z`
  );
}

// Drip columns under the crack: x position, start delay, fall duration.
const DRIPS = [
  { x: 128, delay: 0, dur: 1.5 },
  { x: 134, delay: 0.55, dur: 1.7 },
  { x: 140, delay: 1.05, dur: 1.45 },
  { x: 131, delay: 1.5, dur: 1.6 },
];

const CSS = `
.bp-drip { animation: bp-fall var(--dur, 1.5s) ease-in infinite; }
@keyframes bp-fall {
  0%   { transform: translateY(-1px); opacity: 0; }
  12%  { opacity: 1; }
  82%  { opacity: 1; }
  100% { transform: translateY(74px); opacity: 0; }
}
.bp-puddle { transform-box: fill-box; transform-origin: center; animation: bp-puddle 1.6s ease-in-out infinite; }
@keyframes bp-puddle { 0%,100% { transform: scaleX(0.95); } 50% { transform: scaleX(1.05); } }
.bp-ripple { transform-box: fill-box; transform-origin: center; animation: bp-ripple 1.6s ease-out infinite; }
@keyframes bp-ripple {
  0%, 55% { transform: scale(0.4); opacity: 0; }
  68%     { opacity: 0.55; }
  100%    { transform: scale(1.25); opacity: 0; }
}
.bp-bulge { transform-box: fill-box; transform-origin: 134px 113px; animation: bp-bulge 1.5s ease-in-out infinite; }
@keyframes bp-bulge { 0%,100% { transform: scale(0.85); } 50% { transform: scale(1.1); } }
`;

export function BurstPipe({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 260 210"
      className={cn("w-full max-w-[300px]", className)}
      style={{ aspectRatio: "260 / 210" }}
      role="img"
      aria-hidden
    >
      <style>{CSS}</style>
      <defs>
        <linearGradient id="bp-metal" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#e2e8f0" />
          <stop offset="42%" stopColor="#cbd5e1" />
          <stop offset="100%" stopColor="#64748b" />
        </linearGradient>
        <linearGradient id="bp-band" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#cbd5e1" />
          <stop offset="100%" stopColor="#475569" />
        </linearGradient>
        <linearGradient id="bp-water" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#7dd3fc" />
          <stop offset="100%" stopColor="#0ea5e9" />
        </linearGradient>
      </defs>

      {/* ---- Pipe ---- */}
      {/* End flanges */}
      <rect x="6" y="64" width="14" height="56" rx="3" fill="url(#bp-band)" stroke="#334155" strokeWidth="1" />
      <rect x="240" y="64" width="14" height="56" rx="3" fill="url(#bp-band)" stroke="#334155" strokeWidth="1" />
      {/* Barrel */}
      <rect x="14" y="74" width="232" height="38" rx="19" fill="url(#bp-metal)" stroke="#475569" strokeWidth="1.5" />
      {/* Specular highlight */}
      <rect x="26" y="80" width="208" height="6" rx="3" fill="#f8fafc" opacity="0.6" />

      {/* Coupling band over the burst */}
      <rect x="115" y="70" width="36" height="46" rx="5" fill="url(#bp-band)" stroke="#334155" strokeWidth="1.5" />
      <circle cx="122" cy="93" r="2.2" fill="#334155" />
      <circle cx="144" cy="93" r="2.2" fill="#334155" />

      {/* ---- Burst / crack ---- */}
      <path
        d="M133 73 L129 82 L136 89 L128 97 L134 105 L130 112"
        fill="none"
        stroke="#0f172a"
        strokeWidth="2.4"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
      <path
        d="M133 73 L129 82 L136 89 L128 97 L134 105 L130 112"
        fill="none"
        stroke="#1e293b"
        strokeWidth="0.8"
        opacity="0.5"
      />

      {/* ---- Water ---- */}
      {/* A drop swelling at the crack before it releases. */}
      <path d={drop(134, 108)} fill="url(#bp-water)" className="bp-bulge" />

      {/* Falling drips (staggered loop). */}
      {DRIPS.map((d, i) => (
        <path
          key={i}
          d={drop(d.x, 114)}
          fill="url(#bp-water)"
          className="bp-drip"
          style={
            { animationDelay: `${d.delay}s`, "--dur": `${d.dur}s` } as React.CSSProperties
          }
        />
      ))}

      {/* Two always-on drops so water reads even without animation. */}
      <path d={drop(134, 150)} fill="url(#bp-water)" opacity="0.85" />

      {/* ---- Puddle ---- */}
      <ellipse cx="134" cy="198" rx="30" ry="5.5" fill="url(#bp-water)" opacity="0.85" className="bp-puddle" />
      <ellipse
        cx="134"
        cy="198"
        rx="22"
        ry="4"
        fill="none"
        stroke="#7dd3fc"
        strokeWidth="1.2"
        className="bp-ripple"
      />
    </svg>
  );
}
