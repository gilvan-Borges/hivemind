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
        <div class="qo-search-head">
          <span class="qo-search-icon">${isCmd ? icons.command : icons.search}</span>
          <input id="qo-input" class="qo-search-input" placeholder="${isCmd ? "Digite um comando…" : "Buscar arquivo no projeto ou digite > para comandos…"}" value="${escapeHtml(state.quickOpen.query)}" />
          <span class="qo-mode-pill">${isCmd ? "Comandos" : "Arquivos"}</span>
        </div>
        <div class="quick-open-list" id="qo-list"></div>
        <div class="qo-footer">
          <span><span class="kbd">↑↓</span> navegar</span>
          <span><span class="kbd">Enter</span> ${isCmd ? "executar" : "abrir"}</span>
          ${!isCmd ? `<span><span class="kbd">&gt;</span> comandos</span>` : ""}
          <span><span class="kbd">Esc</span> fechar</span>
          <span class="spacer"></span>
          <span id="qo-count" class="qo-count"></span>
        </div>
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
  const countEl = document.getElementById("qo-count");
  if (state.quickOpen.mode === "commands") {
    const cmds = filteredCommands();
    if (countEl) countEl.textContent = `${cmds.length} comando(s)`;
    if (cmds.length === 0) {
      list.innerHTML = `<div class="qo-empty">Nenhum comando encontrado para essa busca.</div>`;
      return;
    }
    list.innerHTML = cmds
      .map((c, idx) => `
        <button type="button" class="qo-item ${idx === state.quickOpen.selected ? "selected" : ""}" data-qo-cmd="${idx}">
          <span class="qo-item-main">
            <span class="qo-cmd-icon">${icons.command}</span>
            <span class="qo-file-name">${escapeHtml(c.label)}</span>
          </span>
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
    list.querySelector<HTMLElement>(".qo-item.selected")?.scrollIntoView({ block: "nearest" });
    return;
  }

  const results = state.quickOpen.results;
  if (countEl) countEl.textContent = `${results.length} arquivo(s)`;
  if (results.length === 0) {
    list.innerHTML = `<div class="qo-empty">Nenhum arquivo encontrado.</div>`;
    return;
  }
  list.innerHTML = results
    .map((p, idx) => {
      const normalized = p.replace(/\\/g, "/");
      const slash = normalized.lastIndexOf("/");
      const name = slash >= 0 ? normalized.slice(slash + 1) : normalized;
      const dir = slash >= 0 ? normalized.slice(0, slash) : "";
      return `
        <button type="button" class="qo-item ${idx === state.quickOpen.selected ? "selected" : ""}" data-qo-path="${escapeHtml(p)}" title="${escapeHtml(p)}">
          <span class="qo-item-main">
            ${fileBadge(name)}
            <span class="qo-file-name">${escapeHtml(name)}</span>
            ${dir ? `<span class="qo-file-dir">${escapeHtml(dir)}</span>` : ""}
          </span>
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
  list.querySelector<HTMLElement>(".qo-item.selected")?.scrollIntoView({ block: "nearest" });
}
