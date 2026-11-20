import { icons } from "./icons";
import { renderMarkdownLite } from "./markdown";
import type { ChatMessage, ExternalSession, ResidentAuditEntry, ResidentMemoryFact, ResidentRoutine, ResidentStatus } from "./types";
import { escapeHtml } from "./utils";
import { renderEditorBody, renderEditorTabs } from "./editor";
import { updateStatusBar } from "./shell";
import { api, setStatus, state, toast, toastError } from "./state";

export const RESIDENT_TAB_KEY = "hive:resident";

export type ResidentSubTab = "chat" | "memory" | "security" | "audit";

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
  externalSessions: [] as ExternalSession[],
  auditFilter: "",
  binariesDraft: "",
  hostsDraft: "",
  secretNameDraft: "",
  secretValueDraft: "",
  policyDirty: false,
  loadedOnce: false,
};

function syncPolicyDraftsFromStatus(st: ResidentStatus | null): void {
  if (!st || residentState.policyDirty) return;
  residentState.binariesDraft = Array.isArray(st.allowedBinaries) ? st.allowedBinaries.join(", ") : "";
  residentState.hostsDraft = Array.isArray(st.allowedHosts) ? st.allowedHosts.join(", ") : "";
}

function formatRiskBadge(tierRaw?: string): { tier: string; label: string; css: string } {
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
      name: "Hive Residente",
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
    const [sessionMsgs, memList, auditList, routines] = await Promise.all([
      backend.ResidentSessionMessages?.(ws).catch(() => []) ?? Promise.resolve([]),
      backend.ResidentListMemory?.(ws).catch(() => []) ?? Promise.resolve([]),
      backend.ResidentAuditLog?.(80).catch(() => []) ?? Promise.resolve([]),
      backend.ResidentListRoutines?.().catch(() => []) ?? Promise.resolve([]),
    ]);
    if (!residentState.loadedOnce && residentState.messages.length === 0 && Array.isArray(sessionMsgs) && sessionMsgs.length > 0) {
      residentState.messages = sessionMsgs.map((m, idx) => ({
        id: `res-hist-${idx}`,
        kind: m.role === "user" ? "user" : "chief",
        label: m.role === "user" ? "Você" : "Hive Residente",
        text: m.text,
      }));
    }
    residentState.memory = Array.isArray(memList) ? memList : [];
    residentState.audit = Array.isArray(auditList) ? auditList : [];
    residentState.routines = Array.isArray(routines) ? routines : [];
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
  const label = residentState.running ? "pensando" : (st?.state || "ocioso");
  chip.textContent = `hive: ${label}`;
  if (label === "pensando") {
    chip.style.color = "var(--accent-strong)";
  } else if (label === "aguardando você") {
    chip.style.color = "var(--amber)";
  } else {
    chip.style.color = "var(--green)";
  }
  const sbInfo = st?.sandboxAvailable
    ? `Sandbox: ${st.sandboxMechanism || "ativa"}`
    : "Sandbox: indisponível (falha fechada)";
  chip.title = st
    ? `Hive Residente (Fase ${st.phase || 2}) · ${sbInfo} · Conta: ${st.accountId} · Modelo: ${st.provider}/${st.model}`
    : "Hive Residente (Fase 2 · Sandbox e Cofre) — clique para abrir";
}

function rerenderResidentIfOpen(): void {
  if (state.activeEditorPath === RESIDENT_TAB_KEY) {
    renderEditorBody();
  }
}

export function handleResidentLaneEvent(payload: any): void {
  const ev = payload?.event;
  if (!ev) return;

  if (ev.kind === "stage") {
    residentState.running = true;
    if (residentState.status) residentState.status.state = ev.text || "pensando";
  } else if (ev.kind === "user_message" && ev.text) {
    residentState.messages.push({
      id: `res-u-${Date.now()}`,
      kind: "user",
      label: "Remoto",
      text: String(ev.text),
    });
  } else if (ev.kind === "tool_call") {
    const risk = formatRiskBadge(String(ev.gate || ev.riskTier || ""));
    residentState.messages.push({
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
    for (let i = residentState.messages.length - 1; i >= 0; i--) {
      const m = residentState.messages[i];
      if (m.kind === "tool" && m.active) {
        m.active = false;
        break;
      }
    }
  } else if (ev.kind === "approval_required") {
    if (residentState.status) residentState.status.state = "aguardando você";
    const tierRaw = String(ev.gate || ev.riskTier || "");
    residentState.messages.push({
      id: `res-appr-${Date.now()}`,
      kind: "approval",
      label: String(ev.label || "Aprovação do Hive Residente"),
      text: String(ev.text || ""),
      toolName: String(ev.toolName || ""),
      toolDetail: String(ev.detail || ""),
      riskTier: tierRaw,
      approvalId: String(ev.approvalId || ""),
    });
  } else if (ev.kind === "approval_resolved") {
    const target = residentState.messages.find((m) => m.kind === "approval" && m.approvalId === ev.approvalId && !m.resolved);
    if (target) {
      target.resolved = /recusada|negada/i.test(String(ev.text || "")) ? "denied" : "approved";
    }
  } else if (ev.kind === "text" && !ev.thought && ev.text) {
    const last = residentState.messages[residentState.messages.length - 1];
    if (last && last.kind === "chief" && ev.partial) {
      last.text += String(ev.text);
    } else if (last && last.kind === "chief" && !ev.partial) {
      last.text = String(ev.text);
    } else {
      residentState.messages.push({
        id: `res-ai-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
        kind: "chief",
        label: "Hive Residente",
        text: String(ev.text),
      });
    }
  } else if (ev.kind === "error") {
    residentState.running = false;
    if (residentState.status) residentState.status.state = "ocioso";
    residentState.messages.push({
      id: `res-err-${Date.now()}`,
      kind: "error",
      label: "Erro",
      text: String(ev.text || "Erro no agente residente"),
    });
  } else if (ev.kind === "finish") {
    residentState.running = false;
    if (residentState.status) residentState.status.state = "ocioso";
    void refreshResidentData();
  }

  updateResidentStatusChip();
  rerenderResidentIfOpen();
}

export function renderResidentViewHtml(): string {
  const st = residentState.status;
  const acct = st?.accountId || "local";
  const prov = st?.provider || "openrouter";
  const mod = st?.model || "openrouter/auto";
  const stateLabel = residentState.running ? "pensando" : (st?.state || "ocioso");
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
            ${icons.sparkle}
            <h2>Hive Residente</h2>
            <span class="resident-badge readonly">Fase 2 · Sandbox Obrigatória</span>
            <span class="resident-badge ${sbOk ? "risk-read" : "risk-destructive"}" title="${escapeHtml(st?.sandboxReason || sbText)}">${escapeHtml(sbText)}</span>
            <span class="resident-badge ${vaultOk ? "risk-read" : "risk-write"}" title="${escapeHtml(st?.vaultReason || "Cofre do SO")}">${vaultOk ? "Cofre SO: ativo" : "Cofre SO: indisponível"}</span>
            <span class="resident-badge state-${stateLabel === "pensando" ? "running" : stateLabel === "aguardando você" ? "ask" : "idle"}">${escapeHtml(stateLabel)}</span>
          </div>
          <div class="dim">Conta local: <code>${escapeHtml(acct)}</code> · Workspace: <code>${escapeHtml(activeWsName)}</code> · Rota: <code>${escapeHtml(prov)}/${escapeHtml(mod)}</code></div>
        </div>
        <div class="resident-header-controls">
          <input id="res-workstream-input" class="res-compact-input" style="width:115px" value="${escapeHtml(residentState.workstream)}" placeholder="workstream" title="Workstream ativo para memória e sessão" />
          <select id="res-provider-select" class="res-compact-input" title="Provedor HTTP (internal/modelx)">
            ${["auto", "openrouter", "opencode", "gemini", "openai", "anthropic", "cursor"].map((p) =>
              `<option value="${p}" ${prov === p ? "selected" : ""}>${p}</option>`
            ).join("")}
          </select>
          <input id="res-model-input" class="res-compact-input" style="width:180px" value="${escapeHtml(mod)}" placeholder="auto ou slug do modelo" title="Modelo do residente (padrão barato/auto)" />
          <label class="dim" title="Acordar no arquivo salvo, no fim da lane ou numa aprovação. Não executa ferramenta."><input type="checkbox" id="res-event-triggers" ${st?.eventTriggers ? "checked" : ""}/> eventos</label>
          <button type="button" class="mini" id="btn-res-save-cfg">Salvar</button>
        </div>
      </header>

      <div class="resident-subtabs">
        <button type="button" class="drawer-tab ${residentState.subTab === "chat" ? "active" : ""}" data-res-tab="chat">Conversa${pendingCount > 0 ? ` (${pendingCount} aprovação)` : ""}</button>
        <button type="button" class="drawer-tab ${residentState.subTab === "security" ? "active" : ""}" data-res-tab="security">Segurança & Cofre</button>
        <button type="button" class="drawer-tab ${residentState.subTab === "memory" ? "active" : ""}" data-res-tab="memory">Memória Pessoal (${residentState.memory.length})</button>
        <button type="button" class="drawer-tab ${residentState.subTab === "audit" ? "active" : ""}" data-res-tab="audit">Auditoria (${residentState.audit.length})</button>
        <div class="spacer"></div>
        ${residentState.subTab === "chat" ? `<button type="button" class="mini ghost" id="btn-res-clear-chat">Limpar conversa</button>` : ""}
        <button type="button" class="mini ghost" id="btn-res-refresh">${icons.refresh}<span>Atualizar</span></button>
      </div>

      ${residentState.subTab === "chat" ? renderResidentChatHtml() : ""}
      ${residentState.subTab === "security" ? renderResidentSecurityHtml() : ""}
      ${residentState.subTab === "memory" ? renderResidentMemoryHtml(acct) : ""}
      ${residentState.subTab === "audit" ? renderResidentAuditHtml() : ""}
    </div>
  `;
}

function renderResidentChatHtml(): string {
  const msgsHtml = residentState.messages.length === 0
    ? `
      <div class="chat-empty-state">
        <div class="chat-empty-icon">${icons.brandHero}</div>
        <div class="chat-empty-title">Hive Residente pronto (Fase 2 · Sandbox do SO)</div>
        <div class="chat-empty-sub">Leitura local no workspace é direta e auditada. Escrita de arquivos, execução de binários na allowlist (sempre em sandbox com argv explícito, sem sh -c) e rede na allowlist exigem sua aprovação no cartão abaixo.</div>
      </div>
    `
    : residentState.messages.map((m) => {
        if (m.kind === "tool") {
          const risk = formatRiskBadge(m.riskTier);
          return `
            <div class="msg-card role-tool">
              <span class="msg-badge badge-tool">${escapeHtml(m.toolName || m.text)}</span>
              <span class="resident-badge ${risk.css}">${escapeHtml(risk.label)}</span>
              <span class="dim">${escapeHtml(m.toolDetail || "")}</span>
            </div>
          `;
        }
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
            <div class="msg-card role-plan resident-approval-card">
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
        const body = m.kind === "chief" ? renderMarkdownLite(m.text) : escapeHtml(m.text);
        return `
          <div class="msg-card role-${m.kind}">
            <span class="msg-badge badge-${m.kind}">${escapeHtml(m.label)}</span>
            <div class="msg-body">${body}</div>
          </div>
        `;
      }).join("");

  return `
    <div class="resident-chat-body" id="res-chat-scroll">${msgsHtml}</div>
    <div class="resident-composer">
      <textarea id="res-chat-input" rows="2" placeholder="Peça ao Hive Residente para inspecionar, editar arquivos ou rodar um binário autorizado na sandbox… (Enter envia)">${escapeHtml(residentState.draftInput)}</textarea>
      <div class="resident-composer-actions">
        <span class="dim">Auditoria fsync antes de cada efeito · escrita/execução exigem aprovação + sandbox do SO · saída cercada contra prompt injection</span>
        <div class="spacer"></div>
        <button type="button" class="danger mini" id="btn-res-cancel" ${residentState.running ? "" : "disabled"}>${icons.stop}<span>Parar</span></button>
        <button type="button" class="primary" id="btn-res-send" ${residentState.running ? "disabled" : ""}>${icons.send}<span>Enviar</span></button>
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
            <strong>Browser e desktop isolados</strong>
            <span class="resident-badge risk-destructive">Desligados</span>
          </div>
          <div class="dim">${escapeHtml(st?.isolatedBrowserReason || "Browser isolado recusado: não há espaço com egress filtrado.")}</div>
          <div class="dim">${escapeHtml(st?.isolatedDesktopReason || "Desktop isolado recusado: a sessão real não é alvo.")}</div>
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

  document.getElementById("btn-res-refresh")?.addEventListener("click", () => {
    void refreshResidentData();
  });

  document.getElementById("btn-res-save-cfg")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend?.ResidentSaveConfig) return;
    const prov = (document.getElementById("res-provider-select") as HTMLSelectElement | null)?.value || "auto";
    const mod = (document.getElementById("res-model-input") as HTMLInputElement | null)?.value || "";
    const ws = (document.getElementById("res-workstream-input") as HTMLInputElement | null)?.value || "geral";
    try {
      let st = await backend.ResidentSaveConfig(prov === "auto" ? "" : prov, mod, ws);
      const eventsOn = Boolean((document.getElementById("res-event-triggers") as HTMLInputElement | null)?.checked);
      if (backend.ResidentSetEventTriggers) {
        st = await backend.ResidentSetEventTriggers(eventsOn);
      }
      residentState.status = st;
      residentState.workstream = st.workstream || "geral";
      updateResidentStatusChip();
      toast(`Configuração do Hive Residente salva (${st.provider}/${st.model}).`, "success");
      rerenderResidentIfOpen();
    } catch (err: any) {
      toastError("salvar configuração do residente", err);
    }
  });

  document.getElementById("btn-res-clear-chat")?.addEventListener("click", async () => {
    const backend = api();
    residentState.messages = [];
    if (backend?.ResidentClearSession) {
      await backend.ResidentClearSession(residentState.workstream).catch(() => {});
    }
    rerenderResidentIfOpen();
  });

  const chatInput = document.getElementById("res-chat-input") as HTMLTextAreaElement | null;
  chatInput?.addEventListener("input", () => {
    residentState.draftInput = chatInput.value;
  });

  const sendTurn = async () => {
    const backend = api();
    const text = residentState.draftInput.trim();
    if (!backend?.ResidentStartTurn || !text || residentState.running) return;
    residentState.draftInput = "";
    residentState.running = true;
    residentState.messages.push({
      id: `res-u-${Date.now()}`,
      kind: "user",
      label: "Você",
      text,
    });
    updateResidentStatusChip();
    rerenderResidentIfOpen();
    try {
      await backend.ResidentStartTurn(state.project?.root || "", residentState.workstream, text);
      setStatus("Hive Residente respondeu.");
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
      if (id) await api()?.RespondApproval(id, true);
    });
  });
  document.querySelectorAll<HTMLElement>("[data-res-appr-no]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.resApprNo || "";
      if (id) await api()?.RespondApproval(id, false);
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
      toast("Política de segurança da Fase 2 salva.", "success");
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

  const scrollBox = document.getElementById("res-chat-scroll");
  if (scrollBox) {
    scrollBox.scrollTop = scrollBox.scrollHeight;
  }
}
