const log = document.querySelector<HTMLElement>("#log")!;
const composer = document.querySelector<HTMLFormElement>("#composer")!;
const input = document.querySelector<HTMLTextAreaElement>("#text")!;
const statusEl = document.querySelector<HTMLElement>("#status")!;
const stopBtn = document.querySelector<HTMLButtonElement>("#stop")!;
const pickBtn = document.querySelector<HTMLButtonElement>("#pick")!;
const pairForm = document.querySelector<HTMLFormElement>("#pair")!;
const tokenInput = document.querySelector<HTMLInputElement>("#token")!;
const tabctx = document.querySelector<HTMLElement>("#tabctx")!;
const meter = document.querySelector<HTMLElement>("#meter")!;
const clearBtn = document.querySelector<HTMLButtonElement>("#clear")!;

const port = chrome.runtime.connect({ name: "panel" });

type Step = { id: string; label: string; phase: string; text?: string; thumb?: string };
type Item =
  | { k: "user"; text: string }
  | { k: "agent"; text: string; error?: boolean; pending?: boolean }
  | { k: "group"; steps: Step[]; live: boolean }
  | { k: "approve"; id: string; text: string; host: string; choice?: string };

let items: Item[] = [];
let tabId = 0;
let lastUser = "";
let stream = "";
const reduceMotion = matchMedia("(prefers-reduced-motion: reduce)").matches;

const suggestions = ["Resumir esta página", "Pesquisar HiveMind IDE github", "Clicar no botão principal"];

function tokensOf(text: string): number {
  return Math.ceil(new TextEncoder().encode(text).length / 4);
}

function sessionTokens(): number {
  let n = 0;
  for (const item of items) {
    if (item.k === "user" || item.k === "agent") n += tokensOf(item.text);
    if (item.k === "group") n += item.steps.reduce((sum, step) => sum + tokensOf(step.label + (step.text ?? "")), 0);
    if (item.k === "approve") n += tokensOf(item.text);
  }
  return n;
}

function paintMeter(): void {
  const tokens = sessionTokens();
  // shortcut: preço fixo de $3 por milhão de tokens, trocar quando a ponte enviar o custo do modelo.
  const cost = (tokens * 3) / 1_000_000;
  meter.textContent = `sessão ≈ ${tokens} tok · ~$${cost.toFixed(4)}`;
}

function escapeHtml(text: string): string {
  return text.replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[ch]!);
}

function renderMarkdown(src: string): string {
  const codeBlocks: string[] = [];
  let text = escapeHtml(src).replace(/```([a-zA-Z0-9_-]*)\n([\s\S]*?)```/g, (_m, _lang, code) => {
    const idx = codeBlocks.length;
    codeBlocks.push(`<pre><code>${code.replace(/\n$/, "")}</code></pre>`);
    return `\u0000CB${idx}\u0000`;
  });
  text = text.replace(/`([^`]+)`/g, "<code>$1</code>");
  text = text.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  text = text.replace(/^### (.+)$/gm, "<h3>$1</h3>");
  text = text.replace(/^## (.+)$/gm, "<h3>$1</h3>");
  text = text.replace(/^&gt; (.+)$/gm, "<blockquote>$1</blockquote>");
  text = text.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>');
  text = text.replace(/^- (.+)$/gm, "<li>$1</li>");
  text = text.replace(/(?:<li>[\s\S]*?<\/li>\s*)+/g, (block) => `<ul>${block}</ul>`);
  text = text.replace(/\n/g, "<br>");
  text = text.replace(/\u0000CB(\d+)\u0000/g, (_m, idx) => codeBlocks[Number(idx)] ?? "");
  return text;
}

function scroll(): void {
  log.scrollTop = log.scrollHeight;
}

function closeLive(): void {
  const group = [...items].reverse().find((item) => item.k === "group" && item.live);
  if (group && group.k === "group") group.live = false;
}

async function save(): Promise<void> {
  if (!tabId) return;
  await chrome.storage.session.set({ ["chat:" + tabId]: items });
}

async function load(next: number): Promise<void> {
  if (!next || next === tabId) return;
  const previous = tabId;
  if (previous) await save();
  tabId = next;
  const stored = await chrome.storage.session.get(["chat:" + next]);
  if (tabId !== next) return;
  const raw = stored["chat:" + next];
  if (!previous && items.length > 0) {
    await save();
    return;
  }
  items = Array.isArray(raw) ? (raw as Item[]) : [];
  paint();
}

function paint(): void {
  log.replaceChildren();
  clearBtn.hidden = items.length === 0;
  if (items.length === 0) {
    const empty = document.createElement("section");
    empty.className = "empty";
    const title = document.createElement("p");
    title.textContent = "O que você quer fazer nesta página?";
    empty.append(title);
    for (const prompt of suggestions) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = prompt;
      button.addEventListener("click", () => send(prompt));
      empty.append(button);
    }
    log.append(empty);
    paintMeter();
    return;
  }
  for (const item of items) {
    if (item.k === "user") log.append(bubble("user", item.text));
    if (item.k === "agent") log.append(agentBubble(item.text, item.error, item.pending));
    if (item.k === "group") log.append(groupCard(item));
    if (item.k === "approve") log.append(approveCard(item));
  }
  paintMeter();
  scroll();
}

function bubble(kind: string, text: string): HTMLElement {
  const item = document.createElement("div");
  item.className = "bubble " + kind;
  item.textContent = text;
  return item;
}

function agentBubble(text: string, error?: boolean, pending?: boolean): HTMLElement {
  const item = document.createElement("div");
  if (pending && !text) {
    item.className = "bubble agent thinking";
    item.innerHTML = `<span class="thinking-dots" aria-hidden="true"><i></i><i></i><i></i></span><span>Pensando…</span>`;
    return item;
  }
  item.className = "bubble agent" + (error ? " error" : "");
  item.innerHTML = renderMarkdown(text);
  const actions = document.createElement("div");
  actions.className = "row-actions";
  const copy = document.createElement("button");
  copy.type = "button";
  copy.textContent = "Copiar";
  copy.addEventListener("click", () => {
    void copyText(text).then(() => {
      copy.textContent = "Copiado!";
      setTimeout(() => {
        copy.textContent = "Copiar";
      }, 1400);
    });
  });
  actions.append(copy);
  if (error) {
    const retry = document.createElement("button");
    retry.type = "button";
    retry.textContent = "Tentar de novo";
    retry.addEventListener("click", () => {
      if (lastUser) send(lastUser);
    });
    actions.append(retry);
  }
  item.append(actions);
  return item;
}

async function copyText(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const area = document.createElement("textarea");
    area.value = text;
    document.body.append(area);
    area.select();
    document.execCommand("copy");
    area.remove();
  }
}

function groupCard(item: Extract<Item, { k: "group" }>): HTMLElement {
  const details = document.createElement("details");
  details.className = "card" + (item.live ? " live" : " done");
  details.open = item.live;
  const summary = document.createElement("summary");
  const dot = document.createElement("span");
  dot.className = "dot";
  const title = document.createElement("span");
  title.className = "title";
  const done = item.steps.filter((step) => step.phase === "done" || step.phase === "error").length || item.steps.length;
  title.textContent = item.live ? "Executando…" : done === 1 ? "Fez 1 ação" : `Fez ${done} ações`;
  summary.append(dot, title);
  const list = document.createElement("div");
  list.className = "steps";
  for (const step of item.steps) {
    const row = document.createElement("div");
    row.className = "step " + step.phase;
    row.textContent = step.phase === "error" ? `${step.label}: ${step.text || "falhou"}` : step.label;
    if (step.thumb) {
      const img = document.createElement("img");
      img.alt = "Miniatura da aba";
      img.src = "data:image/jpeg;base64," + step.thumb;
      row.append(img);
    }
    if (step.phase === "error") {
      const retry = document.createElement("button");
      retry.type = "button";
      retry.className = "ghost";
      retry.textContent = "Tentar de novo";
      retry.addEventListener("click", () => {
        if (lastUser) send(lastUser);
      });
      row.append(retry);
    }
    list.append(row);
  }
  details.append(summary, list);
  return details;
}

function approveCard(item: Extract<Item, { k: "approve" }>): HTMLElement {
  const card = document.createElement("section");
  card.className = "approve";
  const text = document.createElement("p");
  text.textContent = item.text;
  card.append(text);
  if (item.choice) {
    const done = document.createElement("p");
    done.textContent = item.choice === "deny" ? "Negado" : item.choice === "always" ? "Sempre neste site" : "Permitido uma vez";
    card.append(done);
    return card;
  }
  const choices = document.createElement("div");
  choices.className = "choices";
  for (const [choice, label, cls] of [
    ["once", "Permitir uma vez", "allow"],
    ["always", "Sempre neste site", "allow"],
    ["deny", "Negar", "deny"],
  ] as const) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = cls;
    button.textContent = label;
    button.addEventListener("click", () => {
      item.choice = choice;
      port.postMessage({ type: "approve", id: item.id, choice });
      paint();
      void save();
    });
    choices.append(button);
  }
  card.append(choices);
  return card;
}

function ensureGroup(): Extract<Item, { k: "group" }> {
  const last = items[items.length - 1];
  if (last && last.k === "group" && last.live) return last;
  const group: Item = { k: "group", steps: [], live: true };
  items.push(group);
  return group;
}

function send(text: string): void {
  const clean = text.trim();
  if (!clean) return;
  closeLive();
  lastUser = clean;
  items.push({ k: "user", text: clean });
  stream = "";
  items.push({ k: "agent", text: "", pending: true });
  input.value = "";
  input.style.height = "";
  port.postMessage({ type: "chat", text: clean });
  paint();
  void save();
  statusEl.textContent = "pensando";
  statusEl.classList.add("busy");
}

function agentItem(): Extract<Item, { k: "agent" }> | undefined {
  const last = items[items.length - 1];
  return last && last.k === "agent" ? last : undefined;
}

async function reveal(text: string, error: boolean): Promise<void> {
  const item = agentItem() ?? { k: "agent" as const, text: "" };
  if (!items.includes(item)) items.push(item);
  item.pending = false;
  item.error = error;
  if (reduceMotion || text.length < 40) {
    item.text = text;
    paint();
    await save();
    return;
  }
  item.text = "";
  const step = Math.max(12, Math.ceil(text.length / 20));
  for (let i = 0; i < text.length; i += step) {
    item.text = text.slice(0, i + step);
    paint();
    await new Promise((resolve) => setTimeout(resolve, 16));
  }
  await save();
}

port.onMessage.addListener((message: {
  type?: string;
  text?: string;
  connected?: boolean;
  controlling?: boolean;
  id?: string;
  label?: string;
  phase?: string;
  thumb?: string;
  title?: string;
  url?: string;
  tabId?: number;
  host?: string;
  code?: string;
  choice?: string;
}) => {
  if (message.type === "state") {
    const on = Boolean(message.connected);
    const busy = Boolean(message.controlling);
    statusEl.textContent = busy ? "executando" : on ? "conectado" : "desconectado";
    statusEl.classList.toggle("on", on && !busy);
    statusEl.classList.toggle("busy", busy);
    stopBtn.classList.toggle("live", busy);
    const host = message.host || "";
    tabctx.textContent = message.title ? `${message.title}${host ? " · " + host : ""}` : "Nenhuma aba";
    tabctx.title = message.url || "";
    if (message.tabId && message.tabId !== tabId) void load(message.tabId);
    return;
  }
  if (message.type === "user" && message.text) {
    closeLive();
    lastUser = message.text;
    items.push({ k: "user", text: message.text });
    paint();
    void save();
    return;
  }
  if (message.type === "chat_delta" && message.text) {
    stream += message.text;
    const item = agentItem();
    if (item) {
      item.pending = false;
      item.text = stream;
    } else {
      items.push({ k: "agent", text: stream });
    }
    paint();
    return;
  }
  if (message.type === "chat_reply") {
    stream = "";
    closeLive();
    void reveal(message.text || "Sem resposta.", message.code === "error");
    statusEl.textContent = "conectado";
    statusEl.classList.remove("busy");
    return;
  }
  if (message.type === "error" && message.text) {
    items.push({ k: "agent", text: message.text, error: true });
    paint();
    void save();
    return;
  }
  if (message.type === "picked" && message.text) {
    input.value = (input.value ? input.value + " " : "") + message.text;
    input.focus();
    return;
  }
  if (message.type === "approve" && message.id && message.text) {
    items.push({ k: "approve", id: message.id, text: message.text, host: message.host ?? "" });
    paint();
    void save();
    return;
  }
  if (message.type === "step" && message.id && message.label) {
    const thinking = agentItem();
    if (thinking && (thinking.pending || thinking.text === "Pensando…") && !thinking.text) {
      items.splice(items.indexOf(thinking), 1);
    }
    const group = ensureGroup();
    let step = group.steps.find((item) => item.id === message.id);
    if (!step) {
      step = { id: message.id, label: message.label, phase: "start" };
      group.steps.push(step);
    }
    step.phase = message.phase || step.phase;
    step.label = message.label;
    if (message.text) step.text = message.text;
    if (message.thumb) step.thumb = message.thumb;
    if (message.phase === "done" || message.phase === "error") {
      /* o grupo fecha quando o estado deixa de executar */
    }
    paint();
    void save();
  }
});

composer.addEventListener("submit", (event) => {
  event.preventDefault();
  send(input.value);
});

input.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    send(input.value);
  }
  if (event.key === "Escape") {
    event.preventDefault();
    port.postMessage({ type: "stop" });
  }
});

clearBtn.addEventListener("click", () => {
  items = [];
  stream = "";
  paint();
  void save();
});

input.addEventListener("input", () => {
  input.style.height = "auto";
  input.style.height = Math.min(input.scrollHeight, 120) + "px";
});

stopBtn.addEventListener("click", () => port.postMessage({ type: "stop" }));
pickBtn.addEventListener("click", () => port.postMessage({ type: "pick" }));

pairForm.addEventListener("submit", (event) => {
  event.preventDefault();
  const token = tokenInput.value.trim();
  if (!/^[0-9a-f]{64}$/i.test(token)) {
    items.push({ k: "agent", text: "O código tem 64 caracteres hexadecimais. Rode hivemind browser pair.", error: true });
    paint();
    return;
  }
  port.postMessage({ type: "pair", token, port: 47891 });
  tokenInput.value = "";
  items.push({ k: "agent", text: "Conectando em 127.0.0.1." });
  paint();
});

paint();
