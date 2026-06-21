"use client";

import { motion, useReducedMotion } from "framer-motion";

/**
 * The Dropway prism, refracting a beam of light — then fracturing into
 * spectrum-colored glass shards that tumble under gravity and settle on the
 * ground. Used on the error pages so a failure still feels considered rather
 * than broken. Purely decorative (aria-hidden); the surrounding copy carries the
 * meaning. Honors prefers-reduced-motion by rendering the shards already fallen
 * and at rest (no animation).
 */

interface Shard {
  /** Polygon points (in the 240×220 viewBox) forming this fragment of the prism. */
  points: string;
  color: string;
  /** Resting offset + spin once it has fallen and settled on the ground. */
  dx: number;
  dy: number;
  rotate: number;
  delay: number;
}

// Ten triangular fragments that tile the prism (apex 120,30 → base 68–172,140),
// each tinted a step along the visible spectrum (rose → violet) like dispersed
// light. dx/dy/rotate are where the fragment comes to rest on the ground band.
const SHARDS: Shard[] = [
  { points: "120,30 94,85 118,98", color: "#f43f5e", dx: -28, dy: 101, rotate: -38, delay: 0.06 },
  { points: "120,30 118,98 146,85", color: "#f97316", dx: 30, dy: 101, rotate: 42, delay: 0.0 },
  { points: "94,85 100,116 118,98", color: "#f59e0b", dx: -14, dy: 74, rotate: -22, delay: 0.13 },
  { points: "94,85 68,140 100,116", color: "#eab308", dx: -36, dy: 60, rotate: -54, delay: 0.09 },
  { points: "68,140 120,140 100,116", color: "#22c55e", dx: -12, dy: 42, rotate: 24, delay: 0.19 },
  { points: "100,116 120,140 118,98", color: "#14b8a6", dx: -4, dy: 56, rotate: -14, delay: 0.15 },
  { points: "118,98 120,140 138,118", color: "#3b82f6", dx: 10, dy: 55, rotate: 18, delay: 0.17 },
  { points: "120,140 172,140 138,118", color: "#6366f1", dx: 18, dy: 42, rotate: -26, delay: 0.21 },
  { points: "118,98 138,118 146,85", color: "#8b5cf6", dx: 20, dy: 74, rotate: 32, delay: 0.11 },
  { points: "146,85 138,118 172,140", color: "#a855f7", dx: 34, dy: 60, rotate: 46, delay: 0.07 },
];

const GROUND_Y = 182;

export function PrismShatter({ size = 240 }: { size?: number }) {
  const reduce = useReducedMotion();

  return (
    <svg
      width={size}
      height={(size * 220) / 240}
      viewBox="0 0 240 220"
      fill="none"
      aria-hidden
      className="overflow-visible"
    >
      <defs>
        {/* The dispersed-light fan behind the prism (fades as it shatters). */}
        <linearGradient id="prism-beam" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stopColor="#f43f5e" />
          <stop offset="25%" stopColor="#f59e0b" />
          <stop offset="50%" stopColor="#22c55e" />
          <stop offset="75%" stopColor="#3b82f6" />
          <stop offset="100%" stopColor="#a855f7" />
        </linearGradient>
        <radialGradient id="prism-glow" cx="50%" cy="40%" r="60%">
          <stop offset="0%" stopColor="#a855f7" stopOpacity="0.35" />
          <stop offset="100%" stopColor="#a855f7" stopOpacity="0" />
        </radialGradient>
      </defs>

      {/* Soft refraction glow + light beam, dispersing away as the prism breaks. */}
      <motion.g
        initial={reduce ? { opacity: 0 } : { opacity: 1 }}
        animate={{ opacity: 0 }}
        transition={{ duration: reduce ? 0 : 0.9, ease: "easeOut" }}
      >
        <ellipse cx="120" cy="86" rx="96" ry="70" fill="url(#prism-glow)" />
        <path
          d="M120 86 L240 50 L240 122 Z"
          fill="url(#prism-beam)"
          opacity="0.5"
        />
      </motion.g>

      {/* The ground: a soft shadow band the shards come to rest on. */}
      <motion.ellipse
        cx="120"
        cy={GROUND_Y + 8}
        rx="92"
        ry="11"
        className="fill-foreground/10"
        initial={reduce ? { opacity: 1 } : { opacity: 0 }}
        animate={{ opacity: 1 }}
        transition={{ duration: 0.5, delay: reduce ? 0 : 0.7 }}
      />

      {/* The fracturing shards. */}
      {SHARDS.map((s, i) => {
        const rest = { x: s.dx, y: s.dy, rotate: s.rotate, opacity: 0.92 };
        return (
          <motion.polygon
            key={i}
            points={s.points}
            fill={s.color}
            stroke="rgba(255,255,255,0.55)"
            strokeWidth={0.75}
            strokeLinejoin="round"
            style={{ transformBox: "fill-box", transformOrigin: "center" }}
            initial={reduce ? rest : { x: 0, y: 0, rotate: 0, opacity: 1 }}
            animate={
              reduce
                ? rest
                : {
                    x: s.dx,
                    // Fall with gravity, overshoot slightly, then settle (bounce).
                    y: [0, s.dy * 1.05, s.dy],
                    rotate: s.rotate,
                    opacity: [1, 1, 0.92],
                  }
            }
            transition={
              reduce
                ? { duration: 0 }
                : {
                    delay: s.delay,
                    duration: 1.15,
                    times: [0, 0.82, 1],
                    ease: ["easeIn", "easeOut"],
                  }
            }
          />
        );
      })}
    </svg>
  );
}
