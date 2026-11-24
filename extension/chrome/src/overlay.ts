const HOST_ID = "hivemind-agent-root";

interface OverlayMsg {
  type?: string;
  op?: string;
  label?: string;
  x?: number;
  y?: number;
  w?: number;
  h?: number;
}

function mount(): ShadowRoot {
  let host = document.getElementById(HOST_ID);
  if (!host) {
    host = document.createElement("div");
    host.id = HOST_ID;
    host.setAttribute("data-hivemind", "overlay");
    host.setAttribute("aria-hidden", "true");
    document.documentElement.append(host);
  }
  if (host.shadowRoot) return host.shadowRoot;
  const root = host.attachShadow({ mode: "open" });
  root.innerHTML = `
    <style>
      :host { all: initial; }
      .stage { position: fixed; inset: 0; z-index: 2147483646; pointer-events: none; }
      .vignette {
        position: absolute; inset: 0;
        box-shadow:
          inset 0 0 0 3px rgba(198, 161, 91, .9),
          inset 0 0 46px 10px rgba(232, 200, 129, .38);
        animation: hive-pulse 1.7s ease-in-out infinite;
      }
      .frame {
        position: absolute; inset: 0; padding: 3px;
        background: conic-gradient(from var(--hive-angle), #8c6e33, #e8c881, #c6a15b, #e2ae62, #8c6e33);
        -webkit-mask: linear-gradient(#000 0 0) content-box, linear-gradient(#000 0 0);
        -webkit-mask-composite: xor;
        mask: linear-gradient(#000 0 0) content-box, linear-gradient(#000 0 0);
        mask-composite: exclude;
        animation: hive-spin 3.2s linear infinite;
      }
      .cursor {
        position: absolute; width: 18px; height: 18px; margin: -9px 0 0 -9px;
        border: 2px solid #e8c881; border-radius: 50%;
        background: rgba(198, 161, 91, .35);
        box-shadow: 0 0 14px #c6a15b;
        opacity: 0;
        transition: left .48s cubic-bezier(.2,.8,.2,1), top .48s cubic-bezier(.2,.8,.2,1), opacity .2s;
      }
      .cursor.on { opacity: 1; }
      .ripple {
        position: absolute; width: 18px; height: 18px; margin: -9px 0 0 -9px;
        border: 2px solid #e8c881; border-radius: 50%; opacity: 0; pointer-events: none;
      }
      .ripple.play { animation: hive-ripple .55s ease-out; }
      .target {
        position: absolute; border: 2px solid #e8c881; border-radius: 10px;
        box-shadow: 0 0 0 4px rgba(198,161,91,.28), 0 0 22px rgba(232,200,129,.5);
      }
      .label {
        position: absolute; top: -28px; left: 0;
        background: #111; color: #e8c881;
        border: 1px solid rgba(198,161,91,.55); border-radius: 999px;
        padding: 2px 8px; font: 600 12px/1.4 ui-sans-serif, system-ui, sans-serif;
        white-space: nowrap;
      }
      .stage.picking { pointer-events: auto; cursor: crosshair; }
      .stage.picking .vignette { box-shadow: inset 0 0 0 3px rgba(232, 200, 129, .95); }
      .pill {
        position: absolute; top: 12px; left: 50%; transform: translateX(-50%);
        display: flex; align-items: center; gap: 10px;
        background: rgba(12,12,12,.92); color: #fff;
        border: 1px solid rgba(198,161,91,.55); border-radius: 999px;
        padding: 6px 8px 6px 10px;
        font: 13px/1.3 ui-sans-serif, system-ui, sans-serif;
        box-shadow: 0 8px 28px rgba(0,0,0,.35);
        pointer-events: none;
      }
      .pill strong { display: block; font-size: 12px; }
      .pill .action { color: #e8c881; font-size: 11px; }
      button {
        pointer-events: auto; cursor: pointer;
        border: 1px solid #e58c98; background: transparent; color: #e58c98;
        border-radius: 999px; padding: 4px 10px; font: 600 12px/1 ui-sans-serif, system-ui, sans-serif;
      }
      @property --hive-angle { syntax: "<angle>"; inherits: false; initial-value: 0deg; }
      @keyframes hive-spin { to { --hive-angle: 360deg; } }
      @keyframes hive-pulse {
        50% { box-shadow: inset 0 0 0 4px rgba(232,200,129,1), inset 0 0 78px 18px rgba(198,161,91,.5); }
      }
      @keyframes hive-ripple {
        from { transform: scale(1); opacity: .7; }
        to { transform: scale(3.2); opacity: 0; }
      }
      @media (prefers-reduced-motion: reduce) {
        .vignette, .frame, .cursor, .ripple, .target { animation: none !important; transition: none !important; }
      }
    </style>
    <div class="stage" hidden>
      <div class="vignette"></div>
      <div class="frame"></div>
      <div class="cursor"></div>
      <div class="ripple"></div>
      <div class="target" hidden><span class="label"></span></div>
      <div class="pill">
        <svg viewBox="0 0 32 32" width="22" height="22" aria-hidden="true">
          <path fill="#C6A15B" d="M16 2 28 9v14L16 30 4 23V9L16 2zm0 4.2L8 11v10l8 4.8 8-4.8V11L16 6.2z"/>
          <circle cx="16" cy="16" r="3" fill="#E8C881"/>
        </svg>
        <div>
          <strong>Agent Hive no controle</strong>
          <span class="action">Pronto</span>
        </div>
        <button type="button">Parar</button>
      </div>
    </div>
  `;
  const stop = root.querySelector("button");
  stop?.addEventListener("click", (event) => {
    event.preventDefault();
    event.stopPropagation();
    void chrome.runtime.sendMessage({ type: "hivemind-overlay-stop" });
  });
  const stage = root.querySelector(".stage") as HTMLElement;
  stage.addEventListener("click", (event) => {
    if (!stage.classList.contains("picking")) return;
    event.preventDefault();
    event.stopPropagation();
    const x = event.clientX;
    const y = event.clientY;
    stage.classList.remove("picking");
    stage.style.pointerEvents = "none";
    const el = document.elementFromPoint(x, y);
    const name = el instanceof Element ? elementLabel(el) : "página";
    void chrome.runtime.sendMessage({ type: "hivemind-picked", text: name });
  });
  return root;
}

function elementLabel(el: Element): string {
  const role = el.getAttribute("role") || el.tagName.toLowerCase();
  const name = (el.getAttribute("aria-label") || el.getAttribute("placeholder") || el.textContent || "")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 80);
  return `${role}${name ? ` "${name}"` : ""}`;
}

function stageOf(root: ShadowRoot): HTMLElement {
  return root.querySelector(".stage") as HTMLElement;
}

function apply(msg: OverlayMsg): void {
  const root = mount();
  const stage = stageOf(root);
  const action = root.querySelector(".action");
  if (msg.label && action) action.textContent = msg.label;
  if (msg.op === "hide") {
    stage.hidden = true;
    stage.classList.remove("picking");
    return;
  }
  if (msg.op === "pick") {
    stage.hidden = false;
    stage.classList.add("picking");
    if (action && msg.label) action.textContent = msg.label;
    return;
  }
  if (msg.op === "show" || msg.op === "status") stage.hidden = false;
  if (msg.op === "cursor" && Number.isFinite(msg.x) && Number.isFinite(msg.y)) {
    stage.hidden = false;
    const cursor = root.querySelector(".cursor") as HTMLElement;
    cursor.style.left = `${msg.x}px`;
    cursor.style.top = `${msg.y}px`;
    cursor.classList.add("on");
  }
  if (msg.op === "ripple" && Number.isFinite(msg.x) && Number.isFinite(msg.y)) {
    const ripple = root.querySelector(".ripple") as HTMLElement;
    ripple.style.left = `${msg.x}px`;
    ripple.style.top = `${msg.y}px`;
    ripple.classList.remove("play");
    void ripple.offsetWidth;
    ripple.classList.add("play");
  }
  if (msg.op === "highlight" && Number.isFinite(msg.x) && Number.isFinite(msg.y)) {
    stage.hidden = false;
    const target = root.querySelector(".target") as HTMLElement;
    target.hidden = false;
    target.style.left = `${msg.x}px`;
    target.style.top = `${msg.y}px`;
    target.style.width = `${Math.max(8, msg.w ?? 24)}px`;
    target.style.height = `${Math.max(8, msg.h ?? 24)}px`;
    const label = target.querySelector(".label");
    if (label) label.textContent = msg.label || "";
  }
  if (msg.op === "clear-target") {
    const target = root.querySelector(".target") as HTMLElement;
    if (target) target.hidden = true;
  }
}

chrome.runtime.onMessage.addListener((message: OverlayMsg) => {
  if (message?.type !== "hivemind-overlay") return;
  apply(message);
});
