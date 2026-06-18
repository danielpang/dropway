// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// Interactive Periodic Table — client-side logic and full inline element dataset.
"use strict";

/*
 * Each element:
 *  n  = atomic number
 *  s  = symbol
 *  name
 *  mass = standard atomic weight (u). Bracketed values use the most stable isotope.
 *  cat  = category key (see CATEGORIES)
 *  xpos = grid column (1..18)
 *  ypos = grid row (1..7 main; lanthanides/actinides handled separately)
 *  ec   = electron configuration
 *  state= solid | liquid | gas | unknown (at 0 deg C / standard, room temperature ~298K)
 *  blurb= short description
 */
const ELEMENTS = [
  { n:1,  s:"H",  name:"Hydrogen",     mass:"1.008",   cat:"nonmetal",   xpos:1,  ypos:1, ec:"1s1", state:"gas",     blurb:"The lightest and most abundant element in the universe, fuel of the stars." },
  { n:2,  s:"He", name:"Helium",       mass:"4.0026",  cat:"noble",      xpos:18, ypos:1, ec:"1s2", state:"gas",     blurb:"An inert noble gas, the second most abundant element, lifts balloons and cools magnets." },
  { n:3,  s:"Li", name:"Lithium",      mass:"6.94",    cat:"alkali",     xpos:1,  ypos:2, ec:"[He] 2s1", state:"solid", blurb:"A soft, silvery alkali metal central to rechargeable batteries and mood medicine." },
  { n:4,  s:"Be", name:"Beryllium",    mass:"9.0122",  cat:"alkaline",   xpos:2,  ypos:2, ec:"[He] 2s2", state:"solid", blurb:"A stiff, lightweight metal used in aerospace and X-ray windows; toxic as dust." },
  { n:5,  s:"B",  name:"Boron",        mass:"10.81",   cat:"metalloid",  xpos:13, ypos:2, ec:"[He] 2s2 2p1", state:"solid", blurb:"A metalloid essential to borosilicate glass and plant life." },
  { n:6,  s:"C",  name:"Carbon",       mass:"12.011",  cat:"nonmetal",   xpos:14, ypos:2, ec:"[He] 2s2 2p2", state:"solid", blurb:"The backbone of all known life, with forms from diamond to graphene." },
  { n:7,  s:"N",  name:"Nitrogen",     mass:"14.007",  cat:"nonmetal",   xpos:15, ypos:2, ec:"[He] 2s2 2p3", state:"gas",   blurb:"Makes up 78% of Earth's air; vital to proteins and explosives alike." },
  { n:8,  s:"O",  name:"Oxygen",       mass:"15.999",  cat:"nonmetal",   xpos:16, ypos:2, ec:"[He] 2s2 2p4", state:"gas",   blurb:"The breath of life and the great oxidizer that rusts and burns." },
  { n:9,  s:"F",  name:"Fluorine",     mass:"18.998",  cat:"halogen",    xpos:17, ypos:2, ec:"[He] 2s2 2p5", state:"gas",   blurb:"The most reactive element, a pale corrosive gas, tamed into toothpaste." },
  { n:10, s:"Ne", name:"Neon",         mass:"20.180",  cat:"noble",      xpos:18, ypos:2, ec:"[He] 2s2 2p6", state:"gas",   blurb:"A noble gas that glows orange-red in the iconic signs that bear its name." },
  { n:11, s:"Na", name:"Sodium",       mass:"22.990",  cat:"alkali",     xpos:1,  ypos:3, ec:"[Ne] 3s1", state:"solid", blurb:"A reactive alkali metal; half of table salt and a bright yellow flame." },
  { n:12, s:"Mg", name:"Magnesium",    mass:"24.305",  cat:"alkaline",   xpos:2,  ypos:3, ec:"[Ne] 3s2", state:"solid", blurb:"A light structural metal that burns brilliant white and sits at the heart of chlorophyll." },
  { n:13, s:"Al", name:"Aluminium",    mass:"26.982",  cat:"post",       xpos:13, ypos:3, ec:"[Ne] 3s2 3p1", state:"solid", blurb:"The most abundant metal in Earth's crust, light and endlessly recyclable." },
  { n:14, s:"Si", name:"Silicon",      mass:"28.085",  cat:"metalloid",  xpos:14, ypos:3, ec:"[Ne] 3s2 3p2", state:"solid", blurb:"The metalloid of sand and silicon chips, the foundation of the digital age." },
  { n:15, s:"P",  name:"Phosphorus",   mass:"30.974",  cat:"nonmetal",   xpos:15, ypos:3, ec:"[Ne] 3s2 3p3", state:"solid", blurb:"Essential to DNA and bone; its white form glows and ignites in air." },
  { n:16, s:"S",  name:"Sulfur",       mass:"32.06",   cat:"nonmetal",   xpos:16, ypos:3, ec:"[Ne] 3s2 3p4", state:"solid", blurb:"The yellow brimstone of volcanoes, key to proteins and sulfuric acid." },
  { n:17, s:"Cl", name:"Chlorine",     mass:"35.45",   cat:"halogen",    xpos:17, ypos:3, ec:"[Ne] 3s2 3p5", state:"gas",   blurb:"A green-yellow halogen that disinfects water and forms common salt." },
  { n:18, s:"Ar", name:"Argon",        mass:"39.95",   cat:"noble",      xpos:18, ypos:3, ec:"[Ne] 3s2 3p6", state:"gas",   blurb:"An inert noble gas filling light bulbs and shielding welds." },
  { n:19, s:"K",  name:"Potassium",    mass:"39.098",  cat:"alkali",     xpos:1,  ypos:4, ec:"[Ar] 4s1", state:"solid", blurb:"A reactive alkali metal vital to nerves; gives bananas their trace radioactivity." },
  { n:20, s:"Ca", name:"Calcium",      mass:"40.078",  cat:"alkaline",   xpos:2,  ypos:4, ec:"[Ar] 4s2", state:"solid", blurb:"The metal of bones, teeth, and limestone, and a bright orange flame." },
  { n:21, s:"Sc", name:"Scandium",     mass:"44.956",  cat:"transition", xpos:3,  ypos:4, ec:"[Ar] 3d1 4s2", state:"solid", blurb:"A rare transition metal that strengthens aluminium alloys for aerospace." },
  { n:22, s:"Ti", name:"Titanium",     mass:"47.867",  cat:"transition", xpos:4,  ypos:4, ec:"[Ar] 3d2 4s2", state:"solid", blurb:"Strong as steel but far lighter and corrosion-proof; favored for implants and jets." },
  { n:23, s:"V",  name:"Vanadium",     mass:"50.942",  cat:"transition", xpos:5,  ypos:4, ec:"[Ar] 3d3 4s2", state:"solid", blurb:"A hard transition metal that toughens steel and tints minerals in vivid hues." },
  { n:24, s:"Cr", name:"Chromium",     mass:"51.996",  cat:"transition", xpos:6,  ypos:4, ec:"[Ar] 3d5 4s1", state:"solid", blurb:"The shine of chrome plating and the color of emeralds and rubies." },
  { n:25, s:"Mn", name:"Manganese",    mass:"54.938",  cat:"transition", xpos:7,  ypos:4, ec:"[Ar] 3d5 4s2", state:"solid", blurb:"Essential for hardening steel and for oxygen production in photosynthesis." },
  { n:26, s:"Fe", name:"Iron",         mass:"55.845",  cat:"transition", xpos:8,  ypos:4, ec:"[Ar] 3d6 4s2", state:"solid", blurb:"The metal of civilization and the core of our planet; carries oxygen in our blood." },
  { n:27, s:"Co", name:"Cobalt",       mass:"58.933",  cat:"transition", xpos:9,  ypos:4, ec:"[Ar] 3d7 4s2", state:"solid", blurb:"Gives glass and ceramics a deep blue and powers high-performance batteries." },
  { n:28, s:"Ni", name:"Nickel",       mass:"58.693",  cat:"transition", xpos:10, ypos:4, ec:"[Ar] 3d8 4s2", state:"solid", blurb:"A tough, corrosion-resistant metal in coins, stainless steel, and batteries." },
  { n:29, s:"Cu", name:"Copper",       mass:"63.546",  cat:"transition", xpos:11, ypos:4, ec:"[Ar] 3d10 4s1", state:"solid", blurb:"The reddish conductor of wiring and the patina-green of weathered statues." },
  { n:30, s:"Zn", name:"Zinc",         mass:"65.38",   cat:"transition", xpos:12, ypos:4, ec:"[Ar] 3d10 4s2", state:"solid", blurb:"Galvanizes steel against rust and is essential to immune function." },
  { n:31, s:"Ga", name:"Gallium",      mass:"69.723",  cat:"post",       xpos:13, ypos:4, ec:"[Ar] 3d10 4s2 4p1", state:"solid", blurb:"A metal that melts in your hand and powers LEDs and fast electronics." },
  { n:32, s:"Ge", name:"Germanium",    mass:"72.630",  cat:"metalloid",  xpos:14, ypos:4, ec:"[Ar] 3d10 4s2 4p2", state:"solid", blurb:"A metalloid semiconductor used in fiber optics and infrared optics." },
  { n:33, s:"As", name:"Arsenic",      mass:"74.922",  cat:"metalloid",  xpos:15, ypos:4, ec:"[Ar] 3d10 4s2 4p3", state:"solid", blurb:"An infamous poison and metalloid, also a key semiconductor dopant." },
  { n:34, s:"Se", name:"Selenium",     mass:"78.971",  cat:"nonmetal",   xpos:16, ypos:4, ec:"[Ar] 3d10 4s2 4p4", state:"solid", blurb:"An essential trace nutrient whose conductivity changes with light." },
  { n:35, s:"Br", name:"Bromine",      mass:"79.904",  cat:"halogen",    xpos:17, ypos:4, ec:"[Ar] 3d10 4s2 4p5", state:"liquid", blurb:"One of only two elements liquid at room temperature; a fuming red-brown halogen." },
  { n:36, s:"Kr", name:"Krypton",      mass:"83.798",  cat:"noble",      xpos:18, ypos:4, ec:"[Ar] 3d10 4s2 4p6", state:"gas", blurb:"A noble gas used in high-performance lighting and once defined the metre." },
  { n:37, s:"Rb", name:"Rubidium",     mass:"85.468",  cat:"alkali",     xpos:1,  ypos:5, ec:"[Kr] 5s1", state:"solid", blurb:"A soft, highly reactive alkali metal used in atomic clocks and research." },
  { n:38, s:"Sr", name:"Strontium",    mass:"87.62",   cat:"alkaline",   xpos:2,  ypos:5, ec:"[Kr] 5s2", state:"solid", blurb:"Burns brilliant red in fireworks and flares." },
  { n:39, s:"Y",  name:"Yttrium",      mass:"88.906",  cat:"transition", xpos:3,  ypos:5, ec:"[Kr] 4d1 5s2", state:"solid", blurb:"Key to LED phosphors, superconductors, and strong ceramics." },
  { n:40, s:"Zr", name:"Zirconium",    mass:"91.224",  cat:"transition", xpos:4,  ypos:5, ec:"[Kr] 4d2 5s2", state:"solid", blurb:"Corrosion-resistant and transparent to neutrons; lines nuclear reactor fuel." },
  { n:41, s:"Nb", name:"Niobium",      mass:"92.906",  cat:"transition", xpos:5,  ypos:5, ec:"[Kr] 4d4 5s1", state:"solid", blurb:"A superconductor essential to MRI magnets and particle accelerators." },
  { n:42, s:"Mo", name:"Molybdenum",   mass:"95.95",   cat:"transition", xpos:6,  ypos:5, ec:"[Kr] 4d5 5s1", state:"solid", blurb:"Strengthens steel at high temperatures and is vital to enzymes." },
  { n:43, s:"Tc", name:"Technetium",   mass:"[98]",    cat:"transition", xpos:7,  ypos:5, ec:"[Kr] 4d5 5s2", state:"solid", blurb:"The first artificially produced element; its isotopes image the human body." },
  { n:44, s:"Ru", name:"Ruthenium",    mass:"101.07",  cat:"transition", xpos:8,  ypos:5, ec:"[Kr] 4d7 5s1", state:"solid", blurb:"A hard platinum-group metal that hardens alloys and catalyzes reactions." },
  { n:45, s:"Rh", name:"Rhodium",      mass:"102.91",  cat:"transition", xpos:9,  ypos:5, ec:"[Kr] 4d8 5s1", state:"solid", blurb:"A rare, lustrous catalyst in vehicle exhaust converters; among the priciest metals." },
  { n:46, s:"Pd", name:"Palladium",    mass:"106.42",  cat:"transition", xpos:10, ypos:5, ec:"[Kr] 4d10", state:"solid", blurb:"Soaks up hydrogen like a sponge and cleans exhaust as a catalyst." },
  { n:47, s:"Ag", name:"Silver",       mass:"107.87",  cat:"transition", xpos:11, ypos:5, ec:"[Kr] 4d10 5s1", state:"solid", blurb:"The best electrical conductor of all metals; prized in coinage and photography." },
  { n:48, s:"Cd", name:"Cadmium",      mass:"112.41",  cat:"transition", xpos:12, ypos:5, ec:"[Kr] 4d10 5s2", state:"solid", blurb:"A toxic soft metal once common in pigments and rechargeable batteries." },
  { n:49, s:"In", name:"Indium",       mass:"114.82",  cat:"post",       xpos:13, ypos:5, ec:"[Kr] 4d10 5s2 5p1", state:"solid", blurb:"A soft metal whose oxide coats touchscreens and solar cells." },
  { n:50, s:"Sn", name:"Tin",          mass:"118.71",  cat:"post",       xpos:14, ypos:5, ec:"[Kr] 4d10 5s2 5p2", state:"solid", blurb:"Half of bronze and the solder of electronics; emits a 'cry' when bent." },
  { n:51, s:"Sb", name:"Antimony",     mass:"121.76",  cat:"metalloid",  xpos:15, ypos:5, ec:"[Kr] 4d10 5s2 5p3", state:"solid", blurb:"A brittle metalloid used in flame retardants and ancient cosmetics." },
  { n:52, s:"Te", name:"Tellurium",    mass:"127.60",  cat:"metalloid",  xpos:16, ypos:5, ec:"[Kr] 4d10 5s2 5p4", state:"solid", blurb:"A rare metalloid in solar panels and alloys; gives the breath a garlic odor." },
  { n:53, s:"I",  name:"Iodine",       mass:"126.90",  cat:"halogen",    xpos:17, ypos:5, ec:"[Kr] 4d10 5s2 5p5", state:"solid", blurb:"A purple-vapored halogen essential to the thyroid; antiseptic in tincture." },
  { n:54, s:"Xe", name:"Xenon",        mass:"131.29",  cat:"noble",      xpos:18, ypos:5, ec:"[Kr] 4d10 5s2 5p6", state:"gas", blurb:"A heavy noble gas used in bright lamps, ion thrusters, and anesthesia." },
  { n:55, s:"Cs", name:"Caesium",      mass:"132.91",  cat:"alkali",     xpos:1,  ypos:6, ec:"[Xe] 6s1", state:"solid", blurb:"The most reactive metal and the heartbeat of the atomic clock that defines the second." },
  { n:56, s:"Ba", name:"Barium",       mass:"137.33",  cat:"alkaline",   xpos:2,  ypos:6, ec:"[Xe] 6s2", state:"solid", blurb:"A dense metal whose compounds glow green and coat the gut for X-rays." },
  { n:57, s:"La", name:"Lanthanum",    mass:"138.91",  cat:"lanthanide", xpos:3,  ypos:9, ec:"[Xe] 5d1 6s2", state:"solid", blurb:"The namesake of the lanthanides, used in camera lenses and battery alloys." },
  { n:58, s:"Ce", name:"Cerium",       mass:"140.12",  cat:"lanthanide", xpos:4,  ypos:9, ec:"[Xe] 4f1 5d1 6s2", state:"solid", blurb:"The most abundant rare earth; the spark of lighter flints and a polishing agent." },
  { n:59, s:"Pr", name:"Praseodymium", mass:"140.91",  cat:"lanthanide", xpos:5,  ypos:9, ec:"[Xe] 4f3 6s2", state:"solid", blurb:"Colors glass a vivid green and strengthens powerful magnets." },
  { n:60, s:"Nd", name:"Neodymium",    mass:"144.24",  cat:"lanthanide", xpos:6,  ypos:9, ec:"[Xe] 4f4 6s2", state:"solid", blurb:"The metal behind the strongest permanent magnets in motors and headphones." },
  { n:61, s:"Pm", name:"Promethium",   mass:"[145]",   cat:"lanthanide", xpos:7,  ypos:9, ec:"[Xe] 4f5 6s2", state:"solid", blurb:"A radioactive rare earth once used in glow-in-the-dark paint and nuclear batteries." },
  { n:62, s:"Sm", name:"Samarium",     mass:"150.36",  cat:"lanthanide", xpos:8,  ypos:9, ec:"[Xe] 4f6 6s2", state:"solid", blurb:"Forms heat-resistant magnets and absorbs neutrons in reactors." },
  { n:63, s:"Eu", name:"Europium",     mass:"151.96",  cat:"lanthanide", xpos:9,  ypos:9, ec:"[Xe] 4f7 6s2", state:"solid", blurb:"The red and blue phosphor that lit older televisions and secures banknotes." },
  { n:64, s:"Gd", name:"Gadolinium",   mass:"157.25",  cat:"lanthanide", xpos:10, ypos:9, ec:"[Xe] 4f7 5d1 6s2", state:"solid", blurb:"Sharpens MRI images and absorbs neutrons exceptionally well." },
  { n:65, s:"Tb", name:"Terbium",      mass:"158.93",  cat:"lanthanide", xpos:11, ypos:9, ec:"[Xe] 4f9 6s2", state:"solid", blurb:"Produces green phosphors and materials that change shape in magnetic fields." },
  { n:66, s:"Dy", name:"Dysprosium",   mass:"162.50",  cat:"lanthanide", xpos:12, ypos:9, ec:"[Xe] 4f10 6s2", state:"solid", blurb:"Keeps high-performance magnets working at high temperatures." },
  { n:67, s:"Ho", name:"Holmium",      mass:"164.93",  cat:"lanthanide", xpos:13, ypos:9, ec:"[Xe] 4f11 6s2", state:"solid", blurb:"Has the strongest magnetic moment of any element and powers surgical lasers." },
  { n:68, s:"Er", name:"Erbium",       mass:"167.26",  cat:"lanthanide", xpos:14, ypos:9, ec:"[Xe] 4f12 6s2", state:"solid", blurb:"Amplifies light in the fiber-optic cables that carry the internet." },
  { n:69, s:"Tm", name:"Thulium",      mass:"168.93",  cat:"lanthanide", xpos:15, ypos:9, ec:"[Xe] 4f13 6s2", state:"solid", blurb:"The rarest stable rare earth, used in portable X-ray devices and lasers." },
  { n:70, s:"Yb", name:"Ytterbium",    mass:"173.05",  cat:"lanthanide", xpos:16, ypos:9, ec:"[Xe] 4f14 6s2", state:"solid", blurb:"Powers some of the most precise atomic clocks ever built." },
  { n:71, s:"Lu", name:"Lutetium",     mass:"174.97",  cat:"lanthanide", xpos:17, ypos:9, ec:"[Xe] 4f14 5d1 6s2", state:"solid", blurb:"The hardest and densest lanthanide, used in PET scanners and catalysis." },
  { n:72, s:"Hf", name:"Hafnium",      mass:"178.49",  cat:"transition", xpos:4,  ypos:6, ec:"[Xe] 4f14 5d2 6s2", state:"solid", blurb:"Absorbs neutrons in reactor control rods and insulates microchips." },
  { n:73, s:"Ta", name:"Tantalum",     mass:"180.95",  cat:"transition", xpos:5,  ypos:6, ec:"[Xe] 4f14 5d3 6s2", state:"solid", blurb:"A corrosion-proof metal in tiny capacitors and biocompatible implants." },
  { n:74, s:"W",  name:"Tungsten",     mass:"183.84",  cat:"transition", xpos:6,  ypos:6, ec:"[Xe] 4f14 5d4 6s2", state:"solid", blurb:"Has the highest melting point of all metals; the glow of old light bulbs." },
  { n:75, s:"Re", name:"Rhenium",      mass:"186.21",  cat:"transition", xpos:7,  ypos:6, ec:"[Xe] 4f14 5d5 6s2", state:"solid", blurb:"One of the rarest and highest-melting metals, vital to jet engine alloys." },
  { n:76, s:"Os", name:"Osmium",       mass:"190.23",  cat:"transition", xpos:8,  ypos:6, ec:"[Xe] 4f14 5d6 6s2", state:"solid", blurb:"The densest naturally occurring element; tips fountain pens and instruments." },
  { n:77, s:"Ir", name:"Iridium",      mass:"192.22",  cat:"transition", xpos:9,  ypos:6, ec:"[Xe] 4f14 5d7 6s2", state:"solid", blurb:"Extremely corrosion-resistant; its global layer marks the dinosaur-killing impact." },
  { n:78, s:"Pt", name:"Platinum",     mass:"195.08",  cat:"transition", xpos:10, ypos:6, ec:"[Xe] 4f14 5d9 6s1", state:"solid", blurb:"A precious, unreactive catalyst in jewelry, electronics, and exhaust systems." },
  { n:79, s:"Au", name:"Gold",         mass:"196.97",  cat:"transition", xpos:11, ypos:6, ec:"[Xe] 4f14 5d10 6s1", state:"solid", blurb:"The unchanging measure of wealth; soft, dense, and prized for millennia." },
  { n:80, s:"Hg", name:"Mercury",      mass:"200.59",  cat:"transition", xpos:12, ypos:6, ec:"[Xe] 4f14 5d10 6s2", state:"liquid", blurb:"The only metal liquid at room temperature; quicksilver of old thermometers." },
  { n:81, s:"Tl", name:"Thallium",     mass:"204.38",  cat:"post",       xpos:13, ypos:6, ec:"[Xe] 4f14 5d10 6s2 6p1", state:"solid", blurb:"A soft, highly toxic metal once used in rat poison and now in electronics." },
  { n:82, s:"Pb", name:"Lead",         mass:"207.2",   cat:"post",       xpos:14, ypos:6, ec:"[Xe] 4f14 5d10 6s2 6p2", state:"solid", blurb:"Dense, soft, and toxic; shielded radiation and plumbed Rome's pipes." },
  { n:83, s:"Bi", name:"Bismuth",      mass:"208.98",  cat:"post",       xpos:15, ypos:6, ec:"[Xe] 4f14 5d10 6s2 6p3", state:"solid", blurb:"Forms dazzling iridescent crystals; a heavy yet largely nontoxic metal in medicine." },
  { n:84, s:"Po", name:"Polonium",     mass:"[209]",   cat:"post",       xpos:16, ypos:6, ec:"[Xe] 4f14 5d10 6s2 6p4", state:"solid", blurb:"An intensely radioactive metal discovered by Marie Curie, named for Poland." },
  { n:85, s:"At", name:"Astatine",     mass:"[210]",   cat:"halogen",    xpos:17, ypos:6, ec:"[Xe] 4f14 5d10 6s2 6p5", state:"solid", blurb:"The rarest naturally occurring element; intensely radioactive and barely studied." },
  { n:86, s:"Rn", name:"Radon",        mass:"[222]",   cat:"noble",      xpos:18, ypos:6, ec:"[Xe] 4f14 5d10 6s2 6p6", state:"gas", blurb:"A radioactive noble gas that seeps from rock and poses an indoor hazard." },
  { n:87, s:"Fr", name:"Francium",     mass:"[223]",   cat:"alkali",     xpos:1,  ypos:7, ec:"[Rn] 7s1", state:"solid", blurb:"An exceedingly rare, radioactive alkali metal; only atoms have ever been seen." },
  { n:88, s:"Ra", name:"Radium",       mass:"[226]",   cat:"alkaline",   xpos:2,  ypos:7, ec:"[Rn] 7s2", state:"solid", blurb:"A glowing radioactive metal once painted on watch dials, with tragic consequences." },
  { n:89, s:"Ac", name:"Actinium",     mass:"[227]",   cat:"actinide",   xpos:3,  ypos:10, ec:"[Rn] 6d1 7s2", state:"solid", blurb:"The namesake of the actinides; glows pale blue and is used in cancer therapy." },
  { n:90, s:"Th", name:"Thorium",      mass:"232.04",  cat:"actinide",   xpos:4,  ypos:10, ec:"[Rn] 6d2 7s2", state:"solid", blurb:"A weakly radioactive metal proposed as a safer nuclear fuel." },
  { n:91, s:"Pa", name:"Protactinium", mass:"231.04",  cat:"actinide",   xpos:5,  ypos:10, ec:"[Rn] 5f2 6d1 7s2", state:"solid", blurb:"A rare, dense radioactive metal with few uses beyond research." },
  { n:92, s:"U",  name:"Uranium",      mass:"238.03",  cat:"actinide",   xpos:6,  ypos:10, ec:"[Rn] 5f3 6d1 7s2", state:"solid", blurb:"The heaviest natural element in bulk; fuel of reactors and the atomic age." },
  { n:93, s:"Np", name:"Neptunium",    mass:"[237]",   cat:"actinide",   xpos:7,  ypos:10, ec:"[Rn] 5f4 6d1 7s2", state:"solid", blurb:"The first synthetic transuranium element, a byproduct of reactors." },
  { n:94, s:"Pu", name:"Plutonium",    mass:"[244]",   cat:"actinide",   xpos:8,  ypos:10, ec:"[Rn] 5f6 7s2", state:"solid", blurb:"A radioactive metal that fuels weapons, reactors, and deep-space probes." },
  { n:95, s:"Am", name:"Americium",    mass:"[243]",   cat:"actinide",   xpos:9,  ypos:10, ec:"[Rn] 5f7 7s2", state:"solid", blurb:"A synthetic element found in ordinary smoke detectors." },
  { n:96, s:"Cm", name:"Curium",       mass:"[247]",   cat:"actinide",   xpos:10, ypos:10, ec:"[Rn] 5f7 6d1 7s2", state:"solid", blurb:"A radioactive element named for the Curies; powers some space instruments." },
  { n:97, s:"Bk", name:"Berkelium",    mass:"[247]",   cat:"actinide",   xpos:11, ypos:10, ec:"[Rn] 5f9 7s2", state:"solid", blurb:"A synthetic element made in vanishing amounts, used to discover heavier ones." },
  { n:98, s:"Cf", name:"Californium",  mass:"[251]",   cat:"actinide",   xpos:12, ypos:10, ec:"[Rn] 5f10 7s2", state:"solid", blurb:"A potent neutron source used to start reactors and scan for gold and oil." },
  { n:99, s:"Es", name:"Einsteinium",  mass:"[252]",   cat:"actinide",   xpos:13, ypos:10, ec:"[Rn] 5f11 7s2", state:"solid", blurb:"Discovered in the debris of the first hydrogen bomb; purely for research." },
  { n:100,s:"Fm", name:"Fermium",      mass:"[257]",   cat:"actinide",   xpos:14, ypos:10, ec:"[Rn] 5f12 7s2", state:"solid", blurb:"The heaviest element formed by neutron capture; exists only in trace amounts." },
  { n:101,s:"Md", name:"Mendelevium",  mass:"[258]",   cat:"actinide",   xpos:15, ypos:10, ec:"[Rn] 5f13 7s2", state:"solid", blurb:"Named for Mendeleev, father of the periodic table; made one atom at a time." },
  { n:102,s:"No", name:"Nobelium",     mass:"[259]",   cat:"actinide",   xpos:16, ypos:10, ec:"[Rn] 5f14 7s2", state:"solid", blurb:"A synthetic element honoring Alfred Nobel, studied atom by atom." },
  { n:103,s:"Lr", name:"Lawrencium",   mass:"[266]",   cat:"actinide",   xpos:17, ypos:10, ec:"[Rn] 5f14 7s2 7p1", state:"solid", blurb:"The last actinide, named for cyclotron inventor Ernest Lawrence." },
  { n:104,s:"Rf", name:"Rutherfordium",mass:"[267]",   cat:"transition", xpos:4,  ypos:7, ec:"[Rn] 5f14 6d2 7s2", state:"unknown", blurb:"A superheavy element named for Ernest Rutherford; exists for mere seconds." },
  { n:105,s:"Db", name:"Dubnium",      mass:"[268]",   cat:"transition", xpos:5,  ypos:7, ec:"[Rn] 5f14 6d3 7s2", state:"unknown", blurb:"A fleeting synthetic element named for the Dubna research center." },
  { n:106,s:"Sg", name:"Seaborgium",   mass:"[269]",   cat:"transition", xpos:6,  ypos:7, ec:"[Rn] 5f14 6d4 7s2", state:"unknown", blurb:"Named for Glenn Seaborg, one of few elements named for a living person at the time." },
  { n:107,s:"Bh", name:"Bohrium",      mass:"[270]",   cat:"transition", xpos:7,  ypos:7, ec:"[Rn] 5f14 6d5 7s2", state:"unknown", blurb:"A superheavy element named for Niels Bohr; only a handful of atoms made." },
  { n:108,s:"Hs", name:"Hassium",      mass:"[269]",   cat:"transition", xpos:8,  ypos:7, ec:"[Rn] 5f14 6d6 7s2", state:"unknown", blurb:"Named for the German state of Hesse; among the densest elements predicted." },
  { n:109,s:"Mt", name:"Meitnerium",   mass:"[278]",   cat:"unknown",    xpos:9,  ypos:7, ec:"[Rn] 5f14 6d7 7s2", state:"unknown", blurb:"Named for Lise Meitner, co-discoverer of nuclear fission; properties unknown." },
  { n:110,s:"Ds", name:"Darmstadtium", mass:"[281]",   cat:"unknown",    xpos:10, ypos:7, ec:"[Rn] 5f14 6d8 7s2", state:"unknown", blurb:"Named for Darmstadt, Germany, where it was first synthesized." },
  { n:111,s:"Rg", name:"Roentgenium",  mass:"[282]",   cat:"unknown",    xpos:11, ypos:7, ec:"[Rn] 5f14 6d9 7s2", state:"unknown", blurb:"Named for Wilhelm Roentgen, discoverer of X-rays; extremely short-lived." },
  { n:112,s:"Cn", name:"Copernicium",  mass:"[285]",   cat:"unknown",    xpos:12, ypos:7, ec:"[Rn] 5f14 6d10 7s2", state:"unknown", blurb:"Named for Copernicus; predicted to be a volatile, possibly gaseous metal." },
  { n:113,s:"Nh", name:"Nihonium",     mass:"[286]",   cat:"unknown",    xpos:13, ypos:7, ec:"[Rn] 5f14 6d10 7s2 7p1", state:"unknown", blurb:"The first element discovered in Asia, named for Japan (Nihon)." },
  { n:114,s:"Fl", name:"Flerovium",    mass:"[289]",   cat:"unknown",    xpos:14, ypos:7, ec:"[Rn] 5f14 6d10 7s2 7p2", state:"unknown", blurb:"Named for physicist Georgy Flerov; sits near a predicted island of stability." },
  { n:115,s:"Mc", name:"Moscovium",    mass:"[290]",   cat:"unknown",    xpos:15, ypos:7, ec:"[Rn] 5f14 6d10 7s2 7p3", state:"unknown", blurb:"Named for the Moscow region; only a few atoms have ever existed." },
  { n:116,s:"Lv", name:"Livermorium",  mass:"[293]",   cat:"unknown",    xpos:16, ypos:7, ec:"[Rn] 5f14 6d10 7s2 7p4", state:"unknown", blurb:"Named for the Lawrence Livermore National Laboratory in California." },
  { n:117,s:"Ts", name:"Tennessine",   mass:"[294]",   cat:"unknown",    xpos:17, ypos:7, ec:"[Rn] 5f14 6d10 7s2 7p5", state:"unknown", blurb:"Named for Tennessee; the second-heaviest known element, sitting among the halogens." },
  { n:118,s:"Og", name:"Oganesson",    mass:"[294]",   cat:"unknown",    xpos:18, ypos:7, ec:"[Rn] 5f14 6d10 7s2 7p6", state:"unknown", blurb:"The heaviest known element, named for Yuri Oganessian; nominally a noble gas." }
];

const CATEGORIES = {
  alkali:     "Alkali metal",
  alkaline:   "Alkaline earth metal",
  transition: "Transition metal",
  post:       "Post-transition metal",
  metalloid:  "Metalloid",
  nonmetal:   "Reactive nonmetal",
  halogen:    "Halogen",
  noble:      "Noble gas",
  lanthanide: "Lanthanide",
  actinide:   "Actinide",
  unknown:    "Unknown properties"
};

const STATES = {
  solid:   "Solid",
  liquid:  "Liquid",
  gas:     "Gas",
  unknown: "Unknown"
};

// ---- DOM references ----
const tableEl   = document.getElementById("table");
const legendEl  = document.getElementById("legend");
const searchEl  = document.getElementById("search");
const colorModeEl = document.getElementById("colorMode");
const clearBtn  = document.getElementById("clearFilters");
const modal     = document.getElementById("modal");
const modalCard = document.getElementById("modalCard");
const statusEl  = document.getElementById("status");

let activeCategories = new Set();   // empty = show all
let lastFocused = null;             // element to restore focus to after modal close

// ---- Build legend ----
function buildLegend() {
  for (const [key, label] of Object.entries(CATEGORIES)) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "legend-item";
    btn.dataset.cat = key;
    btn.setAttribute("aria-pressed", "false");
    btn.innerHTML =
      `<span class="legend-swatch cat-${key}" aria-hidden="true"></span>` +
      `<span class="legend-label">${label}</span>`;
    btn.addEventListener("click", () => toggleCategory(key, btn));
    legendEl.appendChild(btn);
  }
}

// ---- Build table cells ----
function buildTable() {
  const frag = document.createDocumentFragment();

  for (const el of ELEMENTS) {
    const cell = document.createElement("button");
    cell.type = "button";
    cell.className = `cell cat-${el.cat} state-${el.state}`;
    cell.style.gridColumn = el.xpos;
    cell.style.gridRow = el.ypos;
    cell.dataset.n = el.n;
    cell.dataset.cat = el.cat;
    cell.dataset.state = el.state;
    cell.setAttribute("aria-label",
      `${el.name}, symbol ${el.s}, atomic number ${el.n}, ${CATEGORIES[el.cat]}`);
    cell.innerHTML =
      `<span class="cell-n">${el.n}</span>` +
      `<span class="cell-s">${el.s}</span>` +
      `<span class="cell-name">${el.name}</span>` +
      `<span class="cell-mass">${el.mass}</span>`;
    cell.addEventListener("click", () => openModal(el, cell));
    frag.appendChild(cell);
  }

  // Decorative connector labels marking where the f-block detaches.
  const laMark = makeMarker("57-71", 3, 6);
  const acMark = makeMarker("89-103", 3, 7);
  frag.appendChild(laMark);
  frag.appendChild(acMark);

  tableEl.appendChild(frag);
}

function makeMarker(text, col, row) {
  const m = document.createElement("div");
  m.className = "marker";
  m.style.gridColumn = col;
  m.style.gridRow = row;
  m.setAttribute("aria-hidden", "true");
  m.textContent = text;
  return m;
}

// ---- Category filtering ----
function toggleCategory(key, btn) {
  if (activeCategories.has(key)) {
    activeCategories.delete(key);
    btn.setAttribute("aria-pressed", "false");
  } else {
    activeCategories.add(key);
    btn.setAttribute("aria-pressed", "true");
  }
  applyFilters();
}

// ---- Search + filter application ----
function applyFilters() {
  const q = searchEl.value.trim().toLowerCase();
  const cells = tableEl.querySelectorAll(".cell");
  let matchCount = 0;
  const filtersActive = q !== "" || activeCategories.size > 0;

  cells.forEach((cell) => {
    const el = ELEMENTS[Number(cell.dataset.n) - 1];
    const catOk = activeCategories.size === 0 || activeCategories.has(el.cat);
    const searchOk = q === "" ||
      el.name.toLowerCase().includes(q) ||
      el.s.toLowerCase() === q ||
      el.s.toLowerCase().startsWith(q) ||
      String(el.n) === q;

    const match = catOk && searchOk;
    cell.classList.toggle("dimmed", filtersActive && !match);
    cell.classList.toggle("match", q !== "" && searchOk && catOk);
    if (match && filtersActive) matchCount++;
  });

  if (!filtersActive) {
    setStatus("");
  } else {
    setStatus(`${matchCount} element${matchCount === 1 ? "" : "s"} match`);
  }

  clearBtn.hidden = !filtersActive;
}

function setStatus(msg) {
  statusEl.textContent = msg;
}

// ---- Color mode (category vs state) ----
function setColorMode(mode) {
  tableEl.dataset.colorMode = mode;
  legendEl.dataset.colorMode = mode;
  buildLegendForMode(mode);
}

function buildLegendForMode(mode) {
  legendEl.innerHTML = "";
  if (mode === "state") {
    for (const [key, label] of Object.entries(STATES)) {
      const item = document.createElement("div");
      item.className = "legend-item static";
      item.innerHTML =
        `<span class="legend-swatch st-${key}" aria-hidden="true"></span>` +
        `<span class="legend-label">${label}</span>`;
      legendEl.appendChild(item);
    }
    // Clear any category filter when switching to state coloring.
    activeCategories.clear();
    applyFilters();
  } else {
    buildLegend();
    // Re-mark previously active categories.
    activeCategories.forEach((key) => {
      const btn = legendEl.querySelector(`.legend-item[data-cat="${key}"]`);
      if (btn) btn.setAttribute("aria-pressed", "true");
    });
  }
}

// ---- Modal ----
function openModal(el, triggerCell) {
  lastFocused = triggerCell || document.activeElement;
  modalCard.className = `modal-card cat-${el.cat}`;
  modalCard.innerHTML = `
    <button class="modal-close" type="button" aria-label="Close">&times;</button>
    <div class="modal-head">
      <div class="modal-symbol">
        <span class="modal-n">${el.n}</span>
        <span class="modal-s">${el.s}</span>
        <span class="modal-m">${el.mass}</span>
      </div>
      <div class="modal-title">
        <h2 id="modalTitle">${el.name}</h2>
        <p class="modal-cat">${CATEGORIES[el.cat]} &middot; ${STATES[el.state]} at room temp</p>
      </div>
    </div>
    <p class="modal-blurb">${el.blurb}</p>
    <dl class="modal-facts">
      <div><dt>Atomic number</dt><dd>${el.n}</dd></div>
      <div><dt>Atomic mass</dt><dd>${el.mass} u</dd></div>
      <div><dt>Category</dt><dd>${CATEGORIES[el.cat]}</dd></div>
      <div><dt>Standard state</dt><dd>${STATES[el.state]}</dd></div>
      <div class="span2"><dt>Electron configuration</dt><dd class="mono">${el.ec}</dd></div>
    </dl>
  `;
  modalCard.querySelector(".modal-close").addEventListener("click", closeModal);

  modal.hidden = false;
  // Force reflow so the transition runs.
  void modal.offsetWidth;
  modal.classList.add("open");
  document.body.classList.add("modal-open");
  modalCard.querySelector(".modal-close").focus();
}

function closeModal() {
  modal.classList.remove("open");
  document.body.classList.remove("modal-open");
  const finish = () => {
    modal.hidden = true;
    modal.removeEventListener("transitionend", finish);
  };
  // Respect reduced motion: if no transition, hide immediately.
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    finish();
  } else {
    modal.addEventListener("transitionend", finish);
  }
  if (lastFocused && document.contains(lastFocused)) lastFocused.focus();
}

// ---- Event wiring ----
function init() {
  buildTable();
  setColorMode("category");

  searchEl.addEventListener("input", applyFilters);

  colorModeEl.addEventListener("change", (e) => {
    setColorMode(e.target.value);
  });

  clearBtn.addEventListener("click", () => {
    searchEl.value = "";
    activeCategories.clear();
    legendEl.querySelectorAll(".legend-item[data-cat]").forEach((b) =>
      b.setAttribute("aria-pressed", "false"));
    applyFilters();
    searchEl.focus();
  });

  // Modal backdrop click (only when clicking the backdrop, not the card).
  modal.addEventListener("click", (e) => {
    if (e.target === modal) closeModal();
  });

  // Keyboard: Escape closes modal; "/" focuses search.
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !modal.hidden) {
      closeModal();
    } else if (e.key === "/" && document.activeElement !== searchEl) {
      e.preventDefault();
      searchEl.focus();
    }
  });
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", init);
} else {
  init();
}
