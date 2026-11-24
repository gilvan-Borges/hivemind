import { findRefs, hostOf, isSensitiveLabel, readPage, redactSecrets, safeURL, type AxNode, type PageRef } from "./pure.js";

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
const consoleBuf: string[] = [];
const networkBuf: string[] = [];
const attached = new Set<number>();
const aborted = new Set<string>();
let currentCall = "";

export function setCurrentCall(id: string): void {
  currentCall = id;
}

export function abortCall(id: string): void {
  if (id) aborted.add(id);
}

function ensureLive(): void {
  if (currentCall && aborted.has(currentCall)) throw new Error("interrompido");
}

chrome.debugger.onEvent.addListener((source, method, params) => {
  if (method === "Runtime.consoleAPICalled") {
    const args = (params?.args as { value?: unknown; description?: string }[] | undefined) ?? [];
    const text = args
      .map((arg) => (arg.value !== undefined ? String(arg.value) : arg.description ?? ""))
      .join(" ");
    push(consoleBuf, `${params?.type ?? "log"}: ${redactSecrets(text)}`);
  }
  if (method === "Network.requestWillBeSent") {
    const req = params?.request ?? {};
    push(networkBuf, `${req.method ?? "GET"} ${safeURL(String(req.url ?? ""))}`);
  }
  if (method === "Network.responseReceived") {
    const res = params?.response ?? {};
    push(networkBuf, `${res.status ?? "?"} ${safeURL(String(res.url ?? ""))}`);
  }
  void source;
});

chrome.debugger.onDetach.addListener((source) => {
  if (source.tabId) attached.delete(source.tabId);
});

function push(buf: string[], line: string): void {
  buf.push(line.slice(0, 500));
  if (buf.length > 40) buf.shift();
}

async function send<T>(tabId: number, method: string, params: object = {}): Promise<T> {
  ensureLive();
  return chrome.debugger.sendCommand({ tabId }, method, params) as Promise<T>;
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
  await showOverlay(tabId);
}

export async function detachAll(): Promise<void> {
  for (const tabId of [...attached]) {
    attached.delete(tabId);
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
  if (Number.isFinite(id) && id > 0) return id;
  const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
  const tab = tabs[0];
  if (!tab?.id) throw new Error("nenhuma aba ativa");
  return tab.id;
}

function requireRef(tabId: number, ref: unknown): PageRef {
  const key = String(ref ?? "");
  const info = refsByTab.get(tabId)?.[key];
  if (!info) throw new Error(`ref ${key || "vazia"} não está na última leitura. Chame read_page.`);
  return info;
}

function assertHttp(raw: string): void {
  if (!raw || raw === "about:blank") return;
  if (!hostOf(raw)) throw new Error("só http(s), sem usuário na URL");
}

async function tell(tabId: number, op: string, extra: Record<string, unknown> = {}): Promise<void> {
  for (let i = 0; i < 8; i++) {
    try {
      await chrome.tabs.sendMessage(tabId, { type: "hivemind-overlay", op, ...extra });
      return;
    } catch {
      await sleep(120);
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
      const described = await send<{ node?: { nodeName?: string; backendNodeId?: number } }>(tabId, "DOM.describeNode", { nodeId });
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

async function focusRef(tabId: number, info: PageRef): Promise<void> {
  await send(tabId, "DOM.focus", { backendNodeId: info.backendNodeId });
}

function approved(args: Record<string, unknown>): boolean {
  return args._approved === true;
}

export async function executeTool(tool: string, args: Record<string, unknown>): Promise<ToolResult> {
  switch (tool) {
    case "navigate":
      return navigate(args);
    case "read_page":
      return read(args);
    case "find":
      return find(args);
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
    case "tabs_list":
      return tabsList();
    case "tabs_create":
      return tabsCreate(args);
    case "tabs_close":
      return tabsClose(args);
    case "tabs_switch":
      return tabsSwitch(args);
    case "console_read":
      return { text: consoleBuf.join("\n") || "console vazio" };
    case "network_read":
      return { text: networkBuf.join("\n") || "rede vazia" };
    case "get_dom":
      return dom(args);
    case "computed_styles":
      return styles(args);
    case "javascript_exec":
      return runJs(args);
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
  return { text: `aberto ${url}` };
}

async function waitComplete(tabId: number): Promise<void> {
  const start = Date.now();
  while (Date.now() - start < 15_000) {
    ensureLive();
    const tab = await chrome.tabs.get(tabId);
    if (tab.status === "complete") return;
    await sleep(120);
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function read(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  await tell(tabId, "status", { label: "Lendo a página" });
  const tree = await send<{ nodes?: AxNode[] }>(tabId, "Accessibility.getFullAXTree");
  const page = readPage(tree.nodes ?? []);
  refsByTab.set(tabId, page.refs);
  const tab = await chrome.tabs.get(tabId);
  const header = `tab ${tabId}: ${tab.title ?? ""} — ${safeURL(tab.url ?? "")}`;
  const text = header + "\n" + (page.text || "(árvore vazia)");
  treeByTab.set(tabId, text);
  return { text };
}

async function find(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  let tree = treeByTab.get(tabId);
  if (!tree) tree = (await read(args)).text;
  const hits = findRefs(tree, String(args.query ?? ""));
  return { text: hits || "nada encontrado" };
}

async function click(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  let name = String(args.name ?? "");
  let x = Number(args.x);
  let y = Number(args.y);
  if (args.ref) {
    const info = requireRef(tabId, args.ref);
    name = name || info.name;
    try {
      await send(tabId, "DOM.scrollIntoViewIfNeeded", { backendNodeId: info.backendNodeId });
    } catch {
      /* o nó pode não rolar */
    }
    if ((args.submit === true || isSensitiveLabel(name) || isSensitiveLabel(info.role + " " + info.name)) && !approved(args)) {
      throw new NeedsApproval(`o elemento ${info.role} "${info.name}" parece enviar, comprar ou confirmar`);
    }
    const box = await clickBox(tabId, info.backendNodeId);
    x = box.left + Math.min(48, Math.max(8, box.width / 3));
    y = box.top + Math.min(16, Math.max(6, box.height / 2));
    await tell(tabId, "highlight", { x: box.left, y: box.top, w: box.width, h: box.height, label: "Clicando" });
  } else if (args.submit === true && !approved(args)) {
    throw new NeedsApproval("clique de envio precisa de aprovação");
  }
  if (!Number.isFinite(x) || !Number.isFinite(y)) throw new Error("informe ref ou x e y");
  await tell(tabId, "cursor", { x, y, label: "Clicando" });
  await sleep(520);
  await tell(tabId, "ripple", { x, y });
  await clickAt(tabId, x, y);
  if (args.ref) {
    try {
      await send(tabId, "DOM.focus", { backendNodeId: requireRef(tabId, args.ref).backendNodeId });
      await send(tabId, "Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13 });
      await send(tabId, "Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13 });
    } catch {
      /* o clique do mouse já pode ter navegado */
    }
  }
  return { text: `clique em ${Math.round(x)},${Math.round(y)}` };
}

async function typeText(args: Record<string, unknown>): Promise<ToolResult> {
  const text = String(args.text ?? "");
  if (!text) throw new Error("texto vazio");
  if (text.length > 20_000) throw new Error("texto longo demais");
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  await tell(tabId, "status", { label: "Digitando" });
  if (args.ref) {
    const info = requireRef(tabId, args.ref);
    await focusRef(tabId, info);
    try {
      const box = await boxOf(tabId, info.backendNodeId);
      await tell(tabId, "highlight", { x: box.left, y: box.top, w: box.width, h: box.height, label: "Digitando" });
      await tell(tabId, "cursor", { x: box.x, y: box.y, label: "Digitando" });
    } catch {
      /* o campo pode não ter caixa ainda */
    }
  }
  const animated = text.slice(0, 48);
  const rest = text.slice(animated.length);
  for (const ch of animated) {
    ensureLive();
    await send(tabId, "Input.insertText", { text: ch });
    await sleep(18);
  }
  if (rest) await send(tabId, "Input.insertText", { text: rest });
  return { text: `digitado ${text.length} caracteres` };
}

async function formInput(args: Record<string, unknown>): Promise<ToolResult> {
  const infoName = args.ref ? requireRef(await activeTab(args.tabId), args.ref).name : "";
  if ((args.submit === true || isSensitiveLabel(infoName) || isSensitiveLabel(String(args.name ?? ""))) && !approved(args)) {
    throw new NeedsApproval("enviar o formulário precisa de aprovação");
  }
  const typed = await typeText({ ...args, text: String(args.value ?? "") });
  if (args.submit === true) {
    const tabId = await activeTab(args.tabId);
    await pressKey({ tabId, key: "Enter" });
  }
  return typed;
}

async function scroll(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  let x = 400;
  let y = 300;
  if (args.ref) {
    const center = await boxOf(tabId, requireRef(tabId, args.ref).backendNodeId);
    x = center.x;
    y = center.y;
  }
  const dx = clamp(Number(args.dx ?? 0), -2000, 2000);
  const dy = clamp(Number(args.dy ?? 400), -2000, 2000);
  await send(tabId, "Input.dispatchMouseEvent", { type: "mouseWheel", x, y, deltaX: dx, deltaY: dy });
  return { text: `rolou ${dx},${dy}` };
}

function clamp(n: number, min: number, max: number): number {
  if (!Number.isFinite(n)) return 0;
  return Math.min(max, Math.max(min, n));
}

async function pressKey(args: Record<string, unknown>): Promise<ToolResult> {
  const key = String(args.key ?? "");
  if (!key) throw new Error("tecla vazia");
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
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
  return { text: `tecla ${key}` };
}

export async function captureThumb(tabId: number): Promise<string> {
  await tell(tabId, "hide");
  await sleep(40);
  try {
    const shot = await send<{ data?: string }>(tabId, "Page.captureScreenshot", { format: "jpeg", quality: 16 });
    if (!shot.data || shot.data.length > 90_000) return "";
    return shot.data;
  } catch {
    return "";
  } finally {
    await tell(tabId, "show", { label: "No controle" });
  }
}

async function screenshot(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  await tell(tabId, "hide");
  await sleep(80);
  let shot: { data?: string };
  try {
    shot = await send<{ data?: string }>(tabId, "Page.captureScreenshot", { format: "jpeg", quality: 40 });
    if ((shot.data?.length ?? 0) > 700_000) {
      shot = await send<{ data?: string }>(tabId, "Page.captureScreenshot", { format: "jpeg", quality: 20 });
    }
  } finally {
    await tell(tabId, "show", { label: "Vendo a tela" });
  }
  if (!shot.data || shot.data.length > 700_000) throw new Error("screenshot grande demais");
  return { text: "screenshot jpeg da aba", image: shot.data };
}

async function tabsList(): Promise<ToolResult> {
  const tabs = await chrome.tabs.query({});
  const lines = tabs.map((tab) => `${tab.id}\t${tab.active ? "ativa" : ""}\t${tab.title ?? ""}\t${safeURL(tab.url ?? "")}`);
  return { text: lines.join("\n") || "nenhuma aba" };
}

async function tabsCreate(args: Record<string, unknown>): Promise<ToolResult> {
  const url = args.url ? String(args.url) : undefined;
  if (url) assertHttp(url);
  const tab = await chrome.tabs.create({ url, active: true });
  if (url && tab.id) await waitComplete(tab.id);
  return { text: `aba ${tab.id}` };
}

async function tabsClose(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = Number(args.tabId);
  if (!Number.isFinite(tabId)) throw new Error("tabId obrigatório");
  await chrome.tabs.remove(tabId);
  attached.delete(tabId);
  return { text: `fechou ${tabId}` };
}

async function tabsSwitch(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = Number(args.tabId);
  if (!Number.isFinite(tabId)) throw new Error("tabId obrigatório");
  await chrome.tabs.update(tabId, { active: true });
  return { text: `aba ${tabId} ativa` };
}

async function dom(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  if (args.ref) {
    const html = await send<{ outerHTML?: string }>(tabId, "DOM.getOuterHTML", {
      backendNodeId: requireRef(tabId, args.ref).backendNodeId,
    });
    return { text: (html.outerHTML ?? "").slice(0, 50_000) };
  }
  const doc = await send<{ root?: { nodeId?: number } }>(tabId, "DOM.getDocument", { depth: 1, pierce: false });
  const html = await send<{ outerHTML?: string }>(tabId, "DOM.getOuterHTML", { nodeId: doc.root?.nodeId });
  return { text: (html.outerHTML ?? "").slice(0, 50_000) };
}

async function styles(args: Record<string, unknown>): Promise<ToolResult> {
  const tabId = await activeTab(args.tabId);
  await attach(tabId);
  const info = requireRef(tabId, args.ref);
  const pushed = await send<{ nodeIds?: number[] }>(tabId, "DOM.pushNodesByBackendIdsToFrontend", {
    backendNodeIds: [info.backendNodeId],
  });
  const nodeId = pushed.nodeIds?.[0];
  if (!nodeId) throw new Error("nó do DOM não encontrado");
  await send(tabId, "CSS.enable");
  const got = await send<{ computedStyle?: { name: string; value: string }[] }>(tabId, "CSS.getComputedStyleForNode", {
    nodeId,
  });
  const want = new Set(["color", "background-color", "font-size", "font-family", "display", "position", "width", "height", "margin", "padding"]);
  const lines = (got.computedStyle ?? [])
    .filter((item) => want.has(item.name))
    .map((item) => `${item.name}: ${item.value}`);
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
    { expression, returnByValue: true, awaitPromise: true, timeout: 10_000 },
  );
  if (result.exceptionDetails) throw new Error(redactSecrets(result.exceptionDetails.text ?? "exceção"));
  const value = result.result?.value !== undefined ? JSON.stringify(result.result.value) : result.result?.description ?? "";
  return { text: redactSecrets(String(value)).slice(0, 20_000) };
}

async function waitFor(args: Record<string, unknown>): Promise<ToolResult> {
  const ms = clamp(Number(args.ms ?? 0), 0, 10_000);
  const text = String(args.text ?? "");
  if (text) {
    const start = Date.now();
    while (Date.now() - start < 10_000) {
      ensureLive();
      const page = await read(args);
      if (page.text.toLowerCase().includes(text.toLowerCase())) return { text: "texto encontrado" };
      await sleep(200);
    }
    return { text: "texto não apareceu" };
  }
  await sleep(ms);
  return { text: `esperou ${ms}ms` };
}
