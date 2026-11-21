import { api } from "../state";
import type { GitHubNotificationEvent } from "../types";
import { channelFor, moodFromSignal, noticeFromGitHubEvents, noticeFromSignal, type LaneSignal, type Mood, type NoticeDraft, type NoticeTarget } from "./logic";
import { openNoticeTarget } from "./navigate";
import { loadSettings, subscribeSettings } from "./settings";
import { mountCompanionView, type BubbleNotice } from "./view";

interface Stored extends BubbleNotice {
  target: NoticeTarget;
}

let view: ReturnType<typeof mountCompanionView> | null = null;
let focused = true;
let liveMood: Mood = "idle";
let openedFromSystem = false;
const stored = new Map<string, Stored>();
const waitingFocus = new Set<string>();

function newId(): string {
  return `hm-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`;
}

function playChime(mood: Mood): void {
  const Ctx = window.AudioContext || (window as Window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!Ctx) return;
  try {
    const ctx = new Ctx();
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();
    osc.type = "sine";
    osc.frequency.value = mood === "error" ? 196 : mood === "attention" ? 523 : 659;
    const now = ctx.currentTime;
    gain.gain.setValueAtTime(0.0001, now);
    gain.gain.exponentialRampToValueAtTime(0.045, now + 0.02);
    gain.gain.exponentialRampToValueAtTime(0.0001, now + 0.18);
    osc.connect(gain);
    gain.connect(ctx.destination);
    osc.start(now);
    osc.stop(now + 0.2);
    osc.onended = () => void ctx.close();
  } catch {
    /* sem dispositivo de áudio */
  }
}

function remember(notice: Stored): void {
  stored.set(notice.id, notice);
  if (stored.size > 24) {
    const oldest = stored.keys().next().value;
    if (oldest) stored.delete(oldest);
  }
}

function openStored(id: string): void {
  const notice = stored.get(id);
  view?.dismiss(id);
  waitingFocus.delete(id);
  if (notice) openNoticeTarget(notice.target);
}

function showBubble(notice: Stored): void {
  view?.show(notice);
  view?.setMood(notice.mood, true);
}

export function mountCompanion(): void {
  if (view || typeof document === "undefined") return;
  focused = !document.hidden && (typeof document.hasFocus !== "function" || document.hasFocus());
  view = mountCompanionView({
    onOpen: (id) => openStored(id),
    onSettings: () => view?.applySettings(loadSettings()),
  });
  subscribeSettings(() => view?.applySettings(loadSettings()));
  window.addEventListener("focus", () => {
    focused = true;
    view?.setPaused(false);
    window.setTimeout(() => {
      if (openedFromSystem) return;
      const settings = loadSettings();
      if (!settings.enabled || settings.doNotDisturb) return;
      waitingFocus.forEach((id) => {
        const notice = stored.get(id);
        if (notice) showBubble(notice);
      });
      waitingFocus.clear();
    }, 400);
  });
  window.addEventListener("blur", () => {
    focused = false;
  });
  document.addEventListener("visibilitychange", () => {
    const hidden = document.hidden;
    if (hidden) focused = false;
    view?.setPaused(hidden || !loadSettings().enabled);
  });
  window.runtime?.EventsOn?.("hivemind:notice-open", (payload: unknown) => {
    const id = typeof payload === "string" ? payload : "";
    if (!id) return;
    openedFromSystem = true;
    openStored(id);
    queueMicrotask(() => {
      openedFromSystem = false;
    });
  });
}

export function noteActivity(signal: LaneSignal): void {
  if (!view || loadSettings().doNotDisturb) return;
  const mood = moodFromSignal(signal);
  if (!mood) return;
  if (mood === "thinking" || mood === "working") liveMood = mood;
  if (mood === "success" || mood === "error") liveMood = "idle";
  if (!view.current()) view.setMood(mood, mood === "success" || mood === "error" || mood === "attention");
}

function deliverDraft(draft: NoticeDraft, target: NoticeTarget): void {
  const settings = loadSettings();
  if (settings.doNotDisturb) return;
  const notice: Stored = {
    id: newId(),
    title: draft.title,
    body: draft.body,
    mood: draft.mood,
    target,
  };
  remember(notice);
  const channel = channelFor({
    focused,
    companion: settings.enabled,
    osNotifications: settings.osNotifications,
    doNotDisturb: settings.doNotDisturb,
  });
  if (channel === "silent") return;
  if (settings.sound) playChime(draft.mood);
  if (channel === "bubble") {
    showBubble(notice);
    return;
  }
  waitingFocus.add(notice.id);
  const send = api()?.ShowDesktopNotification?.(notice.id, draft.title, draft.body, settings.sound);
  if (!send) {
    waitingFocus.delete(notice.id);
    if (settings.enabled) showBubble(notice);
    return;
  }
  void send.catch(() => {
    waitingFocus.delete(notice.id);
    if (loadSettings().enabled) showBubble(notice);
  });
}

export function publishNotice(signal: LaneSignal, target: NoticeTarget): void {
  if (!view) return;
  const settings = loadSettings();
  const draft = noticeFromSignal(signal);
  if (!draft || settings.doNotDisturb) {
    noteActivity(signal);
    return;
  }
  deliverDraft(draft, target);
}

export function publishGitHubNotice(events: GitHubNotificationEvent[], projectId: string, projectName?: string): void {
  if (!view) return;
  const draft = noticeFromGitHubEvents(events, projectName);
  if (!draft) return;
  deliverDraft(draft, { surface: "github", projectId, laneId: "", messageId: "" });
}

export function dismissMessage(messageId: string): void {
  if (!messageId) return;
  for (const [id, notice] of stored) {
    if (notice.target.messageId === messageId) {
      stored.delete(id);
      waitingFocus.delete(id);
      view?.dismiss(id);
    }
  }
}
