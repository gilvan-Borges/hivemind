import {
  diffPage,
  filterLines,
  findRefs,
  fnv32,
  hostOf,
  isSensitiveLabel,
  pageText,
  readPage,
  redactSecrets,
  safeURL,
  type AxNode,
  type PageRef,
} from "./pure.js";

export interface ToolResult {
  text: string;
  image?: string;
  code?: string;
}

export class NeedsApproval extends Error {
  readonly code = "needs_approval";
}

const refsByTab = new Map<number, Record<string, PageRef>>();
const treeByTab = new Map<number, string>();
const snapByTab = new Map<number, { url: string; hash: string }>();
const consoleBuf: string[] = [];
const networkBuf: string[] = [];
const attached = new Set<number>();
const frames = new Map<number, Set<string>>();
const pendingDialog = new Map<number, { type: string; message: string }>();
const downloads: string[] = [];
const inflight = new Map<string, number>();
const aborted = new Set<string>();
const spawned: number[] = [];
let currentCall = "";
let halted = false;
let watchOpener = 0;
let pinnedTabId = 0;
let pickResolve: ((text: string) => void) | null = null;

export function pinSessionTab(tabId: number): void {
  if (Number.isFinite(tabId) && tabId > 0) {
    pinnedTabId = tabId;
  }
}

export function getPinnedSessionTab(): number {
  return pinnedTabId;
}

const batchTools = new Set(["click", "type", "form_input", "key", "scroll", "wait", "find", "read_page"]);

const visibleScript = `(() => {
  const out = [];
  const seen = new Set();
  const vh = innerHeight, vw = innerWidth;
  const sel = "a,button,input,textarea,select,summary,label,[role],[tabindex]:not([tabindex='-1']),[contenteditable=true]";
  function add(el) {
    const r = el.getBoundingClientRect();
    if (r.width < 2 || r.height < 2 || r.bottom <= 0 || r.top >= vh || r.right <= 0 || r.left >= vw) return;
    const cs = getComputedStyle(el);
    if (cs.visibility === "hidden" || cs.display === "none") return;
    const candidates = [
      el.getAttribute("aria-label"),
      el.getAttribute("placeholder"),
      el.getAttribute("title"),
      el.innerText,
      el.textContent,
    ];
    for (const raw of candidates) {
      const name = String(raw || "").replace(/\\s+/g, " ").trim().slice(0, 96).toLowerCase();
      if (!name || seen.has(name)) continue;
      seen.add(name);
      out.push(name);
    }
  }
  function walk(root) {
    let all;
    try { all = root.querySelectorAll("*"); } catch (e) { return; }
    for (const el of all) {
      if (el.shadowRoot) walk(el.shadowRoot);
      try {
        if (el.matches(sel) || getComputedStyle(el).cursor === "pointer") add(el);
      } catch (e) { /* nó sem matches */ }
    }
    let frames;
    try { frames = root.querySelectorAll("iframe"); } catch (e) { return; }
    for (const frame of frames) {
      try { if (frame.contentDocument) walk(frame.contentDocument); } catch (e) { /* outro origin */ }
    }
  }
  walk(document);
  return out.slice(0, 600);
})()`;

const mainTextScript = `(() => {
  const root = document.querySelector("article, main, [role=main]") || document.body;
  if (!root) return "";
  const clone = root.cloneNode(true);
  clone.querySelectorAll("nav,aside,footer,header,script,style,noscript,svg").forEach((n) => n.remove());
  const direct = (clone.innerText || "").replace(/\\n{3,}/g, "\\n\\n").trim();
  if (direct.length >= 60) return direct;
  const chunks = [];
  const seen = new Set();
  function walkText(node) {
    let all;
    try { all = node.querySelectorAll("*"); } catch (e) { return; }
    for (const el of all) {
      if (el.shadowRoot) walkText(el.shadowRoot);
      const tag = (el.tagName || "").toLowerCase();
      if (tag === "script" || tag === "style" || tag === "noscript" || tag === "svg") continue;
      if (el.children && el.children.length > 0) continue;
      const r = el.getBoundingClientRect();
      if (r.width < 2 || r.height < 2) continue;
      const txt = String(el.innerText || el.textContent || "").replace(/\\s+/g, " ").trim();
      if (txt.length < 2 || seen.has(txt)) continue;
      seen.add(txt);
      chunks.push(txt);
    }
  }
  walkText(document);
  return chunks.join("\\n").trim() || direct;
})()`;

export function setCurrentCall(id: string): void {
  currentCall = id;
  halted = false;
  aborted.delete(id);
}

export function abortCall(id: string): void {
  if (id) aborted.add(id);
  if (!id || id === "stop" || id === currentCall) halted = true;
}

function ensureLive(): void {
  if (halted || (currentCall && aborted.has(currentCall))) throw new Error("interrompido");
}

chrome.debugger.onEvent.addListener((source, method, params) => {
  const tabId = source.tabId ?? 0;
  if (method === "Target.attachedToTarget" && tabId && params?.sessionId) {
    const type = String(params?.targetInfo?.type ?? "");
    if (type === "iframe") {
      const set = frames.get(tabId) ?? new Set<string>();
      set.add(String(params.sessionId));
      frames.set(tabId, set);
    }
  }
  if (method === "Target.detachedFromTarget" && tabId && params?.sessionId) {
    frames.get(tabId)?.delete(String(params.sessionId));
  }
  if (method === "Page.javascriptDialogOpening" && tabId) {
    pendingDialog.set(tabId, {
      type: String(params?.type ?? "alert"),
      message: redactSecrets(String(params?.message ?? "")).slice(0, 240),
    });
  }
  if (method === "Page.javascriptDialogClosed" && tabId) pendingDialog.delete(tabId);
  if ((method === "Page.downloadWillBegin" || method === "Browser.downloadWillBegin") && params) {
    const name = String(params.suggestedFilename ?? params.url ?? "arquivo");
    downloads.push(safeURL(name));
    if (downloads.length > 10) downloads.shift();
  }
  if (method === "Runtime.consoleAPICalled") {
    const args = (params?.args as { value?: unknown; description?: string }[] | undefined) ?? [];
    const text = args.map((arg) => (arg.value !== undefined ? String(arg.value) : arg.description ?? "")).join(" ");
    push(consoleBuf, `${params?.type ?? "log"}: ${redactSecrets(text)}`);
  }
  if (method === "Network.requestWillBeSent") {
    const req = params?.request ?? {};
    const id = String(params?.requestId ?? "");
    if (tabId && id) inflight.set(`${tabId}:${id}`, tabId);
    push(networkBuf, `${req.method ?? "GET"} ${safeURL(String(req.url ?? ""))}`);
  }
  if (method === "Network.responseReceived") {
    const res = params?.response ?? {};
    push(networkBuf, `${res.status ?? "?"} ${safeURL(String(res.url ?? ""))}`);
  }
  if (method === "Network.loadingFinished" || method === "Network.loadingFailed") {
    const id = String(params?.requestId ?? "");
    if (tabId && id) inflight.delete(`${tabId}:${id}`);
  }
});

chrome.debugger.onDetach.addListener((source) => {
  if (source.tabId) {
    attached.delete(source.tabId);
    frames.delete(source.tabId);
  }
});

chrome.tabs.onCreated.addListener((tab) => {
  if (watchOpener && tab.openerTabId === watchOpener && tab.id) spawned.push(tab.id);
});

chrome.runtime.onMessage.addListener((message: { type?: string; text?: string }) => {
  if (message?.type === "hivemind-picked" && pickResolve) {
    const done = pickResolve;
    pickResolve = null;
    done(String(message.text ?? ""));
  }
});

function push(buf: string[], line: string): void {
  buf.push(line.slice(0, 240));
  if (buf.length > 80) buf.shift();
}

function inflightCount(tabId: number): number {
  let n = 0;
  for (const value of inflight.values()) if (value === tabId) n += 1;
  return n;
}

async function send<T>(tabId: number, method: string, params: object = {}, sessionId?: string): Promise<T> {
  ensureLive();
  const target: ChromeDebuggee = sessionId ? { tabId, sessionId } : { tabId };
  return chrome.debugger.sendCommand(target, method, params) as Promise<T>;
}

async function attach(tabId: number): Promise<void> {
  if (attached.has(tabId)) return;
  try {
    await chrome.debugger.attach({ tabId }, "1.3");
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    if (!/already/i.test(msg)) throw err;
  }
  attached.add(tabId);
  await send(tabId, "Page.enable");
  await send(tabId, "Runtime.enable");
  await send(tabId, "Network.enable");
  await send(tabId, "DOM.enable");
  await send(tabId, "Log.enable");
  try {
    // Mantém a aba com foco virtual e ciclo ativo mesmo quando o Chrome está em 2º plano,
    // sem nunca chamar Page.bringToFront nem roubar o mouse/teclado do usuário.
    await send(tabId, "Emulation.setFocusEmulationEnabled", { enabled: true });
  } catch {
    /* opcional por versão do Chrome */
  }
  try {
    await send(tabId, "Page.setWebLifecycleState", { state: "active" });
  } catch {
    /* opcional por versão do Chrome */
  }
  try {
    await send(tabId, "Target.setAutoAttach", { autoAttach: true, waitForDebuggerOnStart: false, flatten: true });
  } catch {
    /* o Chrome da máquina pode não aceitar flatten */
  }
  await showOverlay(tabId);
}

export async function detachAll(): Promise<void> {
  halted = true;
  for (const tabId of [...attached]) {
    attached.delete(tabId);
    frames.delete(tabId);
    await hideOverlay(tabId);
    try {
      await chrome.debugger.detach({ tabId });
    } catch {
      /* a aba pode ter fechado */
    }
  }
}

async function activeTab(preferred?: unknown): Promise<number> {
  const id = typeof preferred === "number" ? preferred : Number(preferred);
  if (Number.isFinite(id) && id > 0) {
    pinnedTabId = id;
    return id;
  }
  if (pinnedTabId > 0) {
    try {
      const existing = await chrome.tabs.get(pinnedTabId);
      if (existing?.id) return existing.id;
    } catch {
      pinnedTabId = 0;
    }
  }
  let tabs = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  if (!tabs[0]?.id) {
    tabs = await chrome.tabs.query({ active: true });
  }
  const httpTab = tabs.find((t) => t.id && /^https?:/i.test(t.url ?? "")) ?? tabs[0];
  if (!httpTab?.id) throw new Error("nenhuma aba ativa encontrada no Chrome");
  pinnedTabId = httpTab.id;
  return httpTab.id;
}

async function tabInfo(tabId: number): Promise<ChromeTab> {
  try {
    return await chrome.tabs.get(tabId);
  } catch {
    return { id: tabId };
  }
}

function approved(args: Record<string, unknown>): boolean {
  return args._approved === true;
}

function noteOf(tabId: number): string {
  const dialog = pendingDialog.get(tabId);
  const last = downloads[downloads.length - 1];
  const bits = [];
  if (dialog) bits.push(`dialog ${dialog.type}: ${dialog.message}`);
  if (last) bits.push(`download ${last}`);
  return bits.join(" · ");
}

function shortOk(tab: ChromeTab, extra = ""): string {
  const host = hostOf(tab.url ?? "") ?? "";
  const title = (tab.title ?? "").replace(/\s+/g, " ").trim().slice(0, 60);
  return ["ok", title, host, extra].filter(Boolean).join(" · ");
}

async function tell(tabId: number, op: string, extra: Record<string, unknown> = {}): Promise<void> {
  for (let i = 0; i < 6; i++) {
    try {
      await chrome.tabs.sendMessage(tabId, { type: "hivemind-overlay", op, ...extra });
      return;
    } catch {
      await sleep(80);
    }
  }
}

async function showOverlay(tabId: number, label = "No controle"): Promise<void> {
  await tell(tabId, "show", { label });
  try {
    await chrome.action.setBadgeText({ text: "ON", tabId });
    await chrome.action.setBadgeBackgroundColor({ color: "#C6A15B" });
  } catch {
    /* página restrita */
  }
}

async function hideOverlay(tabId: number): Promise<void> {
  await tell(tabId, "hide");
  try {
    await chrome.action.setBadgeText({ text: "", tabId });
  } catch {
    /* idem */
  }
}

interface Box {
  x: number;
  y: number;
  left: number;
  top: number;
  width: number;
  height: number;
}

async function clickBox(tabId: number, backendNodeId: number): Promise<Box> {
  try {
    const pushed = await send<{ nodeIds?: number[] }>(tabId, "DOM.pushNodesByBackendIdsToFrontend", {
      backendNodeIds: [backendNodeId],
    });
    const nodeId = pushed.nodeIds?.[0];
    if (nodeId) {
      const described = await send<{ node?: { nodeName?: string } }>(tabId, "DOM.describeNode", { nodeId });
      const name = (described.node?.nodeName ?? "").toUpperCase();
      if (name !== "A") {
        const found = await send<{ nodeId?: number }>(tabId, "DOM.querySelector", { nodeId, selector: "a" });
        if (found.nodeId) {
          await send(tabId, "DOM.scrollIntoViewIfNeeded", { nodeId: found.nodeId });
          const model = await send<{ model?: { content?: number[] } }>(tabId, "DOM.getBoxModel", { nodeId: found.nodeId });
          return boxFromQuad(tabId, model.model?.content);
        }
      }
    }
  } catch {
    /* cai no nó acessível */
  }
  return boxOf(tabId, backendNodeId);
}

async function boxOf(tabId: number, backendNodeId: number): Promise<Box> {
  await send(tabId, "DOM.scrollIntoViewIfNeeded", { backendNodeId }).catch(() => undefined);
  const model = await send<{ model?: { content?: number[] } }>(tabId, "DOM.getBoxModel", { backendNodeId });
  return boxFromQuad(tabId, model.model?.content);
}

async function boxFromQuad(tabId: number, quad: number[] | undefined): Promise<Box> {
  if (!quad || quad.length < 8) throw new Error("elemento sem caixa visível");
  const xs = [quad[0], quad[2], quad[4], quad[6]];
  const ys = [quad[1], quad[3], quad[5], quad[7]];
  const left = Math.min(...xs);
  const top = Math.min(...ys);
  const width = Math.max(...xs) - left;
  const height = Math.max(...ys) - top;
  let vx = 0;
  let vy = 0;
  try {
    const metrics = await send<{ cssVisualViewport?: { pageX?: number; pageY?: number } }>(tabId, "Page.getLayoutMetrics");
    vx = metrics.cssVisualViewport?.pageX ?? 0;
    vy = metrics.cssVisualViewport?.pageY ?? 0;
  } catch {
    /* sem métrica, usa a origem do documento */
  }
  return { x: left + width / 2 - vx, y: top + height / 2 - vy, left: left - vx, top: top - vy, width, height };
}

async function clickAt(tabId: number, x: number, y: number): Promise<void> {
  await send(tabId, "Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
  await send(tabId, "Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount: 1 });
  await send(tabId, "Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount: 1 });
}

function staleMessage(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err);
  return /não está|obsoleta|Could not find|não encontrado|No node|sem caixa/i.test(msg);
}

async function refreshRef(tabId: number, info: PageRef): Promise<PageRef> {
  await snapshot(tabId, { fresh: true, scope: "all", limit: 80 });
  const refs = refsByTab.get(tabId) ?? {};
  for (const ref of Object.values(refs)) {
    if (ref.role === info.role && ref.name === info.name) return ref;
  }
  throw new Error("ref obsoleta");
}

async function withRef(tabId: number, ref: unknown): Promise<PageRef> {
  const key = String(ref ?? "");
  const info = refsByTab.get(tabId)?.[key];
  if (info) return info;
  await snapshot(tabId, { fresh: true });
  const again = refsByTab.get(tabId)?.[key];
  if (again) return again;
  throw new Error(`ref ${key || "vazia"} não está na última leitura. Chame read_page.`);
}

async function boxRetry(tabId: number, info: PageRef): Promise<{ box: Box; info: PageRef }> {
  try {
    return { box: await boxOf(tabId, info.backendNodeId), info };
  } catch (err) {
    if (!staleMessage(err)) throw err;
    const fresh = await refreshRef(tabId, info);
    return { box: await boxOf(tabId, fresh.backendNodeId), info: fresh };
  }
}

function assertHttp(raw: string): void {
  if (!raw || raw === "about:blank") return;
  if (!hostOf(raw)) throw new Error("só http(s), sem usuário na URL");
}

export async function executeTool(tool: string, args: Record<string, unknown>): Promise<ToolResult> {
  ensureLive();
  switch (tool) {
    case "navigate":
      return navigate(args);
    case "read_page":
      return read(args);
    case "find":
      return find(args);
    case "get_page_text":
      return mainText(args);
    case "click":
      return click(args);
    case "type":
      return typeText(args);
    case "form_input":
      return formInput(args);
    case "scroll":
      return scroll(args);
    case "key":
      return pressKey(args);
    case "screenshot":
      return screenshot(args);
    case "batch":
      return batch(args);
    case "tabs":
      return tabs(args);
    case "tabs_list":
      return tabsList();
    case "tabs_create":
      return tabsCreate(args);
    case "tabs_close":
      return tabsClose(args);
    case "tabs_switch":
      return tabsSwitch(args);
    case "console_read":
      return { text: consoleText(args) };
    case "network_read":
      return { text: networkText(args) };
    case "get_dom":
      return dom(args);
    case "computed_styles":
      return styles(args);
    case "javascript_exec":
      return runJs(args);
    case "upload":
      return upload(args);
    case "dialog":
      return dialog(args);
    case "pick":
      return pick(args);
    case "devtools":
      return { text: "console_read network_read get_dom computed_styles javascript_exec upload. level=all tira o filtro. javascript_exec e upload pedem aprovação." };
    case "wait":
      return waitFor(args);
    default:
      throw new Error("ferramenta desconhecida: " + tool);
  }
}

async function navigate(args: Record<string, unknown>): Promise<ToolResult> {
  const url = String(args.url ?? "");
  assertHttp(url);
  const tabId = await activeTab(args.tabId);
  await chrome.tabs.update(tabId, { url });
  await waitComplete(tabId);
  await attach(tabId);
  await showOverlay(tabId, "Abrindo a página");
  await waitIdle(tabId, 4000);
  const tab = await tabInfo(tabId);
  snapByTab.delete(tabId);
  return { text: shortOk(tab, noteOf(tabId)) };
}

async function waitComplete(tabId: number): Promise<void> {
  const start = Date.now();
  while (Date.now() - start < 12_000) {
    ensureLive();
    if (pendingDialog.has(tabId)) return;
    const tab = await chrome.tabs.get(tabId);
    if (tab.status === "complete") return;
    await sleep(120);
  }
}

async function waitIdle(tabId: number, budget: number): Promise<void> {
  const start = Date.now();
  let stable = 0;
  let prev = "";
  while (Date.now() - start < budget) {
    ensureLive();
    if (pendingDialog.has(tabId)) return;
    const quiet = inflightCount(tabId) === 0;
    let mark = prev;
    try {
      const ev = await send<{ result?: { value?: number } }>(tabId, "Runtime.evaluate", {
        expression: "document.body ? document.body.innerText.length : 0",
        returnByValue: true,
      });
      mark = String(ev.result?.value ?? "");
    } catch {
      mark = prev;
    }
    if (quiet && mark === prev && prev !== "") {
      stable += 1;
      if (stable >= 2) return;
    } else {
      stable = 0;
    }
    prev = mark;
    await sleep(140);
  }
}

function consoleText(args: Record<string, unknown>): string {
  return (
    filterLines(consoleBuf, {
      pattern: String(args.pattern ?? ""),
      keep: args.level === "all" ? undefined : (line) => /^(error|warning|assert)/.test(line),
      limit: Number(args.limit ?? 15),
    }) || "sem erros"
  );
}

function networkText(args: Record<string, unknown>): string {
  return (
    filterLines(networkBuf, {
      pattern: String(args.pattern ?? ""),
      keep: args.level === "all" ? undefined : (line) => /\b[45]\d\d\b|ERR_|failed/i.test(line),
      limit: Number(args.limit ?? 15),
    }) || "sem erros"
  );
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function axNodes(tabId: number, sessionId?: string): Promise<AxNode[]> {
  const tree = await send<{ nodes?: AxNode[] }>(tabId, "Accessibility.getFullAXTree", {}, sessionId);
  return tree.nodes ?? [];
}

async function visibleNames(tabId: number): Promise<string[]> {
  try {
    const ev = await send<{ result?: { value?: string[] } }>(tabId, "Runtime.evaluate", {
      expression: visibleScript,
      returnByValue: true,
    });
    return Array.isArray(ev.result?.value) ? ev.result.value : [];
  } catch {
    return [];
  }
}

async function snapshot(tabId: number, args: Record<string, unknown>): Promise<{ text: string; url: string; title: string }> {
  await attach(tabId);
  const scope = args.scope === "all" ? "all" : "interactive";
  const visible = scope === "interactive" ? await visibleNames(tabId) : [];
  let nodes = await axNodes(tabId);
  for (const sessionId of frames.get(tabId) ?? []) {
    try {
      const extra = await axNodes(tabId, sessionId);
      nodes = nodes.concat(extra);
    } catch {
      /* iframe destacado */
    }
  }
  const root = String(args.root ?? "");
  const rootBackend = root ? refsByTab.get(tabId)?.[root]?.backendNodeId : undefined;
  const page = readPage(nodes, {
    scope,
    visible,
    depth: Number(args.depth ?? 96) || 96,
    rootBackend,
    offset: Number(args.offset ?? 0) || 0,
    limit: Number(args.limit ?? 90) || 90,
    keep: refsByTab.get(tabId),
  });
  let outTree = page.text;
  if (visible.length > 0) {
    const lowTree = outTree.toLowerCase();
    const extras: string[] = [];
    for (const rawVis of visible) {
      const cleanVis = rawVis.trim();
      if (cleanVis.length < 2 || cleanVis.length > 64) continue;
      if (lowTree.includes(cleanVis)) continue;
      extras.push(`clickable "${cleanVis}"`);
      if (extras.length >= 25) break;
    }
    if (extras.length > 0) {
      outTree = [outTree, ...extras].filter(Boolean).join("\n");
    }
  }
  refsByTab.set(tabId, page.refs);
  treeByTab.set(tabId, outTree);
  const tab = await tabInfo(tabId);
  const url = safeURL(tab.url ?? "");
  const hash = fnv32(url + "\n" + outTree);
  const prev = snapByTab.get(tabId);
  snapByTab.set(tabId, { url, hash });
  const same = !args.fresh && prev && prev.url === url && prev.hash === hash;
  if (same && String(args.view ?? "") !== "full") {
    const host = hostOf(tab.url ?? "") ?? "";
    return { text: `igual ${(tab.title ?? "").slice(0, 60)} ${host}`.trim(), url, title: tab.title ?? "" };
  }
  const header = `${(tab.title ?? "").slice(0, 80)} ${hostOf(tab.url ?? "") ?? ""}`.trim();
  return { text: [header, outTree || "vazio", noteOf(tabId)].filter(Boolean).join("\n"), url, title: tab.title ?? "" };
}

async function read(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await tell(tabId, "status", { label: "Lendo a página" });
  const prevBody = treeByTab.get(tabId) ?? "";
  const page = await snapshot(tabId, args);
  if (String(args.view ?? "") === "delta" && prevBody) {
    const body = treeByTab.get(tabId) ?? "";
    const delta = diffPage(prevBody, body);
    return { text: delta === "igual" ? page.text : delta };
  }
  return { text: page.text };
}

async function changedLine(tabId: number, before: string): Promise<string> {
  const page = await snapshot(tabId, { fresh: true, limit: 48 });
  const delta = diffPage(before, treeByTab.get(tabId) ?? page.text);
  if (!delta || delta === "igual") return "";
  if (delta.startsWith("mudou")) return delta.replace("mudou", "Δ");
  const lines = delta.split("\n").slice(0, 6);
  return lines.join("\n");
}

async function find(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  const query = String(args.query ?? "");
  const role = String(args.role ?? "");
  const limit = Number(args.limit ?? 6) || 6;
  let tree = treeByTab.get(tabId) ?? "";
  let hits = findRefs(tree, query, role, limit);
  if (!hits) {
    const page = await snapshot(tabId, { fresh: true, scope: "all", limit: 80 });
    hits = findRefs(page.text, query, role, limit);
  }
  return { text: hits || "nada" };
}

async function mainText(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  const limit = clamp(Number(args.limit ?? 1500), 200, 6000);
  try {
    const ev = await send<{ result?: { value?: string } }>(tabId, "Runtime.evaluate", {
      expression: mainTextScript,
      returnByValue: true,
    });
    const text = redactSecrets(String(ev.result?.value ?? "")).slice(0, limit);
    if (text.trim()) return { text };
  } catch {
    /* cai na árvore */
  }
  const nodes = await axNodes(tabId);
  return { text: pageText(nodes, limit) || "vazio" };
}

async function followSpawned(): Promise<string> {
  const id = spawned[spawned.length - 1];
  if (!id) return "";
  pinnedTabId = id;
  try {
    await chrome.tabs.update(id, { active: true });
    await waitComplete(id);
    await attach(id);
  } catch {
    return `aba ${id}`;
  }
  return `aba ${id}`;
}

async function locateByName(tabId: number, targetName: string): Promise<{ left: number; top: number; width: number; height: number } | null> {
  const clean = targetName.trim();
  if (!clean) return null;
  const expr = `((needle) => {
    const q = needle.toLowerCase().trim();
    let bestEl = null;
    let bestScore = 999999;
    function check(el) {
      const cs = getComputedStyle(el);
      if (cs.visibility === "hidden" || cs.display === "none") return;
      const r = el.getBoundingClientRect();
      if (r.width < 4 || r.height < 4) return;
      const aria = String(el.getAttribute("aria-label") || el.getAttribute("placeholder") || el.getAttribute("title") || "").replace(/\\s+/g, " ").trim().toLowerCase();
      const txt = String(el.innerText || el.textContent || "").replace(/\\s+/g, " ").trim().toLowerCase();
      const hitAria = aria && (aria === q || aria.includes(q));
      const hitTxt = txt && txt.length <= 160 && (txt === q || txt.includes(q));
      if (!hitAria && !hitTxt) return;
      const tag = (el.tagName || "").toLowerCase();
      const interactive = tag === "button" || tag === "a" || tag === "input" || tag === "textarea" || el.hasAttribute("role") || cs.cursor === "pointer";
      const exact = aria === q || txt === q ? 0 : 50;
      const bonus = interactive ? 0 : 200;
      const area = Math.min(80000, r.width * r.height) / 1000;
      const score = exact + bonus + area + (txt ? txt.length : 40);
      if (score < bestScore) {
        bestScore = score;
        bestEl = el;
      }
    }
    function walk(root) {
      let all;
      try { all = root.querySelectorAll("*"); } catch (e) { return; }
      for (const el of all) {
        if (el.shadowRoot) walk(el.shadowRoot);
        check(el);
      }
    }
    walk(document);
    if (!bestEl) return null;
    try { bestEl.scrollIntoView({ block: "center", inline: "center", behavior: "instant" }); } catch (e) {}
    const rect = bestEl.getBoundingClientRect();
    return { left: rect.left, top: rect.top, width: rect.width, height: rect.height };
  })(${JSON.stringify(clean)})`;
  try {
    const ev = await send<{ result?: { value?: { left: number; top: number; width: number; height: number } | null } }>(tabId, "Runtime.evaluate", {
      expression: expr,
      returnByValue: true,
    });
    return ev.result?.value ?? null;
  } catch {
    return null;
  }
}

async function click(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  const before = treeByTab.get(tabId) ?? "";
  let name = String(args.name ?? "");
  let x = Number(args.x);
  let y = Number(args.y);
  watchOpener = tabId;
  spawned.length = 0;
  if (args.ref) {
    let info = await withRef(tabId, args.ref);
    name = name || info.name;
    if ((args.submit === true || isSensitiveLabel(name) || isSensitiveLabel(info.role + " " + info.name)) && !approved(args)) {
      throw new NeedsApproval(`o elemento ${info.role} "${info.name}" parece enviar, comprar ou confirmar`);
    }
    let box: Box;
    try {
      const got = await boxRetry(tabId, info);
      box = got.box;
      info = got.info;
    } catch (err) {
      if (!staleMessage(err)) throw err;
      const fresh = await refreshRef(tabId, info);
      box = (await boxRetry(tabId, fresh)).box;
    }
    x = box.left + Math.min(48, Math.max(8, box.width / 3));
    y = box.top + Math.min(16, Math.max(6, box.height / 2));
    await tell(tabId, "highlight", { x: box.left, y: box.top, w: box.width, h: box.height, label: "Clicando" });
  } else if (name && (!Number.isFinite(x) || !Number.isFinite(y))) {
    if ((args.submit === true || isSensitiveLabel(name)) && !approved(args)) {
      throw new NeedsApproval(`o elemento "${name}" parece enviar, comprar ou confirmar`);
    }
    const found = await locateByName(tabId, name);
    if (!found) {
      throw new Error(`elemento "${name}" não encontrado visível na aba`);
    }
    x = found.left + Math.min(48, Math.max(8, found.width / 2));
    y = found.top + Math.min(20, Math.max(6, found.height / 2));
    await tell(tabId, "highlight", { x: found.left, y: found.top, w: found.width, h: found.height, label: "Clicando" });
  } else if (args.submit === true && !approved(args)) {
    throw new NeedsApproval("clique de envio precisa de aprovação");
  }
  if (!Number.isFinite(x) || !Number.isFinite(y)) throw new Error("informe ref ou x e y");
  await tell(tabId, "cursor", { x, y, label: "Clicando" });
  await sleep(220);
  await tell(tabId, "ripple", { x, y });
  await clickAt(tabId, x, y);
  if (args.ref) {
    try {
      const info = refsByTab.get(tabId)?.[String(args.ref)];
      if (info) {
        await send(tabId, "DOM.focus", { backendNodeId: info.backendNodeId });
        await send(tabId, "Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13 });
        await send(tabId, "Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13 });
      }
    } catch {
      /* o clique do mouse já pode ter navegado */
    }
  }
  await waitIdle(tabId, 2500);
  const opened = await followSpawned();
  watchOpener = 0;
  const landed = await tabInfo(spawned.length ? spawned[spawned.length - 1] : tabId);
  const delta = await changedLine(landed.id ?? tabId, before).catch(() => "");
  return { text: shortOk(landed, [opened, noteOf(tabId), delta].filter(Boolean).join(" · ")) };
}

async function typeText(args: Record<string, unknown>): Promise<ToolResult> {
  const text = String(args.text ?? args.value ?? "");
  if (!text) throw new Error("texto vazio");
  if (text.length > 20_000) throw new Error("texto longo demais");
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  const before = treeByTab.get(tabId) ?? "";
  await tell(tabId, "status", { label: "Digitando" });
  if (args.ref) {
    let info = await withRef(tabId, args.ref);
    try {
      await send(tabId, "DOM.focus", { backendNodeId: info.backendNodeId });
    } catch {
      info = await refreshRef(tabId, info);
      await send(tabId, "DOM.focus", { backendNodeId: info.backendNodeId });
    }
    try {
      const box = await boxOf(tabId, info.backendNodeId);
      await tell(tabId, "highlight", { x: box.left, y: box.top, w: box.width, h: box.height, label: "Digitando" });
      await tell(tabId, "cursor", { x: box.x, y: box.y, label: "Digitando" });
      await clickAt(tabId, box.x, box.y);
    } catch {
      /* o campo pode não ter caixa ainda */
    }
  } else if (args.name) {
    const found = await locateByName(tabId, String(args.name));
    if (found) {
      const cx = found.left + found.width / 2;
      const cy = found.top + found.height / 2;
      await tell(tabId, "highlight", { x: found.left, y: found.top, w: found.width, h: found.height, label: "Digitando" });
      await tell(tabId, "cursor", { x: cx, y: cy, label: "Digitando" });
      await clickAt(tabId, cx, cy);
    }
  }
  const animated = text.slice(0, 24);
  const rest = text.slice(animated.length);
  for (const ch of animated) {
    ensureLive();
    await send(tabId, "Input.insertText", { text: ch });
    await sleep(12);
  }
  if (rest) await send(tabId, "Input.insertText", { text: rest });
  const tab = await tabInfo(tabId);
  const delta = await changedLine(tabId, before).catch(() => "");
  return { text: shortOk(tab, delta) };
}

async function formInput(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  const info = args.ref ? await withRef(tabId, args.ref) : undefined;
  if ((args.submit === true || isSensitiveLabel(info?.name ?? "") || isSensitiveLabel(String(args.name ?? ""))) && !approved(args)) {
    throw new NeedsApproval("enviar o formulário precisa de aprovação");
  }
  const typed = await typeText({ ...args, text: String(args.value ?? "") });
  if (args.submit === true) await pressKey({ tabId, key: "Enter" });
  return typed;
}

async function scroll(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  const before = treeByTab.get(tabId) ?? "";
  let x = 400;
  let y = 300;
  if (args.ref) {
    const info = await withRef(tabId, args.ref);
    const center = await boxOf(tabId, info.backendNodeId);
    x = center.x;
    y = center.y;
  }
  const dx = clamp(Number(args.dx ?? 0), -2000, 2000);
  const dy = clamp(Number(args.dy ?? 400), -2000, 2000);
  await send(tabId, "Input.dispatchMouseEvent", { type: "mouseWheel", x, y, deltaX: dx, deltaY: dy });
  await sleep(80);
  const tab = await tabInfo(tabId);
  const delta = await changedLine(tabId, before).catch(() => "");
  return { text: shortOk(tab, delta) };
}

function clamp(n: number, min: number, max: number): number {
  if (!Number.isFinite(n)) return min;
  return Math.min(max, Math.max(min, n));
}

async function pressKey(args: Record<string, unknown>): Promise<ToolResult> {
  const key = String(args.key ?? "");
  if (!key) throw new Error("tecla vazia");
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  const before = treeByTab.get(tabId) ?? "";
  await tell(tabId, "status", { label: key === "Enter" ? "Enviando" : "Tecla " + key });
  const named = key.length > 1;
  await send(tabId, "Input.dispatchKeyEvent", {
    type: "keyDown",
    key,
    code: named ? key : "Key" + key.toUpperCase(),
    text: named ? "" : key,
    windowsVirtualKeyCode: named ? 13 : key.charCodeAt(0),
  });
  await send(tabId, "Input.dispatchKeyEvent", { type: "keyUp", key, code: named ? key : "Key" + key.toUpperCase() });
  if (key === "Enter") await waitIdle(tabId, 2500);
  const tab = await tabInfo(tabId);
  const delta = await changedLine(tabId, before).catch(() => "");
  return { text: shortOk(tab, delta) };
}

export async function captureThumb(tabId: number): Promise<string> {
  await tell(tabId, "hide");
  await sleep(30);
  try {
    const shot = await send<{ data?: string }>(tabId, "Page.captureScreenshot", { format: "jpeg", quality: 16 });
    if (!shot.data || shot.data.length > 70_000) return "";
    return shot.data;
  } catch {
    return "";
  } finally {
    await tell(tabId, "show", { label: "No controle" });
  }
}

async function viewportClip(tabId: number, scale: number): Promise<{ x: number; y: number; width: number; height: number; scale: number } | undefined> {
  try {
    const metrics = await send<{ cssVisualViewport?: { clientWidth?: number; clientHeight?: number; pageX?: number; pageY?: number } }>(
      tabId,
      "Page.getLayoutMetrics",
    );
    const vp = metrics.cssVisualViewport;
    if (!vp?.clientWidth || !vp.clientHeight) return undefined;
    return { x: vp.pageX ?? 0, y: vp.pageY ?? 0, width: vp.clientWidth, height: vp.clientHeight, scale };
  } catch {
    return undefined;
  }
}

async function screenshot(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  await tell(tabId, "hide");
  await sleep(40);
  const quality = clamp(Number(args.quality ?? 28), 10, 80);
  const scale = clamp(Number(args.scale ?? 0.5), 0.2, 1);
  const format = args.format === "webp" ? "webp" : "jpeg";
  let shot: { data?: string } = {};
  try {
    const clip = await viewportClip(tabId, scale);
    try {
      shot = await send<{ data?: string }>(tabId, "Page.captureScreenshot", { format, quality, clip, captureBeyondViewport: false });
    } catch {
      shot = await send<{ data?: string }>(tabId, "Page.captureScreenshot", { format: "jpeg", quality, clip, captureBeyondViewport: false });
    }
    if ((shot.data?.length ?? 0) > 90_000) {
      const smaller = await viewportClip(tabId, Math.min(scale, 0.35));
      shot = await send<{ data?: string }>(tabId, "Page.captureScreenshot", { format: "jpeg", quality: 18, clip: smaller, captureBeyondViewport: false });
    }
  } finally {
    await tell(tabId, "show", { label: "Vendo a tela" });
  }
  if (!shot.data || shot.data.length > 120_000) throw new Error("screenshot grande demais");
  return { text: "ok", image: shot.data };
}

async function batch(args: Record<string, unknown>): Promise<ToolResult> {
  const actions = Array.isArray(args.actions) ? args.actions : [];
  if (actions.length < 1 || actions.length > 15) throw new Error("lote de 1 a 15 ações");
  const lines: string[] = [];
  for (const step of actions) {
    ensureLive();
    if (!step || typeof step !== "object") throw new Error("ação inválida");
    const tool = String((step as { tool?: string }).tool ?? "");
    if (!batchTools.has(tool)) throw new Error("o lote aceita click, type, form_input, key, scroll, wait, find e read_page");
    const result = await executeTool(tool, { ...(step as Record<string, unknown>), tabId: args.tabId, _approved: args._approved });
    lines.push(result.text.split("\n")[0].slice(0, 140));
  }
  return { text: lines.join("\n") };
}

async function tabs(args: Record<string, unknown>): Promise<ToolResult> {
  const op = String(args.action ?? args.op ?? "list");
  if (op === "create") return tabsCreate(args);
  if (op === "close") return tabsClose(args);
  if (op === "switch") return tabsSwitch(args);
  return tabsList();
}

async function tabsList(): Promise<ToolResult> {
  const tabs = await chrome.tabs.query({});
  const lines = tabs.slice(0, 20).map((tab) => `${tab.id}\t${tab.active ? "*" : ""}\t${(tab.title ?? "").slice(0, 40)}\t${hostOf(tab.url ?? "") ?? ""}`);
  return { text: lines.join("\n") || "nenhuma aba" };
}

async function tabsCreate(args: Record<string, unknown>): Promise<ToolResult> {
  const url = args.url ? String(args.url) : undefined;
  if (url) assertHttp(url);
  const tab = await chrome.tabs.create({ url, active: true });
  if (url && tab.id) {
    await waitComplete(tab.id);
    await attach(tab.id);
    await waitIdle(tab.id, 4000);
  }
  return { text: `ok aba ${tab.id}` };
}

async function tabsClose(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = Number(args.tabId);
  if (!Number.isFinite(tabId)) throw new Error("tabId obrigatório");
  await chrome.tabs.remove(tabId);
  attached.delete(tabId);
  return { text: `ok ${tabId}` };
}

async function tabsSwitch(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = Number(args.tabId);
  if (!Number.isFinite(tabId)) throw new Error("tabId obrigatório");
  await chrome.tabs.update(tabId, { active: true });
  const tab = await tabInfo(tabId);
  return { text: shortOk(tab) };
}

async function dom(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  const cap = 8_000;
  if (args.ref) {
    const html = await send<{ outerHTML?: string }>(tabId, "DOM.getOuterHTML", {
      backendNodeId: (await withRef(tabId, args.ref)).backendNodeId,
    });
    return { text: (html.outerHTML ?? "").slice(0, cap) };
  }
  const doc = await send<{ root?: { nodeId?: number } }>(tabId, "DOM.getDocument", { depth: 1, pierce: true });
  const html = await send<{ outerHTML?: string }>(tabId, "DOM.getOuterHTML", { nodeId: doc.root?.nodeId });
  return { text: (html.outerHTML ?? "").slice(0, cap) };
}

async function styles(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  const info = await withRef(tabId, args.ref);
  const pushed = await send<{ nodeIds?: number[] }>(tabId, "DOM.pushNodesByBackendIdsToFrontend", {
    backendNodeIds: [info.backendNodeId],
  });
  const nodeId = pushed.nodeIds?.[0];
  if (!nodeId) throw new Error("nó do DOM não encontrado");
  await send(tabId, "CSS.enable");
  const got = await send<{ computedStyle?: { name: string; value: string }[] }>(tabId, "CSS.getComputedStyleForNode", { nodeId });
  const want = new Set(["color", "background-color", "font-size", "font-family", "display", "position", "width", "height"]);
  const lines = (got.computedStyle ?? []).filter((item) => want.has(item.name)).map((item) => `${item.name}: ${item.value}`);
  return { text: lines.join("\n") || "sem estilos" };
}

async function runJs(args: Record<string, unknown>): Promise<ToolResult> {
  if (!approved(args)) throw new NeedsApproval("javascript_exec precisa de aprovação");
  const expression = String(args.expression ?? "");
  if (!expression.trim()) throw new Error("expression vazia");
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  const result = await send<{ result?: { value?: unknown; description?: string }; exceptionDetails?: { text?: string } }>(
    tabId,
    "Runtime.evaluate",
    { expression, returnByValue: true, awaitPromise: true, timeout: 8_000 },
  );
  if (result.exceptionDetails) throw new Error(redactSecrets(result.exceptionDetails.text ?? "exceção"));
  const value = result.result?.value !== undefined ? JSON.stringify(result.result.value) : result.result?.description ?? "";
  return { text: redactSecrets(String(value)).slice(0, 4_000) };
}

async function upload(args: Record<string, unknown>): Promise<ToolResult> {
  if (!approved(args)) throw new NeedsApproval("enviar um arquivo precisa de aprovação");
  const files = Array.isArray(args.files) ? args.files.map((item) => String(item)).filter(Boolean) : [];
  if (!files.length) throw new Error("informe files");
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  const info = await withRef(tabId, args.ref);
  await send(tabId, "DOM.setFileInputFiles", { files, backendNodeId: info.backendNodeId });
  return { text: `ok upload ${files.length}` };
}

async function dialog(args: Record<string, unknown>): Promise<ToolResult> {
  const accept = args.accept !== false && args.accept !== "false";
  if (accept && !approved(args)) throw new NeedsApproval("aceitar o diálogo precisa de aprovação");
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  if (!pendingDialog.has(tabId)) throw new Error("não há diálogo aberto");
  await send(tabId, "Page.handleJavaScriptDialog", { accept, promptText: args.text ? String(args.text) : undefined });
  pendingDialog.delete(tabId);
  return { text: accept ? "ok dialog" : "ok dialog negado" };
}

async function pick(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await tell(tabId, "pick", { label: "Aponte um elemento" });
  const text = await new Promise<string>((resolve) => {
    const timer = setTimeout(() => {
      pickResolve = null;
      resolve("tempo esgotado");
    }, 20_000);
    pickResolve = (value) => {
      clearTimeout(timer);
      resolve(value || "vazio");
    };
  });
  await tell(tabId, "show", { label: "No controle" });
  return { text: text.slice(0, 240) };
}

async function waitFor(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  const text = String(args.text ?? "");
  const budget = clamp(Number(args.ms ?? (text ? 8_000 : 3_000)), 0, 10_000);
  if (text) {
    const start = Date.now();
    while (Date.now() - start < budget) {
      ensureLive();
      const page = await snapshot(tabId, { fresh: true, scope: "all", limit: 80 });
      if (page.text.toLowerCase().includes(text.toLowerCase())) return { text: "ok" };
      await waitIdle(tabId, 400);
    }
    return { text: "não apareceu" };
  }
  await waitIdle(tabId, budget);
  const tab = await tabInfo(tabId);
  return { text: shortOk(tab, noteOf(tabId)) };
}
