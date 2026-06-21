"use client";

import { cn } from "@/lib/utils";

/**
 * A ruptured steel water pipe leaking from the crack — the error-page motif.
 * Pure SVG + CSS so it renders server-side (always visible) and animates with
 * CSS only (no canvas/physics that could silently no-op). Water threads out of
 * the gash and droplets fall — strictly downward, gravity-eased — into a
 * rippling puddle. Decorative (aria-hidden); the copy carries the meaning.
 *
 * Every drip uses `both` fill-mode so it's hidden before its staggered turn
 * (no droplet ever sits at the source or appears to jump upward on loop).
 * Under prefers-reduced-motion the loops freeze, leaving a static leaking pipe.
 */

// Teardrop with its tip at (cx, cyTop) and a rounded bulb below.
function drop(cx: number, cyTop: number, scale = 1): string {
  const w = 4.4 * scale;
  const h = 8 * scale;
  return (
    `M${cx},${cyTop} ` +
    `C${cx + w * 0.55},${cyTop + h * 0.45} ${cx + w},${cyTop + h * 0.7} ${cx + w},${cyTop + h} ` +
    `a${w} ${w} 0 1 1 ${-2 * w} 0 ` +
    `C${cx - w},${cyTop + h * 0.7} ${cx - w * 0.55},${cyTop + h * 0.45} ${cx},${cyTop} Z`
  );
}

// Falling drips: x, tip start-y, start delay, fall duration, scale.
const DRIPS = [
  { x: 138, y: 122, delay: 0.0, dur: 1.5, s: 1 },
  { x: 142, y: 124, delay: 0.5, dur: 1.7, s: 0.8 },
  { x: 135, y: 123, delay: 0.95, dur: 1.4, s: 0.9 },
  { x: 140, y: 122, delay: 1.35, dur: 1.6, s: 0.7 },
];

const CSS = `
.bp-drip {
  animation: bp-fall var(--dur, 1.6s) cubic-bezier(0.5, 0, 0.85, 0.45) infinite both;
  animation-delay: var(--delay, 0s);
}
@keyframes bp-fall {
  0%   { transform: translateY(0);    opacity: 0; }
  14%  { opacity: 0.92; }
  86%  { opacity: 0.92; }
  100% { transform: translateY(74px); opacity: 0; }
}
.bp-trickle { transform-origin: center top; animation: bp-trickle 1.5s ease-in-out infinite; }
@keyframes bp-trickle { 0%, 100% { opacity: 0.45; transform: scaleY(0.9); } 50% { opacity: 0.8; transform: scaleY(1.05); } }
.bp-puddle { transform-box: fill-box; transform-origin: center; animation: bp-puddle 1.6s ease-in-out infinite; }
@keyframes bp-puddle { 0%, 100% { transform: scaleX(0.96); } 50% { transform: scaleX(1.04); } }
.bp-ripple { transform-box: fill-box; transform-origin: center; animation: bp-ripple 1.6s ease-out infinite; }
@keyframes bp-ripple {
  0%, 50% { transform: scale(0.45); opacity: 0; }
  64%     { opacity: 0.5; }
  100%    { transform: scale(1.3); opacity: 0; }
}
`;

export function BurstPipe({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 280 220"
      className={cn("w-full max-w-[300px]", className)}
      style={{ aspectRatio: "280 / 220" }}
      role="img"
      aria-hidden
    >
      <style>{CSS}</style>
      <defs>
        {/* Cylindrical steel: dark rim → mid → specular band → shadow underside. */}
        <linearGradient id="bp-steel" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#cbd3db" />
          <stop offset="15%" stopColor="#aab3be" />
          <stop offset="44%" stopColor="#eef2f6" />
          <stop offset="56%" stopColor="#b7c0cb" />
          <stop offset="82%" stopColor="#69727e" />
          <stop offset="100%" stopColor="#454d57" />
        </linearGradient>
        <linearGradient id="bp-collar" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#b4bcc6" />
          <stop offset="50%" stopColor="#828c98" />
          <stop offset="100%" stopColor="#444c56" />
        </linearGradient>
        <linearGradient id="bp-water" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#e0f2fe" />
          <stop offset="40%" stopColor="#7dd3fc" />
          <stop offset="100%" stopColor="#0ea5e9" />
        </linearGradient>
        <radialGradient id="bp-puddle-grad" cx="50%" cy="40%" r="60%">
          <stop offset="0%" stopColor="#bae6fd" />
          <stop offset="100%" stopColor="#0ea5e9" />
        </radialGradient>
        <filter id="bp-soft" x="-40%" y="-40%" width="180%" height="180%">
          <feGaussianBlur stdDeviation="2.4" />
        </filter>
      </defs>

      {/* ---- End flanges ---- */}
      {[16, 256].map((x) => (
        <g key={x}>
          <rect x={x} y={60} width={16} height={66} rx={4} fill="url(#bp-collar)" stroke="#3a424c" strokeWidth={0.75} />
          <circle cx={x + 8} cy={74} r={2.4} fill="#3a424c" />
          <circle cx={x + 8} cy={74} r={0.9} fill="#aeb7c1" />
          <circle cx={x + 8} cy={112} r={2.4} fill="#3a424c" />
          <circle cx={x + 8} cy={112} r={0.9} fill="#aeb7c1" />
        </g>
      ))}

      {/* ---- Pipe barrel ---- */}
      <rect x={20} y={72} width={240} height={46} rx={23} fill="url(#bp-steel)" stroke="#3f4751" strokeWidth={0.75} strokeOpacity={0.6} />
      {/* Specular streak + underside ambient shadow */}
      <rect x={40} y={78} width={200} height={3.5} rx={1.75} fill="#ffffff" opacity={0.55} />
      <rect x={30} y={110} width={220} height={4} rx={2} fill="#1f2730" opacity={0.22} />

      {/* ---- Coupling collar over the rupture ---- */}
      <rect x={122} y={68} width={40} height={54} rx={6} fill="url(#bp-collar)" stroke="#39414b" strokeWidth={1} />
      <rect x={126} y={71} width={32} height={2.5} rx={1.25} fill="#ffffff" opacity={0.4} />
      <circle cx={129} cy={95} r={2.2} fill="#39414b" />
      <circle cx={155} cy={95} r={2.2} fill="#39414b" />

      {/* ---- Rupture: torn gash with metal-bright edges ---- */}
      <path
        d="M140,74 L134,85 L141,92 L132,101 L140,109 L136,120 L145,120 L141,109 L149,101 L141,92 L147,85 L143,74 Z"
        fill="#10161d"
      />
      <path
        d="M140,74 L134,85 L141,92 L132,101 L140,109 L136,120"
        fill="none"
        stroke="#c2cad3"
        strokeWidth={0.7}
        strokeOpacity={0.7}
        strokeLinejoin="round"
      />

      {/* ---- Water ---- */}
      {/* A glassy thread of water running out of the gash. */}
      <path
        className="bp-trickle"
        d="M139,116 C137.5,128 140.5,136 139,146 C137.5,136 134.5,128 139,116 Z"
        fill="url(#bp-water)"
        opacity={0.7}
      />

      {/* Falling droplets (staggered; strictly downward). */}
      {DRIPS.map((d, i) => (
        <g
          key={i}
          className="bp-drip"
          style={{ "--delay": `${d.delay}s`, "--dur": `${d.dur}s` } as React.CSSProperties}
        >
          <path d={drop(d.x, d.y, d.s)} fill="url(#bp-water)" />
          <ellipse cx={d.x - 1.2 * d.s} cy={d.y + 6 * d.s} rx={1 * d.s} ry={1.6 * d.s} fill="#ffffff" opacity={0.55} />
        </g>
      ))}

      {/* A single always-on droplet so water reads even without animation. */}
      <path d={drop(139, 158, 0.85)} fill="url(#bp-water)" opacity={0.85} />

      {/* ---- Puddle ---- */}
      <ellipse cx={139} cy={203} rx={42} ry={8} fill="#0b1220" opacity={0.16} filter="url(#bp-soft)" />
      <ellipse className="bp-puddle" cx={139} cy={200} rx={34} ry={6.5} fill="url(#bp-puddle-grad)" opacity={0.82} />
      <ellipse cx={130} cy={198} rx={11} ry={2} fill="#ffffff" opacity={0.4} />
      <ellipse className="bp-ripple" cx={139} cy={200} rx={20} ry={4} fill="none" stroke="#bae6fd" strokeWidth={1.1} />
    </svg>
  );
}
