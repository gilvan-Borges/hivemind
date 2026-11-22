import type { ChatMessage } from "./types";
import { formatElapsedDuration } from "./utils";

const AGY_ENVELOPE_PREFIX_RE = /^\{\s*"(?:text|tool_calls)"\s*:/;
const AGY_ENVELOPE_ANY_RE = /\{\s*"(?:text|tool_calls)"\s*:/;
const AGY_TEXT_VALUE_RE = /"text"\s*:\s*"((?:\\.|[^"\\])*)/;
const ORPHAN_TOOL_CALL_ANCHOR_RE = /(?:^|\r?\n)\s*(?:\[\s*\{|\},?\s*\{|\{\s*"(?:arguments|id|name)"\s*:|"(?:call-[^"\n]{0,48}|[^"\n]{1,48})"\s*,\s*"(?:name|arguments|id)"\s*:)|(?:\[\s*\{|\},?\s*\{)\s*"(?:arguments|id|name)"\s*:|"[^"\n]{1,48}","(?:name|arguments|id)"\s*:/;

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

function extractAgyEnvelopeText(rawEnvelope: string): string | null {
  let candidate = rawEnvelope.trim();
  if (!candidate.startsWith("{")) return null;
  candidate = candidate.replace(/\s*```\s*$/, "").trimEnd();
  if (!(candidate.includes('"tool_calls"') || AGY_ENVELOPE_PREFIX_RE.test(candidate))) {
    return null;
  }

  try {
    const parsed = JSON.parse(candidate) as unknown;
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
    const partialMatch = candidate.match(AGY_TEXT_VALUE_RE);
    if (partialMatch) {
      return decodeSimpleJsonFragment(partialMatch[1]);
    }
    if (AGY_ENVELOPE_PREFIX_RE.test(candidate) || candidate.startsWith('{"')) {
      return "";
    }
  }
  return null;
}

function stripOrphanToolCallsTail(input: string): string {
  const hasArgs = /"arguments"\s*:\s*\{/.test(input);
  const hasName = /"name"\s*:\s*"/.test(input);
  const hasIdOrClose = /"id"\s*:\s*"/.test(input) || /\}\s*\]\s*\}?\s*$/.test(input);
  if (!hasArgs || !hasName || !hasIdOrClose) {
    return input;
  }
  const anchor = input.match(ORPHAN_TOOL_CALL_ANCHOR_RE);
  if (!anchor || anchor.index === undefined) {
    return input;
  }
  return input.slice(0, anchor.index).trimEnd();
}

/**
 * Remove vazamentos defensivos do envelope JSON `{"text":...,"tool_calls":[...]}`
 * (puro, cercado por markdown, após prosa inicial ou caudas órfãs de tool_calls).
 * Preserva chunks parciais de texto comum e JSON normal de resposta sem fazer `.trim()`.
 */
export function sanitizeAssistantText(raw: string | undefined | null): string {
  const input = String(raw ?? "");
  if (!input) return "";

  const trimmed = input.trim();
  if (trimmed.startsWith("```")) {
    const fenceMatch = trimmed.match(/^```(?:json)?\s*([\s\S]*?)(?:\s*```\s*)?$/i);
    if (fenceMatch) {
      const inner = fenceMatch[1];
      const extracted = extractAgyEnvelopeText(inner);
      if (extracted !== null) {
        return extracted;
      }
    }
  }

  if (trimmed.startsWith("{")) {
    const extracted = extractAgyEnvelopeText(trimmed);
    if (extracted !== null) {
      return extracted;
    }
  }

  const embeddedMatch = input.match(AGY_ENVELOPE_ANY_RE);
  if (embeddedMatch && embeddedMatch.index !== undefined) {
    const jsonStart = embeddedMatch.index;
    const extracted = extractAgyEnvelopeText(input.slice(jsonStart));
    if (extracted !== null) {
      let prefix = input.slice(0, jsonStart);
      prefix = prefix.replace(/(?:^|\r?\n)```(?:json)?\s*$/i, "");
      if (prefix.trim() === "") {
        return extracted;
      }
      if (extracted.trim() === "") {
        return prefix.trimEnd();
      }
      if (prefix.trim() === extracted.trim() || prefix.trimEnd().endsWith(extracted.trim())) {
        return prefix.trimEnd();
      }
      if (prefix.endsWith("\n") || prefix.endsWith(" ")) {
        return prefix + extracted;
      }
      return `${prefix.trimEnd()}\n${extracted}`;
    }
  }

  return stripOrphanToolCallsTail(input);
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
