import { icons } from "./icons";
import { autoResizeTextarea, groupConversationItems, sanitizeAssistantText, summarizeActivityGroup } from "./chat-timeline";
import { renderMarkdownStreamed } from "./markdown";
import { bindModelPickerPairs, renderModelPickerPairHtml } from "./models";
import type { ChatMessage, ExternalSession, LoopSummary, ResidentAuditEntry, ResidentMemoryFact, ResidentRoutine, ResidentStatus, SharedConversation } from "./types";
import { escapeHtml } from "./utils";
import { renderEditorBody, renderEditorBodyAsync, renderEditorTabs } from "./editor";
import { openIdeSettingsTab } from "./ide-settings";
import { createLane, laneDisplayName, renderLaneBarOnly } from "./lanes";
import { switchProjectTab, updateStatusBar } from "./shell";
import { IDE_SETTINGS_TAB_KEY, api, setStatus, state, toast, toastError } from "./state";
import { noteActivity, publishNotice } from "./companion/host";

export const RESIDENT_TAB_KEY = "hive:resident";

export type ResidentSubTab = "chat" | "memory" | "security" | "audit";

const residentExpandedGroups = new Set<string>();

export const residentState = {
  status: null as ResidentStatus | null,
  subTab: "chat" as ResidentSubTab,
  running: false,
  draftInput: "",
  workstream: "geral",
  messages: [] as ChatMessage[],
  memory: [] as ResidentMemoryFact[],
  memoryDraft: "",
  memoryPinned: false,
  audit: [] as ResidentAuditEntry[],
  routines: [] as ResidentRoutine[],
  loops: [] as LoopSummary[],
  externalSessions: [] as ExternalSession[],
  soulDraft: "",
  soulDirty: false,
  auditFilter: "",
  binariesDraft: "",
  hostsDraft: "",
  secretNameDraft: "",
  secretValueDraft: "",
  policyDirty: false,
  loadedOnce: false,
  boundLaneId: "",
  boundSessionId: "",
  hiveLaneId: "",
  conversations: [] as SharedConversation[],
};

/** Sessão que o envio do Agent Hive deve usar. Sem aba escolhida, abre outra na IDE. */
export function hiveSendTarget(hiveLaneId: string, lanes: { id: string; sessionId?: string }[]): { create: boolean; sessionId: string } {
  const lane = lanes.find((l) => l.id === hiveLaneId);
  if (!lane) return { create: true, sessionId: "hive:new" };
  if (!lane.sessionId) return { create: false, sessionId: "hive:new" };
  return { create: false, sessionId: lane.sessionId };
}

let onLaneMirrored: (() => void) | null = null;

/** boot registra o redraw do chat da IDE quando o Hive escreve na lane aberta. */
export function setResidentLaneMirrorHook(fn: () => void): void {
  onLaneMirrored = fn;
}

export async function syncHiveBinding(): Promise<void> {
  const backend = api();
  const lane = state.lanes.find((l) => l.id === state.activeLaneId) ?? state.lanes[0];
  if (!backend?.ResidentBindSession || !lane) return;
  // A aba que o Hive está escrevendo não segue o foco da IDE.
  if (residentState.hiveLaneId && residentState.hiveLaneId !== lane.id) return;
  residentState.boundLaneId = lane.id;
  residentState.boundSessionId = lane.sessionId || "";
  try {
    await backend.ResidentBindSession(state.project?.root || "", lane.sessionId || "", lane.id);
  } catch {
    // a IDE segue sem o Orb se o residente ainda não subiu
  }
}

function syncPolicyDraftsFromStatus(st: ResidentStatus | null): void {
  if (!st || residentState.policyDirty) return;
  residentState.binariesDraft = Array.isArray(st.allowedBinaries) ? st.allowedBinaries.join(", ") : "";
  residentState.hostsDraft = Array.isArray(st.allowedHosts) ? st.allowedHosts.join(", ") : "";
}

export function formatRiskBadge(tierRaw?: string): { tier: string; label: string; css: string } {
  const t = (tierRaw || "").trim().toLowerCase();
  switch (t) {
    case "read":
      return { tier: "read", label: "Risco Baixo · Leitura", css: "risk-read" };
    case "write":
      return { tier: "write", label: "Risco Médio · Escrita no Workspace", css: "risk-write" };
    case "exec":
      return { tier: "exec", label: "Risco Alto · Execução em Sandbox", css: "risk-exec" };
    case "network":
      return { tier: "network", label: "Risco Alto · Rede Externa (Allowlist)", css: "risk-network" };
    case "credentials":
      return { tier: "credentials", label: "Risco Crítico · Uso de Credencial do Cofre", css: "risk-credentials" };
    case "destructive":
      return { tier: "destructive", label: "Risco Crítico · Ação Destrutiva", css: "risk-destructive" };
    default:
      return { tier: t || "write", label: t ? `Risco · ${t}` : "Aprovação Necessária", css: "risk-write" };
  }
}

export function openResidentTab(): void {
  const existing = state.editorTabs.find((t) => t.kind === "resident");
  if (!existing) {
    state.editorTabs.push({
      kind: "resident",
      relPath: RESIDENT_TAB_KEY,
      name: "Agent Hive",
      content: "",
      dirty: false,
    });
  }
  state.activeEditorPath = RESIDENT_TAB_KEY;
  renderEditorTabs();
  renderEditorBody();
  void refreshResidentData();
  startExternalSessionPoll();
}

export async function refreshResidentStatus(): Promise<void> {
  const backend = api();
  if (!backend?.ResidentStatus) return;
  try {
    const st = await backend.ResidentStatus();
    residentState.status = st;
    syncPolicyDraftsFromStatus(st);
    if (!residentState.loadedOnce && st.workstream) {
      residentState.workstream = st.workstream;
    }
    updateResidentStatusChip();
    rerenderResidentIfOpen();
  } catch {
    // ignore startup race
  }
}

export const EXTERNAL_SESSION_POLL_MS = 2000;
let externalSessionTimer = 0;

function externalSessionsHtml(): string {
  if (residentState.externalSessions.length === 0) {
    return `<div class="dim">Nenhuma sessão externa encontrada.</div>`;
  }
  return residentState.externalSessions
    .map((s) => `<div class="dim"><code>${escapeHtml(s.source)}</code> · ${escapeHtml(s.state)} · pid ${s.pid}</div>`)
    .join("");
}

export function startExternalSessionPoll(): void {
  if (externalSessionTimer) return;
  externalSessionTimer = window.setInterval(() => {
    if (state.activeEditorPath !== RESIDENT_TAB_KEY) {
      stopExternalSessionPoll();
      return;
    }
    void refreshExternalSessions();
  }, EXTERNAL_SESSION_POLL_MS);
}

export function stopExternalSessionPoll(): void {
  if (!externalSessionTimer) return;
  window.clearInterval(externalSessionTimer);
  externalSessionTimer = 0;
}

async function refreshExternalSessions(): Promise<void> {
  const backend = api();
  if (!backend?.ExternalSessions) return;
  try {
    const ext = await backend.ExternalSessions();
    residentState.externalSessions = Array.isArray(ext) ? ext : [];
  } catch {
    residentState.externalSessions = [];
  }
  const el = document.getElementById("res-external-sessions");
  if (el) el.innerHTML = externalSessionsHtml();
}

export async function refreshResidentData(): Promise<void> {
  const backend = api();
  if (!backend?.ResidentStatus) return;
  try {
    const st = await backend.ResidentStatus();
    residentState.status = st;
    syncPolicyDraftsFromStatus(st);
    if (!residentState.loadedOnce && st.workstream) {
      residentState.workstream = st.workstream;
    }
    const ws = residentState.workstream || st.workstream || "geral";
    const [sessionMsgs, memList, auditList, routines, soulText] = await Promise.all([
      backend.ResidentSessionMessages?.(ws).catch(() => []) ?? Promise.resolve([]),
      backend.ResidentListMemory?.(ws).catch(() => []) ?? Promise.resolve([]),
      backend.ResidentAuditLog?.(80).catch(() => []) ?? Promise.resolve([]),
      backend.ResidentListRoutines?.().catch(() => []) ?? Promise.resolve([]),
      backend.ResidentGetSoul?.().catch(() => "") ?? Promise.resolve(""),
    ]);
    if (!residentState.soulDirty) {
      residentState.soulDraft = String(soulText || "");
    }
    const convs = await backend.ResidentListConversations?.(state.project?.root || "").catch(() => []) ?? [];
    residentState.conversations = Array.isArray(convs) ? convs : [];
    if (!residentState.loadedOnce && residentState.hiveLaneId && residentState.messages.length === 0 && Array.isArray(sessionMsgs) && sessionMsgs.length > 0) {
      residentState.messages = sessionMsgs.map((m, idx) => ({
        id: m.id || `res-hist-${idx}`,
        kind: m.role === "user" ? "user" : "chief",
        label: m.role === "user" ? "Você" : "Agent Hive",
        text: m.text,
      }));
    }
    residentState.memory = Array.isArray(memList) ? memList : [];
    residentState.audit = Array.isArray(auditList) ? auditList : [];
    residentState.routines = Array.isArray(routines) ? routines : [];
    const loopList = await backend.ResidentListLoops?.(state.project?.root || "").catch(() => []) ?? [];
    residentState.loops = Array.isArray(loopList) ? loopList : [];
    await refreshExternalSessions();
    residentState.loadedOnce = true;
    updateResidentStatusChip();
    rerenderResidentIfOpen();
  } catch (err: any) {
    toastError("carregar estado do agente residente", err);
  }
}

export function updateResidentStatusChip(): void {
  const chip = document.getElementById("status-resident");
  if (!chip) return;
  const st = residentState.status;
  const label = st?.enabled === false
    ? "desligado"
    : (residentState.running ? "pensando" : (st?.state || "ocioso"));
  chip.textContent = `hive: ${label}`;
  if (label === "desligado") {
    chip.style.color = "var(--text-dim)";
  } else if (label === "pensando") {
    chip.style.color = "var(--accent-strong)";
  } else if (label === "aguardando você") {
    chip.style.color = "var(--amber)";
  } else {
    chip.style.color = "var(--green)";
  }
  const sbInfo = st?.sandboxAvailable
    ? `Sandbox: ${st.sandboxMechanism || "ativa"}`
    : "Sandbox: indisponível (falha fechada)";
  chip.title = st?.enabled === false
    ? "Agent Hive desativado nas Configurações da IDE — clique para abrir"
    : st
      ? `Agent Hive · ${sbInfo} · Conta: ${st.accountId} · Modelo: ${st.provider}/${st.model}`
      : "Agent Hive — clique para abrir";
}

function rerenderResidentIfOpen(): void {
  if (state.activeEditorPath === IDE_SETTINGS_TAB_KEY && state.ideSettingsSection === "agent-hive") {
    renderEditorBodyAsync();
    return;
  }
  if (state.activeEditorPath !== RESIDENT_TAB_KEY) {
    return;
  }
  // Preserva o foco do composer quando eventos assíncronos (streaming,
  // aprovações, refresh) re-renderizam a aba (Spec 007 H007).
  const active = typeof document !== "undefined" ? (document.activeElement as HTMLElement | null) : null;
  const hadChatFocus = Boolean(active && (active.id === "res-chat-input" || active.id === "res-workstream-input"));
  renderEditorBody();
  if (hadChatFocus) {
    const next = document.getElementById(active?.id || "");
    next?.focus();
  }
}

/** Shape mínimo do residentState usado por applyResidentLaneEvent (testável). */
export interface ResidentEventTarget {
  running: boolean;
  status: { state?: string } | null;
  messages: ChatMessage[];
}

/**
 * applyResidentLaneEvent aplica um evento da lane "resident" (streaming,
 * tool calls, aprovações, erros, finish) sobre o estado da conversa.
 * Função pura sobre `target` — sem DOM nem runtime — para testes unitários.
 */
export function applyResidentLaneEvent(target: ResidentEventTarget, payload: any): void {
  const ev = payload?.event;
  if (!ev) return;

  if (ev.kind === "stage") {
    target.running = true;
    if (target.status) target.status.state = ev.text || "pensando";
  } else if (ev.kind === "shared_message" && ev.text) {
    const id = String(ev.messageId || `shared-${Date.now()}`);
    if (target.messages.some((m) => m.id === id)) return;
    const fromUser = String(ev.role || "") === "user";
    target.messages.push({
      id,
      kind: fromUser ? "user" : "chief",
      label: fromUser ? "Você" : String(ev.label || "IDE"),
      text: String(ev.text),
    });
  } else if (ev.kind === "user_message" && ev.text) {
    const id = String(ev.messageId || "");
    const text = String(ev.text);
    const last = target.messages[target.messages.length - 1];
    if (last && last.kind === "user" && last.text === text) {
      if (id) last.id = id;
      return;
    }
    if (id && target.messages.some((m) => m.id === id)) return;
    const fromOrb = String(ev.origin || "") === "orb";
    target.messages.push({
      id: id || `res-u-${Date.now()}`,
      kind: "user",
      label: fromOrb ? "Você" : String(ev.label || "Remoto"),
      text,
    });
  } else if (ev.kind === "tool_call") {
    const risk = formatRiskBadge(String(ev.gate || ev.riskTier || ""));
    target.messages.push({
      id: `res-tool-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
      kind: "tool",
      label: risk.label,
      text: String(ev.toolName || "tool"),
      toolName: String(ev.toolName || ""),
      toolDetail: String(ev.detail || ""),
      riskTier: risk.tier,
      active: true,
    });
  } else if (ev.kind === "tool_result") {
    for (let i = target.messages.length - 1; i >= 0; i--) {
      const m = target.messages[i];
      if (m.kind === "tool" && m.active) {
        m.active = false;
        break;
      }
    }
  } else if (ev.kind === "approval_required") {
    if (target.status) target.status.state = "aguardando você";
    const tierRaw = String(ev.gate || ev.riskTier || "");
    target.messages.push({
      id: `res-appr-${Date.now()}`,
      kind: "approval",
      label: String(ev.label || "Aprovação do Agent Hive"),
      text: String(ev.text || ""),
      toolName: String(ev.toolName || ""),
      toolDetail: String(ev.detail || ""),
      riskTier: tierRaw,
      approvalId: String(ev.approvalId || ""),
    });
  } else if (ev.kind === "approval_resolved") {
    const target1 = target.messages.find((m) => m.kind === "approval" && m.approvalId === ev.approvalId && !m.resolved);
    if (target1) {
      target1.resolved = /recusada|negada/i.test(String(ev.text || "")) ? "denied" : "approved";
    }
  } else if (ev.kind === "text" && ev.thought && ev.text) {
    let th: ChatMessage | undefined;
    for (let i = target.messages.length - 1; i >= 0; i--) {
      const m = target.messages[i];
      if (m.kind === "user") break;
      if (m.kind === "thinking" && m.active) {
        th = m;
        break;
      }
    }
    if (!th) {
      th = {
        id: `res-th-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
        kind: "thinking",
        label: "Pensando",
        text: "",
        active: true,
        startedAt: Date.now(),
      };
      target.messages.push(th);
    }
    if (ev.partial) {
      th.text += String(ev.text);
    } else {
      th.text = th.text ? `${th.text}\n\n${ev.text}` : String(ev.text);
    }
  } else if (ev.kind === "text" && !ev.thought && ev.text) {
    const now = Date.now();
    for (const m of target.messages) {
      if (m.kind === "thinking" && m.active) {
        m.active = false;
        if (m.startedAt && !m.durationMs) m.durationMs = Math.max(1000, now - m.startedAt);
      }
    }
    const sanitized = sanitizeAssistantText(String(ev.text));
    if (!sanitized) return;
    const messageId = String(ev.messageId || "");
    if (messageId) {
      const existing = target.messages.find((m) => m.id === messageId);
      if (existing) {
        existing.text = ev.partial ? sanitizeAssistantText(`${existing.text}${sanitized}`) : sanitized;
        return;
      }
    }
    const last = target.messages[target.messages.length - 1];
    const fromOrb = String(ev.origin || "") === "orb";
    if (last && last.kind === "chief" && ev.partial) {
      last.text = sanitizeAssistantText(`${last.text}${sanitized}`);
    } else if (last && last.kind === "chief" && !ev.partial) {
      last.text = sanitized;
    } else if (sanitized.trim() !== "" || ev.partial) {
      target.messages.push({
        id: messageId || `res-ai-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
        kind: "chief",
        label: fromOrb ? "Agent Hive" : String(ev.label || "Agent Hive"),
        text: sanitized,
      });
    }
  } else if (ev.kind === "error") {
    const now = Date.now();
    for (const m of target.messages) {
      if (m.kind === "thinking" && m.active) {
        m.active = false;
        if (m.startedAt && !m.durationMs) m.durationMs = Math.max(1000, now - m.startedAt);
      }
    }
    target.running = false;
    if (target.status) target.status.state = "ocioso";
    target.messages.push({
      id: `res-err-${Date.now()}`,
      kind: "error",
      label: "Erro",
      text: String(ev.text || "Erro no Agent Hive"),
    });
  } else if (ev.kind === "finish") {
    const now = Date.now();
    for (const m of target.messages) {
      if (m.kind === "thinking" && m.active) {
        m.active = false;
        if (m.startedAt && !m.durationMs) m.durationMs = Math.max(1000, now - m.startedAt);
      }
    }
    target.running = false;
    if (target.status) target.status.state = "ocioso";
    if (String(ev.origin || "") === "routine") {
      target.messages.push({
        id: `res-routine-${Date.now()}`,
        kind: "system",
        label: "Rotina",
        text: String(ev.text || "Rotina concluída."),
      });
    }
  }
}

function mirrorResidentToOpenLane(ev: any): void {
  if (!ev || ev.origin === "ide" || ev.kind === "shared_message" || ev.thought) return;
  if (ev.kind !== "user_message" && ev.kind !== "text") return;
  const id = String(ev.messageId || "");
  const text = String(ev.text || "");
  if (!id || !text) return;
  const sessionId = String(ev.sessionId || "");
  const hiveLane = state.lanes.find((l) => l.id === residentState.hiveLaneId);
  const lane = state.lanes.find((l) => sessionId && l.sessionId === sessionId)
    || (hiveLane && (!hiveLane.sessionId || hiveLane.sessionId === sessionId) ? hiveLane : undefined);
  if (!lane) return;
  const kind = ev.kind === "user_message" ? "user" : "chief";
  const label = kind === "user" ? "Você" : "Agent Hive";
  const existing = lane.messages.find((m) => m.id === id);
  if (existing) {
    existing.text = ev.partial ? `${existing.text}${text}` : text;
  } else {
    lane.messages.push({ id, kind, label, text });
  }
  if (ev.sessionId && !lane.sessionId) {
    lane.sessionId = String(ev.sessionId);
    lane.conversationKind = "hive";
  }
  onLaneMirrored?.();
}

export function handleResidentLaneEvent(payload: any): void {
  const ev = payload?.event;
  if (!ev) return;

  applyResidentLaneEvent(residentState, payload);
  mirrorResidentToOpenLane(ev);

  if (ev.kind === "stage") {
    noteActivity({ laneId: "resident", kind: "stage", text: ev.text, label: ev.label });
  } else if (ev.kind === "tool_call") {
    noteActivity({ laneId: "resident", kind: "tool_call", toolName: String(ev.toolName || "") });
  } else if (ev.kind === "approval_required") {
    const apprId = residentState.messages[residentState.messages.length - 1]?.id || "";
    publishNotice(
      { laneId: "resident", kind: "approval_required", toolName: String(ev.toolName || ""), text: String(ev.text || ""), label: String(ev.label || "") },
      { surface: "resident", projectId: "", laneId: "resident", messageId: apprId },
    );
  } else if (ev.kind === "error") {
    const errId = residentState.messages[residentState.messages.length - 1]?.id || "";
    publishNotice(
      { laneId: "resident", kind: "error", text: String(ev.text || ""), label: "Erro", origin: String(ev.origin || "") },
      { surface: "resident", projectId: "", laneId: "resident", messageId: errId },
    );
  } else if (String(ev.kind || "").startsWith("loop_")) {
    void refreshResidentData();
  } else if (ev.kind === "finish" && String(ev.origin || "") === "routine") {
    const doneId = residentState.messages[residentState.messages.length - 1]?.id || "";
    publishNotice(
      { laneId: "resident", kind: "finish", origin: "routine", text: String(ev.text || "") },
      { surface: "resident", projectId: "", laneId: "resident", messageId: doneId },
    );
    void refreshResidentData();
  } else if (ev.kind === "finish") {
    void refreshResidentData();
  }

  updateResidentStatusChip();
  rerenderResidentIfOpen();
}

export function residentModelSpec(prov?: string, mod?: string): string {
  const p = (prov || "").trim();
  const m = (mod || "").trim();
  if ((!p || p === "auto" || p === "jev") && (!m || m === "auto" || m === "jev:auto")) {
    return "jev:auto";
  }
  if (p === "jev" && m) {
    return m.startsWith("jev:") ? m : `jev:${m}`;
  }
  if (!m || m === "auto") {
    return p ? `${p}:auto` : "jev:auto";
  }
  if (m.includes(":") || m.includes("/")) {
    return m;
  }
  return p && p !== "auto" ? `${p}:${m}` : m;
}

export function parseResidentPickerSelection(provVal: string, modVal: string): { provider: string; model: string } {
  const p = (provVal || "").trim();
  const m = (modVal || "").trim();
  if (p === "jev" || m === "jev:auto") {
    if (!m || m === "jev:auto") return { provider: "", model: "" };
    if (m.startsWith("jev:")) return { provider: m.slice(4), model: "auto" };
  }
  if (m === `${p}:auto` || m === "auto") {
    return { provider: p, model: "auto" };
  }
  return { provider: p, model: m };
}

export function renderResidentViewHtml(): string {
  const st = residentState.status;
  const acct = st?.accountId || "local";
  const prov = st?.provider || "openrouter";
  const mod = st?.model || "openrouter/auto";
  const enabled = st ? st.enabled !== false : true;
  const stateLabel = !enabled
    ? "desligado"
    : (residentState.running ? "pensando" : (st?.state || "ocioso"));
  const activeWsName = state.project?.name || "nenhum workspace";
  const sbOk = Boolean(st?.sandboxAvailable);
  const sbText = sbOk
    ? `Sandbox: ${st?.sandboxMechanism || "ativa"}`
    : `Sandbox indisponível (falha fechada)`;
  const vaultOk = Boolean(st?.vaultAvailable);
  const pendingCount = residentState.messages.filter((m) => m.kind === "approval" && !m.resolved).length;

  return `
    <div class="resident-page">
      <header class="resident-header">
        <div class="resident-title-block">
          <div class="resident-title-row">
            <span class="resident-brand-mark" aria-hidden="true">${icons.sparkle}</span>
            <h2>Agent Hive</h2>
            <span class="resident-badge state-${stateLabel === "pensando" ? "running" : stateLabel === "aguardando você" ? "ask" : "idle"}">${escapeHtml(stateLabel)}</span>
            <span class="resident-badge ${st?.experimentalUnisolated ? "risk-destructive" : "readonly"}">${st?.experimentalUnisolated ? "Sem isolamento (teste)" : "Sandbox obrigatória"}</span>
            <span class="resident-badge ${sbOk ? "risk-read" : "risk-destructive"}" title="${escapeHtml(st?.sandboxReason || sbText)}">${escapeHtml(sbText)}</span>
            <span class="resident-badge ${vaultOk ? "risk-read" : "risk-write"}" title="${escapeHtml(st?.vaultReason || "Cofre do SO")}">${vaultOk ? "Cofre SO: ativo" : "Cofre SO: indisponível"}</span>
          </div>
          <div class="resident-meta-line dim">
            <span title="Conta local isolada">Conta: <code>${escapeHtml(acct)}</code></span>
            <span class="resident-meta-dot">·</span>
            <span>Workspace: <code>${escapeHtml(activeWsName)}</code></span>
            <span class="resident-meta-dot">·</span>
            <span>Rota ativa: <code>${escapeHtml(prov)}/${escapeHtml(mod)}</code></span>
          </div>
        </div>
        <div class="resident-header-controls">
          <input id="res-workstream-input" class="res-compact-input res-ws-input" value="${escapeHtml(residentState.workstream)}" placeholder="workstream" title="Workstream ativo para memória e sessão" />
          ${renderModelPickerPairHtml({
            pairKey: "res-hive",
            selectId: "res-model-select",
            currentModel: residentModelSpec(prov, mod),
            compact: true,
          })}
          <label class="resident-events-toggle" title="Acordar no arquivo salvo, no fim da lane ou numa aprovação. Não executa ferramenta."><input type="checkbox" id="res-event-triggers" ${st?.eventTriggers ? "checked" : ""}/><span>eventos</span></label>
          <button type="button" class="mini primary" id="btn-res-save-cfg">Salvar</button>
        </div>
      </header>

      <div class="resident-subtabs">
        <button type="button" class="drawer-tab ${residentState.subTab === "chat" ? "active" : ""}" data-res-tab="chat">Conversa${pendingCount > 0 ? ` (${pendingCount} aprovação)` : ""}</button>
        <button type="button" class="drawer-tab ${residentState.subTab === "security" ? "active" : ""}" data-res-tab="security">Segurança & Cofre</button>
        <button type="button" class="drawer-tab ${residentState.subTab === "memory" ? "active" : ""}" data-res-tab="memory">Memória Pessoal (${residentState.memory.length})</button>
        <button type="button" class="drawer-tab ${residentState.subTab === "audit" ? "active" : ""}" data-res-tab="audit">Auditoria (${residentState.audit.length})</button>
        <div class="spacer"></div>
        ${residentState.subTab === "chat" ? `<button type="button" class="mini ghost" id="btn-res-clear-chat">Limpar conversa</button>` : ""}
        <button type="button" class="mini ghost" id="btn-res-open-settings" title="Abrir Configurações Globais do Agent Hive">${icons.settings}<span>Configurações</span></button>
        <button type="button" class="mini ghost" id="btn-res-refresh">${icons.refresh}<span>Atualizar</span></button>
      </div>

      ${!enabled ? `
        <div class="tc-error" style="display:flex;align-items:center;justify-content:space-between;gap:10px;flex-wrap:wrap">
          <span>Agent Hive está desativado nas Configurações da IDE. Ative-o para iniciar novos turnos ou reexibir o Orb flutuante.</span>
          <button type="button" class="mini primary" id="btn-res-open-settings-enable">${icons.settings}<span>Ativar nas Configurações</span></button>
        </div>
      ` : ""}

      ${residentState.subTab === "chat" ? renderLoopPanelHtml() : ""}
      ${residentState.subTab === "chat" ? renderResidentChatHtml(enabled) : ""}
      ${residentState.subTab === "security" ? renderResidentSecurityHtml() : ""}
      ${residentState.subTab === "memory" ? renderResidentMemoryHtml(acct) : ""}
      ${residentState.subTab === "audit" ? renderResidentAuditHtml() : ""}
    </div>
  `;
}

function renderLoopPanelHtml(): string {
  const loops = residentState.loops;
  if (loops.length === 0) {
    return `<section id="res-loops" class="resident-loops-bar" hidden aria-label="Loops"><p class="dim">Nenhum loop neste projeto.</p></section>`;
  }
  const rows = loops.map((loop) => {
    const verdict = loop.lastVerdict || loop.outcome || "sem veredito";
    const running = loop.status === "running" || loop.status === "waiting";
    return `<div class="resident-loop-chip">
      <strong>${escapeHtml(loop.name || loop.id)}</strong>
      <span class="resident-badge ${running ? "state-running" : "readonly"}">${escapeHtml(loop.status)}</span>
      <span class="dim">iteração ${Number(loop.iteration) || 0}</span>
      <span class="dim">· ${escapeHtml(verdict)}</span>
      ${running ? `<button type="button" class="mini danger" data-loop-stop="${escapeHtml(loop.id)}">Parar</button>` : ""}
    </div>`;
  }).join("");
  return `<section id="res-loops" class="resident-loops-bar" aria-label="Loops">${rows}</section>`;
}

function renderResidentActivityRowHtml(m: ChatMessage): string {
  if (m.kind === "thinking") {
    if (!m.text.trim()) return "";
    return `<div class="activity-thought-prose md-prose">${renderMarkdownStreamed(`${m.id}:think`, m.text)}</div>`;
  }
  if (m.kind === "tool") {
    const risk = formatRiskBadge(m.riskTier);
    return `
      <div class="activity-tool-row" data-msg-id="${escapeHtml(m.id)}">
        ${m.active ? `<span class="tool-step-spin">${icons.spinner}</span>` : ""}
        <span class="msg-badge badge-tool">${escapeHtml(m.toolName || m.text)}</span>
        <span class="resident-badge ${risk.css}">${escapeHtml(risk.label)}</span>
        ${m.toolDetail ? `<span class="activity-tool-detail">${escapeHtml(m.toolDetail)}</span>` : ""}
      </div>
    `;
  }
  if (m.kind === "system" && m.text.trim()) {
    return `
      <div class="activity-meta-row" data-msg-id="${escapeHtml(m.id)}">
        <span class="activity-meta-label">${escapeHtml(m.label || "Sistema")}</span>
        <span class="activity-meta-text">${escapeHtml(m.text)}</span>
      </div>
    `;
  }
  return "";
}

function renderHiveTabStrip(): string {
  const projects = state.projectTabs;
  const projectTabs = projects.length > 0
    ? projects.map((ctx) => `
        <button type="button" class="project-tab ${ctx.id === state.activeProjectId ? "active" : ""}" data-res-project="${escapeHtml(ctx.id)}">
          ${icons.folderOpen}<span>${escapeHtml(ctx.project?.name || "Projeto")}</span>
        </button>
      `).join("")
    : `<span class="project-tab active">${icons.folderOpen}<span>${escapeHtml(state.project?.name || "Projeto")}</span></span>`;
  const laneTabs = state.lanes.map((lane) => {
    const on = lane.id === residentState.hiveLaneId;
    return `
      <button type="button" class="lane-pill ${on ? "active" : ""}" data-res-lane="${escapeHtml(lane.id)}" role="tab" aria-selected="${on ? "true" : "false"}">
        ${lane.running ? `<span class="lane-dot"></span>` : ""}
        <span>${escapeHtml(laneDisplayName(lane))}</span>
      </button>
    `;
  }).join("");
  const empty = state.lanes.length === 0
    ? `<span class="dim">Nenhuma conversa aberta neste projeto.</span>`
    : "";
  return `
    <div class="resident-sync-tabs">
      <div class="resident-sync-tabs-inner">
        <div class="resident-project-row" role="tablist">${projectTabs}</div>
        <span class="resident-bar-sep" aria-hidden="true"></span>
        <div class="lane-bar resident-lane-bar" role="tablist">
          ${empty}
          ${laneTabs}
          <button type="button" class="lane-pill resident-new-lane-btn" id="btn-res-new-lane">+ Nova conversa</button>
        </div>
      </div>
    </div>
  `;
}

function showLaneInIDE(id: string): void {
  state.activeLaneId = id;
  renderLaneBarOnly();
  document.querySelector<HTMLElement>(`#lane-bar [data-lane="${CSS.escape(id)}"]`)?.click();
}

function hiveMessagesFromLane(laneId: string): ChatMessage[] {
  const lane = state.lanes.find((l) => l.id === laneId);
  if (!lane) return [];
  return lane.messages
    .filter((m) => m.text && (m.kind === "user" || m.kind === "chief" || m.kind === "error"))
    .map((m) => ({
      id: m.id,
      kind: m.kind === "user" ? "user" as const : m.kind === "error" ? "error" as const : "chief" as const,
      label: m.kind === "user" ? "Você" : (m.label || "IDE"),
      text: m.text,
    }));
}

function startHiveLane() {
  const team = state.lanes.find((l) => l.id === state.activeLaneId)?.teamName
    || state.project?.teamConfig?.active
    || "principal";
  const lane = createLane(team);
  const taken = state.lanes.filter((l) => (l.label || "").startsWith("Hive")).length;
  lane.label = taken === 0 ? "Hive" : `Hive ${taken + 1}`;
  lane.conversationKind = "hive";
  lane.sessionId = "";
  state.lanes.push(lane);
  residentState.hiveLaneId = lane.id;
  residentState.boundLaneId = lane.id;
  residentState.boundSessionId = "";
  residentState.messages = [];
  showLaneInIDE(lane.id);
  return lane;
}

function renderResidentChatHtml(enabled = true): string {
  const timeline = groupConversationItems(residentState.messages, residentState.running);
  const msgsHtml = timeline.length === 0
    ? `
      <div class="chat-empty-state">
        <div class="chat-empty-icon">${icons.brandHero}</div>
        <div class="chat-empty-title">Agent Hive pronto</div>
        <div class="chat-empty-sub">Leitura local no workspace é direta e auditada. Escrita de arquivos, execução de binários na allowlist (sempre em sandbox com argv explícito, sem sh -c) e rede na allowlist exigem sua aprovação no cartão abaixo.</div>
      </div>
    `
    : timeline.map((item) => {
        if (item.kind === "activity") {
          const sum = summarizeActivityGroup(
            item.messages,
            item.active,
            Date.now(),
            (toolMsg) => toolMsg.toolDetail ? `${toolMsg.toolName || "ação"} · ${toolMsg.toolDetail}` : (toolMsg.toolName || toolMsg.text || "Executando"),
          );
          const isOpen = residentExpandedGroups.has(item.id);
          const bodyRows = item.messages.map(renderResidentActivityRowHtml).filter(Boolean).join("");
          const bodyHtml = bodyRows || `<div class="activity-meta-row"><span class="activity-meta-text">${escapeHtml(sum.title)}</span></div>`;
          const elapsedHtml = sum.elapsedText
            ? `<span class="activity-fold-elapsed" ${sum.activeStartedAt ? `data-thinking-started="${sum.activeStartedAt}"` : ""}>${escapeHtml(sum.elapsedText)}</span>`
            : "";
          return `
            <div class="activity-fold-wrap" data-tl-key="act:${escapeHtml(item.id)}">
              <details class="activity-fold ${item.active ? "is-active" : ""}" data-res-activity-fold="${escapeHtml(item.id)}" ${isOpen ? "open" : ""}>
                <summary class="activity-fold-summary">
                  ${item.active ? `<span class="activity-fold-spin">${icons.spinner}</span>` : ""}
                  <span class="activity-fold-title">${escapeHtml(sum.title)}</span>
                  ${elapsedHtml}
                  <span class="spacer"></span>
                  <span class="activity-fold-chev">${icons.chevronDown}</span>
                </summary>
                <div class="activity-fold-body">${bodyHtml}</div>
              </details>
            </div>
          `;
        }
        const m = item.message;
        if (m.kind === "approval") {
          const risk = formatRiskBadge(m.riskTier);
          const decided = m.resolved === "approved"
            ? `<span class="tc-jira-pill ok">Aprovado pelo usuário</span>`
            : m.resolved === "denied"
              ? `<span class="tc-jira-pill off">Negado pelo usuário</span>`
              : `
                <div style="display:flex;gap:8px;margin-top:10px;align-items:center">
                  <button type="button" class="mini primary" data-res-appr-ok="${escapeHtml(m.approvalId || "")}">Aprovar ação</button>
                  <button type="button" class="mini danger" data-res-appr-no="${escapeHtml(m.approvalId || "")}">Negar</button>
                  <span class="dim" style="font-size:11px">Sem resposta até o timeout = negação automática (falha fechada)</span>
                </div>
              `;
          return `
            <div class="msg-card role-plan resident-approval-card" data-msg-id="${escapeHtml(m.id)}">
              <div class="resident-approval-meta">
                <span class="msg-badge badge-system">${escapeHtml(m.label)}</span>
                <span class="resident-badge ${risk.css}">${escapeHtml(risk.label)}</span>
                ${m.toolName ? `<code>${escapeHtml(m.toolName)}</code>` : ""}
                ${m.toolDetail ? `<span class="dim">Alvo: <code>${escapeHtml(m.toolDetail)}</code></span>` : ""}
              </div>
              <div class="msg-body" style="white-space:pre-wrap">${escapeHtml(m.text)}</div>
              ${decided}
            </div>
          `;
        }
        const body = m.kind === "chief" ? renderMarkdownStreamed(m.id, sanitizeAssistantText(m.text)) : escapeHtml(m.text);
        return `
          <div class="msg-card role-${m.kind}" data-msg-id="${escapeHtml(m.id)}">
            <span class="msg-badge badge-${m.kind}">${escapeHtml(m.label)}</span>
            <div class="msg-body">${body}</div>
          </div>
        `;
      }).join("");

  const pendingApprovals = residentState.messages.filter((m) => m.kind === "approval" && !m.resolved);
  const approvalDockHtml =
    pendingApprovals.length === 0
      ? ""
      : `
        <div class="chat-approval-dock">
          ${pendingApprovals
            .map((m) => {
              const risk = formatRiskBadge(m.riskTier);
              const detail = m.toolDetail ? `${m.toolName || "ação"} · ${m.toolDetail}` : (m.text || m.toolName || "");
              return `
                <div class="chat-approval-dock-row">
                  <div class="chat-approval-dock-info">
                    <span class="resident-badge ${risk.css}">${escapeHtml(risk.label)}</span>
                    <span class="chat-approval-dock-title">${escapeHtml(m.label || "Aprovação do Agent Hive")}</span>
                    ${detail ? `<span class="chat-approval-dock-detail">${escapeHtml(detail)}</span>` : ""}
                  </div>
                  <div class="chat-approval-dock-actions">
                    <button type="button" class="mini primary" data-res-appr-ok="${escapeHtml(m.approvalId || "")}">${icons.check}<span>Aprovar ação</span></button>
                    <button type="button" class="mini danger" data-res-appr-no="${escapeHtml(m.approvalId || "")}"><span>Negar</span></button>
                  </div>
                </div>
              `;
            })
            .join("")}
        </div>
      `;

  const convSelect = renderHiveTabStrip();

  return `
    ${convSelect}
    <div class="resident-chat-body" id="res-chat-scroll">${msgsHtml}</div>
    <div class="resident-composer">
      ${approvalDockHtml}
      <div class="composer-box resident-composer-box">
        <textarea id="res-chat-input" rows="2" placeholder="Peça ao Agent Hive para inspecionar, editar arquivos, navegar no Chrome ou rodar um binário autorizado… (Enter envia)">${escapeHtml(residentState.draftInput)}</textarea>
        <div class="resident-composer-actions">
          <span class="dim resident-composer-hint">Sandbox + auditoria fsync · escrita e execução pedem aprovação</span>
          <div class="spacer"></div>
          <button type="button" class="danger mini" id="btn-res-cancel" ${residentState.running ? "" : "hidden disabled"}>${icons.stop}<span>Parar</span></button>
          <button type="button" class="primary composer-send" id="btn-res-send" ${residentState.running || !enabled ? "disabled" : ""}>${icons.send}<span>Enviar</span></button>
        </div>
      </div>
    </div>
  `;
}

function renderResidentSecurityHtml(): string {
  const st = residentState.status;
  const secrets = Array.isArray(st?.vaultSecrets) ? st!.vaultSecrets! : [];
  const sbOk = Boolean(st?.sandboxAvailable);
  const vaultOk = Boolean(st?.vaultAvailable);

  return `
    <div class="resident-panel-body">
      <div class="resident-security-grid">
        <div class="resident-row-card">
          <div class="resident-row-head">
            <strong>Browser e desktop</strong>
            <span class="resident-badge ${st?.experimentalUnisolated ? "risk-destructive" : "risk-read"}">${st?.experimentalUnisolated ? "SEM isolamento" : "Isolados desligados"}</span>
          </div>
          <div class="dim">${escapeHtml(st?.isolatedBrowserReason || "Browser isolado recusado: não há espaço com egress filtrado.")}</div>
          <div class="dim">${escapeHtml(st?.isolatedDesktopReason || "Desktop isolado recusado: a sessão real não é alvo.")}</div>
          <label class="dim" style="display:block;margin-top:8px" title="Liga browser e desktop na sessão real, sem espaço isolado.">
            <input type="checkbox" id="res-experimental-unisolated" ${st?.experimentalUnisolated ? "checked" : ""}/>
            experimental_unisolated — usar browser e desktop SEM isolamento
          </label>
          ${st?.experimentalUnisolated ? `<div class="resident-badge risk-destructive" style="margin-top:8px">${escapeHtml(st.experimentalWarning || "SEM isolamento: a sessão real recebe a ação. O cofre não é consultado. Fora de Full Acesso, cada ação pede confirmação.")}</div>` : `<div class="dim">Desligado por padrão. Ligado, cada ação é auditada, avisa que roda SEM isolamento e pede confirmação fora de Full Acesso. O cofre não entra nesse caminho.</div>`}
        </div>

        <div class="resident-row-card">
          <div class="resident-row-head">
            <strong>Sandbox do Sistema Operacional</strong>
            <span class="resident-badge ${sbOk ? "risk-read" : "risk-destructive"}">${sbOk ? "Ativa" : "Indisponível"}</span>
          </div>
          <div class="dim">Mecanismo: <code>${escapeHtml(st?.sandboxMechanism || "desconhecido")}</code></div>
          <div class="dim">${escapeHtml(st?.sandboxReason || "Política falha fechada: sem sandbox aplicável, qualquer escrita ou execução é recusada imediatamente.")}</div>
        </div>

        <div class="resident-row-card">
          <div class="resident-row-head">
            <strong>Cofre do Sistema (Segredos)</strong>
            <span class="resident-badge ${vaultOk ? "risk-read" : "risk-write"}">${vaultOk ? "Windows Credential Manager / DPAPI" : "Indisponível"}</span>
          </div>
          <div class="dim">Segredos nunca entram em prompt, log, auditoria ou evento SSE. Comandos que exibem token (ex.: <code>gh auth token</code>, <code>printenv</code>) são bloqueados.</div>
        </div>
      </div>

      <div class="resident-row-card">
        <div class="resident-row-head">
          <strong>Sessões externas</strong>
          <span class="dim">só leitura</span>
        </div>
        <div id="res-external-sessions">
        ${externalSessionsHtml()}
        </div>
      </div>

      <div class="resident-row-card">
        <div class="resident-row-head">
          <strong>Política Local de Execução e Rede (Falha Fechada)</strong>
          <span class="spacer"></span>
          <button type="button" class="primary mini" id="btn-res-save-policy">Salvar Política</button>
        </div>
        <div class="dim">Configuração local em <code>%LOCALAPPDATA%\\HiveMind\\resident\\policy.json</code> (nunca alterável pelo modelo, repositório ou relay). Curinga <code>*</code> e shells (<code>sh</code>, <code>cmd</code>, <code>powershell</code>) são proibidos.</div>
        <label class="dim" style="margin-top:6px">Binários autorizados para <code>exec_command</code> (separados por vírgula; vazio = bloqueia tudo):</label>
        <input id="res-policy-binaries" placeholder="Ex.: git, go, npm" value="${escapeHtml(residentState.binariesDraft)}" />
        <label class="dim" style="margin-top:6px">Domínios autorizados para <code>net_fetch</code> (separados por vírgula; vazio = rede bloqueada):</label>
        <input id="res-policy-hosts" placeholder="Ex.: api.github.com, *.golang.org" value="${escapeHtml(residentState.hostsDraft)}" />
      </div>

      <div class="resident-row-card">
        <div class="resident-row-head">
          <strong>Credenciais no Cofre do Sistema (${secrets.length})</strong>
        </div>
        <div class="resident-mem-form">
          <input id="res-secret-name" style="max-width:200px" placeholder="Nome lógico (ex.: github_ro)" value="${escapeHtml(residentState.secretNameDraft)}" />
          <input id="res-secret-val" type="password" placeholder="Valor do segredo (gravado apenas no cofre do SO)…" value="${escapeHtml(residentState.secretValueDraft)}" />
          <button type="button" class="primary mini" id="btn-res-secret-add">Guardar no Cofre</button>
        </div>
        <div class="resident-list" style="margin-top:8px">
          ${secrets.length === 0
            ? `<div class="dim">Nenhuma credencial cadastrada no cofre do residente.</div>`
            : secrets.map((name) => `
              <div class="resident-row-head" style="padding:4px 0;border-bottom:1px solid var(--border)">
                <code>${escapeHtml(name)}</code>
                <span class="tok-pill">somente referência lógica</span>
                <span class="spacer"></span>
                <button type="button" class="mini ghost" data-res-sec-del="${escapeHtml(name)}">Remover</button>
              </div>
            `).join("")
          }
        </div>
      </div>

      <div class="resident-row-card">
        <div class="resident-row-head">
          <strong>Rotinas</strong>
          <span class="dim">sem comando</span>
          <span class="spacer"></span>
          <button type="button" class="primary mini" id="btn-res-routine-add">Gravar rotina</button>
        </div>
        <div class="dim">Expressão de cinco campos, fuso e texto. O modelo só grava isto depois de aprovação. Não há campo de comando.</div>
        <div class="resident-mem-form">
          <input id="res-routine-expr" placeholder="0 9 * * 1-5" />
          <input id="res-routine-tz" placeholder="America/Sao_Paulo" />
          <input id="res-routine-text" placeholder="Texto que a rotina envia" />
        </div>
        <div class="resident-list" style="margin-top:8px">
          ${residentState.routines.length === 0
            ? `<div class="dim">Nenhuma rotina.</div>`
            : residentState.routines.map((rt) => `
              <div class="resident-row-head" style="padding:4px 0;border-bottom:1px solid var(--border)">
                <code>${escapeHtml(rt.expr)}</code>
                <span class="dim">${escapeHtml(rt.text)}</span>
                <span class="spacer"></span>
                <button type="button" class="mini ghost" data-res-routine-toggle="${escapeHtml(rt.id)}" data-enabled="${rt.enabled ? "1" : "0"}">${rt.enabled ? "Pausar" : "Retomar"}</button>
                <button type="button" class="mini ghost" data-res-routine-del="${escapeHtml(rt.id)}">Apagar</button>
              </div>
            `).join("")}
        </div>
      </div>

      <div class="resident-row-card">
        <div class="resident-row-head">
          <strong>Revisão de skill nativa</strong>
          <span class="spacer"></span>
          <button type="button" class="primary mini" id="btn-res-skill-review">Revisar</button>
        </div>
        <div class="dim">Grava o hash da skill nativa. Hash diferente tira a skill do residente até nova revisão. Skill de projeto não entra.</div>
        <input id="res-skill-name" placeholder="nome da skill nativa" />
      </div>
    </div>
  `;
}

function renderResidentMemoryHtml(accountId: string): string {
  return `
    <div class="resident-panel-body">
      <div class="resident-row-card" style="margin-bottom:10px">
        <div class="resident-row-head">
          <strong>SOUL.md (Tom e Preferências do Residente)</strong>
          <span class="dim">máx. 2 KiB</span>
          <span class="spacer"></span>
          <button type="button" class="primary mini" id="btn-res-soul-save">Salvar SOUL.md</button>
        </div>
        <div class="dim" style="margin-bottom:6px">Define tom, estilo de resposta e prioridades pessoais injetadas automaticamente nas sessões do residente.</div>
        <textarea id="res-soul-input" rows="3" placeholder="Ex.: Seja direto em português, priorize análises objetivas e resumos de PR curtos…">${escapeHtml(residentState.soulDraft)}</textarea>
      </div>
      <div class="dim" style="margin-bottom:10px">
        Memória pessoal salva apenas neste PC para a conta <code>${escapeHtml(accountId)}</code> (não atravessa o relay; <code>.hivemind/MEMORY.md</code> continua sendo a memória do time).
      </div>
      <div class="resident-mem-form">
        <input id="res-mem-input" placeholder="Adicionar fato pessoal (ex.: Prefiro respostas diretas em PT-BR)…" value="${escapeHtml(residentState.memoryDraft)}" />
        <label class="autoreview-toggle-label"><input type="checkbox" id="res-mem-pin" ${residentState.memoryPinned ? "checked" : ""} /><span>Fixar (pin)</span></label>
        <button type="button" class="primary mini" id="btn-res-mem-add">Salvar fato</button>
      </div>
      <div class="resident-list">
        ${residentState.memory.length === 0
          ? `<div class="dim" style="padding:12px">Nenhum fato salvo nesta conta ainda.</div>`
          : residentState.memory.map((f) => `
            <div class="resident-row-card">
              <div class="resident-row-head">
                <span class="tok-pill">${escapeHtml(f.workstream)}</span>
                ${f.pinned ? `<span class="tc-jira-pill ok">fixado</span>` : ""}
                <span class="spacer"></span>
                <button type="button" class="mini ghost" data-res-mem-del="${escapeHtml(f.id)}">Apagar</button>
              </div>
              <div class="resident-row-text">${escapeHtml(f.text)}</div>
            </div>
          `).join("")
        }
      </div>
    </div>
  `;
}

function renderResidentAuditHtml(): string {
  const q = residentState.auditFilter.trim().toLowerCase();
  const filtered = residentState.audit.filter((e) => {
    if (!q) return true;
    return `${e.tool} ${e.origin} ${e.decision} ${e.outcome || ""} ${e.targetClass || ""} ${e.reason || ""} ${e.sandbox || ""}`.toLowerCase().includes(q);
  });
  return `
    <div class="resident-panel-body">
      <div class="resident-mem-form">
        <input id="res-audit-filter" placeholder="Filtrar auditoria por ferramenta, classe de risco, sandbox, origem, decisão ou alvo…" value="${escapeHtml(residentState.auditFilter)}" />
      </div>
      <div class="resident-list">
        ${filtered.length === 0
          ? `<div class="dim" style="padding:12px">Nenhum registro de auditoria ainda.</div>`
          : filtered.map((e) => {
            const risk = formatRiskBadge(e.tier);
            return `
            <div class="resident-row-card">
              <div class="resident-row-head">
                <code>${escapeHtml(e.tool)}</code>
                <span class="tok-pill">${escapeHtml(e.phase)}</span>
                <span class="tok-pill">${escapeHtml(e.origin)}</span>
                <span class="resident-badge ${risk.css}">${escapeHtml(e.tier)}</span>
                ${e.sandbox ? `<span class="tok-pill">sandbox: ${escapeHtml(e.sandbox)}</span>` : ""}
                <span class="tc-jira-pill ${e.decision === "allow" ? "ok" : "off"}">${escapeHtml(e.decision)}${e.outcome ? ` · ${escapeHtml(e.outcome)}` : ""}</span>
                <span class="spacer"></span>
                <span class="dim" style="font-family:var(--mono);font-size:10.5px">${escapeHtml(e.ts)}</span>
              </div>
              ${e.targetClass ? `<div class="dim">Classe: <code>${escapeHtml(e.targetClass)}</code>${e.targetHash ? ` · ${escapeHtml(e.targetHash)}` : ""}</div>` : ""}
              ${e.reason ? `<div class="dim">${escapeHtml(e.reason)}</div>` : ""}
            </div>
          `;
          }).join("")
        }
      </div>
    </div>
  `;
}

function splitCommaList(raw: string): string[] {
  return raw
    .split(/[,\n]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

export function bindResidentViewEvents(): void {
  document.querySelectorAll<HTMLElement>("[data-res-tab]").forEach((btn) => {
    btn.addEventListener("click", () => {
      residentState.subTab = (btn.dataset.resTab as ResidentSubTab) || "chat";
      rerenderResidentIfOpen();
    });
  });

  document.getElementById("btn-res-open-settings")?.addEventListener("click", () => {
    void openIdeSettingsTab("agent-hive");
  });
  document.getElementById("btn-res-open-settings-enable")?.addEventListener("click", () => {
    void openIdeSettingsTab("agent-hive");
  });
  document.getElementById("btn-res-refresh")?.addEventListener("click", () => {
    void refreshResidentData();
  });
  document.querySelectorAll<HTMLButtonElement>("[data-loop-stop]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = btn.dataset.loopStop || "";
      const backend = api();
      if (!id || !backend?.ResidentStopLoop) return;
      void backend.ResidentStopLoop(state.project?.root || "", id).then(() => refreshResidentData()).catch((err) => {
        toastError("parar o loop", err);
      });
    });
  });

  document.getElementById("res-experimental-unisolated")?.addEventListener("change", async (ev) => {
    const backend = api();
    const on = Boolean((ev.target as HTMLInputElement | null)?.checked);
    if (!backend?.ResidentSetExperimentalUnisolated) return;
    try {
      const st = await backend.ResidentSetExperimentalUnisolated(on);
      residentState.status = st;
      updateResidentStatusChip();
      if (on) {
        toast("Browser e desktop do residente ligados SEM isolamento. Cada ação é auditada e, fora de Full Acesso, pede confirmação.", "success");
      } else {
        toast("Browser e desktop do residente voltaram a recusar.", "success");
      }
      rerenderResidentIfOpen();
    } catch (err: any) {
      toastError("salvar experimental_unisolated", err);
    }
  });

  bindModelPickerPairs(document.querySelector(".resident-header-controls"));

  document.getElementById("btn-res-save-cfg")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend?.ResidentSaveConfig) return;
    const provSel = document.querySelector<HTMLSelectElement>('[data-mp-provider="res-hive"]');
    const modSel = document.getElementById("res-model-select") as HTMLSelectElement | null;
    const { provider: prov, model: mod } = parseResidentPickerSelection(provSel?.value || "", modSel?.value || "");
    const ws = (document.getElementById("res-workstream-input") as HTMLInputElement | null)?.value || "geral";
    try {
      let st = await backend.ResidentSaveConfig(prov, mod, ws);
      const eventsOn = Boolean((document.getElementById("res-event-triggers") as HTMLInputElement | null)?.checked);
      if (backend.ResidentSetEventTriggers) {
        st = await backend.ResidentSetEventTriggers(eventsOn);
      }
      residentState.status = st;
      residentState.workstream = st.workstream || "geral";
      updateResidentStatusChip();
      toast(`Configuração do Agent Hive salva (${st.provider}/${st.model}).`, "success");
      rerenderResidentIfOpen();
    } catch (err: any) {
      toastError("salvar configuração do residente", err);
    }
  });

  document.getElementById("btn-res-clear-chat")?.addEventListener("click", () => {
    residentState.messages = [];
    residentState.hiveLaneId = "";
    residentState.boundSessionId = "";
    rerenderResidentIfOpen();
  });

  document.querySelectorAll<HTMLDetailsElement>("details[data-res-activity-fold]").forEach((det) => {
    det.addEventListener("toggle", () => {
      const gid = det.dataset.resActivityFold || "";
      if (!gid) return;
      if (det.open) residentExpandedGroups.add(gid);
      else residentExpandedGroups.delete(gid);
    });
  });

  const chatInput = document.getElementById("res-chat-input") as HTMLTextAreaElement | null;
  autoResizeTextarea(chatInput);
  chatInput?.addEventListener("input", () => {
    residentState.draftInput = chatInput.value;
    autoResizeTextarea(chatInput);
  });

  const sendTurn = async () => {
    const backend = api();
    const text = residentState.draftInput.trim();
    if (!backend?.ResidentStartTurn || !text || residentState.running) return;
    const target = hiveSendTarget(residentState.hiveLaneId, state.lanes);
    const lane = target.create ? startHiveLane() : state.lanes.find((l) => l.id === residentState.hiveLaneId);
    if (target.create && lane) {
      await backend.ResidentBindSession?.(state.project?.root || "", "", lane.id);
    }
    residentState.draftInput = "";
    residentState.running = true;
    const optimisticId = `res-u-${Date.now()}`;
    residentState.messages.push({
      id: optimisticId,
      kind: "user",
      label: "Você",
      text,
    });
    updateResidentStatusChip();
    rerenderResidentIfOpen();
    try {
      const sessionId = target.sessionId;
      const started = backend.ResidentStartTurnIn
        ? await backend.ResidentStartTurnIn(state.project?.root || "", residentState.workstream, text, sessionId)
        : await backend.ResidentStartTurn(state.project?.root || "", residentState.workstream, text);
      if (started?.userMessageId) {
        const mine = residentState.messages.find((m) => m.id === optimisticId);
        if (mine) mine.id = started.userMessageId;
      }
      if (started?.sharedSessionId && lane) {
        if (!lane.sessionId) {
          lane.sessionId = started.sharedSessionId;
          lane.conversationKind = lane.conversationKind || "hive";
        }
        residentState.boundSessionId = lane.sessionId;
        residentState.boundLaneId = lane.id;
        showLaneInIDE(lane.id);
      }
      setStatus("Agent Hive respondeu.");
    } catch (err: any) {
      residentState.running = false;
      toastError("executar turno do agente residente", err);
    } finally {
      residentState.running = false;
      updateResidentStatusChip();
      updateStatusBar();
      void refreshResidentData();
    }
  };

  chatInput?.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void sendTurn();
    }
  });
  document.querySelectorAll<HTMLElement>("[data-res-project]").forEach((el) => {
    el.addEventListener("click", () => {
      const id = el.dataset.resProject || "";
      if (!id || id === state.activeProjectId) return;
      switchProjectTab(id);
      residentState.hiveLaneId = "";
      residentState.messages = [];
      rerenderResidentIfOpen();
    });
  });
  document.querySelectorAll<HTMLElement>("[data-res-lane]").forEach((el) => {
    el.addEventListener("click", () => {
      const id = el.dataset.resLane || "";
      const lane = state.lanes.find((l) => l.id === id);
      if (!lane) return;
      residentState.hiveLaneId = id;
      residentState.boundLaneId = id;
      residentState.boundSessionId = lane.sessionId || "";
      residentState.messages = hiveMessagesFromLane(id);
      showLaneInIDE(id);
      void api()?.ResidentBindSession?.(state.project?.root || "", lane.sessionId || "", lane.id);
      rerenderResidentIfOpen();
    });
  });
  document.getElementById("btn-res-new-lane")?.addEventListener("click", () => {
    const lane = startHiveLane();
    void api()?.ResidentBindSession?.(state.project?.root || "", "", lane.id);
    rerenderResidentIfOpen();
  });
  document.getElementById("btn-res-send")?.addEventListener("click", () => {
    void sendTurn();
  });
  document.getElementById("btn-res-cancel")?.addEventListener("click", async () => {
    await api()?.ResidentCancel?.();
    residentState.running = false;
    updateResidentStatusChip();
    rerenderResidentIfOpen();
  });

  document.querySelectorAll<HTMLElement>("[data-res-appr-ok]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.resApprOk || "";
      if (!id) return;
      const target = residentState.messages.find((m) => m.kind === "approval" && m.approvalId === id && !m.resolved);
      if (target) {
        target.resolved = "approved";
        rerenderResidentIfOpen();
      }
      await api()?.RespondApproval(id, true);
    });
  });
  document.querySelectorAll<HTMLElement>("[data-res-appr-no]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.resApprNo || "";
      if (!id) return;
      const target = residentState.messages.find((m) => m.kind === "approval" && m.approvalId === id && !m.resolved);
      if (target) {
        target.resolved = "denied";
        rerenderResidentIfOpen();
      }
      await api()?.RespondApproval(id, false);
    });
  });

  const binInput = document.getElementById("res-policy-binaries") as HTMLInputElement | null;
  binInput?.addEventListener("input", () => {
    residentState.binariesDraft = binInput.value;
    residentState.policyDirty = true;
  });
  const hostInput = document.getElementById("res-policy-hosts") as HTMLInputElement | null;
  hostInput?.addEventListener("input", () => {
    residentState.hostsDraft = hostInput.value;
    residentState.policyDirty = true;
  });
  document.getElementById("btn-res-save-policy")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend?.ResidentSavePolicy) return;
    try {
      const bins = splitCommaList(residentState.binariesDraft);
      const hosts = splitCommaList(residentState.hostsDraft);
      const st = await backend.ResidentSavePolicy(bins, hosts);
      residentState.status = st;
      residentState.policyDirty = false;
      syncPolicyDraftsFromStatus(st);
      toast("Política de segurança salva.", "success");
      rerenderResidentIfOpen();
    } catch (err: any) {
      toastError("salvar política do residente", err);
    }
  });

  const secNameInput = document.getElementById("res-secret-name") as HTMLInputElement | null;
  secNameInput?.addEventListener("input", () => {
    residentState.secretNameDraft = secNameInput.value;
  });
  const secValInput = document.getElementById("res-secret-val") as HTMLInputElement | null;
  secValInput?.addEventListener("input", () => {
    residentState.secretValueDraft = secValInput.value;
  });
  document.getElementById("btn-res-secret-add")?.addEventListener("click", async () => {
    const backend = api();
    const name = residentState.secretNameDraft.trim();
    const val = residentState.secretValueDraft;
    if (!backend?.ResidentSetSecret || !name || !val) return;
    try {
      const st = await backend.ResidentSetSecret(name, val);
      residentState.status = st;
      residentState.secretNameDraft = "";
      residentState.secretValueDraft = "";
      toast(`Segredo '${name}' gravado no cofre do sistema.`, "success");
      rerenderResidentIfOpen();
    } catch (err: any) {
      toastError("gravar segredo no cofre", err);
    }
  });
  document.querySelectorAll<HTMLElement>("[data-res-sec-del]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const name = btn.dataset.resSecDel || "";
      if (!name || !api()?.ResidentDeleteSecret) return;
      try {
        const st = await api()!.ResidentDeleteSecret!(name);
        residentState.status = st;
        toast(`Segredo '${name}' removido do cofre.`, "success");
        rerenderResidentIfOpen();
      } catch (err: any) {
        toastError("remover segredo do cofre", err);
      }
    });
  });

  document.getElementById("btn-res-routine-add")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend?.ResidentSaveRoutine) return;
    const expr = (document.getElementById("res-routine-expr") as HTMLInputElement | null)?.value.trim() || "";
    const timezone = (document.getElementById("res-routine-tz") as HTMLInputElement | null)?.value.trim() || "";
    const text = (document.getElementById("res-routine-text") as HTMLInputElement | null)?.value.trim() || "";
    try {
      await backend.ResidentSaveRoutine(expr, timezone, text, residentState.workstream, []);
      toast("Rotina gravada.", "success");
      await refreshResidentData();
    } catch (err: any) {
      toastError("gravar rotina", err);
    }
  });
  document.querySelectorAll<HTMLElement>("[data-res-routine-del]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.resRoutineDel || "";
      if (!id || !api()?.ResidentDeleteRoutine) return;
      try {
        await api()!.ResidentDeleteRoutine!(id);
        await refreshResidentData();
      } catch (err: any) {
        toastError("apagar rotina", err);
      }
    });
  });
  document.querySelectorAll<HTMLElement>("[data-res-routine-toggle]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.resRoutineToggle || "";
      const enabled = btn.dataset.enabled !== "1";
      if (!id || !api()?.ResidentSetRoutineEnabled) return;
      try {
        await api()!.ResidentSetRoutineEnabled!(id, enabled);
        await refreshResidentData();
      } catch (err: any) {
        toastError("atualizar rotina", err);
      }
    });
  });
  document.getElementById("btn-res-skill-review")?.addEventListener("click", async () => {
    const backend = api();
    const name = (document.getElementById("res-skill-name") as HTMLInputElement | null)?.value.trim() || "";
    if (!backend?.ResidentReviewSkill || !name) return;
    try {
      await backend.ResidentReviewSkill(name);
      toast(`Skill ${name} revisada.`, "success");
    } catch (err: any) {
      toastError("revisar skill", err);
    }
  });

  const memInput = document.getElementById("res-mem-input") as HTMLInputElement | null;
  memInput?.addEventListener("input", () => {
    residentState.memoryDraft = memInput.value;
  });
  const memPin = document.getElementById("res-mem-pin") as HTMLInputElement | null;
  memPin?.addEventListener("change", () => {
    residentState.memoryPinned = Boolean(memPin.checked);
  });
  document.getElementById("btn-res-mem-add")?.addEventListener("click", async () => {
    const backend = api();
    const text = residentState.memoryDraft.trim();
    if (!backend?.ResidentAddMemory || !text) return;
    try {
      await backend.ResidentAddMemory(residentState.workstream, text, residentState.memoryPinned);
      residentState.memoryDraft = "";
      residentState.memoryPinned = false;
      await refreshResidentData();
      toast("Fato salvo na memória pessoal local.", "success");
    } catch (err: any) {
      toastError("salvar fato de memória", err);
    }
  });
  document.querySelectorAll<HTMLElement>("[data-res-mem-del]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.resMemDel || "";
      if (!id) return;
      await api()?.ResidentDeleteMemory?.(id);
      await refreshResidentData();
    });
  });

  const auditFilter = document.getElementById("res-audit-filter") as HTMLInputElement | null;
  auditFilter?.addEventListener("input", () => {
    residentState.auditFilter = auditFilter.value;
    rerenderResidentIfOpen();
  });

  const soulInput = document.getElementById("res-soul-input") as HTMLTextAreaElement | null;
  soulInput?.addEventListener("input", () => {
    residentState.soulDraft = soulInput.value;
    residentState.soulDirty = true;
  });
  document.getElementById("btn-res-soul-save")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend?.ResidentSaveSoul) return;
    try {
      await backend.ResidentSaveSoul(residentState.soulDraft);
      residentState.soulDirty = false;
      toast("SOUL.md atualizado e injetado no agente residente.", "success");
    } catch (err: any) {
      toastError("salvar SOUL.md", err);
    }
  });

  const scrollBox = document.getElementById("res-chat-scroll");
  if (scrollBox) {
    scrollBox.scrollTop = scrollBox.scrollHeight;
  }
}
