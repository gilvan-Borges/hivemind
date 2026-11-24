import { abortCall, detachAll, executeTool, NeedsApproval, setCurrentCall } from "./cdp.js";
import { isLoopbackWebSocket, redactSecrets } from "./pure.js";

const HOST = "com.gilvan_borges.hivemind";
const activities: string[] = [];
let nativePort: ChromePort | null = null;
let socket: WebSocket | null = null;
let connected = false;
let controlling = false;
let mode: "native" | "socket" | "" = "";
const panels = new Set<ChromePort>();

interface BridgeMessage {
  type?: string;
  id?: string;
  tool?: string;
  args?: Record<string, unknown>;
  text?: string;
  ok?: boolean;
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

function pushActivity(text: string): void {
  activities.push(text);
  if (activities.length > 30) activities.shift();
  broadcast({ type: "activity", text });
}

function state(): unknown {
  return { type: "state", connected, controlling, mode, activities };
}

function send(message: unknown): void {
  const payload = message;
  if (mode === "native" && nativePort) {
    nativePort.postMessage(payload);
    return;
  }
  if (socket && socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify(payload));
}

async function onBridge(message: BridgeMessage): Promise<void> {
  if (message.type === "hello" && message.ok) {
    connected = true;
    broadcast(state());
    return;
  }
  if (message.type === "activity" && message.text) {
    pushActivity(message.text);
    return;
  }
  if (message.type === "chat_reply") {
    broadcast({ type: "chat_reply", text: message.text ?? "" });
    return;
  }
  if (message.type === "abort" && message.id) {
    abortCall(message.id);
    return;
  }
  if (message.type !== "call" || !message.id || !message.tool) return;
  controlling = true;
  setCurrentCall(message.id);
  broadcast(state());
  try {
    const result = await executeTool(message.tool, message.args ?? {});
    send({ type: "result", id: message.id, ok: true, text: result.text, image: result.image });
  } catch (err) {
    if (err instanceof NeedsApproval) {
      send({ type: "result", id: message.id, ok: false, code: "needs_approval", text: err.message });
    } else {
      const text = redactSecrets(err instanceof Error ? err.message : String(err));
      send({ type: "result", id: message.id, ok: false, text });
    }
  } finally {
    controlling = false;
    broadcast(state());
  }
}

function connectNative(): void {
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
        broadcast(state());
        void fallbackSocket();
      }
    });
    port.postMessage({ type: "hello", role: "extension" });
  } catch {
    void fallbackSocket();
  }
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
        broadcast(state());
      }
    }
  };
}

function stopNow(): void {
  send({ type: "stop" });
  abortCall("stop");
  controlling = false;
  void detachAll();
  broadcast(state());
}

chrome.runtime.onConnect.addListener((port) => {
  if (port.name !== "panel") return;
  panels.add(port);
  port.onDisconnect.addListener(() => panels.delete(port));
  port.postMessage(state());
  port.onMessage.addListener((message: { type?: string; text?: string; token?: string; port?: number }) => {
    if (message.type === "chat" && message.text) {
      pushActivity("você: " + message.text.slice(0, 180));
      send({ type: "chat", text: message.text });
    } else if (message.type === "stop") {
      stopNow();
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
  if (message?.type === "hivemind-test-connect" && typeof message.token === "string") {
    connectSocket(message.token, Number(message.port) || 47891);
    sendResponse({ ok: true });
    return true;
  }
  return undefined;
});

chrome.alarms.create("hivemind-reconnect", { periodInMinutes: 1 });
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === "hivemind-reconnect" && !connected) connectNative();
});

void chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: true });
connectNative();

// O teste end-to-end chama isto no service worker. Sem o token, não conecta.
(globalThis as unknown as { hivemindTestConnect?: (token: string, port?: number) => void }).hivemindTestConnect = (token, port) => {
  connectSocket(token, port ?? 47891);
};
