// Material Symbols (Google) — nomes validados contra a tabela oficial de codepoints.
// A fonte é embutida via @fontsource/material-symbols-outlined (funciona offline).
const ms = (name: string, size = 15, fill = false) =>
  `<span class="ms${fill ? " fill" : ""}" style="font-size:${size}px">${name}</span>`;

const svg = (d: string, size = 15, extra = "") =>
  `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" ${extra}>${d}</svg>`;

export const icons = {
  brand: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none"><path d="M12 2L20.5 7V17L12 22L3.5 17V7L12 2Z" stroke="#c9ced6" stroke-width="1.9" fill="rgba(255,255,255,0.08)"/><path d="M12 7.5L16 9.8V14.2L12 16.5L8 14.2V9.8L12 7.5Z" fill="#c9ced6"/></svg>`,
  explorer: ms("folder_open", 17, true),
  team: ms("groups", 17, true),
  swarm: ms("hub", 17, true),
  gitBranch: ms("alt_route", 17, true),
  gitSmall: ms("alt_route", 12),
  settings: ms("settings", 17, true),
  search: ms("search", 13),
  searchLarge: ms("search", 17, true),
  refresh: ms("refresh", 13),
  folderClosed: ms("folder", 14),
  folderOpen: ms("folder_open", 14),
  chevronRight: ms("chevron_right", 12),
  chevronDown: ms("expand_more", 12),
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
  skills: ms("extension", 17, true),
  download: ms("download", 13),
  tokens: ms("bolt", 12),
  editPen: ms("edit", 12),
  newFile: ms("note_add", 13),
  newFolder: ms("create_new_folder", 13),
  arrowRight: ms("arrow_right", 11),
  fileDefault: ms("description", 13),
};

export function fileBadge(name: string): string {
  const ext = name.split(".").pop()?.toLowerCase() ?? "";
  const map: Record<string, [string, string]> = {
    go: ["GO", "#9cc7d4"],
    java: ["JV", "#cfa47e"],
    ts: ["TS", "#a8b4c6"],
    tsx: ["TX", "#a8b4c6"],
    js: ["JS", "#cbb56a"],
    jsx: ["JX", "#cbb56a"],
    json: ["{}", "#cbb56a"],
    yaml: ["YM", "#9cc0cc"],
    yml: ["YM", "#9cc0cc"],
    xml: ["XM", "#c9a084"],
    md: ["MD", "#b9c0cb"],
    css: ["CS", "#9cc0cc"],
    html: ["HT", "#d99a9a"],
    sql: ["SQ", "#8fc9a8"],
    cmd: ["SH", "#9aa1ac"],
    sh: ["SH", "#9aa1ac"],
  };
  const hit = map[ext];
  if (hit) {
    return `<span class="file-ext-badge" style="color:${hit[1]}">${hit[0]}</span>`;
  }
  return `<span class="file-ext-icon">${icons.fileDefault}</span>`;
}
