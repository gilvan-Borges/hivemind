import { icons } from "../icons";
import type { Mood } from "./logic";
import { clampPosition, latestNotice, pushNotice, dropNotice } from "./logic";
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

export function mountCompanionView(handlers: {
  onOpen: (id: string) => void;
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
  `;
  document.body.appendChild(root);

  const live = root.querySelector<HTMLElement>(".hm-live")!;
  const bubble = root.querySelector<HTMLElement>(".hm-bubble")!;
  const bubbleBtn = root.querySelector<HTMLButtonElement>(".hm-bubble-open")!;
  let queue: BubbleNotice[] = [];
  let hideTimer = 0;

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

  const setMood = (mood: Mood, _replay = false) => {
    root.dataset.mood = mood;
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
  root.addEventListener("pointerenter", () => {
    if (hideTimer) window.clearTimeout(hideTimer);
  });
  root.addEventListener("pointerleave", () => armHide());
  root.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") {
      const notice = latestNotice(queue);
      if (notice) {
        queue = dropNotice(queue, notice.id);
        paint();
      }
    }
  });

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
      if (settings.x != null && settings.y != null) {
        place(settings.x, settings.y, false);
      } else if (!root.style.left) {
        root.style.left = "auto";
        root.style.top = "auto";
        root.style.right = "18px";
        root.style.bottom = "44px";
      }
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
