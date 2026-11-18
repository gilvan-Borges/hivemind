const svg = (d: string, size = 15, extra = "") =>
  `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" ${extra}>${d}</svg>`;

export const icons = {
  brand: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none"><path d="M12 2L20.5 7V17L12 22L3.5 17V7L12 2Z" stroke="#a78bfa" stroke-width="1.9" fill="rgba(139,92,246,0.18)"/><path d="M12 7.5L16 9.8V14.2L12 16.5L8 14.2V9.8L12 7.5Z" fill="#a78bfa"/></svg>`,
  explorer: svg(`<path d="M3 7v10a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2h-6l-2-2H5a2 2 0 0 0-2 2z"/>`, 17),
  team: svg(`<path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>`, 17),
  swarm: svg(`<circle cx="12" cy="5" r="2.5"/><circle cx="5" cy="18" r="2.5"/><circle cx="19" cy="18" r="2.5"/><line x1="10.5" y1="7" x2="6.3" y2="15.8"/><line x1="13.5" y1="7" x2="17.7" y2="15.8"/><line x1="7.5" y1="18" x2="16.5" y2="18"/>`, 17),
  gitBranch: svg(`<circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M6 21V9a9 9 0 0 0 9 9"/>`, 17),
  gitSmall: svg(`<line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>`, 12),
  settings: svg(`<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>`, 17),
  search: svg(`<circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>`, 13),
  refresh: svg(`<polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>`, 13),
  folderClosed: svg(`<path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>`, 14),
  folderOpen: svg(`<path d="m6 14 1.5-2.9A2 2 0 0 1 9.24 10H20a2 2 0 0 1 1.94 2.5l-1.54 6a2 2 0 0 1-1.95 1.5H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H18a2 2 0 0 1 2 2v2"/>`, 14),
  chevronRight: svg(`<polyline points="9 18 15 12 9 6"/>`, 12),
  chevronDown: svg(`<polyline points="6 9 12 15 18 9"/>`, 12),
  plus: svg(`<line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>`, 13),
  trash: svg(`<polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/>`, 13),
  send: svg(`<line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/>`, 13),
  stop: svg(`<rect x="5" y="5" width="14" height="14" rx="2.5" fill="currentColor" stroke="none"/>`, 12),
  play: `<svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><polygon points="6 4 20 12 6 20 6 4"/></svg>`,
  bolt: svg(`<polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>`, 12),
  sparkle: svg(`<path d="m12 3-1.9 5.8a2 2 0 0 1-1.3 1.3L3 12l5.8 1.9a2 2 0 0 1 1.3 1.3L12 21l1.9-5.8a2 2 0 0 1 1.3-1.3L21 12l-5.8-1.9a2 2 0 0 1-1.3-1.3Z"/>`, 12),
  plan: svg(`<path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/>`, 12),
  memory: svg(`<path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/>`, 13),
  command: svg(`<path d="M18 3a3 3 0 0 0-3 3v12a3 3 0 0 0 3 3 3 3 0 0 0 3-3 3 3 0 0 0-3-3H6a3 3 0 0 0-3 3 3 3 0 0 0 3 3 3 3 0 0 0 3-3V6a3 3 0 0 0-3-3 3 3 0 0 0-3 3 3 3 0 0 0 3 3h12a3 3 0 0 0 3-3 3 3 0 0 0-3-3z"/>`, 12),
  dot: `<svg width="8" height="8" viewBox="0 0 12 12"><circle cx="6" cy="6" r="4.5" fill="currentColor"/></svg>`,
  check: svg(`<polyline points="20 6 9 17 4 12"/>`, 13),
  spinner: svg(`<path d="M21 12a9 9 0 1 1-6.219-8.56"/>`, 13, `class="spin"`),
  sidebarLeft: svg(`<rect x="3" y="3" width="18" height="18" rx="2"/><line x1="9" y1="3" x2="9" y2="21"/>`, 15),
  sidebarRight: svg(`<rect x="3" y="3" width="18" height="18" rx="2"/><line x1="15" y1="3" x2="15" y2="21"/>`, 15),
  commit: svg(`<circle cx="12" cy="12" r="3"/><line x1="3" y1="12" x2="9" y2="12"/><line x1="15" y1="12" x2="21" y2="12"/>`, 13),
  pr: svg(`<circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M13 6h3a2 2 0 0 1 2 2v7"/><line x1="6" y1="9" x2="6" y2="21"/>`, 13),
  closeSmall: svg(`<line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>`, 12),
  winMin: `<svg width="11" height="11" viewBox="0 0 12 12"><line x1="2" y1="6" x2="10" y2="6" stroke="currentColor" stroke-width="1.3"/></svg>`,
  winMax: `<svg width="11" height="11" viewBox="0 0 12 12" fill="none"><rect x="2.2" y="2.2" width="7.6" height="7.6" stroke="currentColor" stroke-width="1.2"/></svg>`,
  winClose: `<svg width="11" height="11" viewBox="0 0 12 12"><line x1="2.2" y1="2.2" x2="9.8" y2="9.8" stroke="currentColor" stroke-width="1.3"/><line x1="9.8" y1="2.2" x2="2.2" y2="9.8" stroke="currentColor" stroke-width="1.3"/></svg>`,
  save: svg(`<path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><polyline points="17 21 17 13 7 13 7 21"/><polyline points="7 3 7 8 15 8"/>`, 13),
  image: svg(`<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>`, 14),
  terminal: svg(`<polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/>`, 14),
  searchLarge: svg(`<circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>`, 17),
  sessions: svg(`<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/><line x1="9" y1="9" x2="15" y2="9"/><line x1="9" y1="13" x2="13" y2="13"/>`, 17),
  tokens: svg(`<polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>`, 12),
  editPen: svg(`<path d="M12 20h9"/><path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/>`, 12),
  newFile: svg(`<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="12" y1="18" x2="12" y2="12"/><line x1="9" y1="15" x2="15" y2="15"/>`, 13),
  newFolder: svg(`<path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><line x1="12" y1="11" x2="12" y2="17"/><line x1="9" y1="14" x2="15" y2="14"/>`, 13),
  arrowRight: svg(`<line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/>`, 11),
  fileDefault: svg(`<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/>`, 13),
};

export function fileBadge(name: string): string {
  const ext = name.split(".").pop()?.toLowerCase() ?? "";
  const map: Record<string, [string, string]> = {
    go: ["GO", "#60d5ff"],
    java: ["JV", "#f59e6b"],
    ts: ["TS", "#60a5fa"],
    tsx: ["TX", "#60a5fa"],
    js: ["JS", "#facc15"],
    jsx: ["JX", "#facc15"],
    json: ["{}", "#fbbf24"],
    yaml: ["YM", "#a78bfa"],
    yml: ["YM", "#a78bfa"],
    xml: ["XM", "#fb923c"],
    md: ["MD", "#c4b5fd"],
    css: ["CS", "#38bdf8"],
    html: ["HT", "#f87171"],
    sql: ["SQ", "#34d399"],
    cmd: ["SH", "#94a3b8"],
    sh: ["SH", "#94a3b8"],
  };
  const hit = map[ext];
  if (hit) {
    return `<span class="file-ext-badge" style="color:${hit[1]}">${hit[0]}</span>`;
  }
  return `<span class="file-ext-icon">${icons.fileDefault}</span>`;
}
