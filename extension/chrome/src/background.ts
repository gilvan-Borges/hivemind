import { abortCall, captureThumb, detachAll, executeTool, NeedsApproval, setCurrentCall } from "./cdp.js";
import { hostOf, isLoopbackWebSocket, redactSecrets } from "./pure.js";

const HOST = "com.gilvan_borges.hivemind";
let nativePort: ChromePort | null = null;
let socket: WebSocket | null = null;
let connected = false;
let controlling = false;
let mode: "native" | "socket" | "" = "";
let reconnects = 0;
let hiveGroupId: number | null = null;
let activeOverlayTabId: number | null = null;
const groupedTabs = new Set<number>();
const panels = new Set<ChromePort>();
const decisions = new Map<string, (choice: string) => void>();

async function ensureTabGroup(tabId: number): Promise<void> {
  if (!chrome.tabs.group) return;
  try {
    let gid: number;
    if (hiveGroupId !== null) {
      try {
        gid = await chrome.tabs.group({ tabIds: [tabId], groupId: hiveGroupId });
      } catch {
        gid = await chrome.tabs.group({ tabIds: [tabId] });
      }
    } else {
      gid = await chrome.tabs.group({ tabIds: [tabId] });
    }
    hiveGroupId = gid;
    groupedTabs.add(tabId);
    await chrome.tabGroups?.update(gid, { title: "Agent Hive", color: "yellow", collapsed: false });
  } catch {
    // Some internal or restricted tabs cannot be grouped; ignore cleanly.
  }
}

async function clearTabGroup(): Promise<void> {
  if (!chrome.tabs.ungroup || groupedTabs.size === 0) {
    hiveGroupId = null;
    groupedTabs.clear();
    return;
  }
  const ids = Array.from(groupedTabs);
  groupedTabs.clear();
  hiveGroupId = null;
  for (const tid of ids) {
    try {
      await chrome.tabs.ungroup(tid);
    } catch {
      // Tab may already be closed or ungrouped.
    }
  }
}

async function syncTabVisualEffect(active: boolean, label?: string): Promise<void> {
  const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
  const tabId = tabs[0]?.id ?? null;
  if (active && tabId !== null) {
    if (activeOverlayTabId !== null && activeOverlayTabId !== tabId) {
      void chrome.tabs.sendMessage(activeOverlayTabId, { type: "hivemind-overlay", op: "hide" }).catch(() => {});
    }
    activeOverlayTabId = tabId;
    await ensureTabGroup(tabId);
    void chrome.tabs.sendMessage(tabId, {
      type: "hivemind-overlay",
      op: "show",
      label: label || "Agindo na página…",
    }).catch(() => {});
    return;
  }
  if (!active) {
    if (activeOverlayTabId !== null) {
      const prev = activeOverlayTabId;
      activeOverlayTabId = null;
      void chrome.tabs.sendMessage(prev, { type: "hivemind-overlay", op: "hide" }).catch(() => {});
    }
    if (tabId !== null) {
      void chrome.tabs.sendMessage(tabId, { type: "hivemind-overlay", op: "hide" }).catch(() => {});
    }
  }
}

interface BridgeMessage {
  type?: string;
  id?: string;
  tool?: string;
  args?: Record<string, unknown>;
  text?: string;
  ok?: boolean;
  code?: string;
  host?: string;
  image?: string;
}

function broadcast(message: unknown): void {
  for (const panel of panels) {
    try {
      panel.postMessage(message);
    } catch {
      panels.delete(panel);
    }
  }
}

async function publish(): Promise<void> {
  let title = "";
  let url = "";
  let tabId = 0;
  try {
    const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
    const tab = tabs[0];
    title = tab?.title ?? "";
    url = tab?.url ?? "";
    tabId = tab?.id ?? 0;
  } catch {
    /* sem aba */
  }
  broadcast({ type: "state", connected, controlling, mode, title, url, tabId, host: hostOf(url) ?? "" });
}

function send(message: unknown): void {
  if (mode === "native" && nativePort) {
    nativePort.postMessage(message);
    return;
  }
  if (socket && socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify(message));
}

function askPanel(id: string, text: string, host: string): Promise<string> {
  if (panels.size === 0) return Promise.resolve("");
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      decisions.delete(id);
      resolve("");
    }, 45_000);
    decisions.set(id, (choice) => {
      clearTimeout(timer);
      resolve(choice);
    });
    broadcast({ type: "approve", id, text, host });
  });
}

async function onBridge(message: BridgeMessage): Promise<void> {
  if (message.type === "hello" && message.ok) {
    connected = true;
    reconnects = 0;
    void publish();
    return;
  }
  if (message.type === "activity" && message.text) {
    broadcast({ type: "activity", text: message.text });
    return;
  }
  if (message.type === "chat_reply" || message.type === "chat_delta") {
    broadcast({ type: message.type, text: message.text ?? "", code: message.code ?? "" });
    return;
  }
  if (message.type === "approve") {
    broadcast({ type: "approve", id: message.id, text: message.text ?? "", host: message.host ?? "" });
    return;
  }
  if (message.type === "abort" && message.id) {
    abortCall(message.id);
    return;
  }
  if (message.type === "user_line" && message.text) {
    broadcast({ type: "user", text: message.text });
    return;
  }
  if (message.type !== "call" || !message.id || !message.tool) return;
  controlling = true;
  setCurrentCall(message.id);
  const label = stepLabel(message.tool, message.args ?? {});
  broadcast({ type: "step", id: message.id, phase: "start", label });
  void syncTabVisualEffect(true, label);
  void publish();
  try {
    const result = await runCall(message.id, message.tool, message.args ?? {});
    const thumb = message.tool === "screenshot" ? await stepThumb() : "";
    broadcast({ type: "step", id: message.id, phase: "done", label, thumb });
    send({ type: "result", id: message.id, ok: true, text: result.text, image: message.tool === "screenshot" ? result.image : undefined });
  } catch (err) {
    const text = redactSecrets(err instanceof Error ? err.message : String(err));
    broadcast({ type: "step", id: message.id, phase: "error", label, text });
    if (err instanceof NeedsApproval) {
      send({ type: "result", id: message.id, ok: false, code: "needs_approval", text: err.message });
    } else {
      send({ type: "result", id: message.id, ok: false, text });
    }
  } finally {
    controlling = false;
    void syncTabVisualEffect(false);
    void publish();
  }
}

async function runCall(id: string, tool: string, args: Record<string, unknown>) {
  try {
    return await executeTool(tool, args);
  } catch (err) {
    if (!(err instanceof NeedsApproval) || panels.size === 0) throw err;
    const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
    const host = hostOf(tabs[0]?.url ?? "") ?? "";
    const choice = await askPanel(id, err.message, host);
    if (choice !== "once" && choice !== "always") {
      if (choice === "deny") throw new Error("negado no painel");
      throw err;
    }
    if (choice === "always" && host) send({ type: "allow_host", host });
    setCurrentCall(id);
    return executeTool(tool, { ...args, _approved: true });
  }
}

function connectNative(): void {
  if (nativePort) return;
  try {
    const port = chrome.runtime.connectNative(HOST);
    nativePort = port;
    mode = "native";
    port.onMessage.addListener((message) => {
      void onBridge(message as BridgeMessage);
    });
    port.onDisconnect.addListener(() => {
      nativePort = null;
      if (mode === "native") {
        mode = "";
        connected = false;
        void publish();
        scheduleReconnect();
      }
    });
    port.postMessage({ type: "hello", role: "extension" });
  } catch {
    scheduleReconnect();
  }
}

function scheduleReconnect(): void {
  reconnects += 1;
  const wait = Math.min(8000, 400 * 2 ** Math.min(reconnects, 4));
  setTimeout(() => {
    if (connected) return;
    if (nativePort) return;
    void fallbackSocket().then(() => {
      if (!connected) connectNative();
    });
  }, wait);
}

async function fallbackSocket(): Promise<void> {
  const saved = await chrome.storage.local.get(["bridgeToken", "bridgePort"]);
  const token = typeof saved.bridgeToken === "string" ? saved.bridgeToken : "";
  const port = typeof saved.bridgePort === "number" ? saved.bridgePort : 47891;
  if (token) connectSocket(token, port);
}

function connectSocket(token: string, port: number): void {
  const url = `ws://127.0.0.1:${port}/browser`;
  if (!isLoopbackWebSocket(url)) return;
  if (socket) socket.close();
  const ws = new WebSocket(url);
  socket = ws;
  mode = "socket";
  ws.onopen = () => {
    ws.send(JSON.stringify({ type: "hello", role: "extension", token }));
  };
  ws.onmessage = (ev) => {
    try {
      void onBridge(JSON.parse(String(ev.data)) as BridgeMessage);
    } catch {
      /* quadro inválido */
    }
  };
  ws.onclose = () => {
    if (socket === ws) {
      socket = null;
      if (mode === "socket") {
        mode = "";
        connected = false;
        void publish();
        scheduleReconnect();
      }
    }
  };
}

function stopNow(): void {
  send({ type: "stop" });
  abortCall("stop");
  controlling = false;
  void detachAll();
  void syncTabVisualEffect(false);
  void clearTabGroup();
  void publish();
}

async function beginPick(): Promise<void> {
  const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
  const tabId = tabs[0]?.id;
  if (!tabId) return;
  try {
    await chrome.tabs.sendMessage(tabId, { type: "hivemind-overlay", op: "pick", label: "Aponte um elemento" });
  } catch {
    broadcast({ type: "error", text: "Não deu para apontar nesta página. Recarregue a aba e tente de novo." });
  }
}

chrome.runtime.onConnect.addListener((port) => {
  if (port.name !== "panel") return;
  panels.add(port);
  port.onDisconnect.addListener(() => panels.delete(port));
  void publish();
  port.onMessage.addListener((message: { type?: string; text?: string; token?: string; port?: number; id?: string; choice?: string }) => {
    if (message.type === "chat" && message.text) {
      void chrome.tabs.query({ active: true, currentWindow: true }).then((tabs) => {
        if (tabs[0]?.id !== undefined) void ensureTabGroup(tabs[0].id);
      });
      send({ type: "chat", text: message.text });
    } else if (message.type === "stop") {
      stopNow();
    } else if (message.type === "pick") {
      void beginPick();
    } else if (message.type === "approve" && message.id) {
      const decide = decisions.get(message.id);
      decisions.delete(message.id);
      decide?.(message.choice ?? "deny");
    } else if (message.type === "pair" && message.token) {
      const bridgePort = message.port && message.port > 0 ? message.port : 47891;
      void chrome.storage.local.set({ bridgeToken: message.token, bridgePort });
      if (nativePort) nativePort.disconnect();
      connectSocket(message.token, bridgePort);
    }
  });
});

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  if (message?.type === "hivemind-overlay-stop") {
    stopNow();
    sendResponse({ ok: true });
    return true;
  }
  if (message?.type === "hivemind-picked") {
    broadcast({ type: "picked", text: String(message.text ?? "") });
    sendResponse({ ok: true });
    return true;
  }
  if (message?.type === "hivemind-test-connect" && typeof message.token === "string") {
    connectSocket(message.token, Number(message.port) || 47891);
    sendResponse({ ok: true });
    return true;
  }
  return undefined;
});

chrome.tabs.onActivated.addListener(() => {
  if (controlling) {
    void syncTabVisualEffect(true, "Agindo na página…");
  }
  void publish();
});

chrome.alarms.create("hivemind-reconnect", { periodInMinutes: 1 });
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === "hivemind-reconnect" && !connected) connectNative();
});

void chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: true });

chrome.commands.onCommand.addListener(async (command) => {
  if (command !== "open-side-panel") return;
  const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
  const tabId = tabs[0]?.id;
  if (tabId) await chrome.sidePanel.open({ tabId });
});

function stepLabel(tool: string, args: Record<string, unknown>): string {
  const url = String(args.url ?? "").replace(/^https?:\/\//, "");
  switch (tool) {
    case "navigate":
    case "tabs_create":
      return "Abrindo " + (url.slice(0, 42) || "a página");
    case "type":
    case "form_input":
      return "Digitando";
    case "click":
      return "Clicando";
    case "read_page":
    case "find":
    case "get_page_text":
      return "Lendo a página";
    case "key":
      return args.key === "Enter" ? "Enviando" : "Tecla";
    case "screenshot":
      return "Vendo a tela";
    case "batch":
      return "Várias ações";
    default:
      return tool;
  }
}

async function stepThumb(): Promise<string> {
  const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
  const tabId = tabs[0]?.id;
  if (!tabId) return "";
  return captureThumb(tabId);
}

connectNative();

(globalThis as unknown as { hivemindTestConnect?: (token: string, port?: number) => void }).hivemindTestConnect = (token, port) => {
  connectSocket(token, port ?? 47891);
};
