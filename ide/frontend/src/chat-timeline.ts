import type { ChatMessage } from "./types";
import { formatElapsedDuration } from "./utils";

const AGY_ENVELOPE_PREFIX_RE = /^\{\s*"(?:text|tool_calls)"\s*:/;
const AGY_TEXT_VALUE_RE = /"text"\s*:\s*"((?:\\.|[^"\\])*)/;

function decodeSimpleJsonFragment(escaped: string): string {
  return escaped
    .replace(/\\n/g, "\n")
    .replace(/\\r/g, "\r")
    .replace(/\\t/g, "\t")
    .replace(/\\"/g, '"')
    .replace(/\\\\/g, "\\")
    .replace(/\\\//g, "/")
    .replace(/\\u([0-9a-fA-F]{4})/g, (_, hex: string) => String.fromCharCode(parseInt(hex, 16)));
}

/**
 * Remove vazamentos defensivos do envelope JSON `{"text":...,"tool_calls":[...]}`
 * caso algum backend antigo ou chunk parcial chegue no frontend.
 * Preserva chunks parciais de texto comum sem fazer `.trim()`.
 */
export function sanitizeAssistantText(raw: string | undefined | null): string {
  const input = String(raw ?? "");
  if (!input) return "";
  const trimmed = input.trim();
  if (!trimmed.startsWith("{")) return input;

  const looksLikeAgyEnvelope =
    trimmed.includes('"tool_calls"') || AGY_ENVELOPE_PREFIX_RE.test(trimmed);
  if (!looksLikeAgyEnvelope) return input;

  try {
    const parsed = JSON.parse(trimmed) as unknown;
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      const rec = parsed as Record<string, unknown>;
      const keys = Object.keys(rec);
      const onlyEnvelopeKeys = keys.every((k) => k === "text" || k === "tool_calls");
      if (typeof rec.text === "string" && ("tool_calls" in rec || onlyEnvelopeKeys)) {
        return rec.text;
      }
      if ("tool_calls" in rec && onlyEnvelopeKeys) {
        return "";
      }
    }
  } catch {
    // JSON incompleto em streaming parcial: extrai apenas a porção de "text" se já iniciada.
    const partialMatch = trimmed.match(AGY_TEXT_VALUE_RE);
    if (partialMatch) {
      return decodeSimpleJsonFragment(partialMatch[1]);
    }
    if (AGY_ENVELOPE_PREFIX_RE.test(trimmed) || trimmed.startsWith('{"')) {
      return "";
    }
  }

  return input;
}

export interface TimelineSourceMessage {
  id: string;
  kind: ChatMessage["kind"] | string;
  label?: string;
  text: string;
  active?: boolean;
  images?: string[];
  startedAt?: number;
  durationMs?: number;
  toolName?: string;
  toolDetail?: string;
}

export function isActivityMessage(m: Pick<TimelineSourceMessage, "kind" | "label" | "text">): boolean {
  if (m.kind === "thinking" || m.kind === "tool" || m.kind === "handoff") {
    return true;
  }
  if (m.kind === "system" && (m.label === "Rota Jev" || m.label === "Failover")) {
    return true;
  }
  return false;
}

export type TimelineItem<T extends TimelineSourceMessage> =
  | { kind: "message"; id: string; message: T }
  | { kind: "activity"; id: string; messages: T[]; active: boolean };

/**
 * Agrupa etapas intermediárias consecutivas (pensamento, Rota Jev, chamadas de tool e handoff)
 * em um único bloco recolhível estilo Antigravity, mantendo abertas apenas mensagens de usuário,
 * respostas principais, planos/aprovações, diffs de turno e erros.
 */
export function groupConversationItems<T extends TimelineSourceMessage>(
  messages: T[],
  running: boolean,
): Array<TimelineItem<T>> {
  const items: Array<TimelineItem<T>> = [];
  let buf: T[] = [];

  const flushActivity = (isLastVisible: boolean) => {
    if (buf.length === 0) return;
    const anyActive = buf.some((m) => Boolean(m.active));
    items.push({
      kind: "activity",
      id: `act-${buf[0].id}`,
      messages: buf,
      active: Boolean(running && isLastVisible && (anyActive || buf.length > 0)),
    });
    buf = [];
  };

  const visibleMessages = messages.filter((m) => {
    if (m.kind !== "chief") return true;
    const cleaned = sanitizeAssistantText(m.text).trim();
    const hasImages = Array.isArray(m.images) && m.images.length > 0;
    return cleaned.length > 0 || hasImages;
  });

  for (const m of visibleMessages) {
    if (isActivityMessage(m)) {
      buf.push(m);
    } else {
      flushActivity(false);
      items.push({ kind: "message", id: m.id, message: m });
    }
  }
  flushActivity(true);
  return items;
}

export interface ActivityGroupSummary {
  title: string;
  elapsedText: string;
  activeStartedAt?: number;
  stepCount: number;
}

export function summarizeActivityGroup<T extends TimelineSourceMessage>(
  messages: T[],
  active: boolean,
  nowMs = Date.now(),
  formatActiveTool?: (m: T) => string,
): ActivityGroupSummary {
  let stepCount = 0;
  let totalThinkMs = 0;
  let activeThinkStart: number | undefined;
  let activeToolMsg: T | undefined;
  let firstRouteText = "";

  for (const m of messages) {
    if (m.kind === "tool" || m.kind === "handoff") {
      stepCount += 1;
      if (m.kind === "tool" && m.active) {
        activeToolMsg = m;
      }
    } else if (m.kind === "thinking") {
      if (m.active && m.startedAt) {
        activeThinkStart = m.startedAt;
        totalThinkMs += Math.max(0, nowMs - m.startedAt);
      } else if (m.durationMs && m.durationMs > 0) {
        totalThinkMs += m.durationMs;
      }
    } else if (m.kind === "system" && (m.label === "Rota Jev" || m.label === "Failover") && !firstRouteText) {
      firstRouteText = (m.text || "").trim();
    }
  }

  const elapsedText =
    activeThinkStart && active
      ? formatElapsedDuration(Math.max(0, nowMs - activeThinkStart))
      : totalThinkMs >= 1000
        ? formatElapsedDuration(totalThinkMs)
        : "";

  const stepsLabel = stepCount === 1 ? "1 passo" : stepCount > 1 ? `${stepCount} passos` : "";

  if (active) {
    if (activeToolMsg) {
      const desc = formatActiveTool
        ? formatActiveTool(activeToolMsg)
        : activeToolMsg.toolName || activeToolMsg.text || "Executando";
      return {
        title: stepsLabel ? `${desc} · ${stepsLabel}` : desc,
        elapsedText,
        activeStartedAt: activeThinkStart,
        stepCount,
      };
    }
    return {
      title: stepsLabel ? `Pensando · ${stepsLabel}` : "Pensando",
      elapsedText,
      activeStartedAt: activeThinkStart,
      stepCount,
    };
  }

  if (totalThinkMs >= 1000 && stepsLabel) {
    return {
      title: `Pensou por ${formatElapsedDuration(totalThinkMs)} · ${stepsLabel}`,
      elapsedText: "",
      stepCount,
    };
  }
  if (stepsLabel) {
    return {
      title: stepsLabel,
      elapsedText: "",
      stepCount,
    };
  }
  if (totalThinkMs >= 1000) {
    return {
      title: `Pensou por ${formatElapsedDuration(totalThinkMs)}`,
      elapsedText: "",
      stepCount,
    };
  }
  if (firstRouteText) {
    return {
      title: firstRouteText,
      elapsedText: "",
      stepCount,
    };
  }
  return {
    title: "Atividade concluída",
    elapsedText: "",
    stepCount,
  };
}

export function autoResizeTextarea(
  el: HTMLTextAreaElement | null | undefined,
  minPx = 22,
  maxPx = 180,
): void {
  if (!el) return;
  el.style.height = "auto";
  const measured = el.scrollHeight > 0 ? el.scrollHeight : minPx;
  const clamped = Math.min(maxPx, Math.max(minPx, measured));
  el.style.height = `${clamped}px`;
  el.style.overflowY = measured > maxPx ? "auto" : "hidden";
}
