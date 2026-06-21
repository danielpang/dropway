"use client";

import { useEffect, useRef } from "react";

import { cn } from "@/lib/utils";

/**
 * A ruptured steel water pipe leaking from a crack — the error-page motif.
 *
 * Two layers, by design:
 *  - An SVG pipe that renders SERVER-SIDE, so the page is never blank.
 *  - A three.js (WebGL) scene — a lit metallic pipe with a falling water-particle
 *    leak and a puddle — that mounts on top and reveals itself ONLY once it has
 *    initialized and drawn a frame. If WebGL is unavailable, three fails to load,
 *    or the user prefers reduced motion, the SVG simply stays. It can't regress
 *    to a blank canvas.
 *
 * three.js is dynamically imported, so it ships only in the error/404 route
 * chunks and never runs during SSR. Decorative (aria-hidden); the copy carries
 * the meaning.
 */

// ---- SVG fallback (always rendered, server-safe) --------------------------

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

function BurstPipeSvg({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 280 220"
      className={cn("h-full w-full", className)}
      role="img"
      aria-hidden
    >
      <defs>
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
      </defs>
      {[16, 256].map((x) => (
        <g key={x}>
          <rect x={x} y={60} width={16} height={66} rx={4} fill="url(#bp-collar)" stroke="#3a424c" strokeWidth={0.75} />
          <circle cx={x + 8} cy={74} r={2.4} fill="#3a424c" />
          <circle cx={x + 8} cy={112} r={2.4} fill="#3a424c" />
        </g>
      ))}
      <rect x={20} y={72} width={240} height={46} rx={23} fill="url(#bp-steel)" stroke="#3f4751" strokeWidth={0.75} strokeOpacity={0.6} />
      <rect x={40} y={78} width={200} height={3.5} rx={1.75} fill="#ffffff" opacity={0.55} />
      <rect x={30} y={110} width={220} height={4} rx={2} fill="#1f2730" opacity={0.22} />
      <rect x={122} y={68} width={40} height={54} rx={6} fill="url(#bp-collar)" stroke="#39414b" strokeWidth={1} />
      <path d="M140,74 L134,85 L141,92 L132,101 L140,109 L136,120 L145,120 L141,109 L149,101 L141,92 L147,85 L143,74 Z" fill="#10161d" />
      <path d="M139,116 C137.5,128 140.5,136 139,146 C137.5,136 134.5,128 139,116 Z" fill="url(#bp-water)" opacity={0.7} />
      <path d={drop(139, 130, 0.9)} fill="url(#bp-water)" opacity={0.85} />
      <path d={drop(139, 158, 0.8)} fill="url(#bp-water)" opacity={0.8} />
      <ellipse cx={139} cy={200} rx={34} ry={6.5} fill="url(#bp-water)" opacity={0.8} />
      <ellipse cx={130} cy={198} rx={11} ry={2} fill="#ffffff" opacity={0.4} />
    </svg>
  );
}

// ---- three.js enhancement -------------------------------------------------

/** A soft round droplet sprite (radial gradient) for the water Points. */
function dropletTexture(THREE: typeof import("three")): import("three").Texture {
  const c = document.createElement("canvas");
  c.width = c.height = 64;
  const ctx = c.getContext("2d")!;
  const g = ctx.createRadialGradient(32, 28, 1, 32, 32, 30);
  g.addColorStop(0, "rgba(255,255,255,0.95)");
  g.addColorStop(0.4, "rgba(196,232,255,0.85)");
  g.addColorStop(1, "rgba(125,211,252,0)");
  ctx.fillStyle = g;
  ctx.beginPath();
  ctx.arc(32, 32, 30, 0, Math.PI * 2);
  ctx.fill();
  const tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  return tex;
}

export function BurstPipe({ className }: { className?: string }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const svgWrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    const container = containerRef.current;
    if (!canvas || !container) return;
    if (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) return;

    let raf = 0;
    let disposed = false;
    let cleanup = () => {};

    void (async () => {
      let THREE: typeof import("three");
      try {
        THREE = await import("three");
      } catch {
        return; // keep the SVG
      }
      if (disposed) return;

      let renderer: import("three").WebGLRenderer;
      try {
        renderer = new THREE.WebGLRenderer({ canvas, alpha: true, antialias: true });
      } catch {
        return; // no WebGL → keep the SVG
      }
      renderer.setPixelRatio(Math.min(2, window.devicePixelRatio || 1));
      renderer.setClearColor(0x000000, 0);
      renderer.outputColorSpace = THREE.SRGBColorSpace;
      renderer.toneMapping = THREE.ACESFilmicToneMapping;
      renderer.toneMappingExposure = 1.15;

      const scene = new THREE.Scene();
      const camera = new THREE.PerspectiveCamera(32, 1, 0.1, 100);
      camera.position.set(0, 0.5, 7.4);
      camera.lookAt(0, -0.25, 0);

      // Lighting — gives the metal real specular shading without an env map.
      scene.add(new THREE.HemisphereLight(0xdfefff, 0x20262e, 1.15));
      const key = new THREE.DirectionalLight(0xffffff, 2.3);
      key.position.set(3.5, 6, 5);
      scene.add(key);
      const rim = new THREE.PointLight(0x9fd0ff, 26, 40);
      rim.position.set(-4, 2.5, 3.5);
      scene.add(rim);

      const group = new THREE.Group();
      group.rotation.set(0.14, -0.38, 0);
      scene.add(group);

      const disposables: { dispose(): void }[] = [];
      const track = <T extends { dispose(): void }>(o: T): T => {
        disposables.push(o);
        return o;
      };

      const steel = track(
        new THREE.MeshStandardMaterial({ color: 0x9aa6b2, metalness: 0.62, roughness: 0.3 }),
      );
      const dark = track(
        new THREE.MeshStandardMaterial({ color: 0x6c7884, metalness: 0.6, roughness: 0.42 }),
      );

      const barrel = new THREE.Mesh(track(new THREE.CylinderGeometry(0.5, 0.5, 5, 48)), steel);
      barrel.rotation.z = Math.PI / 2;
      group.add(barrel);

      for (const x of [-2.45, 2.45]) {
        const f = new THREE.Mesh(track(new THREE.CylinderGeometry(0.62, 0.62, 0.28, 48)), dark);
        f.rotation.z = Math.PI / 2;
        f.position.x = x;
        group.add(f);
      }
      const collar = new THREE.Mesh(track(new THREE.CylinderGeometry(0.6, 0.6, 0.82, 48)), dark);
      collar.rotation.z = Math.PI / 2;
      group.add(collar);

      // Water as a particle leak falling from the crack (top-front of the pipe).
      const N = 260;
      const floorY = -2.35;
      const crackX = 0;
      const crackY = 0.5;
      const crackZ = 0.2;
      const positions = new Float32Array(N * 3);
      const vel = new Float32Array(N * 3);

      const spawn = (i: number, prefill: boolean) => {
        const b = i * 3;
        positions[b] = crackX + (Math.random() - 0.5) * 0.28;
        positions[b + 1] = prefill ? crackY - Math.random() * 2.7 : crackY + Math.random() * 0.08;
        positions[b + 2] = crackZ + (Math.random() - 0.5) * 0.28;
        vel[b] = (Math.random() - 0.5) * 0.012;
        vel[b + 1] = -Math.random() * 0.03;
        vel[b + 2] = (Math.random() - 0.5) * 0.012;
      };
      for (let i = 0; i < N; i++) spawn(i, true);

      const pgeo = track(new THREE.BufferGeometry());
      pgeo.setAttribute("position", new THREE.BufferAttribute(positions, 3));
      const sprite = track(dropletTexture(THREE));
      const pmat = track(
        new THREE.PointsMaterial({
          size: 0.17,
          map: sprite,
          color: 0x9bd8fb,
          transparent: true,
          opacity: 0.95,
          depthWrite: false,
          sizeAttenuation: true,
        }),
      );
      group.add(new THREE.Points(pgeo, pmat));

      const puddle = new THREE.Mesh(
        track(new THREE.CircleGeometry(1.55, 48)),
        track(
          new THREE.MeshStandardMaterial({
            color: 0x38bdf8,
            metalness: 0.35,
            roughness: 0.12,
            transparent: true,
            opacity: 0.5,
          }),
        ),
      );
      puddle.rotation.x = -Math.PI / 2;
      puddle.position.y = floorY;
      group.add(puddle);

      const g = 0.0062;
      const stepWater = () => {
        for (let i = 0; i < N; i++) {
          const b = i * 3;
          const vy = (vel[b + 1] ?? 0) - g;
          vel[b + 1] = vy;
          positions[b] = (positions[b] ?? 0) + (vel[b] ?? 0);
          positions[b + 1] = (positions[b + 1] ?? 0) + vy;
          positions[b + 2] = (positions[b + 2] ?? 0) + (vel[b + 2] ?? 0);
          if ((positions[b + 1] ?? 0) < floorY) spawn(i, false);
        }
        pgeo.attributes.position!.needsUpdate = true;
      };

      const resize = () => {
        const w = container.clientWidth || 280;
        const h = container.clientHeight || 220;
        renderer.setSize(w, h, false);
        camera.aspect = w / h;
        camera.updateProjectionMatrix();
      };
      resize();
      const ro = new ResizeObserver(resize);
      ro.observe(container);

      let t = 0;
      const loop = () => {
        if (disposed) return;
        t += 0.006;
        group.rotation.y = -0.38 + Math.sin(t) * 0.09; // gentle idle sway
        stepWater();
        renderer.render(scene, camera);
        raf = requestAnimationFrame(loop);
      };

      // First frame, then cross-fade from the SVG to the live scene.
      renderer.render(scene, camera);
      canvas.style.opacity = "1";
      if (svgWrapRef.current) svgWrapRef.current.style.opacity = "0";
      loop();

      cleanup = () => {
        cancelAnimationFrame(raf);
        ro.disconnect();
        for (const d of disposables) {
          try {
            d.dispose();
          } catch {
            /* ignore */
          }
        }
        renderer.dispose();
      };
    })();

    return () => {
      disposed = true;
      cancelAnimationFrame(raf);
      cleanup();
    };
  }, []);

  return (
    <div
      ref={containerRef}
      className={cn("relative w-full max-w-[320px]", className)}
      style={{ aspectRatio: "280 / 220" }}
    >
      <div
        ref={svgWrapRef}
        className="absolute inset-0 transition-opacity duration-700"
      >
        <BurstPipeSvg />
      </div>
      <canvas
        ref={canvasRef}
        aria-hidden
        className="absolute inset-0 h-full w-full opacity-0 transition-opacity duration-700"
      />
    </div>
  );
}
