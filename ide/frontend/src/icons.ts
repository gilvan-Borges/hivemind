// Material Symbols (Google) — nomes validados contra a tabela oficial de codepoints.
// A fonte é embutida via @fontsource/material-symbols-outlined (funciona offline).
const ms = (name: string, size = 15, fill = false) =>
  `<span class="ms${fill ? " fill" : ""}" style="font-size:${size}px">${name}</span>`;

const svg = (d: string, size = 15, extra = "") =>
  `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" ${extra}>${d}</svg>`;

export const icons = {
  brand: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <defs>
      <linearGradient id="hm-hex" x1="3" y1="2" x2="21" y2="22" gradientUnits="userSpaceOnUse">
        <stop offset="0%" stop-color="#c4b5fd"/>
        <stop offset="100%" stop-color="#7c5cff"/>
      </linearGradient>
    </defs>
    <path d="M12 2.4L16.3 4.9V9.8L12 12.2L7.7 9.8V4.9L12 2.4Z" stroke="url(#hm-hex)" stroke-width="1.55" stroke-linejoin="round"/>
    <path d="M7.7 9.8L12 12.2V17.1L7.7 19.6L3.4 17.1V12.2L7.7 9.8Z" stroke="url(#hm-hex)" stroke-width="1.55" stroke-linejoin="round"/>
    <path d="M16.3 9.8L20.6 12.2V17.1L16.3 19.6L12 17.1V12.2L16.3 9.8Z" stroke="url(#hm-hex)" stroke-width="1.55" stroke-linejoin="round"/>
    <line x1="12" y1="4.8" x2="12" y2="12.2" stroke="#a78bfa" stroke-width="1.2"/>
    <line x1="5.6" y1="15.8" x2="12" y2="12.2" stroke="#a78bfa" stroke-width="1.2"/>
    <line x1="18.4" y1="15.8" x2="12" y2="12.2" stroke="#a78bfa" stroke-width="1.2"/>
    <circle cx="12" cy="12.2" r="2.3" fill="#4c8dff"/>
    <circle cx="12" cy="2.9" r="1.2" fill="#e9d5ff"/>
    <circle cx="3.9" cy="16.8" r="1.2" fill="#e9d5ff"/>
    <circle cx="20.1" cy="16.8" r="1.2" fill="#e9d5ff"/>
  </svg>`,
  brandHero: `<svg width="44" height="44" viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <rect x="1" y="1" width="22" height="22" rx="6" fill="#0d101a" stroke="rgba(167,139,250,0.35)" stroke-width="1"/>
    <path d="M12 4.2L15.6 6.3V10.4L12 12.4L8.4 10.4V6.3L12 4.2Z" stroke="#a78bfa" stroke-width="1.35" stroke-linejoin="round"/>
    <path d="M8.4 10.4L12 12.4V16.5L8.4 18.6L4.8 16.5V12.4L8.4 10.4Z" stroke="#8b5cf6" stroke-width="1.35" stroke-linejoin="round"/>
    <path d="M15.6 10.4L19.2 12.4V16.5L15.6 18.6L12 16.5V12.4L15.6 10.4Z" stroke="#8b5cf6" stroke-width="1.35" stroke-linejoin="round"/>
    <line x1="12" y1="6.3" x2="12" y2="12.4" stroke="#a78bfa" stroke-width="1.05"/>
    <line x1="6.7" y1="15.5" x2="12" y2="12.4" stroke="#a78bfa" stroke-width="1.05"/>
    <line x1="17.3" y1="15.5" x2="12" y2="12.4" stroke="#a78bfa" stroke-width="1.05"/>
    <circle cx="12" cy="12.4" r="2.0" fill="#4c8dff"/>
    <circle cx="12" cy="4.6" r="1.0" fill="#e9d5ff"/>
    <circle cx="5.2" cy="16.3" r="1.0" fill="#e9d5ff"/>
    <circle cx="18.8" cy="16.3" r="1.0" fill="#e9d5ff"/>
  </svg>`,
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
  moreHoriz: ms("more_horiz", 15),
  undo: ms("undo", 13),
  minus: ms("remove", 13),
  openInNew: ms("open_in_new", 13),
  arrowDown: ms("arrow_downward", 13),
  arrowUp: ms("arrow_upward", 13),
  sync: ms("sync", 13),
  target: ms("adjust", 12),
  cloud: ms("cloud", 12),
  aiUsage: ms("auto_awesome", 18, true),
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
