"use client";

import { cn } from "@/lib/utils";

/**
 * A pane of shattered glass, tinted with the Dropway prism spectrum. Cracks race
 * out from the impact point, the shards snap into place radiating outward, an
 * impact flash blooms, and a light glint sweeps across the glass. Pure SVG + CSS
 * — it renders server-side (so it's always visible) and animates deterministically
 * with no canvas or physics engine that could silently no-op.
 *
 * The geometry is generated once with a seeded PRNG, so the server and client
 * produce identical markup (no hydration mismatch). Decorative (aria-hidden);
 * the surrounding copy carries the meaning. Under prefers-reduced-motion the
 * animations collapse to their resting state (the static shattered pane).
 */

const W = 480;
const H = 360;
const CX = 240;
const CY = 166;

// Light dispersed through the prism — each shard a step along the spectrum.
const COLORS = [
  "#f43f5e", "#fb7185", "#f97316", "#f59e0b", "#eab308",
  "#84cc16", "#22c55e", "#14b8a6", "#3b82f6", "#6366f1", "#a855f7",
];

/** Deterministic PRNG (mulberry32) so the shatter is identical on server + client. */
function mulberry32(seed: number): () => number {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const rand = mulberry32(0x9e3779b9);

const SPOKES = 14;
const RING_R = [0, 30, 66, 112, 168, 240, 330];
const RINGS = RING_R.length - 1;

// Jittered spoke angles + a point grid (ring × spoke) the shards are cut from.
const ANG: number[] = [];
for (let s = 0; s < SPOKES; s++) {
  ANG.push((s / SPOKES) * Math.PI * 2 + (rand() - 0.5) * 0.2);
}
const P: { x: number; y: number }[][] = [];
for (let r = 0; r <= RINGS; r++) {
  const row: { x: number; y: number }[] = [];
  for (let s = 0; s < SPOKES; s++) {
    if (r === 0) {
      row.push({ x: CX, y: CY });
    } else {
      const rr = RING_R[r]! * (1 + (rand() - 0.5) * 0.16);
      row.push({ x: CX + Math.cos(ANG[s]!) * rr, y: CY + Math.sin(ANG[s]!) * rr });
    }
  }
  P.push(row);
}

const fmt = (p: { x: number; y: number }) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`;

interface Shard {
  points: string;
  ring: number;
  color: string;
  alpha: number;
}

const SHARDS: Shard[] = [];
for (let r = 1; r <= RINGS; r++) {
  for (let s = 0; s < SPOKES; s++) {
    const s2 = (s + 1) % SPOKES;
    const pts =
      r === 1
        ? [P[0]![s]!, P[1]![s]!, P[1]![s2]!]
        : [P[r - 1]![s]!, P[r]![s]!, P[r]![s2]!, P[r - 1]![s2]!];
    SHARDS.push({
      points: pts.map(fmt).join(" "),
      ring: r,
      color: COLORS[s % COLORS.length]!,
      alpha: 0.13 + rand() * 0.16,
    });
  }
}

// Crack network: radial fractures from the impact + the concentric ring cracks.
const RADIAL_CRACKS: string[] = [];
for (let s = 0; s < SPOKES; s++) {
  let d = `M${fmt(P[0]![s]!)}`;
  for (let r = 1; r <= RINGS; r++) d += ` L${fmt(P[r]![s]!)}`;
  RADIAL_CRACKS.push(d);
}
const RING_CRACKS: { d: string; ring: number }[] = [];
for (let r = 1; r <= RINGS; r++) {
  let d = `M${fmt(P[r]![0]!)}`;
  for (let s = 1; s < SPOKES; s++) d += ` L${fmt(P[r]![s]!)}`;
  RING_CRACKS.push({ d: d + " Z", ring: r });
}

const CSS = `
.pg-shard {
  transform-box: fill-box;
  transform-origin: center;
  animation: pg-shard 0.55s cubic-bezier(0.2, 0.8, 0.3, 1) both;
}
@keyframes pg-shard {
  from { opacity: 0; transform: scale(0.45); }
  to   { opacity: var(--a); transform: scale(1); }
}
.pg-crack {
  fill: none;
  stroke: rgba(255, 255, 255, 0.55);
  stroke-width: 1;
  stroke-dasharray: 1;
  animation: pg-crack 0.45s ease-out both;
}
@keyframes pg-crack { from { stroke-dashoffset: 1; } to { stroke-dashoffset: 0; } }
.pg-flash {
  fill: #fff;
  transform-box: fill-box;
  transform-origin: center;
  animation: pg-flash 0.6s ease-out both;
}
@keyframes pg-flash {
  0%   { opacity: 0.85; transform: scale(0.2); }
  100% { opacity: 0; transform: scale(7); }
}
.pg-glint {
  opacity: 0.28;
  animation: pg-glint 5s ease-in-out 1.1s infinite;
}
@keyframes pg-glint {
  0%, 100% { transform: translateX(-42%); }
  50%      { transform: translateX(42%); }
}
`;

export function PrismShatter({ className }: { className?: string }) {
  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      className={cn("w-full", className)}
      style={{ aspectRatio: `${W} / ${H}` }}
      role="img"
      aria-hidden
    >
      <style>{CSS}</style>
      <defs>
        <clipPath id="pg-clip">
          <rect width={W} height={H} rx="10" />
        </clipPath>
        <linearGradient id="pg-glint-grad" x1="0" y1="0" x2="1" y2="0">
          <stop offset="0%" stopColor="#fff" stopOpacity="0" />
          <stop offset="46%" stopColor="#fff" stopOpacity="0" />
          <stop offset="50%" stopColor="#fff" stopOpacity="0.9" />
          <stop offset="54%" stopColor="#fff" stopOpacity="0" />
          <stop offset="100%" stopColor="#fff" stopOpacity="0" />
        </linearGradient>
      </defs>

      <g clipPath="url(#pg-clip)">
        {/* Faint glass body behind the shards. */}
        <rect width={W} height={H} className="fill-foreground/[0.03]" />

        {SHARDS.map((sh, i) => (
          <polygon
            key={i}
            points={sh.points}
            fill={sh.color}
            stroke="rgba(255,255,255,0.18)"
            strokeWidth={0.5}
            className="pg-shard"
            style={
              {
                "--a": sh.alpha,
                animationDelay: `${0.06 + sh.ring * 0.05}s`,
              } as React.CSSProperties
            }
          />
        ))}

        {RADIAL_CRACKS.map((d, i) => (
          <path
            key={`rc-${i}`}
            d={d}
            pathLength={1}
            className="pg-crack"
            style={{ animationDelay: `${i * 0.012}s` }}
          />
        ))}
        {RING_CRACKS.map((c, i) => (
          <path
            key={`ring-${i}`}
            d={c.d}
            pathLength={1}
            className="pg-crack"
            style={{ animationDelay: `${0.1 + c.ring * 0.05}s` }}
          />
        ))}

        {/* Impact bloom + travelling glint. */}
        <circle cx={CX} cy={CY} r={10} className="pg-flash" />
        <rect
          x={-W}
          y={0}
          width={W * 2}
          height={H}
          fill="url(#pg-glint-grad)"
          className="pg-glint"
        />
      </g>
    </svg>
  );
}
