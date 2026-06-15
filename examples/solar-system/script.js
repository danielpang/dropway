// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// Orrery — interactive 3D Solar System. Three.js r0.160.0 (ES modules via import map).
// All geometry, materials, textures and data are generated/embedded inline — no external assets.

import * as THREE from "three";
import { OrbitControls } from "three/addons/controls/OrbitControls.js";

/* =========================================================================
   SOLAR SYSTEM DATA
   - `size`     : visual radius (compressed; relative order roughly preserved)
   - `distance` : visual orbit radius (compressed; order is correct)
   - `speed`    : visual angular speed (inner planets faster)
   - `spin`     : self-rotation rate
   - `color`    : base color
   - real-world `diameter`, `auDistance`, `period`, `fact` are factual.
   ========================================================================= */
const BODIES = [
  {
    key: "mercury", name: "Mercury", color: 0x9a8f86, size: 0.55, distance: 9,
    speed: 1.59, spin: 0.012, tilt: 0.001, surface: "rock",
    diameter: "4,879 km", auDistance: "57.9 million km (0.39 AU)", period: "88 days",
    tag: "Terrestrial · Innermost",
    fact: "A year on Mercury is just 88 Earth days, but a single solar day lasts about 176 Earth days — longer than its year.",
  },
  {
    key: "venus", name: "Venus", color: 0xddb778, size: 0.94, distance: 13,
    speed: 1.18, spin: -0.005, tilt: 3.09, surface: "rock",
    diameter: "12,104 km", auDistance: "108.2 million km (0.72 AU)", period: "225 days",
    tag: "Terrestrial · The Twin",
    fact: "Venus spins backwards and so slowly that its day is longer than its year. A thick CO₂ blanket makes it the hottest planet at ~465 °C.",
  },
  {
    key: "earth", name: "Earth", color: 0x3a73c4, accent: 0x47a05a, size: 1.0, distance: 17,
    speed: 1.0, spin: 0.02, tilt: 0.41, surface: "earth",
    diameter: "12,742 km", auDistance: "149.6 million km (1.00 AU)", period: "365.25 days",
    tag: "Terrestrial · Home",
    fact: "The only world known to harbour life — and the only planet not named after a Greek or Roman deity.",
  },
  {
    key: "mars", name: "Mars", color: 0xc1502e, size: 0.66, distance: 21,
    speed: 0.81, spin: 0.018, tilt: 0.44, surface: "rock",
    diameter: "6,779 km", auDistance: "227.9 million km (1.52 AU)", period: "687 days",
    tag: "Terrestrial · The Red Planet",
    fact: "Home to Olympus Mons, the tallest volcano in the Solar System at ~22 km — nearly three times the height of Mount Everest.",
  },
  {
    key: "jupiter", name: "Jupiter", color: 0xcaa472, bandColor: 0x9c6a3e, size: 3.4, distance: 30,
    speed: 0.44, spin: 0.045, tilt: 0.05, surface: "gasBands",
    diameter: "139,820 km", auDistance: "778.5 million km (5.20 AU)", period: "11.9 years",
    tag: "Gas Giant · The King",
    fact: "Jupiter is so massive that more than 1,300 Earths could fit inside it. Its Great Red Spot is a storm wider than our entire planet.",
  },
  {
    key: "saturn", name: "Saturn", color: 0xd8c79a, bandColor: 0xb39e70, size: 2.9, distance: 40,
    speed: 0.32, spin: 0.04, tilt: 0.47, surface: "gasBands", rings: true,
    diameter: "116,460 km", auDistance: "1.43 billion km (9.58 AU)", period: "29.4 years",
    tag: "Gas Giant · The Jewel",
    fact: "Saturn's rings span up to 280,000 km yet are often less than 1 km thick. The planet itself is light enough to float in water.",
  },
  {
    key: "uranus", name: "Uranus", color: 0x8fd5dd, bandColor: 0x6fb8c4, size: 1.9, distance: 49,
    speed: 0.23, spin: 0.03, tilt: 1.71, surface: "gasBands",
    diameter: "50,724 km", auDistance: "2.87 billion km (19.2 AU)", period: "84 years",
    tag: "Ice Giant · The Tilted One",
    fact: "Uranus rolls around the Sun on its side, tilted ~98°, likely knocked over by a colossal ancient collision.",
  },
  {
    key: "neptune", name: "Neptune", color: 0x3b5bdb, bandColor: 0x2c46aa, size: 1.85, distance: 58,
    speed: 0.18, spin: 0.032, tilt: 0.49, surface: "gasBands",
    diameter: "49,244 km", auDistance: "4.50 billion km (30.1 AU)", period: "164.8 years",
    tag: "Ice Giant · The Windy",
    fact: "Neptune hosts the fastest winds in the Solar System — supersonic gusts reaching about 2,100 km/h.",
  },
  {
    key: "pluto", name: "Pluto", color: 0xbfa590, size: 0.42, distance: 66,
    speed: 0.15, spin: 0.009, tilt: 2.13, surface: "rock",
    diameter: "2,377 km", auDistance: "5.91 billion km (39.5 AU)", period: "248 years",
    tag: "Dwarf Planet · The Outsider",
    fact: "Reclassified as a dwarf planet in 2006, Pluto has a heart-shaped nitrogen-ice plain — Tombaugh Regio — larger than Texas.",
  },
];

/* =========================================================================
   ENVIRONMENT FLAGS
   ========================================================================= */
const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

/* =========================================================================
   PROCEDURAL TEXTURE HELPERS (canvas → CanvasTexture, no external images)
   ========================================================================= */
function makeCanvas(w, h) {
  const c = document.createElement("canvas");
  c.width = w; c.height = h;
  return { c, ctx: c.getContext("2d") };
}

function toTexture(canvas) {
  const tex = new THREE.CanvasTexture(canvas);
  tex.colorSpace = THREE.SRGBColorSpace;
  tex.anisotropy = 8;
  return tex;
}

const hex = (n) => "#" + n.toString(16).padStart(6, "0");

// Mix two hex-ints toward `t` (0..1)
function mixHex(a, b, t) {
  const ar = (a >> 16) & 255, ag = (a >> 8) & 255, ab = a & 255;
  const br = (b >> 16) & 255, bg = (b >> 8) & 255, bb = b & 255;
  const r = Math.round(ar + (br - ar) * t);
  const g = Math.round(ag + (bg - ag) * t);
  const bl = Math.round(ab + (bb - ab) * t);
  return (r << 16) | (g << 8) | bl;
}

// Soft mottled rocky surface
function rockTexture(base) {
  const { c, ctx } = makeCanvas(512, 256);
  ctx.fillStyle = hex(base);
  ctx.fillRect(0, 0, 512, 256);
  for (let i = 0; i < 1600; i++) {
    const x = Math.random() * 512, y = Math.random() * 256;
    const r = Math.random() * 14 + 2;
    const shade = (Math.random() - 0.5) * 0.5;
    ctx.fillStyle = hex(mixHex(base, shade < 0 ? 0x000000 : 0xffffff, Math.abs(shade)));
    ctx.globalAlpha = 0.18;
    ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2); ctx.fill();
  }
  // a few craters
  ctx.globalAlpha = 0.3;
  for (let i = 0; i < 40; i++) {
    const x = Math.random() * 512, y = Math.random() * 256, r = Math.random() * 8 + 2;
    ctx.fillStyle = hex(mixHex(base, 0x000000, 0.4));
    ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2); ctx.fill();
    ctx.fillStyle = hex(mixHex(base, 0xffffff, 0.2));
    ctx.beginPath(); ctx.arc(x - r * 0.25, y - r * 0.25, r * 0.6, 0, Math.PI * 2); ctx.fill();
  }
  ctx.globalAlpha = 1;
  return toTexture(c);
}

// Earth — continents + oceans
function earthTexture(ocean, land) {
  const { c, ctx } = makeCanvas(512, 256);
  ctx.fillStyle = hex(ocean);
  ctx.fillRect(0, 0, 512, 256);
  // continents as clustered blobs
  ctx.fillStyle = hex(land);
  for (let cl = 0; cl < 14; cl++) {
    const cx = Math.random() * 512, cy = 40 + Math.random() * 176;
    const blobs = 14 + Math.floor(Math.random() * 16);
    for (let b = 0; b < blobs; b++) {
      const x = cx + (Math.random() - 0.5) * 90;
      const y = cy + (Math.random() - 0.5) * 60;
      const r = Math.random() * 18 + 6;
      ctx.globalAlpha = 0.5;
      ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2); ctx.fill();
    }
  }
  // polar ice
  ctx.globalAlpha = 0.85;
  ctx.fillStyle = "#dfeaf5";
  ctx.fillRect(0, 0, 512, 14);
  ctx.fillRect(0, 242, 512, 14);
  ctx.globalAlpha = 1;
  return toTexture(c);
}

// Gas giant — horizontal banding via gradient + striped noise
function bandTexture(base, bandColor) {
  const { c, ctx } = makeCanvas(512, 256);
  const g = ctx.createLinearGradient(0, 0, 0, 256);
  g.addColorStop(0, hex(mixHex(base, bandColor, 0.6)));
  g.addColorStop(0.5, hex(base));
  g.addColorStop(1, hex(mixHex(base, bandColor, 0.5)));
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, 512, 256);
  // horizontal bands of varying tone
  let y = 0;
  while (y < 256) {
    const h = 4 + Math.random() * 16;
    const t = (Math.random() - 0.5) * 0.6;
    ctx.fillStyle = hex(mixHex(base, t < 0 ? 0x000000 : bandColor, Math.abs(t)));
    ctx.globalAlpha = 0.5;
    ctx.fillRect(0, y, 512, h);
    y += h;
  }
  // subtle turbulence with wavy strokes
  ctx.globalAlpha = 0.12;
  for (let i = 0; i < 60; i++) {
    const yy = Math.random() * 256;
    ctx.strokeStyle = hex(mixHex(base, 0xffffff, 0.5));
    ctx.lineWidth = 1 + Math.random() * 2;
    ctx.beginPath();
    for (let x = 0; x <= 512; x += 16) {
      const oy = yy + Math.sin((x / 512) * Math.PI * 4 + i) * 3;
      x === 0 ? ctx.moveTo(x, oy) : ctx.lineTo(x, oy);
    }
    ctx.stroke();
  }
  ctx.globalAlpha = 1;
  return toTexture(c);
}

// Radial sprite for the Sun's glow halo
function glowSprite(inner, outer) {
  const { c, ctx } = makeCanvas(256, 256);
  const g = ctx.createRadialGradient(128, 128, 0, 128, 128, 128);
  g.addColorStop(0.0, inner);
  g.addColorStop(0.25, outer);
  g.addColorStop(1.0, "rgba(0,0,0,0)");
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, 256, 256);
  const tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  return tex;
}

// Procedural sun surface
function sunTexture() {
  const { c, ctx } = makeCanvas(512, 256);
  const g = ctx.createLinearGradient(0, 0, 0, 256);
  g.addColorStop(0, "#ffd86b");
  g.addColorStop(0.5, "#ffae33");
  g.addColorStop(1, "#ff8c1a");
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, 512, 256);
  for (let i = 0; i < 900; i++) {
    const x = Math.random() * 512, y = Math.random() * 256, r = Math.random() * 10 + 2;
    const bright = Math.random() > 0.5;
    ctx.fillStyle = bright ? "rgba(255,240,180,0.5)" : "rgba(200,90,10,0.4)";
    ctx.globalAlpha = 0.5;
    ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2); ctx.fill();
  }
  ctx.globalAlpha = 1;
  return toTexture(c);
}

/* =========================================================================
   SCENE SETUP
   ========================================================================= */
const mount = document.getElementById("scene");
const scene = new THREE.Scene();
scene.fog = new THREE.FogExp2(0x04050a, 0.0009);

const camera = new THREE.PerspectiveCamera(50, window.innerWidth / window.innerHeight, 0.1, 4000);
camera.position.set(0, 42, 96);

const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: false, powerPreference: "high-performance" });
renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
renderer.setSize(window.innerWidth, window.innerHeight);
renderer.toneMapping = THREE.ACESFilmicToneMapping;
renderer.toneMappingExposure = 1.05;
renderer.outputColorSpace = THREE.SRGBColorSpace;
mount.appendChild(renderer.domElement);

// Controls
const controls = new OrbitControls(camera, renderer.domElement);
controls.enableDamping = true;
controls.dampingFactor = 0.06;
controls.rotateSpeed = 0.55;
controls.zoomSpeed = 0.9;
controls.minDistance = 14;
controls.maxDistance = 320;
controls.maxPolarAngle = Math.PI * 0.92;
controls.target.set(0, 0, 0);
controls.autoRotate = !reduceMotion;
controls.autoRotateSpeed = 0.18;

/* =========================================================================
   LIGHTING
   ========================================================================= */
scene.add(new THREE.AmbientLight(0x222a44, 1.1));
const sunLight = new THREE.PointLight(0xfff0d0, 4.2, 0, 1.3);
sunLight.position.set(0, 0, 0);
scene.add(sunLight);
// faint fill so the dark sides aren't pure black
const fill = new THREE.DirectionalLight(0x4a5a8a, 0.35);
fill.position.set(-1, 0.5, -1);
scene.add(fill);

/* =========================================================================
   STARFIELD (procedural Points)
   ========================================================================= */
function buildStarfield() {
  const group = new THREE.Group();
  const COUNT = 7000;
  const positions = new Float32Array(COUNT * 3);
  const colors = new Float32Array(COUNT * 3);
  const sizes = new Float32Array(COUNT);
  const palette = [0xffffff, 0xcfe0ff, 0xfff0d0, 0xffd6c2, 0xd9ecff];
  for (let i = 0; i < COUNT; i++) {
    // distribute on a large sphere shell
    const r = 600 + Math.random() * 1400;
    const theta = Math.random() * Math.PI * 2;
    const phi = Math.acos(2 * Math.random() - 1);
    positions[i * 3] = r * Math.sin(phi) * Math.cos(theta);
    positions[i * 3 + 1] = r * Math.cos(phi);
    positions[i * 3 + 2] = r * Math.sin(phi) * Math.sin(theta);
    const col = new THREE.Color(palette[(Math.random() * palette.length) | 0]);
    const tw = 0.5 + Math.random() * 0.5;
    colors[i * 3] = col.r * tw;
    colors[i * 3 + 1] = col.g * tw;
    colors[i * 3 + 2] = col.b * tw;
    sizes[i] = Math.random() < 0.06 ? 3.2 : Math.random() * 1.6 + 0.4;
  }
  const geo = new THREE.BufferGeometry();
  geo.setAttribute("position", new THREE.BufferAttribute(positions, 3));
  geo.setAttribute("color", new THREE.BufferAttribute(colors, 3));
  geo.setAttribute("size", new THREE.BufferAttribute(sizes, 1));

  // round soft point sprite
  const { c, ctx } = makeCanvas(64, 64);
  const g = ctx.createRadialGradient(32, 32, 0, 32, 32, 32);
  g.addColorStop(0, "rgba(255,255,255,1)");
  g.addColorStop(0.4, "rgba(255,255,255,0.5)");
  g.addColorStop(1, "rgba(255,255,255,0)");
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, 64, 64);
  const sprite = new THREE.CanvasTexture(c);

  const mat = new THREE.PointsMaterial({
    size: 2.4,
    map: sprite,
    vertexColors: true,
    transparent: true,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
    sizeAttenuation: true,
  });
  const points = new THREE.Points(geo, mat);
  group.add(points);

  // a faint distant dust layer for depth
  const dustGeo = new THREE.BufferGeometry();
  const dustPos = new Float32Array(1200 * 3);
  for (let i = 0; i < 1200; i++) {
    const r = 200 + Math.random() * 400;
    const theta = Math.random() * Math.PI * 2;
    const phi = Math.acos(2 * Math.random() - 1);
    dustPos[i * 3] = r * Math.sin(phi) * Math.cos(theta);
    dustPos[i * 3 + 1] = r * Math.cos(phi) * 0.4;
    dustPos[i * 3 + 2] = r * Math.sin(phi) * Math.sin(theta);
  }
  dustGeo.setAttribute("position", new THREE.BufferAttribute(dustPos, 3));
  const dust = new THREE.Points(dustGeo, new THREE.PointsMaterial({
    size: 1.1, color: 0x6677aa, transparent: true, opacity: 0.4,
    map: sprite, depthWrite: false, blending: THREE.AdditiveBlending,
  }));
  group.add(dust);

  scene.add(group);
  return group;
}
const starfield = buildStarfield();

/* =========================================================================
   THE SUN
   ========================================================================= */
const sunGroup = new THREE.Group();
const SUN_RADIUS = 4.2;
const sunMesh = new THREE.Mesh(
  new THREE.SphereGeometry(SUN_RADIUS, 64, 64),
  new THREE.MeshBasicMaterial({ map: sunTexture(), color: 0xffffff })
);
sunGroup.add(sunMesh);

// Layered glow halos (faked bloom via additive sprites)
function addHalo(scale, tex, opacity) {
  const mat = new THREE.SpriteMaterial({ map: tex, transparent: true, opacity, blending: THREE.AdditiveBlending, depthWrite: false });
  const s = new THREE.Sprite(mat);
  s.scale.set(scale, scale, 1);
  sunGroup.add(s);
  return s;
}
const haloTexHot = glowSprite("rgba(255,240,200,0.95)", "rgba(255,170,60,0.5)");
const haloTexWide = glowSprite("rgba(255,200,110,0.7)", "rgba(255,130,40,0.25)");
addHalo(SUN_RADIUS * 4.2, haloTexHot, 0.9);
addHalo(SUN_RADIUS * 9.5, haloTexWide, 0.55);
const coronaPulse = addHalo(SUN_RADIUS * 6.2, haloTexHot, 0.4);

// corona shell (thin additive sphere)
const corona = new THREE.Mesh(
  new THREE.SphereGeometry(SUN_RADIUS * 1.18, 48, 48),
  new THREE.MeshBasicMaterial({ color: 0xffb347, transparent: true, opacity: 0.22, blending: THREE.AdditiveBlending, side: THREE.BackSide })
);
sunGroup.add(corona);
scene.add(sunGroup);

/* =========================================================================
   ORBIT RING (thin glowing line)
   ========================================================================= */
function buildOrbitRing(radius) {
  const SEG = 192;
  const pts = [];
  for (let i = 0; i <= SEG; i++) {
    const a = (i / SEG) * Math.PI * 2;
    pts.push(new THREE.Vector3(Math.cos(a) * radius, 0, Math.sin(a) * radius));
  }
  const geo = new THREE.BufferGeometry().setFromPoints(pts);
  const mat = new THREE.LineBasicMaterial({ color: 0x6a78a8, transparent: true, opacity: 0.22 });
  return new THREE.Line(geo, mat);
}

/* =========================================================================
   PLANETS
   ========================================================================= */
const planets = [];

function makeSurface(b) {
  switch (b.surface) {
    case "earth": return earthTexture(b.color, b.accent);
    case "gasBands": return bandTexture(b.color, b.bandColor);
    default: return rockTexture(b.color);
  }
}

for (const b of BODIES) {
  // pivot rotates around the sun
  const pivot = new THREE.Group();
  pivot.rotation.y = Math.random() * Math.PI * 2; // random starting angle
  scene.add(pivot);

  // orbit ring
  scene.add(buildOrbitRing(b.distance));

  // planet mesh
  const tex = makeSurface(b);
  const mat = new THREE.MeshStandardMaterial({
    map: tex,
    roughness: b.surface === "gasBands" ? 0.85 : 0.95,
    metalness: 0.0,
  });
  if (b.surface === "earth") {
    mat.emissive = new THREE.Color(0x0a1a2a);
    mat.emissiveIntensity = 0.25;
  }
  const mesh = new THREE.Mesh(new THREE.SphereGeometry(b.size, 48, 48), mat);
  mesh.position.x = b.distance;
  mesh.rotation.z = b.tilt;
  mesh.userData.key = b.key;

  // tilt holder so spin axis respects tilt
  const tiltHolder = new THREE.Group();
  tiltHolder.position.x = b.distance;
  tiltHolder.rotation.z = b.tilt;
  mesh.position.x = 0;
  tiltHolder.add(mesh);
  pivot.add(tiltHolder);

  // atmosphere rim for terrestrial/ice bodies
  if (b.surface === "earth" || b.surface === "gasBands") {
    const atmoColor = b.surface === "earth" ? 0x5fa3ff : b.color;
    const atmo = new THREE.Mesh(
      new THREE.SphereGeometry(b.size * 1.06, 32, 32),
      new THREE.MeshBasicMaterial({ color: atmoColor, transparent: true, opacity: 0.12, blending: THREE.AdditiveBlending, side: THREE.BackSide })
    );
    mesh.add(atmo);
  }

  // Saturn's rings
  if (b.rings) {
    const ring = buildSaturnRings(b.size);
    ring.rotation.x = Math.PI / 2;
    mesh.add(ring);
  }

  // pick-helper: invisible larger sphere so small bodies are easy to hover
  const pick = new THREE.Mesh(
    new THREE.SphereGeometry(Math.max(b.size * 1.8, 1.4), 12, 12),
    new THREE.MeshBasicMaterial({ visible: false })
  );
  pick.userData.key = b.key;
  mesh.add(pick);

  planets.push({ data: b, pivot, mesh, pick, angle: pivot.rotation.y });
}

function buildSaturnRings(planetSize) {
  const inner = planetSize * 1.35;
  const outer = planetSize * 2.4;
  const geo = new THREE.RingGeometry(inner, outer, 128, 4);
  // remap UVs radially so the texture gradient runs across the ring width
  const pos = geo.attributes.position;
  const uv = geo.attributes.uv;
  const v = new THREE.Vector3();
  for (let i = 0; i < pos.count; i++) {
    v.fromBufferAttribute(pos, i);
    const r = v.length();
    const t = (r - inner) / (outer - inner);
    uv.setXY(i, t, 0.5);
  }
  // ring texture: banded translucency
  const { c, ctx } = makeCanvas(256, 8);
  for (let x = 0; x < 256; x++) {
    const t = x / 256;
    // gaps (Cassini-like) at certain radii
    let a = 0.85;
    if (t > 0.45 && t < 0.52) a = 0.1;        // Cassini division
    if (t > 0.7 && t < 0.73) a = 0.25;
    const shade = 200 - Math.sin(t * 40) * 30 - t * 40;
    ctx.fillStyle = `rgba(${shade + 20},${shade},${shade - 40},${a})`;
    ctx.fillRect(x, 0, 1, 8);
  }
  const tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  const mat = new THREE.MeshBasicMaterial({
    map: tex, side: THREE.DoubleSide, transparent: true, opacity: 0.9, depthWrite: false,
  });
  return new THREE.Mesh(geo, mat);
}

/* =========================================================================
   HUD — legend, controls, info card, interactions
   ========================================================================= */
const legendList = document.getElementById("legend-list");
const card = document.getElementById("info-card");
const speedInput = document.getElementById("speed");
const speedReadout = document.getElementById("speed-readout");
const playBtn = document.getElementById("toggle-play");
const playLabel = document.getElementById("toggle-label");

let playing = true;
let speedMul = 1;
let focusedKey = null;

// Build legend
for (const b of BODIES) {
  const li = document.createElement("li");
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "legend__item";
  btn.dataset.key = b.key;
  btn.innerHTML = `
    <span class="legend__dot" style="color:${hex(b.color)}"></span>
    <span class="legend__name">${b.name}</span>
    <span class="legend__meta">${b.period.replace(/ days| years/, (m) => m.trim()[0])}</span>`;
  btn.addEventListener("click", () => focusBody(b.key));
  li.appendChild(btn);
  legendList.appendChild(li);
}
const legendButtons = [...legendList.querySelectorAll(".legend__item")];

// Play / pause
function setPlaying(state) {
  playing = state;
  playBtn.setAttribute("aria-pressed", String(state));
  playLabel.textContent = state ? "Pause" : "Play";
  controls.autoRotate = state && !reduceMotion && !focusedKey;
}
playBtn.addEventListener("click", () => setPlaying(!playing));

// Speed slider
function updateSpeed() {
  speedMul = parseFloat(speedInput.value);
  speedReadout.textContent = speedMul.toFixed(2) + "×";
  const pct = (speedMul / parseFloat(speedInput.max)) * 100;
  speedInput.style.setProperty("--fill", pct + "%");
}
speedInput.addEventListener("input", updateSpeed);
updateSpeed();

// Info card population
function showCard(b) {
  card.querySelector("#card-swatch").style.color = hex(b.color);
  card.querySelector("#card-swatch").style.background =
    `radial-gradient(circle at 35% 30%, ${hex(mixHex(b.color, 0xffffff, 0.4))}, ${hex(b.color)} 60%, ${hex(mixHex(b.color, 0x000000, 0.4))})`;
  card.querySelector("#card-name").textContent = b.name;
  card.querySelector("#card-tag").textContent = b.tag;
  card.querySelector("#card-diameter").textContent = b.diameter;
  card.querySelector("#card-distance").textContent = b.auDistance;
  card.querySelector("#card-period").textContent = b.period;
  card.querySelector("#card-fact").textContent = b.fact;
  card.hidden = false;
  requestAnimationFrame(() => card.classList.add("is-visible"));
}
function hideCard() {
  card.classList.remove("is-visible");
  setTimeout(() => { if (!card.classList.contains("is-visible")) card.hidden = true; }, 360);
}
document.getElementById("card-close").addEventListener("click", () => {
  unfocus();
});

// Focus / unfocus a body (click from legend or canvas)
function focusBody(key) {
  const p = planets.find((pl) => pl.data.key === key);
  if (!p) return;
  focusedKey = key;
  showCard(p.data);
  legendButtons.forEach((el) => el.classList.toggle("is-active", el.dataset.key === key));
  controls.autoRotate = false;
}
function unfocus() {
  focusedKey = null;
  hideCard();
  legendButtons.forEach((el) => el.classList.remove("is-active"));
  controls.autoRotate = playing && !reduceMotion;
}

/* ----- Raycasting for hover + click on the canvas ----- */
const raycaster = new THREE.Raycaster();
const pointer = new THREE.Vector2();
let hovered = null;
const pickTargets = planets.map((p) => p.pick);

function updatePointer(e) {
  const rect = renderer.domElement.getBoundingClientRect();
  pointer.x = ((e.clientX - rect.left) / rect.width) * 2 - 1;
  pointer.y = -((e.clientY - rect.top) / rect.height) * 2 + 1;
}

function pickAt() {
  raycaster.setFromCamera(pointer, camera);
  const hits = raycaster.intersectObjects(pickTargets, false);
  return hits.length ? hits[0].object.userData.key : null;
}

let moved = false;
renderer.domElement.addEventListener("pointerdown", () => { moved = false; });
renderer.domElement.addEventListener("pointermove", (e) => {
  moved = true;
  updatePointer(e);
  const key = pickAt();
  if (key !== hovered) {
    hovered = key;
    renderer.domElement.style.cursor = key ? "pointer" : "grab";
    // hover preview only if nothing is pinned via click
    if (!focusedKey) {
      if (key) {
        const b = planets.find((p) => p.data.key === key).data;
        showCard(b);
      } else {
        hideCard();
      }
    }
  }
});
renderer.domElement.addEventListener("pointerup", (e) => {
  if (moved) return; // ignore drags
  updatePointer(e);
  const key = pickAt();
  if (key) focusBody(key);
  else unfocus();
});
renderer.domElement.style.cursor = "grab";

// keyboard: Escape clears focus
window.addEventListener("keydown", (e) => {
  if (e.key === "Escape") unfocus();
  if (e.key === " " && e.target === document.body) { e.preventDefault(); setPlaying(!playing); }
});

/* =========================================================================
   RESIZE
   ========================================================================= */
function onResize() {
  camera.aspect = window.innerWidth / window.innerHeight;
  camera.updateProjectionMatrix();
  renderer.setSize(window.innerWidth, window.innerHeight);
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
}
window.addEventListener("resize", onResize);

/* =========================================================================
   ANIMATION LOOP
   ========================================================================= */
const clock = new THREE.Clock();
const BASE_SPEED = 0.12; // global tempo for orbital motion

function animate() {
  requestAnimationFrame(animate);
  const dt = Math.min(clock.getDelta(), 0.05);
  const t = clock.elapsedTime;

  if (playing && !reduceMotion) {
    for (const p of planets) {
      p.angle += p.data.speed * BASE_SPEED * speedMul * dt;
      p.pivot.rotation.y = p.angle;
      p.mesh.rotation.y += p.data.spin * speedMul * (dt * 60);
    }
    // sun shimmer + slow self rotation
    sunMesh.rotation.y += 0.0012 * (dt * 60);
  } else if (reduceMotion) {
    // static layout but keep a gentle planet self-spin off; sun still
  }

  // sun corona pulse (subtle, runs even when paused but very slow)
  const pulse = 1 + Math.sin(t * 0.9) * (reduceMotion ? 0 : 0.06);
  coronaPulse.scale.set(SUN_RADIUS * 6.2 * pulse, SUN_RADIUS * 6.2 * pulse, 1);
  corona.material.opacity = 0.2 + (reduceMotion ? 0 : Math.sin(t * 1.3) * 0.04);

  // slow starfield drift for parallax
  if (!reduceMotion) starfield.rotation.y += 0.00006 * (dt * 60);

  controls.update();
  renderer.render(scene, camera);
}

/* =========================================================================
   KICKOFF
   ========================================================================= */
function start() {
  animate();
  const loader = document.getElementById("loader");
  // give the first frame a beat to paint, then reveal
  setTimeout(() => loader.classList.add("is-hidden"), 350);
}

// Ensure WebGL is actually available before starting
if (renderer && renderer.getContext()) {
  start();
} else {
  document.getElementById("loader").innerHTML =
    '<p class="loader__text">WebGL is unavailable in this browser.</p>';
}
