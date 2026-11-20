import { icons } from "./icons";
import { renderMarkdownLite } from "./markdown";
import type { ChatMessage, ResidentAuditEntry, ResidentMemoryFact, ResidentStatus } from "./types";
import { escapeHtml } from "./utils";
import { renderEditorBody, renderEditorTabs } from "./editor";
import { updateStatusBar } from "./shell";
import { api, setStatus, state, toast, toastError } from "./state";

export const RESIDENT_TAB_KEY = "hive:resident";

export type ResidentSubTab = "chat" | "memory" | "audit";

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
  auditFilter: "",
  loadedOnce: false,
};

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
}

export async function refreshResidentStatus(): Promise<void> {
  const backend = api();
  if (!backend?.ResidentStatus) return;
  try {
    const st = await backend.ResidentStatus();
    residentState.status = st;
    if (!residentState.loadedOnce && st.workstream) {
      residentState.workstream = st.workstream;
    }
    updateResidentStatusChip();
    rerenderResidentIfOpen();
  } catch {
    // ignore startup race
  }
}

export async function refreshResidentData(): Promise<void> {
  const backend = api();
  if (!backend?.ResidentStatus) return;
  try {
    const st = await backend.ResidentStatus();
    residentState.status = st;
    if (!residentState.loadedOnce && st.workstream) {
      residentState.workstream = st.workstream;
    }
    const ws = residentState.workstream || st.workstream || "geral";
    const [sessionMsgs, memList, auditList] = await Promise.all([
      backend.ResidentSessionMessages?.(ws).catch(() => []) ?? Promise.resolve([]),
      backend.ResidentListMemory?.(ws).catch(() => []) ?? Promise.resolve([]),
      backend.ResidentAuditLog?.(80).catch(() => []) ?? Promise.resolve([]),
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
  chip.title = st
    ? `Hive Residente (Fase 1 · Somente Leitura) · Conta: ${st.accountId} · Modelo: ${st.provider}/${st.model}`
    : "Hive Residente (Fase 1 · Somente Leitura) — clique para abrir";
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
    residentState.messages.push({
      id: `res-tool-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
      kind: "tool",
      label: "Leitura",
      text: String(ev.toolName || "read"),
      toolName: String(ev.toolName || ""),
      toolDetail: String(ev.detail || ""),
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
    residentState.messages.push({
      id: `res-appr-${Date.now()}`,
      kind: "approval",
      label: "Aprovação do Hive Residente",
      text: String(ev.text || ""),
      approvalId: String(ev.approvalId || ""),
    });
  } else if (ev.kind === "approval_resolved") {
    const target = residentState.messages.find((m) => m.kind === "approval" && m.approvalId === ev.approvalId && !m.resolved);
    if (target) {
      target.resolved = /recusada/.test(String(ev.text || "")) ? "denied" : "approved";
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

  return `
    <div class="resident-page">
      <header class="resident-header">
        <div class="resident-title-block">
          <div class="resident-title-row">
            ${icons.sparkle}
            <h2>Hive Residente</h2>
            <span class="resident-badge readonly">Fase 1 · Somente Leitura</span>
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
          <button type="button" class="mini" id="btn-res-save-cfg">Salvar</button>
        </div>
      </header>

      <div class="resident-subtabs">
        <button type="button" class="drawer-tab ${residentState.subTab === "chat" ? "active" : ""}" data-res-tab="chat">Conversa</button>
        <button type="button" class="drawer-tab ${residentState.subTab === "memory" ? "active" : ""}" data-res-tab="memory">Memória Pessoal (${residentState.memory.length})</button>
        <button type="button" class="drawer-tab ${residentState.subTab === "audit" ? "active" : ""}" data-res-tab="audit">Auditoria (${residentState.audit.length})</button>
        <div class="spacer"></div>
        ${residentState.subTab === "chat" ? `<button type="button" class="mini ghost" id="btn-res-clear-chat">Limpar conversa</button>` : ""}
        <button type="button" class="mini ghost" id="btn-res-refresh">${icons.refresh}<span>Atualizar</span></button>
      </div>

      ${residentState.subTab === "chat" ? renderResidentChatHtml() : ""}
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
        <div class="chat-empty-title">Hive Residente pronto (somente leitura)</div>
        <div class="chat-empty-sub">Ele pode ler arquivos do workspace, listar projetos e consultar sua memória pessoal local desta conta — sem escrita, shell ou ações destrutivas.</div>
      </div>
    `
    : residentState.messages.map((m) => {
        if (m.kind === "tool") {
          return `
            <div class="msg-card role-tool">
              <span class="msg-badge badge-tool">${escapeHtml(m.toolName || m.text)}</span>
              <span class="dim">${escapeHtml(m.toolDetail || "")}</span>
            </div>
          `;
        }
        if (m.kind === "approval") {
          const decided = m.resolved === "approved"
            ? `<span class="tc-jira-pill ok">Aprovado</span>`
            : m.resolved === "denied"
              ? `<span class="tc-jira-pill off">Recusado</span>`
              : `
                <div style="display:flex;gap:6px;margin-top:8px">
                  <button type="button" class="mini primary" data-res-appr-ok="${escapeHtml(m.approvalId || "")}">Aprovar</button>
                  <button type="button" class="mini danger" data-res-appr-no="${escapeHtml(m.approvalId || "")}">Recusar</button>
                </div>
              `;
          return `
            <div class="msg-card role-plan">
              <span class="msg-badge badge-system">${escapeHtml(m.label)}</span>
              <div class="msg-body">${escapeHtml(m.text)}</div>
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
      <textarea id="res-chat-input" rows="2" placeholder="Pergunte ao Hive Residente sobre o projeto, seus repositórios ou sua memória pessoal… (Enter envia)">${escapeHtml(residentState.draftInput)}</textarea>
      <div class="resident-composer-actions">
        <span class="dim">Leitura auditada em disco antes de executar · sem escrita/shell na Fase 1</span>
        <div class="spacer"></div>
        <button type="button" class="danger mini" id="btn-res-cancel" ${residentState.running ? "" : "disabled"}>${icons.stop}<span>Parar</span></button>
        <button type="button" class="primary" id="btn-res-send" ${residentState.running ? "disabled" : ""}>${icons.send}<span>Enviar</span></button>
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
    return `${e.tool} ${e.origin} ${e.decision} ${e.outcome || ""} ${e.target || ""} ${e.reason || ""}`.toLowerCase().includes(q);
  });
  return `
    <div class="resident-panel-body">
      <div class="resident-mem-form">
        <input id="res-audit-filter" placeholder="Filtrar auditoria por ferramenta, origem, decisão ou alvo…" value="${escapeHtml(residentState.auditFilter)}" />
      </div>
      <div class="resident-list">
        ${filtered.length === 0
          ? `<div class="dim" style="padding:12px">Nenhum registro de auditoria ainda.</div>`
          : filtered.map((e) => `
            <div class="resident-row-card">
              <div class="resident-row-head">
                <code>${escapeHtml(e.tool)}</code>
                <span class="tok-pill">${escapeHtml(e.phase)}</span>
                <span class="tok-pill">${escapeHtml(e.origin)}</span>
                <span class="tok-pill">${escapeHtml(e.tier)}</span>
                <span class="tc-jira-pill ${e.decision === "allow" ? "ok" : "off"}">${escapeHtml(e.decision)}${e.outcome ? ` · ${escapeHtml(e.outcome)}` : ""}</span>
                <span class="spacer"></span>
                <span class="dim" style="font-family:var(--mono);font-size:10.5px">${escapeHtml(e.ts)}</span>
              </div>
              ${e.target ? `<div class="dim">Alvo: <code>${escapeHtml(e.target)}</code></div>` : ""}
              ${e.reason ? `<div class="dim">${escapeHtml(e.reason)}</div>` : ""}
            </div>
          `).join("")
        }
      </div>
    </div>
  `;
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
      const st = await backend.ResidentSaveConfig(prov === "auto" ? "" : prov, mod, ws);
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
