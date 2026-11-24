interface ChromeDebuggee {
  tabId?: number;
}

interface ChromePort {
  name: string;
  postMessage(message: unknown): void;
  onMessage: { addListener(cb: (message: any) => void): void };
  onDisconnect: { addListener(cb: () => void): void };
  disconnect(): void;
}

interface ChromeTab {
  id?: number;
  url?: string;
  title?: string;
  active?: boolean;
  windowId?: number;
  status?: string;
}

declare const chrome: {
  runtime: {
    lastError: { message?: string } | undefined;
    id: string;
    connect(info?: { name?: string }): ChromePort;
    connectNative(name: string): ChromePort;
    sendMessage(message: unknown): Promise<unknown>;
    onConnect: { addListener(cb: (port: ChromePort) => void): void };
    onMessage: {
      addListener(cb: (message: any, sender: unknown, sendResponse: (response: unknown) => void) => boolean | void): void;
    };
  };
  debugger: {
    attach(target: ChromeDebuggee, version: string): Promise<void>;
    detach(target: ChromeDebuggee): Promise<void>;
    sendCommand(target: ChromeDebuggee, method: string, params?: object): Promise<any>;
    onEvent: { addListener(cb: (source: ChromeDebuggee, method: string, params?: any) => void): void };
    onDetach: { addListener(cb: (source: ChromeDebuggee) => void): void };
  };
  tabs: {
    query(query: object): Promise<ChromeTab[]>;
    create(create: { url?: string; active?: boolean }): Promise<ChromeTab>;
    remove(tabId: number): Promise<void>;
    update(tabId: number, props: { url?: string; active?: boolean }): Promise<ChromeTab>;
    get(tabId: number): Promise<ChromeTab>;
    sendMessage(tabId: number, message: unknown): Promise<unknown>;
    onUpdated: { addListener(cb: (tabId: number, info: { status?: string }) => void): void };
  };
  scripting: {
    executeScript(injection: { target: { tabId: number }; func: () => void }): Promise<unknown>;
  };
  sidePanel: {
    setPanelBehavior(options: { openPanelOnActionClick: boolean }): Promise<void>;
  };
  action: {
    setBadgeText(details: { text: string; tabId?: number }): Promise<void>;
    setBadgeBackgroundColor(details: { color: string }): Promise<void>;
  };
  storage: {
    local: {
      get(keys: string[]): Promise<Record<string, unknown>>;
      set(items: Record<string, unknown>): Promise<void>;
    };
  };
  alarms: {
    create(name: string, info: { periodInMinutes: number }): void;
    onAlarm: { addListener(cb: (alarm: { name: string }) => void): void };
  };
};
