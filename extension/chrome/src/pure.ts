export const sensitiveWords = [
  "submit",
  "enviar",
  "comprar",
  "buy now",
  "buy",
  "pay",
  "pagar",
  "checkout",
  "purchase",
  "place order",
  "assinar",
  "subscribe",
  "transferir",
  "sacar",
  "withdraw",
  "confirmar compra",
  "excluir conta",
  "delete account",
];

export function isSensitiveLabel(text: string): boolean {
  const hay = text.toLowerCase();
  return sensitiveWords.some((word) => hay.includes(word));
}

export function redactSecrets(text: string): string {
  return text
    .replace(/bearer\s+\S+/gi, "Bearer [redigido]")
    .replace(/(token|password|passwd|secret|api[_-]?key)=([^&\s]+)/gi, "$1=[redigido]")
    .replace(/\b(ghp_|github_pat_|sk-|xoxb-|ya29\.|AIza)[A-Za-z0-9._-]+/g, "[redigido]");
}

export function hostOf(raw: string): string | null {
  try {
    const url = new URL(raw);
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    if (url.username || url.password) return null;
    const host = url.hostname.toLowerCase().replace(/\.$/, "");
    return host || null;
  } catch {
    return null;
  }
}

export function isLoopbackWebSocket(raw: string): boolean {
  try {
    const url = new URL(raw);
    if (url.protocol !== "ws:") return false;
    const host = url.hostname.toLowerCase();
    return host === "127.0.0.1" || host === "localhost" || host === "::1";
  } catch {
    return false;
  }
}

export function safeURL(raw: string): string {
  try {
    const url = new URL(raw);
    const secretKeys: string[] = [];
    url.searchParams.forEach((_value, key) => {
      if (/token|password|passwd|secret|key|auth/i.test(key)) secretKeys.push(key);
    });
    for (const key of secretKeys) url.searchParams.set(key, "[redigido]");
    return url.toString().slice(0, 180);
  } catch {
    return redactSecrets(raw).slice(0, 180);
  }
}

export interface AxNode {
  nodeId?: string;
  parentId?: string;
  role?: { value?: string };
  name?: { value?: string };
  backendDOMNodeId?: number;
  ignored?: boolean;
}

export interface PageRef {
  backendNodeId: number;
  role: string;
  name: string;
}

export interface ReadOpts {
  scope?: "interactive" | "all";
  depth?: number;
  rootBackend?: number;
  offset?: number;
  limit?: number;
  /** Nomes visíveis no viewport, em minúsculas. Lista vazia não filtra. */
  visible?: string[];
  keep?: Record<string, PageRef>;
  nameLimit?: number;
}

const skipRoles = new Set(["none", "generic", "InlineTextBox", "LineBreak", "Ignored", "presentation"]);

const interactiveRoles = new Set([
  "button",
  "link",
  "textbox",
  "searchbox",
  "combobox",
  "checkbox",
  "radio",
  "switch",
  "slider",
  "tab",
  "menuitem",
  "menuitemcheckbox",
  "menuitemradio",
  "option",
  "listbox",
  "spinbutton",
  "treeitem",
]);

const unnamedOk = new Set(["textbox", "searchbox", "combobox"]);

export function fnv32(text: string): string {
  let h = 2166136261;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return (h >>> 0).toString(16);
}

export function tokenCount(text: string): number {
  const bytes = new TextEncoder().encode(text).length;
  return Math.ceil(bytes / 4);
}

function depthsOf(nodes: AxNode[]): Map<string, number> {
  const byId = new Map<string, AxNode>();
  for (const node of nodes) {
    if (node.nodeId) byId.set(node.nodeId, node);
  }
  const memo = new Map<string, number>();
  const walk = (id: string, guard: number): number => {
    const cached = memo.get(id);
    if (cached !== undefined) return cached;
    const node = byId.get(id);
    if (!node?.parentId || guard > 140 || !byId.has(node.parentId)) {
      memo.set(id, 0);
      return 0;
    }
    const depth = walk(node.parentId, guard + 1) + 1;
    memo.set(id, depth);
    return depth;
  };
  for (const id of byId.keys()) walk(id, 0);
  return memo;
}

function descendantIds(nodes: AxNode[], rootBackend: number): Set<string> | null {
  let rootId = "";
  for (const node of nodes) {
    if (node.backendDOMNodeId === rootBackend && node.nodeId) {
      rootId = node.nodeId;
      break;
    }
  }
  if (!rootId) return null;
  const children = new Map<string, string[]>();
  for (const node of nodes) {
    if (!node.nodeId || !node.parentId) continue;
    const list = children.get(node.parentId) ?? [];
    list.push(node.nodeId);
    children.set(node.parentId, list);
  }
  const out = new Set<string>([rootId]);
  const stack = [rootId];
  while (stack.length) {
    const id = stack.pop()!;
    for (const child of children.get(id) ?? []) {
      if (out.has(child)) continue;
      out.add(child);
      stack.push(child);
    }
  }
  return out;
}

function visibleHit(name: string, role: string, visible: Set<string> | null): boolean {
  if (!visible || visible.size === 0) return true;
  const n = name.toLowerCase();
  if (!n) return unnamedOk.has(role);
  if (visible.has(n)) return true;
  const head = n.slice(0, 40);
  for (const item of visible) {
    if (item.includes(head) || head.includes(item)) return true;
  }
  return false;
}

export function readPage(nodes: AxNode[], opts: ReadOpts = {}): { text: string; refs: Record<string, PageRef> } {
  const scope = opts.scope ?? "interactive";
  const limit = Math.min(140, Math.max(1, opts.limit ?? 90));
  const offset = Math.max(0, opts.offset ?? 0);
  const depthMax = opts.depth ?? 96;
  const nameLimit = opts.nameLimit ?? 96;
  const visible = opts.visible && opts.visible.length ? new Set(opts.visible.map((item) => item.toLowerCase())) : null;
  const depths = depthsOf(nodes);
  const allowed = opts.rootBackend ? descendantIds(nodes, opts.rootBackend) : null;
  const byId = new Map<string, AxNode>();
  for (const node of nodes) if (node.nodeId) byId.set(node.nodeId, node);
  const chromeRoles = new Set(["navigation", "banner", "contentinfo", "toolbar"]);
  const inChrome = (node: AxNode): boolean => {
    let id = node.parentId;
    let guard = 0;
    while (id && guard < 120) {
      const parent = byId.get(id);
      if (!parent) return false;
      if (chromeRoles.has(parent.role?.value ?? "")) return true;
      id = parent.parentId;
      guard += 1;
    }
    return false;
  };
  const collect = (visSet: Set<string> | null) => {
    const out: { role: string; name: string; backend?: number; chrome: boolean }[] = [];
    for (const node of nodes) {
      if (node.ignored) continue;
      const role = node.role?.value ?? "";
      if (!role || skipRoles.has(role)) continue;
      if (scope === "interactive" && !interactiveRoles.has(role)) continue;
      if (allowed && node.nodeId && !allowed.has(node.nodeId)) continue;
      const depth = node.nodeId ? depths.get(node.nodeId) ?? 0 : 0;
      if (depth > depthMax) continue;
      const name = (node.name?.value ?? "").replace(/\s+/g, " ").trim().slice(0, nameLimit);
      if (!name && !unnamedOk.has(role)) continue;
      if (scope === "interactive" && !visibleHit(name, role, visSet)) continue;
      out.push({ role, name, backend: node.backendDOMNodeId, chrome: inChrome(node) });
    }
    return out;
  };
  let items = collect(visible);
  if (items.length === 0 && visible) {
    items = collect(null);
  }
  items.sort((a, b) => Number(a.chrome) - Number(b.chrome));
  const folded: { role: string; name: string; backend?: number; n: number }[] = [];
  for (const item of items) {
    const last = folded[folded.length - 1];
    if (last && last.role === item.role && last.name === item.name) {
      last.n += 1;
      continue;
    }
    folded.push({ ...item, n: 1 });
  }
  const slice = folded.slice(offset, offset + limit);
  const refs: Record<string, PageRef> = {};
  const used = new Set<string>();
  const byBackend = new Map<number, string>();
  let next = 0;
  for (const [ref, info] of Object.entries(opts.keep ?? {})) {
    const match = /^e(\d+)$/.exec(ref);
    if (!match || !info.backendNodeId) continue;
    byBackend.set(info.backendNodeId, ref);
    next = Math.max(next, Number(match[1]));
  }
  const lines: string[] = [];
  for (const item of slice) {
    let ref = item.backend ? byBackend.get(item.backend) : undefined;
    if (!ref || used.has(ref)) {
      next += 1;
      ref = "e" + next;
    }
    used.add(ref);
    if (item.backend) refs[ref] = { backendNodeId: item.backend, role: item.role, name: item.name };
    const times = item.n > 1 ? ` ×${item.n}` : "";
    lines.push(`${ref} ${item.role}${item.name ? ` "${item.name}"` : ""}${times}`);
  }
  const rest = folded.length - (offset + slice.length);
  if (rest > 0) lines.push(`+${rest}`);
  return { text: lines.join("\n"), refs };
}

/** Formatador da v1. Só o benchmark de tokens compara contra ele. */
export function readPageLegacy(nodes: AxNode[]): string {
  const lines: string[] = [];
  for (const node of nodes) {
    const role = node.role?.value ?? "";
    if (!role || skipRoles.has(role)) continue;
    const name = (node.name?.value ?? "").replace(/\s+/g, " ").trim();
    if (!name && (role === "StaticText" || role === "image")) continue;
    const ref = "e" + (lines.length + 1);
    lines.push(`- ${ref} ${role}${name ? ` "${name.slice(0, 160)}"` : ""}`);
    if (lines.length >= 700) break;
  }
  return lines.join("\n");
}

export function diffPage(prev: string, next: string): string {
  const key = (line: string) => line.replace(/^e\d+\s+/, "").replace(/ ×\d+$/, "");
  const prevKeys = new Set(prev.split("\n").map((line) => line.trim()).filter((line) => /^e\d+\s/.test(line)).map(key));
  const nextLines = next.split("\n").map((line) => line.trim()).filter((line) => /^e\d+\s/.test(line));
  const nextKeys = new Set(nextLines.map(key));
  const added = nextLines.filter((line) => !prevKeys.has(key(line)));
  let removed = 0;
  for (const item of prevKeys) if (!nextKeys.has(item)) removed += 1;
  if (added.length === 0 && removed === 0) return "igual";
  if (added.length > 8) return `mudou +${added.length} -${removed}`;
  const body = added.map((line) => "+ " + line).join("\n");
  return removed ? `${body}\n-${removed}` : body;
}

export function findRefs(tree: string, query: string, role = "", limit = 6): string {
  const q = query.trim().toLowerCase();
  if (!q) return "";
  const want = role.trim().toLowerCase();
  const cap = Math.min(12, Math.max(1, limit));
  return tree
    .split("\n")
    .filter((line) => {
      const low = line.toLowerCase();
      if (!low.includes(q)) return false;
      if (want && !low.includes(want)) return false;
      return true;
    })
    .slice(0, cap)
    .join("\n");
}

const skipTextRoles = new Set(["navigation", "banner", "contentinfo", "complementary", "menu", "toolbar", "button", "link"]);
const textRoles = new Set(["heading", "paragraph", "StaticText", "article", "main", "listitem", "note"]);

export function pageText(nodes: AxNode[], limit = 1500): string {
  const cap = Math.min(6000, Math.max(200, limit));
  const depths = depthsOf(nodes);
  const byId = new Map<string, AxNode>();
  for (const node of nodes) if (node.nodeId) byId.set(node.nodeId, node);
  const blocked = new Set<string>();
  for (const node of nodes) {
    const role = node.role?.value ?? "";
    if (!node.nodeId || !skipTextRoles.has(role)) continue;
    blocked.add(node.nodeId);
  }
  const skipNode = (node: AxNode): boolean => {
    let id = node.parentId;
    let guard = 0;
    while (id && guard < 120) {
      if (blocked.has(id)) return true;
      id = byId.get(id)?.parentId;
      guard += 1;
    }
    return false;
  };
  const parts: string[] = [];
  let used = 0;
  let last = "";
  for (const node of nodes) {
    const role = node.role?.value ?? "";
    if (!textRoles.has(role) || skipNode(node)) continue;
    const depth = node.nodeId ? depths.get(node.nodeId) ?? 0 : 0;
    if (depth > 96) continue;
    const name = (node.name?.value ?? "").replace(/\s+/g, " ").trim();
    if (name.length < 2 || name === last) continue;
    last = name;
    parts.push(name);
    used += name.length + 1;
    if (used >= cap) break;
  }
  return parts.join("\n").slice(0, cap);
}

export function filterLines(lines: string[], opts: { pattern?: string; keep?: (line: string) => boolean; limit?: number } = {}): string {
  const cap = Math.min(40, Math.max(1, opts.limit ?? 15));
  let re: RegExp | null = null;
  if (opts.pattern) {
    try {
      re = new RegExp(opts.pattern, "i");
    } catch {
      return "regex inválido";
    }
  }
  const out: string[] = [];
  for (const line of lines) {
    if (opts.keep && !opts.keep(line)) continue;
    if (re && !re.test(line)) continue;
    out.push(line);
    if (out.length >= cap) break;
  }
  return out.join("\n");
}

export interface BenchRow {
  name: string;
  before: number;
  after: number;
}

function benchNodes(): { nodes: AxNode[]; visible: string[] } {
  const nodes: AxNode[] = [];
  const visible: string[] = [];
  const push = (role: string, name: string, id: number, parent?: string) => {
    nodes.push({
      nodeId: "n" + id,
      parentId: parent,
      role: { value: role },
      name: name ? { value: name } : undefined,
      backendDOMNodeId: 1000 + id,
    });
  };
  push("generic", "", 0);
  for (let i = 0; i < 30; i++) push("link", "Item " + i, i + 1, "n0");
  for (let i = 0; i < 12; i++) push("link", "Ad", 40 + i, "n0");
  push("searchbox", "Search", 60, "n0");
  push("button", "Google Search", 61, "n0");
  for (let i = 0; i < 80; i++) {
    push("link", `HiveMind IDE github resultado orgânico número ${i} com título longo o bastante`, 100 + i, "n0");
  }
  for (let i = 0; i < 250; i++) {
    push("StaticText", "Parágrafo de enchimento ".repeat(8) + i, 200 + i, "n0");
  }
  for (let i = 0; i < 200; i++) push("generic", "caixa", 500 + i, "n0");
  for (let i = 0; i < 40; i++) push("InlineTextBox", "x", 800 + i, "n0");
  visible.push("search", "google search");
  for (let i = 0; i < 18; i++) visible.push(("hivemind ide github resultado orgânico número " + i).slice(0, 40));
  for (let i = 0; i < 8; i++) visible.push("item " + i);
  return { nodes, visible };
}

export function tokenBench(): BenchRow[] {
  const { nodes, visible } = benchNodes();
  const legacy = readPageLegacy(nodes);
  const compact = readPage(nodes, { scope: "interactive", visible, limit: 48 }).text;
  const again = "igual Search · google.com";
  const changed = readPage(nodes, { scope: "interactive", visible, limit: 48 }).text + '\ne99 button "Ok"';
  const delta = diffPage(compact, changed);
  const foundLegacy = legacy
    .split("\n")
    .filter((line) => line.toLowerCase().includes("hivemind"))
    .slice(0, 40)
    .join("\n");
  const found = findRefs(compact + "\n" + legacy, "hivemind", "link", 6);
  const logs = Array.from({ length: 40 }, (_, i) => (i < 3 ? `error: falha ${i}` : `log: linha ${i} `.repeat(6)));
  const net = Array.from({ length: 40 }, (_, i) => (i === 2 ? "404 https://example.com/missing" : `200 https://example.com/asset-${i}.js`));
  return [
    { name: "read_page", before: tokenCount(legacy), after: tokenCount(compact) },
    { name: "read_page igual", before: tokenCount(legacy), after: tokenCount(again) },
    { name: "delta", before: tokenCount(legacy), after: tokenCount(delta) },
    { name: "find", before: tokenCount(foundLegacy), after: tokenCount(found) },
    { name: "console", before: tokenCount(logs.join("\n")), after: tokenCount(filterLines(logs, { keep: (line) => /^(error|warning)/.test(line), limit: 15 }) || "sem erros") },
    { name: "network", before: tokenCount(net.join("\n")), after: tokenCount(filterLines(net, { keep: (line) => /\b[45]\d\d\b|ERR_/.test(line), limit: 15 }) || "sem erros") },
  ];
}
