/* SPDX-License-Identifier: FSL-1.1-Apache-2.0 */
/* Particle Flow — flow-field particle engine.
 *
 * Thousands of particles are advected through a slowly evolving 3D simplex-noise
 * vector field (x, y, time). Instead of clearing the canvas each frame we paint a
 * translucent dark rectangle, so trails persist and fade for an elegant smear.
 *
 * No external libraries: the simplex-noise implementation below is self-contained
 * so the page never depends on a CDN that could 404 or trip CORS. The only remote
 * asset is an optional Google Fonts stylesheet, which degrades to a system stack.
 */
(() => {
  "use strict";

  // ----------------------------------------------------------------------
  // 3D Simplex noise (public-domain reference implementation, compacted).
  // Seeded permutation so each load looks the same; deterministic & fast.
  // ----------------------------------------------------------------------
  function makeNoise3D(seed) {
    const p = new Uint8Array(256);
    for (let i = 0; i < 256; i++) p[i] = i;
    // xorshift-seeded Fisher–Yates shuffle.
    let s = (seed >>> 0) || 1;
    const rnd = () => {
      s ^= s << 13; s ^= s >>> 17; s ^= s << 5;
      return ((s >>> 0) % 1000000) / 1000000;
    };
    for (let i = 255; i > 0; i--) {
      const j = (rnd() * (i + 1)) | 0;
      const t = p[i]; p[i] = p[j]; p[j] = t;
    }
    const perm = new Uint8Array(512);
    const permMod12 = new Uint8Array(512);
    for (let i = 0; i < 512; i++) {
      perm[i] = p[i & 255];
      permMod12[i] = perm[i] % 12;
    }

    const grad3 = new Float32Array([
      1, 1, 0, -1, 1, 0, 1, -1, 0, -1, -1, 0,
      1, 0, 1, -1, 0, 1, 1, 0, -1, -1, 0, -1,
      0, 1, 1, 0, -1, 1, 0, 1, -1, 0, -1, -1,
    ]);

    const F3 = 1 / 3;
    const G3 = 1 / 6;

    return function noise(xin, yin, zin) {
      let n0, n1, n2, n3;
      const skew = (xin + yin + zin) * F3;
      const i = Math.floor(xin + skew);
      const j = Math.floor(yin + skew);
      const k = Math.floor(zin + skew);
      const t = (i + j + k) * G3;
      const x0 = xin - (i - t);
      const y0 = yin - (j - t);
      const z0 = zin - (k - t);

      let i1, j1, k1, i2, j2, k2;
      if (x0 >= y0) {
        if (y0 >= z0) { i1 = 1; j1 = 0; k1 = 0; i2 = 1; j2 = 1; k2 = 0; }
        else if (x0 >= z0) { i1 = 1; j1 = 0; k1 = 0; i2 = 1; j2 = 0; k2 = 1; }
        else { i1 = 0; j1 = 0; k1 = 1; i2 = 1; j2 = 0; k2 = 1; }
      } else {
        if (y0 < z0) { i1 = 0; j1 = 0; k1 = 1; i2 = 0; j2 = 1; k2 = 1; }
        else if (x0 < z0) { i1 = 0; j1 = 1; k1 = 0; i2 = 0; j2 = 1; k2 = 1; }
        else { i1 = 0; j1 = 1; k1 = 0; i2 = 1; j2 = 1; k2 = 0; }
      }

      const x1 = x0 - i1 + G3, y1 = y0 - j1 + G3, z1 = z0 - k1 + G3;
      const x2 = x0 - i2 + 2 * G3, y2 = y0 - j2 + 2 * G3, z2 = z0 - k2 + 2 * G3;
      const x3 = x0 - 1 + 3 * G3, y3 = y0 - 1 + 3 * G3, z3 = z0 - 1 + 3 * G3;

      const ii = i & 255, jj = j & 255, kk = k & 255;

      let t0 = 0.6 - x0 * x0 - y0 * y0 - z0 * z0;
      if (t0 < 0) n0 = 0;
      else {
        const gi0 = permMod12[ii + perm[jj + perm[kk]]] * 3;
        t0 *= t0;
        n0 = t0 * t0 * (grad3[gi0] * x0 + grad3[gi0 + 1] * y0 + grad3[gi0 + 2] * z0);
      }
      let t1 = 0.6 - x1 * x1 - y1 * y1 - z1 * z1;
      if (t1 < 0) n1 = 0;
      else {
        const gi1 = permMod12[ii + i1 + perm[jj + j1 + perm[kk + k1]]] * 3;
        t1 *= t1;
        n1 = t1 * t1 * (grad3[gi1] * x1 + grad3[gi1 + 1] * y1 + grad3[gi1 + 2] * z1);
      }
      let t2 = 0.6 - x2 * x2 - y2 * y2 - z2 * z2;
      if (t2 < 0) n2 = 0;
      else {
        const gi2 = permMod12[ii + i2 + perm[jj + j2 + perm[kk + k2]]] * 3;
        t2 *= t2;
        n2 = t2 * t2 * (grad3[gi2] * x2 + grad3[gi2 + 1] * y2 + grad3[gi2 + 2] * z2);
      }
      let t3 = 0.6 - x3 * x3 - y3 * y3 - z3 * z3;
      if (t3 < 0) n3 = 0;
      else {
        const gi3 = permMod12[ii + 1 + perm[jj + 1 + perm[kk + 1]]] * 3;
        t3 *= t3;
        n3 = t3 * t3 * (grad3[gi3] * x3 + grad3[gi3 + 1] * y3 + grad3[gi3 + 2] * z3);
      }
      return 32 * (n0 + n1 + n2 + n3);
    };
  }

  // ----------------------------------------------------------------------
  // Palettes — each is a set of HSL stops sampled by a 0..1 parameter.
  // Hand-tuned for an aurora / nebula glow against a near-black backdrop.
  // ----------------------------------------------------------------------
  const PALETTES = [
    { name: "Aurora",          fade: "#04060a", stops: [[170, 90, 60], [150, 85, 58], [200, 80, 62], [275, 70, 64]] },
    { name: "Nebula",          fade: "#06040c", stops: [[290, 75, 60], [330, 80, 62], [255, 70, 60], [205, 75, 60]] },
    { name: "Ember",           fade: "#0a0503", stops: [[18, 90, 58], [38, 95, 60], [350, 80, 58], [300, 60, 52]] },
    { name: "Bioluminescence", fade: "#02080a", stops: [[160, 90, 55], [185, 95, 60], [210, 90, 62], [125, 80, 56]] },
    { name: "Iridium",         fade: "#070710", stops: [[210, 35, 72], [250, 45, 70], [320, 40, 70], [180, 40, 72]] },
  ];

  // Sample a palette at t in [0,1], returning an "hsl(...)" string.
  function paletteColor(pal, t, light) {
    const stops = pal.stops;
    const f = (t % 1 + 1) % 1;
    const scaled = f * stops.length;
    const i = Math.floor(scaled) % stops.length;
    const j = (i + 1) % stops.length;
    const k = scaled - Math.floor(scaled);
    const a = stops[i], b = stops[j];
    // Shortest-path hue interpolation.
    let dh = b[0] - a[0];
    if (dh > 180) dh -= 360; else if (dh < -180) dh += 360;
    const h = (a[0] + dh * k + 360) % 360;
    const sat = a[1] + (b[1] - a[1]) * k;
    const lig = (a[2] + (b[2] - a[2]) * k) * light;
    return "hsl(" + h.toFixed(1) + "," + sat.toFixed(0) + "%," + lig.toFixed(0) + "%)";
  }

  // ----------------------------------------------------------------------
  // Setup
  // ----------------------------------------------------------------------
  const canvas = document.getElementById("field");
  const ctx = canvas.getContext("2d", { alpha: false });
  const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  const noise = makeNoise3D(20260614);

  const state = {
    count: 3000,
    speed: 1,
    trail: 0.86,           // persistence 0..1 -> fade alpha = 1 - trail
    palette: 0,
    paused: false,
    width: 0,
    height: 0,
    dpr: 1,
    zTime: 0,
    hue: 0,
    pointer: { x: 0, y: 0, px: 0, py: 0, active: false, vx: 0, vy: 0 },
  };

  // Spatial scale of the field. Smaller -> broader, smoother currents.
  const NOISE_SCALE = 0.0016;
  const FIELD_STRENGTH = 2.4; // radians of turn the field can impose
  const MOUSE_RADIUS = 220;
  const MOUSE_FORCE = 1.6;

  let particles = [];

  function rand(min, max) { return min + Math.random() * (max - min); }

  function spawn(pP) {
    pP.x = Math.random() * state.width;
    pP.y = Math.random() * state.height;
    pP.vx = 0;
    pP.vy = 0;
    pP.life = rand(60, 320);   // frames before recycling -> keeps trails fresh
    pP.maxLife = pP.life;
    pP.hue = Math.random();    // per-particle palette offset
    pP.w = rand(0.6, 1.7);
    return pP;
  }

  function buildParticles(n) {
    const next = new Array(n);
    for (let i = 0; i < n; i++) {
      next[i] = i < particles.length ? particles[i] : spawn({});
    }
    particles = next;
  }

  function resize() {
    const cap = window.innerWidth > 1400 ? 1.5 : 2; // cap DPR for perf
    state.dpr = Math.min(window.devicePixelRatio || 1, cap);
    state.width = window.innerWidth;
    state.height = window.innerHeight;
    canvas.width = Math.round(state.width * state.dpr);
    canvas.height = Math.round(state.height * state.dpr);
    canvas.style.width = state.width + "px";
    canvas.style.height = state.height + "px";
    ctx.setTransform(state.dpr, 0, 0, state.dpr, 0, 0);
    // Prime the backdrop so the first fade rect has something to blend over.
    ctx.fillStyle = PALETTES[state.palette].fade;
    ctx.fillRect(0, 0, state.width, state.height);
    state.pointer.x = state.pointer.px = state.width / 2;
    state.pointer.y = state.pointer.py = state.height / 2;
  }

  // ----------------------------------------------------------------------
  // Simulation step
  // ----------------------------------------------------------------------
  function step() {
    const pal = PALETTES[state.palette];
    const w = state.width, h = state.height;

    // Trail fade: draw a translucent dark rect instead of clearing.
    const fadeAlpha = Math.max(0.012, 1 - state.trail);
    ctx.globalCompositeOperation = "source-over";
    ctx.globalAlpha = fadeAlpha;
    ctx.fillStyle = pal.fade;
    ctx.fillRect(0, 0, w, h);

    // Additive glow for the particle strokes.
    ctx.globalCompositeOperation = "lighter";
    ctx.globalAlpha = 1;
    ctx.lineCap = "round";

    state.zTime += 0.0009 * state.speed;
    state.hue += 0.0006;

    const ptr = state.pointer;
    ptr.vx = ptr.x - ptr.px;
    ptr.vy = ptr.y - ptr.py;
    ptr.px = ptr.x;
    ptr.py = ptr.y;
    const swirl = Math.min(1, Math.hypot(ptr.vx, ptr.vy) / 40);

    const accel = 0.55 * state.speed;
    const maxSpeed = 3.4 * state.speed;

    for (let i = 0; i < particles.length; i++) {
      const pt = particles[i];

      // Field angle from noise. Map noise (-1..1) to a wide angular sweep.
      const n = noise(pt.x * NOISE_SCALE, pt.y * NOISE_SCALE, state.zTime);
      let angle = n * Math.PI * FIELD_STRENGTH;
      let fx = Math.cos(angle);
      let fy = Math.sin(angle);

      // Mouse interaction: swirl + attract within a radius.
      if (ptr.active) {
        const dx = ptr.x - pt.x;
        const dy = ptr.y - pt.y;
        const dist = Math.hypot(dx, dy);
        if (dist < MOUSE_RADIUS && dist > 0.001) {
          const falloff = (1 - dist / MOUSE_RADIUS);
          const inv = 1 / dist;
          // tangential (swirl) component, rotated 90deg from radial.
          const tx = -dy * inv, ty = dx * inv;
          const rx = dx * inv, ry = dy * inv;
          const force = MOUSE_FORCE * falloff;
          fx += (tx * (0.7 + swirl) + rx * 0.35) * force;
          fy += (ty * (0.7 + swirl) + ry * 0.35) * force;
        }
      }

      pt.vx += fx * accel;
      pt.vy += fy * accel;

      // Clamp velocity.
      const sp = Math.hypot(pt.vx, pt.vy);
      if (sp > maxSpeed) {
        const s = maxSpeed / sp;
        pt.vx *= s; pt.vy *= s;
      }
      pt.vx *= 0.92; // drag keeps motion silky
      pt.vy *= 0.92;

      const nx = pt.x + pt.vx;
      const ny = pt.y + pt.vy;

      // Color by velocity + position + per-particle offset + global drift.
      const speedT = Math.min(1, sp / maxSpeed);
      const t = pt.hue + state.hue + (pt.y / h) * 0.25 + speedT * 0.15;
      const light = 0.55 + speedT * 0.65;
      const alpha = 0.16 + speedT * 0.5;

      ctx.globalAlpha = alpha;
      ctx.strokeStyle = paletteColor(pal, t, light);
      ctx.lineWidth = pt.w * (0.7 + speedT * 0.9);
      ctx.beginPath();
      ctx.moveTo(pt.x, pt.y);
      ctx.lineTo(nx, ny);
      ctx.stroke();

      pt.x = nx;
      pt.y = ny;
      pt.life--;

      // Recycle when off-screen or expired so the field stays alive.
      if (pt.x < -10 || pt.x > w + 10 || pt.y < -10 || pt.y > h + 10 || pt.life <= 0) {
        spawn(pt);
      }
    }

    ctx.globalAlpha = 1;
    ctx.globalCompositeOperation = "source-over";
  }

  // Calm, mostly-static render for prefers-reduced-motion: paint a single
  // luminous field of streaks once, then leave it still.
  function renderStill() {
    const pal = PALETTES[state.palette];
    const w = state.width, h = state.height;
    ctx.globalCompositeOperation = "source-over";
    ctx.fillStyle = pal.fade;
    ctx.fillRect(0, 0, w, h);
    ctx.globalCompositeOperation = "lighter";
    ctx.lineCap = "round";

    for (let i = 0; i < particles.length; i++) {
      const pt = particles[i];
      let x = pt.x, y = pt.y;
      ctx.beginPath();
      ctx.moveTo(x, y);
      // Trace a short streamline through the static field.
      for (let s = 0; s < 26; s++) {
        const n = noise(x * NOISE_SCALE, y * NOISE_SCALE, 0);
        const a = n * Math.PI * FIELD_STRENGTH;
        x += Math.cos(a) * 2.2;
        y += Math.sin(a) * 2.2;
        ctx.lineTo(x, y);
      }
      const t = pt.hue + (pt.y / h) * 0.3;
      ctx.globalAlpha = 0.28;
      ctx.strokeStyle = paletteColor(pal, t, 0.9);
      ctx.lineWidth = pt.w;
      ctx.stroke();
    }
    ctx.globalAlpha = 1;
    ctx.globalCompositeOperation = "source-over";
  }

  // ----------------------------------------------------------------------
  // Main loop (frame-rate aware via timestamp; falls back gracefully).
  // ----------------------------------------------------------------------
  let rafId = 0;
  function loop() {
    if (!state.paused) step();
    rafId = requestAnimationFrame(loop);
  }

  function start() {
    cancelAnimationFrame(rafId);
    if (reduceMotion) {
      renderStill();
      return;
    }
    loop();
  }

  // ----------------------------------------------------------------------
  // Interaction wiring
  // ----------------------------------------------------------------------
  function setPointer(x, y) {
    state.pointer.x = x;
    state.pointer.y = y;
    state.pointer.active = true;
  }

  window.addEventListener("pointermove", (e) => {
    setPointer(e.clientX, e.clientY);
  }, { passive: true });

  window.addEventListener("pointerdown", (e) => {
    // Ignore clicks that land on the HUD controls.
    if (e.target.closest && e.target.closest(".hud, .hud-toggle")) return;
    setPointer(e.clientX, e.clientY);
    cyclePalette();
  });

  window.addEventListener("pointerleave", () => { state.pointer.active = false; });
  window.addEventListener("blur", () => { state.pointer.active = false; });

  function cyclePalette() {
    state.palette = (state.palette + 1) % PALETTES.length;
    paletteSel.value = String(state.palette);
    flashFade();
  }

  // Briefly wash the canvas toward the new palette's fade color so the
  // transition reads as intentional rather than a hard cut.
  function flashFade() {
    const pal = PALETTES[state.palette];
    ctx.globalCompositeOperation = "source-over";
    ctx.globalAlpha = 0.4;
    ctx.fillStyle = pal.fade;
    ctx.fillRect(0, 0, state.width, state.height);
    ctx.globalAlpha = 1;
    if (reduceMotion) renderStill();
  }

  // ----- Controls -----
  const countIn = document.getElementById("count");
  const speedIn = document.getElementById("speed");
  const trailIn = document.getElementById("trail");
  const paletteSel = document.getElementById("palette");
  const countOut = document.getElementById("countOut");
  const speedOut = document.getElementById("speedOut");
  const trailOut = document.getElementById("trailOut");

  countIn.addEventListener("input", () => {
    state.count = +countIn.value;
    countOut.textContent = state.count;
    buildParticles(state.count);
    if (reduceMotion) renderStill();
  });
  speedIn.addEventListener("input", () => {
    state.speed = +speedIn.value;
    speedOut.textContent = state.speed.toFixed(2).replace(/0$/, "") + "×";
  });
  trailIn.addEventListener("input", () => {
    const pct = +trailIn.value;
    state.trail = pct / 100;
    trailOut.textContent = pct + "%";
  });
  paletteSel.addEventListener("change", () => {
    state.palette = +paletteSel.value;
    flashFade();
  });

  // Keyboard: space toggles pause when focus isn't in a control.
  window.addEventListener("keydown", (e) => {
    if (e.code === "Space" && !/^(INPUT|SELECT|BUTTON|TEXTAREA)$/.test(document.activeElement.tagName)) {
      e.preventDefault();
      state.paused = !state.paused;
    }
  });

  // HUD show/hide.
  const hud = document.getElementById("hud");
  const hudToggle = document.getElementById("hudToggle");
  hudToggle.addEventListener("click", () => {
    const collapsed = hud.classList.toggle("is-collapsed");
    hudToggle.setAttribute("aria-expanded", String(!collapsed));
    hudToggle.textContent = collapsed ? "Show panel" : "Hide panel";
  });

  // ----------------------------------------------------------------------
  // Boot
  // ----------------------------------------------------------------------
  window.addEventListener("resize", () => {
    resize();
    if (reduceMotion) renderStill();
  });

  resize();
  buildParticles(state.count);
  start();
})();
