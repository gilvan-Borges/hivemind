import type { GitHubNotificationEvent } from "../types";

export type Mood = "idle" | "thinking" | "working" | "success" | "error" | "attention";

export type NoticeKind = "turn_end" | "plan_ready" | "confirm" | "error" | "routine_done" | "github";

export type Channel = "bubble" | "system" | "silent";

export interface LaneSignal {
  laneId: string;
  kind: string;
  stage?: string;
  label?: string;
  text?: string;
  toolName?: string;
  author?: string;
  origin?: string;
  gate?: string;
  thought?: boolean;
  fileCount?: number;
}

export interface NoticeDraft {
  kind: NoticeKind;
  title: string;
  body: string;
  mood: Mood;
}

export interface NoticeTarget {
  surface: "team" | "resident" | "github";
  projectId: string;
  laneId: string;
  messageId: string;
}

export interface DeliveryInput {
  focused: boolean;
  companion: boolean;
  osNotifications: boolean;
  doNotDisturb: boolean;
}

const FILE_TOOLS = new Set(["write_file", "edit_file"]);

export function clip(text: string, max: number): string {
  const t = String(text || "").replace(/\s+/g, " ").trim();
  if (t.length <= max) return t;
  return `${t.slice(0, Math.max(0, max - 1)).trimEnd()}…`;
}

export function channelFor(input: DeliveryInput): Channel {
  if (input.doNotDisturb) return "silent";
  if (input.focused) return input.companion ? "bubble" : "silent";
  if (input.osNotifications) return "system";
  if (input.companion) return "bubble";
  return "silent";
}

export function confirmTitle(toolName: string): string | null {
  switch ((toolName || "").toLowerCase()) {
    case "run_shell":
      return "Confirmar shell";
    case "browser_open":
      return "Confirmar navegador";
    case "desktop_click":
    case "desktop_type":
      return "Confirmar desktop";
    default:
      return null;
  }
}

export function turnTitle(stage: string, files: number): string {
  if (files === 1) return "Dev terminou — 1 arquivo";
  if (files > 1) return `Dev terminou — ${files} arquivos`;
  if (stage === "dev") return "Dev terminou";
  if (stage === "qa") return "QA terminou";
  return "Turno concluído";
}

export function countTurnFiles(messages: { kind: string; toolName?: string; toolDetail?: string }[]): number {
  const paths = new Set<string>();
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m.kind === "user") break;
    if (m.kind === "tool" && m.toolName && FILE_TOOLS.has(m.toolName)) {
      const path = (m.toolDetail || "").split("\n")[0].trim();
      if (path) paths.add(path);
    }
  }
  return paths.size;
}

function isQA(signal: LaneSignal): boolean {
  const blob = `${signal.stage || ""} ${signal.author || ""} ${signal.label || ""}`.toLowerCase();
  return /(^|[^a-z])qa([^a-z]|$)/.test(blob);
}

function isPlanFinish(text: string): boolean {
  return /plano pronto|aprovação do gate/i.test(text);
}

export function noticeFromSignal(signal: LaneSignal): NoticeDraft | null {
  if (signal.kind === "error") {
    const title = isQA(signal)
      ? "Erro no QA"
      : signal.laneId === "resident"
        ? "Erro no residente"
        : "Erro na equipe";
    return { kind: "error", mood: "error", title, body: clip(signal.text || "", 90) };
  }
  if (signal.kind === "plan_ready" || signal.kind === "sdd_gate") {
    const title = signal.gate === "jira" ? "Backlog pronto para aprovar" : "Plano pronto para aprovar";
    return { kind: "plan_ready", mood: "attention", title, body: "" };
  }
  if (signal.kind === "approval_required") {
    const title = confirmTitle(signal.toolName || "");
    if (!title) return null;
    return { kind: "confirm", mood: "attention", title, body: clip(signal.text || "", 90) };
  }
  if (signal.kind === "finish") {
    if (signal.origin === "routine") {
      return { kind: "routine_done", mood: "success", title: "Rotina concluída", body: clip(signal.text || "", 90) };
    }
    if (signal.laneId === "resident" || isPlanFinish(signal.text || "")) return null;
    return {
      kind: "turn_end",
      mood: "success",
      title: turnTitle(signal.stage || "", signal.fileCount || 0),
      body: "",
    };
  }
  return null;
}

export function moodFromSignal(signal: LaneSignal): Mood | null {
  const draft = noticeFromSignal(signal);
  if (draft) return draft.mood;
  if (signal.kind === "finish" && isPlanFinish(signal.text || "")) return null;
  if (signal.kind === "tool_call" || signal.kind === "tool_result") return "working";
  if (signal.kind === "text" && signal.thought) return "thinking";
  if (signal.kind === "stage") return "thinking";
  return null;
}

export function pushNotice<T extends { id: string }>(items: T[], item: T, max = 6): T[] {
  const next = items.filter((entry) => entry.id !== item.id);
  next.push(item);
  return next.length > max ? next.slice(next.length - max) : next;
}

export function dropNotice<T extends { id: string }>(items: T[], id: string): T[] {
  return items.filter((entry) => entry.id !== id);
}

export function latestNotice<T>(items: T[]): T | null {
  return items.length > 0 ? items[items.length - 1] : null;
}

export function clampPosition(x: number, y: number, w: number, h: number, vw: number, vh: number): { x: number; y: number } {
  const maxX = Math.max(0, vw - Math.max(0, w));
  const maxY = Math.max(0, vh - Math.max(0, h));
  return {
    x: Math.min(maxX, Math.max(0, x)),
    y: Math.min(maxY, Math.max(0, y)),
  };
}

export function formatGitHubEventNotice(ev: GitHubNotificationEvent): { title: string; body: string; mood: Mood } {
  const who = ev.author ? `@${ev.author}` : "";
  let title = "";
  let mood: Mood = "attention";

  switch (ev.kind) {
    case "mention":
      title = who ? `${who} marcou você no PR #${ev.prNumber}` : `Você foi marcado no PR #${ev.prNumber}`;
      break;
    case "my_pr_approved":
      title = `PR #${ev.prNumber} pronto para merge`;
      mood = "success";
      break;
    case "my_pr_changes":
      title = `Ajustes pedidos no seu PR #${ev.prNumber}`;
      break;
    case "my_pr_comment":
      title = who ? `${who} comentou no seu PR #${ev.prNumber}` : `Novo comentário no seu PR #${ev.prNumber}`;
      break;
    case "review_requested":
      title = ev.reason === "Re-revisão"
        ? `Re-revisão pedida no PR #${ev.prNumber}`
        : `Revisão solicitada no PR #${ev.prNumber}`;
      break;
    case "pr_commit":
      title = `Novo commit no PR #${ev.prNumber}`;
      mood = "working";
      break;
    default:
      title = who ? `Nova resposta de ${who} no PR #${ev.prNumber}` : `Nova atividade no PR #${ev.prNumber}`;
      break;
  }

  const snippet = String(ev.snippet || "").trim();
  const prTitle = String(ev.title || "").trim();
  const rawBody = snippet ? (prTitle ? `${prTitle} — “${snippet}”` : snippet) : prTitle;
  return { title, body: clip(rawBody, 110), mood };
}

export function noticeFromGitHubEvents(events: GitHubNotificationEvent[], projectName?: string): NoticeDraft | null {
  if (!Array.isArray(events) || events.length === 0) return null;
  if (events.length === 1) {
    const one = formatGitHubEventNotice(events[0]);
    const prefix = projectName ? `[${projectName}] ` : "";
    return { kind: "github", title: `${prefix}${one.title}`, body: one.body, mood: one.mood };
  }
  const summary = events
    .slice(0, 3)
    .map((ev) => formatGitHubEventNotice(ev).title)
    .join(" · ");
  const extra = events.length > 3 ? ` (+${events.length - 3})` : "";
  return {
    kind: "github",
    title: `${events.length} novidades no GitHub${projectName ? ` (${projectName})` : ""}`,
    body: clip(`${summary}${extra}`, 110),
    mood: "attention",
  };
}
