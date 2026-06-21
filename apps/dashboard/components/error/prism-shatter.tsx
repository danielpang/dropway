"use client";

import { useEffect, useRef } from "react";
import { useReducedMotion } from "framer-motion";
import type { Body as MatterBody } from "matter-js";

import { cn } from "@/lib/utils";

/**
 * The Dropway prism, refracting a beam of light — then SHATTERING into
 * spectrum-colored glass shards driven by a real rigid-body physics simulation
 * (matter-js): the shards burst, tumble, collide, and pile up naturally on the
 * ground. The pile is interactive — move the cursor to stir the shards, click to
 * re-shatter. Decorative (aria-hidden); the surrounding copy carries the meaning.
 *
 * Honors prefers-reduced-motion by settling the simulation off-screen and
 * drawing the resulting static pile once (no animation, no interaction).
 *
 * matter-js is imported only by the error/not-found routes, so it never weighs
 * down the rest of the app's bundle.
 */

// Fixed simulation world (the canvas scales responsively to its container).
const W = 440;
const H = 320;
const GROUND_Y = H - 26;

// Light dispersed through a prism: each fragment a step along the spectrum.
const COLORS = [
  "#f43f5e", "#fb7185", "#f97316", "#f59e0b", "#eab308",
  "#22c55e", "#14b8a6", "#3b82f6", "#6366f1", "#a855f7",
];

// Ten triangles tiling the prism (apex 220,46 → base 150–290,170). Same
// topology fans out from the centroid G so the pieces read as a fractured prism.
type Pt = [number, number];
const A: Pt = [220, 46], B: Pt = [150, 170], C: Pt = [290, 170];
const mAB: Pt = [176, 108], mAC: Pt = [264, 108], mBC: Pt = [220, 170];
const G: Pt = [218, 120], q1: Pt = [192, 142], q2: Pt = [248, 144];
const SHARDS: Pt[][] = [
  [A, mAB, G], [A, G, mAC], [mAB, q1, G], [mAB, B, q1], [B, mBC, q1],
  [q1, mBC, G], [G, mBC, q2], [mBC, C, q2], [G, q2, mAC], [mAC, q2, C],
];

const PRISM_CENTER = { x: 220, y: 120 };

function centroid(pts: Pt[]): { x: number; y: number } {
  const n = pts.length;
  return {
    x: pts.reduce((s, p) => s + p[0], 0) / n,
    y: pts.reduce((s, p) => s + p[1], 0) / n,
  };
}

export function PrismShatter({ className }: { className?: string }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const reduce = useReducedMotion();

  useEffect(() => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !ctx) return;

    let raf = 0;
    let disposed = false;
    let cleanup = () => {};

    void (async () => {
      // matter-js is CommonJS; depending on the bundler's interop the namespace
      // may live on the module or under `.default` — pick whichever exposes the API.
      const mod = await import("matter-js");
      if (disposed) return;
      const Matter = (
        (mod as { Engine?: unknown }).Engine
          ? mod
          : (mod as unknown as { default: typeof mod }).default
      ) as typeof import("matter-js");
      const { Engine, Bodies, Body, Composite, Vector } = Matter;

      const engine = Engine.create();
      engine.gravity.y = 1;
      engine.enableSleeping = true;

      // Static bounds: a floor the shards land on + side walls so they stay in view.
      const walls = [
        Bodies.rectangle(W / 2, GROUND_Y + 40, W + 80, 80, { isStatic: true }),
        Bodies.rectangle(-30, H / 2, 60, H * 3, { isStatic: true }),
        Bodies.rectangle(W + 30, H / 2, 60, H * 3, { isStatic: true }),
      ];
      Composite.add(engine.world, walls);

      // One rigid body per shard, placed where it sits in the intact prism.
      const shards = SHARDS.map((pts, i) => {
        const c = centroid(pts);
        const verts = pts.map(([x, y]) => ({ x, y }));
        const body = Bodies.fromVertices(c.x, c.y, [verts], {
          restitution: 0.28,
          friction: 0.45,
          frictionAir: 0.012,
          density: 0.0014,
          isStatic: true, // held assembled until the "shatter" kicks in
        });
        (body as { _color?: string })._color = COLORS[i % COLORS.length];
        return body;
      });
      Composite.add(engine.world, shards);

      // Pointer interaction (skipped under reduced motion): repel nearby shards as
      // you move, and re-shatter from the click point.
      let pointer: { x: number; y: number } | null = null;
      const toWorld = (e: { clientX: number; clientY: number }) => {
        const r = canvas.getBoundingClientRect();
        return {
          x: ((e.clientX - r.left) / r.width) * W,
          y: ((e.clientY - r.top) / r.height) * H,
        };
      };
      const onMove = (e: PointerEvent) => (pointer = toWorld(e));
      const onLeave = () => (pointer = null);
      const onDown = (e: PointerEvent) => {
        const p = toWorld(e);
        for (const s of shards) {
          const d = Vector.sub(s.position, p);
          const dist = Math.max(20, Vector.magnitude(d));
          const f = 0.06 / dist;
          Body.setStatic(s, false);
          Body.applyForce(s, s.position, {
            x: (d.x / dist) * f * s.mass,
            y: (d.y / dist) * f * s.mass - 0.004 * s.mass,
          });
        }
      };

      // Crack the prism apart: release the shards with an outward burst + spin.
      const shatter = () => {
        for (const s of shards) {
          Body.setStatic(s, false);
          const d = Vector.sub(s.position, PRISM_CENTER);
          const dist = Math.max(8, Vector.magnitude(d));
          Body.setVelocity(s, {
            x: (d.x / dist) * 2.4 + (Math.random() - 0.5),
            y: (d.y / dist) * 1.2 - 0.5,
          });
          Body.setAngularVelocity(s, (Math.random() - 0.5) * 0.25);
        }
      };

      // Per-frame: repulsion force from the cursor, then a fixed physics step.
      const step = () => {
        if (pointer) {
          for (const s of shards) {
            const d = Vector.sub(s.position, pointer);
            const dist = Vector.magnitude(d);
            if (dist < 90 && dist > 0.01) {
              const f = (0.02 * (1 - dist / 90)) / dist;
              Body.applyForce(s, s.position, {
                x: (d.x / dist) * f * s.mass,
                y: (d.y / dist) * f * s.mass,
              });
            }
          }
        }
        Engine.update(engine, 1000 / 60);
      };

      // --- Rendering ---------------------------------------------------------
      const dpr = Math.min(2, window.devicePixelRatio || 1);
      const fit = () => {
        const cssW = canvas.clientWidth || W;
        const cssH = (cssW * H) / W;
        canvas.width = Math.round(cssW * dpr);
        canvas.height = Math.round(cssH * dpr);
      };
      fit();
      const onResize = () => fit();
      window.addEventListener("resize", onResize);

      const cssVar = (name: string, fallback: string) => {
        const v = getComputedStyle(canvas).getPropertyValue(name).trim();
        return v ? `hsl(${v})` : fallback;
      };

      let beam = 1; // refraction-glow opacity, fades out after the shatter

      const drawShard = (body: MatterBody, color: string) => {
        const vs = body.vertices;
        const first = vs[0];
        if (!first) return;
        ctx.beginPath();
        ctx.moveTo(first.x, first.y);
        for (let i = 1; i < vs.length; i++) {
          const v = vs[i];
          if (v) ctx.lineTo(v.x, v.y);
        }
        ctx.closePath();

        // Glassy gradient fill across the shard's bounding box.
        const g = ctx.createLinearGradient(
          body.bounds.min.x, body.bounds.min.y,
          body.bounds.max.x, body.bounds.max.y,
        );
        g.addColorStop(0, color);
        g.addColorStop(1, color + "99");
        ctx.fillStyle = g;
        ctx.globalAlpha = 0.86;
        ctx.shadowColor = color;
        ctx.shadowBlur = 12;
        ctx.fill();
        ctx.shadowBlur = 0;

        ctx.globalAlpha = 0.9;
        ctx.lineWidth = 1;
        ctx.strokeStyle = "rgba(255,255,255,0.55)";
        ctx.stroke();
        ctx.globalAlpha = 1;
      };

      const render = () => {
        const scale = canvas.width / W;
        ctx.setTransform(scale, 0, 0, scale, 0, 0);
        ctx.clearRect(0, 0, W, H);

        // Refraction beam + glow behind the prism, dispersing as it breaks.
        if (beam > 0.01) {
          ctx.save();
          ctx.globalAlpha = beam;
          const rg = ctx.createRadialGradient(220, 96, 6, 220, 96, 150);
          rg.addColorStop(0, "rgba(168,85,247,0.30)");
          rg.addColorStop(1, "rgba(168,85,247,0)");
          ctx.fillStyle = rg;
          ctx.fillRect(0, 0, W, H);
          const lg = ctx.createLinearGradient(300, 40, 440, 150);
          COLORS.forEach((c, i) => lg.addColorStop(i / (COLORS.length - 1), c));
          ctx.globalAlpha = beam * 0.5;
          ctx.fillStyle = lg;
          ctx.beginPath();
          ctx.moveTo(222, 96);
          ctx.lineTo(440, 44);
          ctx.lineTo(440, 150);
          ctx.closePath();
          ctx.fill();
          ctx.restore();
        }

        // Ground line.
        ctx.strokeStyle = cssVar("--border", "rgba(120,120,130,0.4)");
        ctx.globalAlpha = 0.7;
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.moveTo(40, GROUND_Y + 12);
        ctx.lineTo(W - 40, GROUND_Y + 12);
        ctx.stroke();
        ctx.globalAlpha = 1;

        for (const s of shards) {
          drawShard(s, (s as { _color?: string })._color ?? "#a855f7");
        }
      };

      if (reduce) {
        // No motion: settle the pile off-screen, then paint it once.
        shatter();
        beam = 0;
        for (let i = 0; i < 240; i++) step();
        render();
        cleanup = () => {
          window.removeEventListener("resize", onResize);
          Composite.clear(engine.world, false);
          Engine.clear(engine);
        };
        return;
      }

      canvas.addEventListener("pointermove", onMove);
      canvas.addEventListener("pointerleave", onLeave);
      canvas.addEventListener("pointerdown", onDown);

      // Hold the intact, glowing prism for a beat, then shatter.
      const holdMs = 480;
      const start = performance.now();
      let released = false;

      const loop = (now: number) => {
        if (disposed) return;
        if (!released && now - start >= holdMs) {
          released = true;
          shatter();
        }
        if (released) {
          beam = Math.max(0, beam - 0.025);
          step();
        }
        render();
        raf = requestAnimationFrame(loop);
      };
      raf = requestAnimationFrame(loop);

      cleanup = () => {
        cancelAnimationFrame(raf);
        canvas.removeEventListener("pointermove", onMove);
        canvas.removeEventListener("pointerleave", onLeave);
        canvas.removeEventListener("pointerdown", onDown);
        window.removeEventListener("resize", onResize);
        Composite.clear(engine.world, false);
        Engine.clear(engine);
      };
    })();

    return () => {
      disposed = true;
      cancelAnimationFrame(raf);
      cleanup();
    };
  }, [reduce]);

  return (
    <canvas
      ref={canvasRef}
      aria-hidden
      className={cn("w-full max-w-[420px] touch-none select-none", className)}
      style={{ aspectRatio: `${W} / ${H}` }}
    />
  );
}
