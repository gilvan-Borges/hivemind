import { createWriteStream, readFileSync, readdirSync, statSync } from "node:fs";
import { cp, mkdir, copyFile } from "node:fs/promises";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { deflateRawSync } from "node:zlib";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const dist = join(root, "dist");
const zipPath = join(root, "hivemind-chrome-extension.zip");

await mkdir(dist, { recursive: true });
await copyFile(join(root, "manifest.json"), join(dist, "manifest.json"));
await copyFile(join(root, "src", "sidepanel.html"), join(dist, "sidepanel.html"));
await copyFile(join(root, "src", "sidepanel.css"), join(dist, "sidepanel.css"));
await cp(join(root, "..", "..", "assets", "brand", "hivemind-icon.png"), join(dist, "icon.png"));

const files = [];
function walk(dir) {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) walk(path);
    else files.push(path);
  }
}
walk(dist);
files.sort();

const parts = [];
const central = [];
let offset = 0;
for (const path of files) {
  const name = relative(dist, path).replaceAll("\\", "/");
  const data = readFileSync(path);
  const compressed = deflateRawSync(data);
  const nameBytes = Buffer.from(name);
  const local = Buffer.alloc(30);
  local.writeUInt32LE(0x04034b50, 0);
  local.writeUInt16LE(20, 4);
  local.writeUInt16LE(0, 6);
  local.writeUInt16LE(8, 8);
  local.writeUInt16LE(0, 10);
  local.writeUInt16LE(0, 12);
  local.writeUInt32LE(crc32(data), 14);
  local.writeUInt32LE(compressed.length, 18);
  local.writeUInt32LE(data.length, 22);
  local.writeUInt16LE(nameBytes.length, 26);
  local.writeUInt16LE(0, 28);
  const cent = Buffer.alloc(46);
  cent.writeUInt32LE(0x02014b50, 0);
  cent.writeUInt16LE(20, 4);
  cent.writeUInt16LE(20, 6);
  cent.writeUInt16LE(0, 8);
  cent.writeUInt16LE(8, 10);
  cent.writeUInt32LE(crc32(data), 16);
  cent.writeUInt32LE(compressed.length, 20);
  cent.writeUInt32LE(data.length, 24);
  cent.writeUInt16LE(nameBytes.length, 28);
  cent.writeUInt32LE(offset, 42);
  parts.push(local, nameBytes, compressed);
  central.push(cent, nameBytes);
  offset += local.length + nameBytes.length + compressed.length;
}
const centralBuf = Buffer.concat(central);
const end = Buffer.alloc(22);
end.writeUInt32LE(0x06054b50, 0);
end.writeUInt16LE(files.length, 8);
end.writeUInt16LE(files.length, 10);
end.writeUInt32LE(centralBuf.length, 12);
end.writeUInt32LE(offset, 16);
await new Promise((resolve, reject) => {
  const out = createWriteStream(zipPath);
  out.on("error", reject);
  out.on("finish", resolve);
  out.end(Buffer.concat([...parts, centralBuf, end]));
});
console.log(zipPath);

function crc32(buf) {
  let c = ~0;
  for (const byte of buf) {
    c ^= byte;
    for (let i = 0; i < 8; i++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1));
  }
  return ~c >>> 0;
}
