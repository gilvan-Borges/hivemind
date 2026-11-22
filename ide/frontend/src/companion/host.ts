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

function playWebAlarm(mood: Mood): void {
  const Ctx = window.AudioContext || (window as Window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!Ctx) return;
  try {
    const ctx = new Ctx();
    const now = ctx.currentTime;
    const pulses =
      mood === "error"
        ? [
            { s: 0.0, e: 0.38, a: 960, b: 720 },
            { s: 0.48, e: 0.86, a: 960, b: 720 },
            { s: 0.96, e: 1.34, a: 1020, b: 760 },
            { s: 1.44, e: 1.98, a: 1040, b: 720 },
          ]
        : [
            { s: 0.0, e: 0.22, a: 784, b: 1175 },
            { s: 0.25, e: 0.48, a: 1046.5, b: 1568 },
            { s: 0.65, e: 0.87, a: 784, b: 1175 },
            { s: 0.9, e: 1.15, a: 1046.5, b: 1568 },
            { s: 1.32, e: 1.98, a: 1175, b: 1568 },
          ];

    for (const p of pulses) {
      const g = ctx.createGain();
      g.gain.setValueAtTime(0.0001, now + p.s);
      g.gain.linearRampToValueAtTime(0.18, now + p.s + 0.02);
      g.gain.setValueAtTime(0.16, now + p.e - 0.04);
      g.gain.exponentialRampToValueAtTime(0.0001, now + p.e);
      g.connect(ctx.destination);

      const oscA = ctx.createOscillator();
      oscA.type = mood === "error" ? "sawtooth" : "triangle";
      oscA.frequency.value = p.a;
      oscA.connect(g);
      oscA.start(now + p.s);
      oscA.stop(now + p.e + 0.01);

      const oscB = ctx.createOscillator();
      oscB.type = "sine";
      oscB.frequency.value = p.b;
      oscB.connect(g);
      oscB.start(now + p.s);
      oscB.stop(now + p.e + 0.01);
    }

    window.setTimeout(() => void ctx.close(), 2200);
  } catch {
    /* sem dispositivo de áudio */
  }
}

function playChime(mood: Mood): void {
  const native = api()?.PlayAlertSound?.(mood);
  if (!native) {
    playWebAlarm(mood);
    return;
  }
  void native
    .then((played) => {
      if (!played) playWebAlarm(mood);
    })
    .catch(() => playWebAlarm(mood));
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
  void api()?.OrbDismissNotice?.(id);
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
  // Keep the legacy in-IDE hexagon widget hidden by default so it never flashes
  // on cold-start installs before startOrbHost() finishes spawning the HiveBee Orb.
  view.root.hidden = true;
  const syncOrbCompanionVisibility = () => {
    const appApi = api();
    if (!appApi?.OrbRunning) {
      if (view) view.root.hidden = false;
      return;
    }
    void appApi.OrbRunning().then((running) => {
      if (view) view.root.hidden = Boolean(running);
    });
  };
  syncOrbCompanionVisibility();
  window.setTimeout(syncOrbCompanionVisibility, 1200);
  window.setTimeout(syncOrbCompanionVisibility, 4000);
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
  void api()?.OrbSetMood?.(mood);
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
  void api()?.OrbPushNotice?.(
    notice.id,
    draft.title,
    draft.body,
    draft.mood,
    target.surface,
    target.projectId,
    target.laneId,
    target.messageId
  );
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
