// Material Symbols (Google) — nomes validados contra a tabela oficial de codepoints.
// A fonte é embutida via @fontsource/material-symbols-outlined (funciona offline).
const ms = (name: string, size = 15, fill = false) =>
  `<span class="ms${fill ? " fill" : ""}" style="font-size:${size}px">${name}</span>`;

const svg = (d: string, size = 15, extra = "") =>
  `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" ${extra}>${d}</svg>`;

export const icons = {
  brand: `<img class="brand-mark" src="/brand/hivemind-icon.png" width="18" height="18" alt="" />`,
  brandHero: `<img class="brand-hero-mark" src="/brand/hivemind-icon.png" width="72" height="72" alt="" />`,
  // Activity bar: outline (estilo traço fino, próximo do Lucide do IPG) sem sair de Material Symbols (D6).
  explorer: ms("folder_open", 17),
  team: ms("groups", 17),
  swarm: ms("hub", 17),
  gitBranch: ms("alt_route", 17),
  gitSmall: ms("alt_route", 12),
  settings: ms("settings", 17),
  search: ms("search", 13),
  searchLarge: ms("search", 17),
  refresh: ms("refresh", 13),
  folderClosed: ms("folder", 15, true),
  folderOpen: ms("folder_open", 15, true),
  chevronRight: ms("chevron_right", 14),
  chevronDown: ms("expand_more", 14),
  plus: ms("add", 13),
  trash: ms("delete", 13),
  send: ms("send", 13),
  stop: ms("stop", 12, true),
  play: ms("play_arrow", 12, true),
  bolt: ms("bolt", 12),
  sparkle: ms("auto_awesome", 12),
  plan: ms("checklist", 12),
  memory: ms("memory", 13),
  command: ms("keyboard_command_key", 13),
  dot: `<svg width="8" height="8" viewBox="0 0 12 12"><circle cx="6" cy="6" r="4.5" fill="currentColor"/></svg>`,
  check: ms("check", 13),
  spinner: svg(`<path d="M21 12a9 9 0 1 1-6.219-8.56"/>`, 13, `class="spin"`),
  sidebarLeft: ms("left_panel_close", 15),
  sidebarRight: ms("right_panel_close", 15),
  expand: ms("open_in_full", 15),
  commit: ms("commit", 13),
  pr: ms("merge_type", 13),
  closeSmall: ms("close", 12),
  winMin: `<svg width="11" height="11" viewBox="0 0 12 12"><line x1="2" y1="6" x2="10" y2="6" stroke="currentColor" stroke-width="1.3"/></svg>`,
  winMax: `<svg width="11" height="11" viewBox="0 0 12 12" fill="none"><rect x="2.2" y="2.2" width="7.6" height="7.6" stroke="currentColor" stroke-width="1.2"/></svg>`,
  winClose: `<svg width="11" height="11" viewBox="0 0 12 12"><line x1="2.2" y1="2.2" x2="9.8" y2="9.8" stroke="currentColor" stroke-width="1.3"/><line x1="9.8" y1="2.2" x2="2.2" y2="9.8" stroke="currentColor" stroke-width="1.3"/></svg>`,
  save: ms("save", 13),
  image: ms("image", 14),
  terminal: ms("terminal", 14),
  sessions: ms("history", 17),
  skills: ms("extension", 17),
  // Antes: hub (colidia com swarm). dns = servidores MCP.
  mcp: ms("dns", 17),
  download: ms("download", 13),
  // Antes: bolt (colidia com bolt). generating_tokens = consumo.
  tokens: ms("generating_tokens", 12),
  editPen: ms("edit", 12),
  newFile: ms("note_add", 13),
  newFolder: ms("create_new_folder", 13),
  arrowRight: ms("arrow_right", 11),
  fileDefault: ms("description", 13),
  moreHoriz: ms("more_horiz", 15),
  undo: ms("undo", 13),
  minus: ms("remove", 13),
  openInNew: ms("open_in_new", 13),
  arrowDown: ms("arrow_downward", 13),
  arrowUp: ms("arrow_upward", 13),
  sync: ms("sync", 13),
  target: ms("adjust", 12),
  cloud: ms("cloud", 12),
  // Antes: auto_awesome (colidia com sparkle). monitoring = uso & limites.
  aiUsage: ms("monitoring", 18),
};

export function fileBadge(name: string): string {
  const low = name.toLowerCase();
  const ext = low.includes(".") ? (low.split(".").pop() ?? "") : "";
  const map: Record<string, [string, string]> = {
    go: ["GO", "#22d3ee"],
    java: ["JV", "#f59e0b"],
    kt: ["KT", "var(--accent)"],
    ts: ["TS", "#3b82f6"],
    mts: ["TS", "#3b82f6"],
    tsx: ["TX", "#38bdf8"],
    js: ["JS", "#eab308"],
    mjs: ["JS", "#eab308"],
    cjs: ["JS", "#eab308"],
    jsx: ["JX", "#facc15"],
    json: ["{}", "#fbbf24"],
    yaml: ["YM", "#f472b6"],
    yml: ["YM", "#f472b6"],
    toml: ["TM", "var(--accent)"],
    xml: ["XM", "#fb923c"],
    md: ["MD", "#38bdf8"],
    mdx: ["MD", "#38bdf8"],
    css: ["CS", "var(--accent)"],
    scss: ["SC", "#f472b6"],
    less: ["LS", "var(--accent)"],
    html: ["HT", "#f97316"],
    sql: ["SQ", "var(--accent)"],
    cmd: [">_", "#f97316"],
    bat: [">_", "#f97316"],
    ps1: [">_", "#38bdf8"],
    sh: [">_", "#34d399"],
    py: ["PY", "#facc15"],
    rs: ["RS", "#fb923c"],
    iss: ["IS", "#60a5fa"],
    svg: ["SV", "#fbbf24"],
    png: ["IM", "#2dd4bf"],
    jpg: ["IM", "#2dd4bf"],
    ico: ["IC", "#2dd4bf"],
    env: ["EN", "#fbbf24"],
    lock: ["LK", "#64748b"],
    gitignore: ["GI", "#fb7185"],
  };
  const hit = map[ext] || (low.startsWith(".git") ? map["gitignore"] : undefined);
  if (hit) {
    return `<span class="file-ext-badge" style="color:${hit[1]}">${hit[0]}</span>`;
  }
  return `<span class="file-ext-icon">${icons.fileDefault}</span>`;
}
