import { icons } from "../icons";
import type { Mood } from "./logic";
import { clampPosition, latestNotice, pushNotice, dropNotice } from "./logic";
import { mascotSVG } from "./mascot";
import { loadSettings, updateSettings, type CompanionSettings } from "./settings";

export interface BubbleNotice {
  id: string;
  title: string;
  body: string;
  mood: Mood;
}

export interface CompanionView {
  root: HTMLElement;
  applySettings(settings: CompanionSettings): void;
  setMood(mood: Mood, replay?: boolean): void;
  setPaused(paused: boolean): void;
  show(notice: BubbleNotice): void;
  dismiss(id?: string): void;
  current(): BubbleNotice | null;
}

const MOOD_LABEL: Record<Mood, string> = {
  idle: "ocioso",
  thinking: "pensando",
  working: "trabalhando",
  success: "concluiu",
  error: "erro",
  attention: "precisa de você",
};

export function mountCompanionView(handlers: {
  onOpen: (id: string) => void;
  onSettings: () => void;
}): CompanionView {
  const root = document.createElement("aside");
  root.className = "hm-companion";
  root.dataset.mood = "idle";
  root.dataset.min = "0";
  root.dataset.paused = "0";
  root.setAttribute("aria-label", "Companion do HiveMind");
  root.innerHTML = `
    <div class="hm-live" role="status" aria-live="polite"></div>
    <div class="hm-bubble" hidden>
      <button type="button" class="hm-bubble-open"></button>
      <button type="button" class="hm-bubble-x" aria-label="Dispensar aviso">${icons.closeSmall}</button>
    </div>
    <div class="hm-settings" hidden>
      <label><input type="checkbox" data-set="enabled"/> Companion</label>
      <label><input type="checkbox" data-set="osNotifications"/> Notificações do sistema</label>
      <label><input type="checkbox" data-set="sound"/> Som</label>
      <label><input type="checkbox" data-set="doNotDisturb"/> Não perturbe</label>
    </div>
    <div class="hm-dock">
      <button type="button" class="hm-gear" aria-label="Ajustes do companion" aria-expanded="false">${icons.settings}</button>
      <button type="button" class="hm-min" aria-label="Minimizar companion">${icons.chevronDown}</button>
      <button type="button" class="hm-mascot" aria-label="HiveMind, ocioso">${mascotSVG()}</button>
    </div>
  `;
  document.body.appendChild(root);

  const live = root.querySelector<HTMLElement>(".hm-live")!;
  const bubble = root.querySelector<HTMLElement>(".hm-bubble")!;
  const bubbleBtn = root.querySelector<HTMLButtonElement>(".hm-bubble-open")!;
  const svg = root.querySelector<SVGElement>(".hm-svg")!;
  const mascotBtn = root.querySelector<HTMLButtonElement>(".hm-mascot")!;
  const settingsEl = root.querySelector<HTMLElement>(".hm-settings")!;
  const gear = root.querySelector<HTMLButtonElement>(".hm-gear")!;
  let queue: BubbleNotice[] = [];
  let hideTimer = 0;
  let suppressClick = false;

  const paint = () => {
    const notice = latestNotice(queue);
    const minimized = root.dataset.min === "1";
    if (!notice) {
      bubble.hidden = true;
      bubbleBtn.textContent = "";
      live.textContent = "";
      return;
    }
    const text = notice.body && notice.body !== notice.title ? `${notice.title}. ${notice.body}` : notice.title;
    live.textContent = text;
    bubbleBtn.textContent = text;
    bubble.hidden = minimized;
    setMood(notice.mood, true);
  };

  const armHide = () => {
    if (hideTimer) window.clearTimeout(hideTimer);
    const notice = latestNotice(queue);
    if (!notice) return;
    hideTimer = window.setTimeout(() => {
      hideTimer = 0;
      queue = dropNotice(queue, notice.id);
      paint();
      if (!latestNotice(queue)) setMood("idle");
    }, 6500);
  };

  const setMood = (mood: Mood, replay = false) => {
    if (replay && (mood === "success" || mood === "error" || mood === "attention")) {
      svg.dataset.mood = "idle";
      void svg.getBoundingClientRect();
    }
    svg.dataset.mood = mood;
    root.dataset.mood = mood;
    mascotBtn.setAttribute("aria-label", `HiveMind, ${MOOD_LABEL[mood]}`);
  };

  const place = (x: number, y: number, persist: boolean) => {
    const box = root.getBoundingClientRect();
    const next = clampPosition(x, y, box.width || 72, box.height || 72, window.innerWidth, window.innerHeight);
    root.style.left = `${next.x}px`;
    root.style.top = `${next.y}px`;
    root.style.right = "auto";
    root.style.bottom = "auto";
    if (persist) updateSettings({ x: next.x, y: next.y });
  };

  bubbleBtn.addEventListener("click", () => {
    const notice = latestNotice(queue);
    if (notice) handlers.onOpen(notice.id);
  });
  root.querySelector(".hm-bubble-x")?.addEventListener("click", (ev) => {
    ev.stopPropagation();
    const notice = latestNotice(queue);
    if (!notice) return;
    queue = dropNotice(queue, notice.id);
    paint();
  });
  mascotBtn.addEventListener("click", () => {
    if (suppressClick) {
      suppressClick = false;
      return;
    }
    if (root.dataset.min === "1") {
      updateSettings({ minimized: false });
      handlers.onSettings();
      return;
    }
    const notice = latestNotice(queue);
    if (notice) handlers.onOpen(notice.id);
  });
  gear.addEventListener("click", () => {
    const open = settingsEl.hidden;
    settingsEl.hidden = !open;
    gear.setAttribute("aria-expanded", open ? "true" : "false");
    if (open) syncChecks();
  });
  root.querySelector(".hm-min")?.addEventListener("click", () => {
    updateSettings({ minimized: root.dataset.min !== "1" });
    handlers.onSettings();
  });
  settingsEl.addEventListener("change", (ev) => {
    const input = ev.target as HTMLInputElement | null;
    const key = input?.dataset.set as keyof CompanionSettings | undefined;
    if (!input || !key) return;
    updateSettings({ [key]: input.checked });
    handlers.onSettings();
  });
  root.addEventListener("pointerenter", () => {
    if (hideTimer) window.clearTimeout(hideTimer);
  });
  root.addEventListener("pointerleave", () => armHide());
  root.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") {
      settingsEl.hidden = true;
      gear.setAttribute("aria-expanded", "false");
      const notice = latestNotice(queue);
      if (notice) {
        queue = dropNotice(queue, notice.id);
        paint();
      }
    }
  });

  let drag: { x: number; y: number; left: number; top: number; moved: boolean } | null = null;
  mascotBtn.addEventListener("pointerdown", (ev) => {
    if (ev.button !== 0) return;
    const rect = root.getBoundingClientRect();
    drag = { x: ev.clientX, y: ev.clientY, left: rect.left, top: rect.top, moved: false };
    mascotBtn.setPointerCapture(ev.pointerId);
  });
  mascotBtn.addEventListener("pointermove", (ev) => {
    if (!drag) return;
    const dx = ev.clientX - drag.x;
    const dy = ev.clientY - drag.y;
    if (!drag.moved && Math.hypot(dx, dy) < 4) return;
    drag.moved = true;
    place(drag.left + dx, drag.top + dy, false);
  });
  const endDrag = (ev: PointerEvent) => {
    if (!drag) return;
    if (drag.moved) {
      suppressClick = true;
      const rect = root.getBoundingClientRect();
      place(rect.left, rect.top, true);
    }
    drag = null;
    if (mascotBtn.hasPointerCapture(ev.pointerId)) mascotBtn.releasePointerCapture(ev.pointerId);
  };
  mascotBtn.addEventListener("pointerup", endDrag);
  mascotBtn.addEventListener("pointercancel", endDrag);

  const syncChecks = () => {
    const settings = loadSettings();
    settingsEl.querySelectorAll<HTMLInputElement>("input[data-set]").forEach((input) => {
      const key = input.dataset.set as keyof CompanionSettings;
      input.checked = Boolean(settings[key]);
    });
  };

  window.addEventListener("resize", () => {
    const rect = root.getBoundingClientRect();
    place(rect.left, rect.top, false);
  });

  const view: CompanionView = {
    root,
    applySettings(settings) {
      root.hidden = !settings.enabled;
      root.dataset.min = settings.minimized ? "1" : "0";
      root.dataset.paused = !settings.enabled || document.hidden ? "1" : "0";
      const minBtn = root.querySelector(".hm-min");
      minBtn?.setAttribute("aria-label", settings.minimized ? "Expandir companion" : "Minimizar companion");
      if (settings.x != null && settings.y != null) {
        place(settings.x, settings.y, false);
      } else if (!root.style.left) {
        root.style.left = "auto";
        root.style.top = "auto";
        root.style.right = "18px";
        root.style.bottom = "44px";
      }
      syncChecks();
      paint();
    },
    setMood,
    setPaused(paused) {
      root.dataset.paused = paused ? "1" : "0";
    },
    show(notice) {
      queue = pushNotice(queue, notice);
      paint();
      armHide();
    },
    dismiss(id) {
      if (!id) {
        const current = latestNotice(queue);
        id = current?.id;
      }
      if (!id) return;
      queue = dropNotice(queue, id);
      paint();
      if (!latestNotice(queue)) setMood("idle");
    },
    current: () => latestNotice(queue),
  };
  view.applySettings(loadSettings());
  return view;
}
