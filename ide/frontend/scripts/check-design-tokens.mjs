import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const stylesPath = path.resolve(__dirname, "../src/styles.css");
const css = fs.readFileSync(stylesPath, "utf8");

const rootMatch = css.match(/:root\s*\{([\s\S]*?)\}/);
if (!rootMatch) {
  console.error("ERRO: bloco :root não encontrado em src/styles.css");
  process.exit(1);
}

const tokens = new Map();
for (const line of rootMatch[1].split(/\r?\n/)) {
  const m = line.match(/^\s*(--[a-z0-9-]+)\s*:\s*([^;]+);/i);
  if (m) tokens.set(m[1], m[2].trim());
}

const requiredTokens = [
  "--surface-page",
  "--surface-panel",
  "--surface-card",
  "--surface-elevated",
  "--surface-input",
  "--text-primary",
  "--text-body",
  "--text-secondary",
  "--text-tertiary",
  "--action-primary",
  "--action-primary-text",
  "--accent",
  "--accent-strong",
  "--color-danger",
  "--color-danger-text",
  "--color-success",
  "--color-warning",
  "--color-info",
  "--color-info-text",
];

let failed = false;
for (const name of requiredTokens) {
  if (!tokens.has(name)) {
    console.error(`ERRO: token obrigatório ausente em :root -> ${name}`);
    failed = true;
  }
}

function resolveHex(nameOrHex, depth = 0) {
  if (depth > 5) return null;
  const v = nameOrHex.startsWith("--") ? tokens.get(nameOrHex) : nameOrHex;
  if (!v) return null;
  const varMatch = v.match(/^var\((--[a-z0-9-]+)\)$/i);
  if (varMatch) return resolveHex(varMatch[1], depth + 1);
  const hexMatch = v.match(/^#([0-9a-f]{6})$/i);
  return hexMatch ? `#${hexMatch[1].toUpperCase()}` : null;
}

function luminance(hex) {
  const r = parseInt(hex.slice(1, 3), 16) / 255;
  const g = parseInt(hex.slice(3, 5), 16) / 255;
  const b = parseInt(hex.slice(5, 7), 16) / 255;
  const conv = (c) => (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4));
  return 0.2126 * conv(r) + 0.7152 * conv(g) + 0.0722 * conv(b);
}

function contrastRatio(fgHex, bgHex) {
  const l1 = luminance(fgHex);
  const l2 = luminance(bgHex);
  const [hi, lo] = l1 >= l2 ? [l1, l2] : [l2, l1];
  return (hi + 0.05) / (lo + 0.05);
}

const contrastPairs = [
  ["--text-primary", "--surface-card", 4.5],
  ["--text-body", "--surface-card", 4.5],
  ["--text-secondary", "--surface-card", 4.5],
  ["--text-tertiary", "--surface-card", 4.5],
  ["--text-primary", "--surface-elevated", 4.5],
  ["--text-secondary", "--surface-elevated", 4.5],
  ["--action-primary-text", "--action-primary", 4.5],
  ["--accent-strong", "--surface-card", 4.5],
  ["--color-danger-text", "--surface-card", 4.5],
  ["--color-success", "--surface-card", 4.5],
  ["--color-warning", "--surface-card", 4.5],
  ["--color-info-text", "--surface-card", 4.5],
];

for (const [fg, bg, min] of contrastPairs) {
  const fgHex = resolveHex(fg);
  const bgHex = resolveHex(bg);
  if (!fgHex || !bgHex) {
    console.error(`ERRO: não foi possível resolver hex para ${fg} (${fgHex}) sobre ${bg} (${bgHex})`);
    failed = true;
    continue;
  }
  const ratio = contrastRatio(fgHex, bgHex);
  if (ratio < min) {
    console.error(`ERRO WCAG: ${fg} (${fgHex}) sobre ${bg} (${bgHex}) = ${ratio.toFixed(2)}:1 (< ${min}:1)`);
    failed = true;
  }
}

if (failed) process.exit(1);
console.log(`check-design-tokens OK: ${requiredTokens.length} tokens presentes, ${contrastPairs.length} pares WCAG >= 4.5:1 verificados.`);
