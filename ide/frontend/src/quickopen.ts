import { fileBadge, icons } from "./icons";
import type { CommandAction } from "./types";
import { escapeHtml } from "./utils";
import { openFolderDialog, refreshSessions } from "./boot";
import { renderComposerModeBar } from "./chat";
import { openEditorFind, openFileInEditor, renderEditorBody } from "./editor";
import { setProjectPermissionMode } from "./explorer";
import { openInlineEdit, toggleGhostText } from "./inline-ai";
import { openDoctorModal, openGitHubModal, openJiraModal, openOpenCodeModal } from "./modals";
import { openSidebarView, updateStatusBar } from "./shell";
import { api, state } from "./state";
import { openMissionControlTab, openTeamConfigTab, openTeamMemoryFile } from "./teamconfig";
import { renderTerminalDrawer, runTerminalCommand, toggleTerminal } from "./terminal";

export let quickOpenTimer = 0;

export function getCommandActions(): CommandAction[] {
  return [
    { id: "mission", label: "Abrir Quadro de Controle Multiagente", shortcut: "Ctrl+Shift+A", run: () => openMissionControlTab() },
    { id: "mode-auto", label: "Modo de Execução: Auto (Triagem Inteligente)", run: () => { state.runMode = "auto"; renderComposerModeBar(); updateStatusBar(); } },
    { id: "mode-direct", label: "Modo de Execução: Direto (1 Agente · Rápido)", run: () => { state.runMode = "direct"; renderComposerModeBar(); updateStatusBar(); } },
    { id: "mode-plan", label: "Modo de Execução: Planejar · Feature / Task Grande (Aprovação Humana)", run: () => { state.runMode = "plan"; renderComposerModeBar(); updateStatusBar(); } },
    { id: "mode-plan-sdd", label: "Modo de Execução: Planejar · Projeto do Zero (SDD com Gates)", run: () => { state.runMode = "plan:sdd"; renderComposerModeBar(); updateStatusBar(); } },
    { id: "mode-squad", label: "Modo de Execução: Squad Completo (Paralelo + QA)", run: () => { state.runMode = "squad"; renderComposerModeBar(); updateStatusBar(); } },
    { id: "perm-governed", label: "Permissão: Governado (Worktree + PR + Aprovação p/ main)", run: () => setProjectPermissionMode("governed") },
    { id: "perm-auto", label: "Permissão: Auto-Edição (Edições e comandos locais livres)", run: () => setProjectPermissionMode("auto_edit") },
    { id: "perm-full", label: "Permissão: Full Acesso & Permissão Total (estilo Claude Code / GPT)", run: () => setProjectPermissionMode("full_access") },
    { id: "memory", label: "Abrir Memória Compartilhada (.hivemind/MEMORY.md)", run: () => openTeamMemoryFile() },
    { id: "sessions", label: "Ver Sessões Salvas & Consumo de Tokens", run: () => { openSidebarView("sessions"); refreshSessions(); } },
    { id: "find-file", label: "Localizar e Substituir no Arquivo Atual", shortcut: "Ctrl+F / Ctrl+H", run: () => openEditorFind(true) },
    { id: "inline-edit", label: "Editar Seleção Inline com IA (Ctrl+K)", shortcut: "Ctrl+K", run: () => openInlineEdit() },
    { id: "toggle-ghost", label: `Ghost Text Inline (${state.ghostTextEnabled ? "Desativar" : "Ativar"})`, shortcut: "Tab", run: () => toggleGhostText() },
    { id: "diff-toggle", label: `Alternar Modo de Diff (${state.diffViewMode === "split" ? "Lado a Lado → Unificado" : "Unificado → Lado a Lado"})`, run: () => { state.diffViewMode = state.diffViewMode === "split" ? "unified" : "split"; renderEditorBody(); } },
    { id: "search", label: "Busca Global no Código", shortcut: "Ctrl+Shift+F", run: () => { openSidebarView("search"); (document.getElementById("global-search-input") as HTMLInputElement | null)?.focus(); } },
    { id: "test", label: `Rodar Testes da Stack (${state.project?.stack?.testHint || "git status"})`, run: () => { if (!state.terminal.open) { state.terminal.open = true; renderTerminalDrawer(); } runTerminalCommand(state.project?.stack?.testHint || "git status"); } },
    { id: "term", label: "Alternar Terminal Integrado", shortcut: "Ctrl+J", run: () => toggleTerminal() },
    { id: "wizard", label: "Configurar Equipe e Skills (.hivemind/team.yaml)", run: () => void openTeamConfigTab() },
    { id: "opencode-token", label: "Configurar Token OpenCode Zen (OPENCODE_API_KEY)", run: () => openOpenCodeModal() },
    { id: "github-connector", label: "Conector GitHub Nativo (Token / API REST)", run: () => openGitHubModal() },
    { id: "jira-connector", label: "Conector Jira Nativo (URL / E-mail / API Token)", run: () => openJiraModal() },
    { id: "doctor", label: "Doctor — Verificar Antigravity CLI (agy)", run: () => openDoctorModal() },
    { id: "open-folder-tab", label: "Abrir Outro Projeto em Nova Aba Isolada…", shortcut: "Ctrl+Shift+O", run: () => openFolderDialog(true) },
    { id: "open-folder", label: "Substituir Projeto na Aba Atual…", run: () => openFolderDialog(false) },
  ];
}

export function openQuickOpen() {
  state.quickOpen.open = true;
  state.quickOpen.mode = "files";
  state.quickOpen.query = "";
  state.quickOpen.selected = 0;
  renderQuickOpenModal();
  if (state.project) {
    api()?.SearchFiles(state.project.root, "").then((res) => {
      state.quickOpen.results = res || [];
      renderQuickOpenList();
    });
  }
}

export function openCommandPalette() {
  state.quickOpen.open = true;
  state.quickOpen.mode = "commands";
  state.quickOpen.query = "";
  state.quickOpen.selected = 0;
  renderQuickOpenModal();
}

export function closeQuickOpen() {
  state.quickOpen.open = false;
  const layer = document.getElementById("quick-open-layer");
  if (layer) layer.innerHTML = "";
}

export function filteredCommands(): CommandAction[] {
  const q = state.quickOpen.query.trim().toLowerCase();
  const all = getCommandActions();
  if (!q) return all;
  return all.filter((c) => c.label.toLowerCase().includes(q));
}

export function renderQuickOpenModal() {
  const layer = document.getElementById("quick-open-layer");
  if (!layer) return;
  if (!state.quickOpen.open) {
    layer.innerHTML = "";
    return;
  }
  const isCmd = state.quickOpen.mode === "commands";
  layer.innerHTML = `
    <div class="quick-open-backdrop" id="qo-backdrop">
      <div class="quick-open-modal" role="dialog" aria-modal="true" aria-label="${isCmd ? "Paleta de Comandos" : "Busca Rápida de Arquivos"}">
        <input id="qo-input" placeholder="${isCmd ? "Digite um comando (Ctrl+K)…" : "Buscar arquivo no projeto (Ctrl+P) ou comece com > para comandos…"}" value="${escapeHtml(state.quickOpen.query)}" />
        <div class="quick-open-list" id="qo-list"></div>
      </div>
    </div>
  `;
  const input = document.getElementById("qo-input") as HTMLInputElement | null;
  input?.focus();
  document.getElementById("qo-backdrop")?.addEventListener("click", (e) => {
    if ((e.target as HTMLElement).id === "qo-backdrop") closeQuickOpen();
  });
  input?.addEventListener("input", () => {
    const val = input.value;
    if (!isCmd && val.startsWith(">")) {
      state.quickOpen.mode = "commands";
      state.quickOpen.query = val.slice(1);
      state.quickOpen.selected = 0;
      renderQuickOpenModal();
      return;
    }
    state.quickOpen.query = val;
    state.quickOpen.selected = 0;
    if (state.quickOpen.mode === "commands") {
      renderQuickOpenList();
      return;
    }
    if (quickOpenTimer) window.clearTimeout(quickOpenTimer);
    quickOpenTimer = window.setTimeout(async () => {
      if (!state.project) return;
      const res = await api()?.SearchFiles(state.project.root, state.quickOpen.query);
      state.quickOpen.results = res || [];
      renderQuickOpenList();
    }, 45);
  });
  input?.addEventListener("keydown", (e) => {
    const items = state.quickOpen.mode === "commands" ? filteredCommands() : state.quickOpen.results;
    const total = items.length;
    if (e.key === "ArrowDown" && total > 0) {
      e.preventDefault();
      state.quickOpen.selected = (state.quickOpen.selected + 1) % total;
      renderQuickOpenList();
    } else if (e.key === "ArrowUp" && total > 0) {
      e.preventDefault();
      state.quickOpen.selected = (state.quickOpen.selected - 1 + total) % total;
      renderQuickOpenList();
    } else if (e.key === "Enter" && total > 0) {
      e.preventDefault();
      if (state.quickOpen.mode === "commands") {
        const cmd = filteredCommands()[state.quickOpen.selected];
        closeQuickOpen();
        cmd?.run();
      } else {
        const chosen = state.quickOpen.results[state.quickOpen.selected];
        if (chosen) {
          closeQuickOpen();
          openFileInEditor(chosen, chosen.split("/").pop() || chosen);
        }
      }
    }
  });
  renderQuickOpenList();
}

export function renderQuickOpenList() {
  const list = document.getElementById("qo-list");
  if (!list) return;
  if (state.quickOpen.mode === "commands") {
    const cmds = filteredCommands();
    if (cmds.length === 0) {
      list.innerHTML = `<div class="dim" style="padding:8px">Nenhum comando correspondente</div>`;
      return;
    }
    list.innerHTML = cmds
      .map((c, idx) => `
        <button class="dropdown-item ${idx === state.quickOpen.selected ? "selected" : ""}" data-qo-cmd="${idx}" style="justify-content:space-between">
          <span style="display:inline-flex;align-items:center;gap:8px">${icons.command}<span>${escapeHtml(c.label)}</span></span>
          ${c.shortcut ? `<span class="kbd">${escapeHtml(c.shortcut)}</span>` : ""}
        </button>
      `)
      .join("");
    list.querySelectorAll("[data-qo-cmd]").forEach((el) => {
      el.addEventListener("click", () => {
        const idx = parseInt((el as HTMLElement).dataset.qoCmd || "0", 10);
        const cmd = cmds[idx];
        closeQuickOpen();
        cmd?.run();
      });
    });
    return;
  }

  if (state.quickOpen.results.length === 0) {
    list.innerHTML = `<div class="dim" style="padding:8px">Nenhum arquivo encontrado</div>`;
    return;
  }
  list.innerHTML = state.quickOpen.results
    .map((p, idx) => {
      const name = p.split("/").pop() || p;
      return `
        <button class="dropdown-item ${idx === state.quickOpen.selected ? "selected" : ""}" data-qo-path="${escapeHtml(p)}">
          ${fileBadge(name)}
          <span>${escapeHtml(p)}</span>
        </button>
      `;
    })
    .join("");
  list.querySelectorAll("[data-qo-path]").forEach((el) => {
    el.addEventListener("click", () => {
      const p = (el as HTMLElement).dataset.qoPath!;
      closeQuickOpen();
      openFileInEditor(p, p.split("/").pop() || p);
    });
  });
}
