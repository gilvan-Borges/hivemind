const log = document.querySelector<HTMLElement>("#log")!;
const composer = document.querySelector<HTMLFormElement>("#composer")!;
const input = document.querySelector<HTMLTextAreaElement>("#text")!;
const statusEl = document.querySelector<HTMLElement>("#status")!;
const stopBtn = document.querySelector<HTMLButtonElement>("#stop")!;
const pairForm = document.querySelector<HTMLFormElement>("#pair")!;
const tokenInput = document.querySelector<HTMLInputElement>("#token")!;

const port = chrome.runtime.connect({ name: "panel" });
const cards = new Map<string, HTMLElement>();

function scroll(): void {
  log.scrollTop = log.scrollHeight;
}

function bubble(kind: string, text: string): void {
  const item = document.createElement("div");
  item.className = "bubble " + kind;
  item.textContent = text;
  log.append(item);
  scroll();
}

function card(id: string, label: string): HTMLElement {
  const details = document.createElement("details");
  details.className = "card";
  details.open = true;
  const summary = document.createElement("summary");
  const dot = document.createElement("span");
  dot.className = "dot";
  const title = document.createElement("span");
  title.className = "title";
  title.textContent = label;
  const phase = document.createElement("span");
  phase.className = "phase";
  phase.textContent = "em curso";
  summary.append(dot, title, phase);
  details.append(summary);
  log.append(details);
  cards.set(id, details);
  scroll();
  return details;
}

function finish(id: string, thumb?: string): void {
  const details = cards.get(id);
  if (!details) return;
  details.classList.add("done");
  const phase = details.querySelector(".phase");
  if (phase) phase.textContent = "feito";
  if (thumb) {
    const img = document.createElement("img");
    img.alt = "";
    img.src = "data:image/jpeg;base64," + thumb;
    details.append(img);
  }
  scroll();
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
}) => {
  if (message.type === "state") {
    const on = Boolean(message.connected);
    statusEl.textContent = on ? "conectado" : "desconectado";
    statusEl.classList.toggle("on", on);
    stopBtn.classList.toggle("live", Boolean(message.controlling));
    return;
  }
  if (message.type === "user" && message.text) {
    bubble("user", message.text);
    return;
  }
  if (message.type === "chat_reply" && message.text) {
    bubble("agent", message.text);
    return;
  }
  if (message.type === "step" && message.id && message.label) {
    if (message.phase === "done") finish(message.id, message.thumb);
    else card(message.id, message.label);
  }
});

composer.addEventListener("submit", (event) => {
  event.preventDefault();
  const text = input.value.trim();
  if (!text) return;
  bubble("user", text);
  port.postMessage({ type: "chat", text });
  input.value = "";
});

stopBtn.addEventListener("click", () => port.postMessage({ type: "stop" }));

pairForm.addEventListener("submit", (event) => {
  event.preventDefault();
  const token = tokenInput.value.trim();
  if (!/^[0-9a-f]{64}$/i.test(token)) {
    bubble("agent", "O código tem 64 caracteres hexadecimais. Rode hivemind browser pair.");
    return;
  }
  port.postMessage({ type: "pair", token, port: 47891 });
  tokenInput.value = "";
  bubble("agent", "Conectando em 127.0.0.1.");
});
