const log = document.querySelector<HTMLElement>("#log")!;
const composer = document.querySelector<HTMLFormElement>("#composer")!;
const input = document.querySelector<HTMLTextAreaElement>("#text")!;
const statusEl = document.querySelector<HTMLElement>("#status")!;
const stopBtn = document.querySelector<HTMLButtonElement>("#stop")!;
const pairForm = document.querySelector<HTMLFormElement>("#pair")!;
const tokenInput = document.querySelector<HTMLInputElement>("#token")!;

const port = chrome.runtime.connect({ name: "panel" });

function add(kind: string, text: string): void {
  const item = document.createElement("div");
  item.className = "msg " + kind;
  item.textContent = text;
  log.append(item);
  log.scrollTop = log.scrollHeight;
}

port.onMessage.addListener((message: { type?: string; text?: string; connected?: boolean; controlling?: boolean; mode?: string; activities?: string[] }) => {
  if (message.type === "state") {
    const on = Boolean(message.connected);
    statusEl.textContent = on ? `conectado · ${message.mode === "native" ? "host nativo" : "loopback"}` : "desconectado";
    statusEl.classList.toggle("on", on);
    stopBtn.hidden = !message.controlling;
    if (log.childElementCount === 0 && message.activities) {
      for (const line of message.activities) add(line.startsWith("você:") ? "user" : "activity", line);
    }
    return;
  }
  if (message.type === "activity" && message.text) {
    if (message.text.startsWith("você:")) return;
    add("activity", message.text);
    return;
  }
  if (message.type === "chat_reply" && message.text) add("agent", message.text);
});

composer.addEventListener("submit", (event) => {
  event.preventDefault();
  const text = input.value.trim();
  if (!text) return;
  add("user", text);
  port.postMessage({ type: "chat", text });
  input.value = "";
});

stopBtn.addEventListener("click", () => port.postMessage({ type: "stop" }));

pairForm.addEventListener("submit", (event) => {
  event.preventDefault();
  const token = tokenInput.value.trim();
  if (!/^[0-9a-f]{64}$/i.test(token)) {
    add("agent", "O código tem 64 caracteres hexadecimais. Rode hivemind browser pair.");
    return;
  }
  port.postMessage({ type: "pair", token, port: 47891 });
  tokenInput.value = "";
  add("agent", "Código guardado só nesta extensão. Conectando em 127.0.0.1.");
});
