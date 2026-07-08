/**
 * Minimal, dependency-free ZIP writer (STORE only — no compression) for the
 * dashboard's "download skill" affordance: skill files are small (≤ 5 MiB per
 * skill by the server's cap) and mostly markdown, so shipping them uncompressed
 * in a structurally-simple archive beats pulling in a zip dependency. Produces
 * a standard ZIP v2.0 archive (local file headers + central directory + EOCD)
 * that every OS archiver opens.
 */

const textEncoder = new TextEncoder();

// Standard CRC-32 (IEEE 802.3), table-driven.
const crcTable = (() => {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[n] = c >>> 0;
  }
  return table;
})();

export function crc32(data: Uint8Array): number {
  let crc = 0xffffffff;
  for (let i = 0; i < data.length; i++) {
    crc = (crcTable[(crc ^ (data[i] ?? 0)) & 0xff] ?? 0) ^ (crc >>> 8);
  }
  return (crc ^ 0xffffffff) >>> 0;
}

export interface ZipEntry {
  /** Forward-slash relative path inside the archive. */
  path: string;
  data: Uint8Array;
}

/** Build a STORE-only ZIP archive from entries. */
export function buildZip(entries: ZipEntry[]): Uint8Array {
  const chunks: Uint8Array[] = [];
  const central: Uint8Array[] = [];
  let offset = 0;

  for (const entry of entries) {
    const name = textEncoder.encode(entry.path);
    const crc = crc32(entry.data);
    const size = entry.data.length;

    // Local file header (30 bytes + name), then the stored bytes.
    const local = new DataView(new ArrayBuffer(30));
    local.setUint32(0, 0x04034b50, true); // signature
    local.setUint16(4, 20, true); // version needed
    local.setUint16(6, 0x0800, true); // flags: UTF-8 names
    local.setUint16(8, 0, true); // method: STORE
    local.setUint16(10, 0, true); // mod time
    local.setUint16(12, 0, true); // mod date
    local.setUint32(14, crc, true);
    local.setUint32(18, size, true); // compressed (== stored)
    local.setUint32(22, size, true); // uncompressed
    local.setUint16(26, name.length, true);
    local.setUint16(28, 0, true); // extra length
    chunks.push(new Uint8Array(local.buffer), name, entry.data);

    // Matching central-directory record (46 bytes + name).
    const cen = new DataView(new ArrayBuffer(46));
    cen.setUint32(0, 0x02014b50, true);
    cen.setUint16(4, 20, true); // version made by
    cen.setUint16(6, 20, true); // version needed
    cen.setUint16(8, 0x0800, true);
    cen.setUint16(10, 0, true);
    cen.setUint16(12, 0, true);
    cen.setUint16(14, 0, true);
    cen.setUint32(16, crc, true);
    cen.setUint32(20, size, true);
    cen.setUint32(24, size, true);
    cen.setUint16(28, name.length, true);
    // extra/comment/disk/attrs all zero.
    cen.setUint32(42, offset, true); // local header offset
    central.push(new Uint8Array(cen.buffer), name);

    offset += 30 + name.length + size;
  }

  const centralSize = central.reduce((n, c) => n + c.length, 0);
  const eocd = new DataView(new ArrayBuffer(22));
  eocd.setUint32(0, 0x06054b50, true);
  eocd.setUint16(8, entries.length, true); // entries on this disk
  eocd.setUint16(10, entries.length, true); // total entries
  eocd.setUint32(12, centralSize, true);
  eocd.setUint32(16, offset, true); // central directory offset
  chunks.push(...central, new Uint8Array(eocd.buffer));

  const total = chunks.reduce((n, c) => n + c.length, 0);
  const out = new Uint8Array(total);
  let pos = 0;
  for (const c of chunks) {
    out.set(c, pos);
    pos += c.length;
  }
  return out;
}
