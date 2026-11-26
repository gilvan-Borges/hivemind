import { icons } from "./icons";
import { refreshMCP } from "./mcp";
import type { SidebarView } from "./types";
import { escapeHtml, exactTokens, formatTokens, permissionModeLabel, runModeLabel, shouldExpandChat } from "./utils";
import { openFolderDialog, openProjectPath, refreshSessions } from "./boot";
import { renderComposerModeBar, renderMentionPopup, renderRightPane } from "./chat";
import { openEditorFind, reloadCleanOpenTabs, renderEditorBody, renderEditorFindBar, renderEditorTabs, saveActiveEditor, sendEditorSelectionToChief } from "./editor";
import { refreshExplorerTree, renderLeftSidebar } from "./explorer";
import { clearCenterStage, clearGitHubUnread, isCenterGitStageActive, loadGitGraph, loadProjectIssues, refreshDiffs, updateGitActivityBadge } from "./git";
import { closeInlineEdit, ghostButtonTitle, isCodeEditorActive, openInlineEdit, toggleGhostText } from "./inline-ai";
import { activeLane, laneDisplayName } from "./lanes";
import { lowestRemainingQuotaPct, openAIUsagePage, openMobileModal, openUpdatesModal, quotaChipNote, refreshJiraStatusAndProjects, refreshProviderProfiles, renderModalLayer } from "./modals";
import { openIdeSettingsTab } from "./ide-settings";
import { refreshSkills } from "./models";
import { closeQuickOpen, openCommandPalette, openQuickOpen } from "./quickopen";
import { openResidentTab, updateResidentStatusChip } from "./resident";
import { loadSettings, toggleCompanionEnabled } from "./companion/settings";
import { IDE_SETTINGS_TAB_KEY, api, askConfirm, hasAnyProjectDirtyTabs, hasDirtyTabs, loadFromProjectContext, normalizePathKey, saveActiveToProjectContext, setStatus, state, syncGlobalBusyUi, toastError, triggerEnterMotion } from "./state";
import { openMissionControlTab, openTeamConfigTab, openTeamMemoryFile } from "./teamconfig";
import { countProblems, refreshLSPStatus } from "./lsp";
import { openProblemsPanel, renderTerminalDrawer, runTerminalCommand, toggleTerminal } from "./terminal";

export function switchProjectTab(projectId: string) {
  if (projectId === state.activeProjectId) return;
  saveActiveToProjectContext();
  const target = state.projectTabs.find((p) => p.id === projectId);
  if (!target) return;
  loadFromProjectContext(target);
  syncActivityButtons();
  syncLayoutVisibility();
  renderProjectTabsBar();
  renderBranchPopover();
  updateHeaderAndStatusBar();
  renderLeftSidebar();
  renderEditorTabs();
  renderEditorBody();
  renderTerminalDrawer();
  renderRightPane();
  updateGitActivityBadge();
  triggerEnterMotion(document.getElementById("left-sidebar"), "hm-view-enter");
  triggerEnterMotion(document.getElementById("center-area"), "hm-page-enter");
  triggerEnterMotion(document.getElementById("right-pane"), "hm-view-enter");
  setStatus(`Projeto ativo: ${target.project.name} (${target.project.root})`);
  void refreshJiraStatusAndProjects();
  void refreshProviderProfiles();
}

export function closeProjectTab(projectId: string) {
  saveActiveToProjectContext();
  if (state.projectTabs.length <= 1) {
    setStatus("Mantenha ao menos uma aba de projeto aberta (ou abra outro projeto).");
    return;
  }
  const target = state.projectTabs.find((p) => p.id === projectId);
  if (!target) return;

  const hasDirty = target.editorTabs.some((t) => t.kind === "file" && t.dirty);
  const hasRunning = target.lanes.some((l) => l.running);

  const performClose = async () => {
    for (const l of target.lanes) {
      if (l.running) {
        await api()?.CancelRun(l.id);
      }
    }
    await api()?.CloseProject?.(target.project.root);
    const idx = state.projectTabs.findIndex((p) => p.id === projectId);
    state.projectTabs = state.projectTabs.filter((p) => p.id !== projectId);
    if (state.activeProjectId === projectId) {
      const next = state.projectTabs[Math.max(0, idx - 1)] || state.projectTabs[0];
      if (next) {
        loadFromProjectContext(next);
        syncActivityButtons();
        syncLayoutVisibility();
        updateHeaderAndStatusBar();
        renderLeftSidebar();
        renderEditorTabs();
        renderEditorBody();
        renderTerminalDrawer();
        renderRightPane();
      }
    }
    renderProjectTabsBar();
    setStatus(`Aba do projeto "${target.project.name}" fechada.`);
  };

  if (hasDirty || hasRunning) {
    askConfirm({
      title: `Fechar Aba do Projeto "${target.project.name}"`,
      body: hasRunning
        ? `A equipe de "${target.project.name}" ainda está em execução (e/ou há arquivos não salvos). Deseja interromper e fechar esta aba de projeto?`
        : `O projeto "${target.project.name}" possui arquivos editados não salvos. Deseja fechar esta aba de projeto sem salvar?`,
      confirmLabel: "Fechar Aba do Projeto",
      danger: true,
      onConfirm: performClose,
    });
    return;
  }
  performClose();
}

export function renderProjectTabsBar() {
  saveActiveToProjectContext();
  const bar = document.getElementById("project-tabs-bar");
  if (!bar) return;
  bar.innerHTML = `
    ${state.projectTabs.map((ctx) => {
      const isAct = ctx.id === state.activeProjectId;
      const isRunning = ctx.lanes.some((l) => l.running);
      const isDirty = ctx.editorTabs.some((t) => t.kind === "file" && t.dirty);
      const branch = ctx.project.branch || "sem git";
      return `
        <div class="project-tab ${isAct ? "active" : ""}" data-proj-tab="${escapeHtml(ctx.id)}" title="${escapeHtml(ctx.project.name)} · ${escapeHtml(branch)} — ${escapeHtml(ctx.project.root)}">
          ${icons.folderOpen}
          <span>${escapeHtml(ctx.project.name)}</span>
          ${isRunning ? `<span class="proj-run-dot" title="Equipe executando em segundo plano neste projeto"></span>` : ""}
          ${isDirty ? `<span class="proj-dirty-dot" title="Arquivos com edições não salvas">${icons.dot}</span>` : ""}
          ${state.projectTabs.length > 1
            ? `<span class="proj-close-x" data-close-proj="${escapeHtml(ctx.id)}" title="Fechar aba deste projeto">${icons.closeSmall}</span>`
            : ""
          }
        </div>
      `;
    }).join("")}
    <button class="project-tab-add ${state.projectLoading.active ? "is-loading" : ""}" id="btn-add-project-tab" ${state.projectLoading.active ? "disabled" : ""} title="${escapeHtml(state.projectLoading.active ? state.projectLoading.label : "Abrir outro projeto em uma nova aba isolada (Ctrl+Shift+O)")}">
      ${state.projectLoading.active ? icons.spinner : icons.plus}
      <span>${state.projectLoading.active ? escapeHtml(state.projectLoading.label || "Abrindo…") : "Novo Projeto"}</span>
    </button>
  `;

  bar.querySelectorAll("[data-proj-tab]").forEach((el) => {
    el.addEventListener("click", () => {
      switchProjectTab((el as HTMLElement).dataset.projTab!);
    });
  });
  bar.querySelectorAll("[data-close-proj]").forEach((el) => {
    el.addEventListener("click", (e) => {
      e.stopPropagation();
      closeProjectTab((el as HTMLElement).dataset.closeProj!);
    });
  });
  document.getElementById("btn-add-project-tab")?.addEventListener("click", () => {
    openFolderDialog(true);
  });
}

export function mountShell() {
  try {
    state.chatExpandedPref = localStorage.getItem("hivemind.chatExpanded") === "1";
    const savedGhost = localStorage.getItem("hivemind.ghostTextEnabled");
    if (savedGhost === "0" || savedGhost === "1") state.ghostTextEnabled = savedGhost === "1";
    const savedInlineRev = localStorage.getItem("hivemind.inlineReviewEnabled");
    if (savedInlineRev === "0" || savedInlineRev === "1") state.inlineReviewEnabled = savedInlineRev === "1";
    const savedDiff = localStorage.getItem("hivemind.diffViewMode");
    if (savedDiff === "split" || savedDiff === "unified") state.diffViewMode = savedDiff;
  } catch {
    state.chatExpandedPref = false;
  }
  const rootEl = document.getElementById("app");
  if (!rootEl) return;
  rootEl.innerHTML = `
    <div class="ide-shell">
      <header class="titlebar" id="titlebar">
        <div class="dropdown">
          <button type="button" class="brand-logo titlebar-interactive" id="btn-file-menu" title="Arquivo">${icons.brand}<span>HiveMind</span><span class="brand-suffix">IDE</span></button>
          <div class="dropdown-menu" id="file-menu-dropdown" style="display:none"></div>
        </div>
        <div class="project-tabs-bar" id="project-tabs-bar"></div>
        <button class="title-center-cmd titlebar-interactive" id="btn-quick-open" title="Buscar arquivos (Ctrl+P) ou Comandos (Ctrl+K)">
          ${icons.search}
          <span class="cmd-proj" id="title-cmd-proj">Buscar</span>
        </button>
        <button class="icon-btn titlebar-interactive" id="btn-cmd-palette" title="Paleta de Comandos (Ctrl+K)">${icons.command}</button>
        <div class="win-controls">
          <button class="win-btn" id="win-min" title="Minimizar">${icons.winMin}</button>
          <button class="win-btn" id="win-max" title="Maximizar / Restaurar">${icons.winMax}</button>
          <button class="win-btn close" id="win-close" title="Fechar">${icons.winClose}</button>
        </div>
      </header>
      <div class="global-progress-bar" id="global-progress-bar" role="progressbar" aria-label="Carregando" aria-busy="false">
        <div class="global-progress-bar-fill"></div>
      </div>

      <div class="team-strip" id="team-strip">
        <button type="button" class="team-strip-label" id="team-strip-label" title="Abrir Equipe e modelos">Equipe · Auto (Triagem)</button>
        <div class="pipeline-bar" id="pipeline-bar" aria-label="Trilha da equipe"></div>
        <button type="button" class="team-strip-tok" id="status-tokens" title="Sessões e consumo de tokens">${icons.tokens}<span>0 tok</span></button>
        <div class="spacer"></div>
        <button type="button" class="team-strip-action" id="btn-header-ai-usage" title="Cotas, tokens restantes e saúde dos provedores de IA" aria-label="Cotas e uso das IAs">${icons.aiUsage}<span>Cotas IA</span><span class="team-strip-quota-badge" id="header-quota-badge" hidden></span></button>
        <button type="button" class="team-strip-action" id="btn-header-swarm" title="Abrir Quadro Multiagente (Ctrl+Shift+A)">${icons.swarm}<span>Quadro</span></button>
        <button type="button" class="team-strip-action" id="act-ide-settings" title="Configurações da IDE (Ctrl+,)">${icons.settings}<span>Configurar</span></button>
      </div>

      <div class="workbench">
        <div class="left-dock" id="left-dock" style="width:${state.leftWidth}px">
          <nav class="side-rail activity-bar" id="activity-bar" aria-label="Painéis">
            <button class="act-btn side-tab active" data-act="explorer" title="Explorador de Arquivos">${icons.explorer}<span>Explorador</span></button>
            <button class="act-btn side-tab" data-act="search" title="Busca Global no Projeto (Ctrl+Shift+F)">${icons.searchLarge}<span>Busca</span></button>
            <button class="act-btn side-tab" data-act="git" title="Git / Diffs / PR / Issues">
              ${icons.gitBranch}
              <span>Git</span>
              <span class="act-badge" id="git-badge" style="display:none">0</span>
              <span class="act-badge act-badge-gh" id="git-gh-badge" style="display:none" title="Novidades no GitHub">0</span>
            </button>
            <button class="act-btn side-tab" data-act="sessions" title="Sessões Salvas e Consumo de Tokens">${icons.sessions}<span>Sessões</span></button>
            <div class="side-more">
              <button type="button" class="act-btn side-tab" id="btn-rail-more" title="Skills, MCP, Agent Hive e Uso das IAs" aria-haspopup="menu" aria-expanded="false">${icons.moreHoriz}<span>Mais</span></button>
              <div class="side-more-menu" id="rail-more-menu" hidden>
                <button type="button" class="dropdown-item" data-act="skills">${icons.skills}<span>Skills</span></button>
                <button type="button" class="dropdown-item" data-act="mcp">${icons.mcp}<span>MCP</span></button>
                <button type="button" class="dropdown-item" data-act="team">${icons.team}<span>Equipe</span></button>
                <button type="button" class="dropdown-item" id="act-resident">${icons.sparkle}<span>Agent Hive</span></button>
                <button type="button" class="dropdown-item" id="act-ai-usage">${icons.aiUsage}<span>Uso das IAs</span></button>
                <button type="button" class="dropdown-item" id="btn-toggle-right" title="Alternar painel da equipe">${icons.sidebarRight}<span>Painel da equipe</span></button>
                <button type="button" class="dropdown-item" id="btn-toggle-left" title="Alternar barra lateral">${icons.sidebarLeft}<span>Ocultar lateral</span></button>
                <button type="button" class="dropdown-item" id="btn-toggle-term" title="Alternar Terminal (Ctrl+J)">${icons.terminal}<span>Terminal</span></button>
                <button type="button" class="dropdown-item" id="btn-header-run" title="Rodar comando de teste ou build da stack">${icons.play}<span id="header-run-label">Testar</span></button>
              </div>
            </div>
          </nav>
          <aside class="left-sidebar" id="left-sidebar"></aside>
        </div>
        <div class="resizer-v" id="resizer-left"></div>

        <section class="center-area" id="center-area">
          <div class="center-stage" id="center-stage" hidden></div>
          <div class="editor-tabs" id="editor-tabs"></div>
          <div class="editor-body-wrap" id="editor-body"></div>
          <div class="terminal-drawer hidden" id="terminal-drawer"></div>
        </section>

        <div class="resizer-v" id="resizer-right"></div>
        <aside class="right-ai-pane" id="right-pane" style="width:${state.rightWidth}px"></aside>
      </div>

      <footer class="status-bar">
        <button type="button" class="status-chip" id="status-branch" title="Clique para trocar ou criar branch Git">${icons.gitSmall} <span>sem git</span></button>
        <span id="status-stack"></span>
        <button type="button" id="status-problems" class="status-chip" title="Painel de Problemas (Ctrl+Shift+M)">✕ 0 · ▲ 0</button>
        <button type="button" class="ghost mini" id="status-term-btn" title="Abrir ou fechar Terminal (Ctrl+J)">${icons.terminal}<span>Terminal</span></button>
        <span class="status-busy-pill" id="status-busy-indicator" hidden aria-live="polite">${icons.spinner}<span id="status-busy-label"></span></span>
        <span id="status-text">Pronto</span>
        <div class="spacer"></div>
        <div class="sistema-wrap">
          <button type="button" class="status-chip" id="btn-sistema" aria-expanded="false" title="Provedor, Jira, ghost, revisão automática, LSP, mobile e hive">sistema ▴</button>
          <div class="sistema-popover" id="sistema-popover" hidden>
            <button type="button" id="status-providers" class="dim plain-chip" title="Pool de Provedores e Contas LLM">provedor: …</button>
            <button type="button" id="status-jira" class="dim plain-chip" title="Abrir a área Jira">jira: …</button>
            <button type="button" id="status-ghost" class="dim plain-chip" title="Ghost Text inline (Tab aceita)">ghost: …</button>
            <button type="button" id="status-autoreview" class="dim plain-chip" title="Revisão Automática do Fluxo de PRs (Sem Merge)">auto-rev: off</button>
            <button type="button" id="status-lsp" class="dim plain-chip" title="LSP gopls">lsp: …</button>
            <button type="button" id="status-mobile" class="dim plain-chip" title="App Mobile">mobile: …</button>
            <button type="button" id="status-resident" class="dim plain-chip" title="Agent Hive">hive: ocioso</button>
          </div>
        </div>
        <span id="status-env"></span>
      </footer>
    </div>
    <div id="branch-popover-layer"></div>
    <div id="quick-open-layer"></div>
    <div id="modal-layer"></div>
  `;

  bindStaticShellEvents();
  syncChatExpandLayout();
  syncGlobalBusyUi();
}

export function openSidebarView(view: SidebarView) {
  state.sidebarView = view;
  state.showLeftSidebar = true;
  if (view === "git" && state.gitTab === "flow") {
    clearGitHubUnread();
  }
  syncActivityButtons();
  syncLayoutVisibility();
  renderLeftSidebar();
  if (view === "git" && (state.project?.branch || state.gitRepos.length > 0) && !state.gitGraph.loadedOnce && !state.gitGraph.loading) {
    void loadGitGraph();
  }
}

export async function toggleBranchPopover(anchorTop: boolean, targetRepoRoot?: string) {
  if (!state.project) return;
  const effectiveRoot = targetRepoRoot || state.activeGitRepoRoot || state.project.root;
  const repo = state.gitRepos.find((r) => normalizePathKey(r.root) === normalizePathKey(effectiveRoot));
  const effectiveBranch = repo?.branch || (normalizePathKey(effectiveRoot) === normalizePathKey(state.project.root) ? state.project.branch : "");
  if (!effectiveBranch) {
    openSidebarView("git");
    return;
  }
  if (
    state.branchPopover.open &&
    state.branchPopover.anchorTop === anchorTop &&
    normalizePathKey(state.branchPopover.repoRoot || state.project.root) === normalizePathKey(effectiveRoot)
  ) {
    state.branchPopover.open = false;
    renderBranchPopover();
    return;
  }
  try {
    const branches = (await api()?.ListBranches(effectiveRoot)) || [];
    state.branchPopover.branches = branches;
    state.branchPopover.newBranch = "";
    state.branchPopover.anchorTop = anchorTop;
    state.branchPopover.repoRoot = effectiveRoot;
    state.branchPopover.open = true;
    renderBranchPopover();
  } catch (err: any) {
    toastError("listar as branches do Git", err);
  }
}

function positionFloatingPopover(anchor: HTMLElement, pop: HTMLElement, preferAbove: boolean) {
  const rect = anchor.getBoundingClientRect();
  const margin = 8;
  pop.style.position = "fixed";
  pop.style.right = "auto";
  pop.style.bottom = "auto";
  pop.style.left = `${Math.max(margin, rect.left)}px`;
  pop.style.top = `${Math.max(margin, rect.bottom + 6)}px`;
  const popRect = pop.getBoundingClientRect();
  const vw = window.innerWidth || 1280;
  const vh = window.innerHeight || 720;
  let left = rect.right - popRect.width;
  if (left < margin) left = rect.left;
  if (left + popRect.width > vw - margin) left = Math.max(margin, vw - popRect.width - margin);
  let top = preferAbove ? rect.top - popRect.height - 8 : rect.bottom + 6;
  if (top < margin) top = rect.bottom + 6;
  if (top + popRect.height > vh - margin) top = Math.max(margin, rect.top - popRect.height - 8);
  pop.style.left = `${Math.round(left)}px`;
  pop.style.top = `${Math.round(top)}px`;
}

function dismissCenterGitStageForEditorTab() {
  if (!isCenterGitStageActive()) return;
  state.gitTab = "changes";
  clearCenterStage();
  renderLeftSidebar();
}

function closeSistemaPopover() {
  state.sistemaOpen = false;
  const pop = document.getElementById("sistema-popover");
  if (pop) pop.hidden = true;
  document.getElementById("btn-sistema")?.setAttribute("aria-expanded", "false");
}

function closeRailMoreMenu() {
  state.railMoreOpen = false;
  const menu = document.getElementById("rail-more-menu");
  if (menu) menu.hidden = true;
  document.getElementById("btn-rail-more")?.setAttribute("aria-expanded", "false");
}

export function runStackTest() {
  const cmd = state.project?.stack?.testHint || "git status";
  if (!state.terminal.open) {
    state.terminal.open = true;
    renderTerminalDrawer();
  }
  runTerminalCommand(cmd);
}

export function bindStaticShellEvents() {
  const titlebar = document.getElementById("titlebar");
  titlebar?.addEventListener("dblclick", (e) => {
    const target = e.target as HTMLElement;
    if (target.closest(".titlebar-interactive") || target.closest(".win-controls") || target.closest(".project-tab, .project-tab-add") || target.closest("button")) {
      return;
    }
    api()?.WindowToggleMaximize();
  });

  document.getElementById("win-min")?.addEventListener("click", () => api()?.WindowMinimize());
  document.getElementById("win-max")?.addEventListener("click", () => api()?.WindowToggleMaximize());
  document.getElementById("win-close")?.addEventListener("click", () => {
    if (hasAnyProjectDirtyTabs()) {
      askConfirm({
        title: "Alterações não salvas",
        body: "Existem abas editadas sem salvar em um ou mais projetos abertos. Deseja fechar a HiveMind IDE mesmo assim?",
        confirmLabel: "Fechar sem salvar",
        danger: true,
        onConfirm: () => api()?.WindowClose(),
      });
      return;
    }
    api()?.WindowClose();
  });

  document.getElementById("btn-toggle-term")?.addEventListener("click", () => toggleTerminal());
  document.getElementById("status-term-btn")?.addEventListener("click", () => toggleTerminal());
  document.getElementById("team-strip-label")?.addEventListener("click", () => openSidebarView("team"));
  document.getElementById("btn-rail-more")?.addEventListener("click", (e) => {
    e.stopPropagation();
    const btn = e.currentTarget as HTMLElement;
    state.railMoreOpen = !state.railMoreOpen;
    const menu = document.getElementById("rail-more-menu");
    if (menu) {
      menu.hidden = !state.railMoreOpen;
      if (state.railMoreOpen) positionFloatingPopover(btn, menu, false);
    }
    btn.setAttribute("aria-expanded", state.railMoreOpen ? "true" : "false");
  });
  document.getElementById("btn-sistema")?.addEventListener("click", (e) => {
    e.stopPropagation();
    const btn = e.currentTarget as HTMLElement;
    state.sistemaOpen = !state.sistemaOpen;
    const pop = document.getElementById("sistema-popover");
    if (pop) {
      pop.hidden = !state.sistemaOpen;
      if (state.sistemaOpen) positionFloatingPopover(btn, pop, true);
    }
    btn.setAttribute("aria-expanded", state.sistemaOpen ? "true" : "false");
  });
  document.getElementById("btn-toggle-left")?.addEventListener("click", () => {
    closeRailMoreMenu();
    state.showLeftSidebar = !state.showLeftSidebar;
    syncLayoutVisibility();
    if (state.showLeftSidebar) renderLeftSidebar();
  });
  document.getElementById("btn-toggle-right")?.addEventListener("click", () => {
    closeRailMoreMenu();
    state.showRightAI = !state.showRightAI;
    syncLayoutVisibility();
  });

  document.getElementById("btn-header-swarm")?.addEventListener("click", () => {
    dismissCenterGitStageForEditorTab();
    openMissionControlTab();
  });
  document.getElementById("act-mission")?.addEventListener("click", () => {
    dismissCenterGitStageForEditorTab();
    openMissionControlTab();
  });

  document.getElementById("btn-header-run")?.addEventListener("click", () => {
    closeRailMoreMenu();
    runStackTest();
  });

  document.getElementById("btn-cmd-palette")?.addEventListener("click", () => openCommandPalette());
  document.getElementById("status-providers")?.addEventListener("click", () => {
    closeSistemaPopover();
    dismissCenterGitStageForEditorTab();
    void openIdeSettingsTab("providers");
  });
  document.getElementById("status-jira")?.addEventListener("click", () => {
    closeSistemaPopover();
    state.gitTab = "jira";
    openSidebarView("git");
    if (!state.issues.loadedOnce && !state.issues.loading) void loadProjectIssues();
  });
  document.getElementById("status-ghost")?.addEventListener("click", () => {
    closeSistemaPopover();
    toggleGhostText();
  });
  document.getElementById("status-autoreview")?.addEventListener("click", () => {
    closeSistemaPopover();
    state.gitTab = "flow";
    openSidebarView("git");
  });
  document.getElementById("status-problems")?.addEventListener("click", () => openProblemsPanel());
  document.getElementById("status-lsp")?.addEventListener("click", () => {
    closeSistemaPopover();
    void refreshLSPStatus();
    openProblemsPanel();
  });
  document.getElementById("status-mobile")?.addEventListener("click", () => {
    closeSistemaPopover();
    void openMobileModal();
  });
  document.getElementById("status-resident")?.addEventListener("click", () => {
    closeSistemaPopover();
    dismissCenterGitStageForEditorTab();
    openResidentTab();
  });
  document.getElementById("act-resident")?.addEventListener("click", () => {
    closeRailMoreMenu();
    dismissCenterGitStageForEditorTab();
    openResidentTab();
  });

  document.getElementById("status-tokens")?.addEventListener("click", () => {
    openSidebarView("sessions");
    refreshSessions();
  });

  document.getElementById("status-branch")?.addEventListener("click", (e) => {
    e.stopPropagation();
    toggleBranchPopover(false);
  });
  document.getElementById("btn-header-vcs")?.addEventListener("click", (e) => {
    e.stopPropagation();
    toggleBranchPopover(true);
  });

  document.getElementById("btn-file-menu")?.addEventListener("click", (e) => {
    e.stopPropagation();
    state.showFileMenu = !state.showFileMenu;
    renderFileMenu();
  });
  document.addEventListener("click", (e) => {
    const target = e.target as HTMLElement;
    if ((state.planMenuOpen || state.composerMenuOpen) && !target.closest(".composer-select-wrap")) {
      state.planMenuOpen = false;
      state.composerMenuOpen = "";
      renderComposerModeBar();
    }
    if (state.showFileMenu && !target.closest("#btn-file-menu") && !target.closest("#file-menu-dropdown")) {
      state.showFileMenu = false;
      renderFileMenu();
    }
    if (state.railMoreOpen && !target.closest(".side-more")) {
      state.railMoreOpen = false;
      const menu = document.getElementById("rail-more-menu");
      if (menu) menu.hidden = true;
    }
    if (state.sistemaOpen && !target.closest(".sistema-wrap")) {
      state.sistemaOpen = false;
      const pop = document.getElementById("sistema-popover");
      if (pop) pop.hidden = true;
    }
    if (state.chatActionsOpen && !target.closest(".chat-actions")) {
      state.chatActionsOpen = false;
      const menu = document.getElementById("chat-actions-menu");
      if (menu) menu.hidden = true;
    }
    if (state.composerToolsOpen && !target.closest(".composer-tools")) {
      state.composerToolsOpen = false;
      const menu = document.getElementById("composer-tools-menu");
      if (menu) menu.hidden = true;
    }
    if (state.branchPopover.open) {
      if (!target.closest("#branch-popover-layer") && !target.closest("#status-branch") && !target.closest("#btn-header-vcs") && !target.closest("#btn-scm-branch-switch")) {
        state.branchPopover.open = false;
        renderBranchPopover();
      }
    }
    if ((state.gitMoreMenuOpen || state.commitMenuOpen) && !target.closest(".scm-dropdown") && !target.closest("#btn-scm-more") && !target.closest("[data-commit-menu-repo]")) {
      state.gitMoreMenuOpen = false;
      state.commitMenuOpen = false;
      if (state.sidebarView === "git") renderLeftSidebar();
    }
  });

  document.getElementById("act-ai-usage")?.addEventListener("click", () => {
    closeRailMoreMenu();
    dismissCenterGitStageForEditorTab();
    void openAIUsagePage();
  });
  document.getElementById("btn-header-ai-usage")?.addEventListener("click", () => {
    dismissCenterGitStageForEditorTab();
    void openAIUsagePage();
  });
  document.getElementById("act-ide-settings")?.addEventListener("click", () => {
    dismissCenterGitStageForEditorTab();
    void openIdeSettingsTab();
  });

  document.getElementById("btn-quick-open")?.addEventListener("click", () => openQuickOpen());

  document.querySelectorAll("[data-act]").forEach((el) => {
    el.addEventListener("click", () => {
      const view = (el as HTMLElement).dataset.act as SidebarView;
      if (state.sidebarView === view && state.showLeftSidebar) {
        state.showLeftSidebar = false;
      } else {
        state.sidebarView = view;
        state.showLeftSidebar = true;
        if (view === "sessions") refreshSessions();
        if (view === "skills") refreshSkills();
        if (view === "mcp") void refreshMCP().then(() => renderLeftSidebar());
        if (view === "git" && state.project?.branch && !state.gitGraph.loadedOnce && !state.gitGraph.loading) {
          void loadGitGraph();
        }
      }
      state.railMoreOpen = false;
      const more = document.getElementById("rail-more-menu");
      if (more) more.hidden = true;
      syncActivityButtons();
      syncLayoutVisibility();
      if (state.showLeftSidebar) {
        renderLeftSidebar();
        if (view === "search") {
          (document.getElementById("global-search-input") as HTMLInputElement | null)?.focus();
        }
      }
    });
  });

  setupResizer("resizer-left", (dx) => {
    state.leftWidth = Math.max(240, Math.min(560, state.leftWidth + dx));
    const el = document.getElementById("left-dock");
    if (el) el.style.width = `${state.leftWidth}px`;
    const wb = document.querySelector<HTMLElement>(".workbench");
    wb?.style.setProperty("--left-pane-w", `${state.leftWidth}px`);
  });
  setupResizer("resizer-right", (dx) => {
    const wb = document.querySelector<HTMLElement>(".workbench");
    if (isCenterGitStageActive()) {
      const rightEl = document.getElementById("right-pane");
      const curW = rightEl ? rightEl.getBoundingClientRect().width : state.rightWidth;
      const maxW = Math.max(320, Math.round((window.innerWidth || 1280) * 0.72));
      const nextW = Math.max(260, Math.min(maxW, Math.round(curW - dx)));
      wb?.style.setProperty("--stage-right-w", `${nextW}px`);
      return;
    }
    state.rightWidth = Math.max(260, Math.min(680, state.rightWidth - dx));
    const el = document.getElementById("right-pane");
    if (el) el.style.width = `${state.rightWidth}px`;
    wb?.style.setProperty("--right-pane-w", `${state.rightWidth}px`);
  });

  document.addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && !e.shiftKey && e.key === ",") {
      e.preventDefault();
      void openIdeSettingsTab();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === "e") {
      e.preventDefault();
      toggleChatExpanded();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === "f") {
      e.preventDefault();
      openSidebarView("search");
      (document.getElementById("global-search-input") as HTMLInputElement | null)?.focus();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === "a") {
      e.preventDefault();
      openMissionControlTab();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === "o") {
      e.preventDefault();
      openFolderDialog(true);
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.key === "Tab" && state.projectTabs.length > 1) {
      e.preventDefault();
      const idx = state.projectTabs.findIndex((p) => p.id === state.activeProjectId);
      const nextIdx = e.shiftKey
        ? (idx - 1 + state.projectTabs.length) % state.projectTabs.length
        : (idx + 1) % state.projectTabs.length;
      switchProjectTab(state.projectTabs[nextIdx].id);
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === "p") {
      e.preventDefault();
      openCommandPalette();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && !e.shiftKey && e.key.toLowerCase() === "k") {
      e.preventDefault();
      if (isCodeEditorActive()) {
        openInlineEdit();
      } else {
        openCommandPalette();
      }
      return;
    }
    if ((e.ctrlKey || e.metaKey) && !e.shiftKey && e.key.toLowerCase() === "l") {
      const activeTab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
      if (activeTab) {
        e.preventDefault();
        sendEditorSelectionToChief();
        return;
      }
    }
    if ((e.ctrlKey || e.metaKey) && !e.shiftKey && e.key.toLowerCase() === "p") {
      e.preventDefault();
      openQuickOpen();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "j") {
      e.preventDefault();
      toggleTerminal();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === "m") {
      e.preventDefault();
      openProblemsPanel();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && !e.shiftKey && e.key.toLowerCase() === "f") {
      const activeTab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
      if (activeTab) {
        e.preventDefault();
        openEditorFind(false);
        return;
      }
    }
    if ((e.ctrlKey || e.metaKey) && !e.shiftKey && e.key.toLowerCase() === "h") {
      const activeTab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
      if (activeTab) {
        e.preventDefault();
        openEditorFind(true);
        return;
      }
    }
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      saveActiveEditor();
      return;
    }
    if (e.key === "Escape") {
      if (state.confirmModal) {
        state.confirmModal = null;
        renderModalLayer();
      } else if (state.imagePreviewUrl) {
        state.imagePreviewUrl = "";
        renderModalLayer();
      } else if (state.pathModal) {
        state.pathModal = null;
        renderModalLayer();
      } else if (state.inlineEdit.open) {
        closeInlineEdit(true);
      } else if (state.editorFind.open) {
        state.editorFind.open = false;
        renderEditorFindBar();
      } else if (state.branchPopover.open) {
        state.branchPopover.open = false;
        renderBranchPopover();
      } else if (state.quickOpen.open) {
        closeQuickOpen();
      } else if (state.showFileMenu) {
        state.showFileMenu = false;
        renderFileMenu();
      } else if (state.mentionResults.length > 0) {
        state.mentionResults = [];
        renderMentionPopup();
      } else if (state.showGitHubModal) {
        state.showGitHubModal = false;
        renderModalLayer();
      } else if (state.showAPIKeysModal) {
        state.showAPIKeysModal = false;
        renderModalLayer();
      } else if (state.showMobileModal) {
        state.showMobileModal = false;
        renderModalLayer();
      } else if (state.showJiraModal) {
        state.showJiraModal = false;
        renderModalLayer();
      } else if (state.skillEditor) {
        state.skillEditor = null;
        renderModalLayer();
      }
    }
  });
}

export function renderBranchPopover() {
  const layer = document.getElementById("branch-popover-layer");
  if (!layer) return;
  if (!state.branchPopover.open || !state.project) {
    layer.innerHTML = "";
    return;
  }
  const targetRoot = state.branchPopover.repoRoot || state.project.root;
  const targetRepo = state.gitRepos.find((r) => normalizePathKey(r.root) === normalizePathKey(targetRoot));
  const isWorkspaceRoot = normalizePathKey(targetRoot) === normalizePathKey(state.project.root);
  const cur = targetRepo?.branch || (isWorkspaceRoot ? state.project.branch : "");
  const repoLabel = targetRepo && state.gitRepos.length > 1 ? ` · ${targetRepo.name}` : "";
  const posStyle = state.branchPopover.anchorTop
    ? "top:44px;bottom:auto;left:110px"
    : "bottom:30px;left:12px";

  layer.innerHTML = `
    <div class="branch-popover" style="${posStyle}">
      <div class="eyebrow">${icons.gitSmall}<span>Branches Git${escapeHtml(repoLabel)}</span></div>
      <div style="max-height:170px;overflow-y:auto;display:flex;flex-direction:column;gap:2px">
        ${state.branchPopover.branches.length === 0
          ? `<div class="dim" style="padding:4px">Sem commits ainda (${escapeHtml(cur)})</div>`
          : state.branchPopover.branches.map((b) => `
            <button class="dropdown-item ${b === cur ? "selected" : ""}" data-switch-branch="${escapeHtml(b)}">
              ${b === cur ? icons.check : icons.gitSmall}
              <span>${escapeHtml(b)}</span>
            </button>
          `).join("")
        }
      </div>
      <div style="display:flex;gap:5px;border-top:1px solid var(--border);padding-top:6px">
        <input id="new-branch-input" placeholder="Nova branch (ex: feat/login)" value="${escapeHtml(state.branchPopover.newBranch)}" style="flex:1;padding:4px 7px;font-size:11.5px" />
        <button class="primary mini" id="btn-create-branch">Criar</button>
      </div>
      <div id="branch-popover-err" style="color:var(--red);font-size:11px;margin-top:4px;display:none"></div>
    </div>
  `;

  layer.querySelectorAll("[data-switch-branch]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const targetBranch = (btn as HTMLElement).dataset.switchBranch!;
      if (!state.project || targetBranch === cur) {
        state.branchPopover.open = false;
        renderBranchPopover();
        return;
      }
      const performSwitch = async () => {
        if (!state.project) return;
        try {
          await api()?.CheckoutBranch(targetRoot, targetBranch, false);
          if (targetRepo) targetRepo.branch = targetBranch;
          if (isWorkspaceRoot) state.project.branch = targetBranch;
          state.branchPopover.open = false;
          renderBranchPopover();
          renderProjectTabsBar();
          updateHeaderAndStatusBar();
          refreshDiffs();
          void loadGitGraph();
          refreshExplorerTree();
          await reloadCleanOpenTabs();
          setStatus(`Branch alterada para ${targetBranch}.`);
        } catch (err: any) {
          toastError(`trocar para a branch "${targetBranch}"`, err, () => { void performSwitch(); });
        }
      };
      if (hasDirtyTabs()) {
        state.branchPopover.open = false;
        renderBranchPopover();
        askConfirm({
          title: "Trocar de Branch com Edições Abertas",
          body: `Existem arquivos editados não salvos no editor. Deseja trocar para "${targetBranch}" mesmo assim?`,
          confirmLabel: "Trocar Branch",
          danger: true,
          onConfirm: performSwitch,
        });
        return;
      }
      await performSwitch();
    });
  });

  const nbInput = document.getElementById("new-branch-input") as HTMLInputElement | null;
  const doCreate = async () => {
    const name = (nbInput?.value || "").trim();
    const branchErr = document.getElementById("branch-popover-err");
    if (!name || !state.project) return;
    if (!/^[A-Za-z0-9][A-Za-z0-9._\-/]*$/.test(name)) {
      if (branchErr) {
        branchErr.textContent = "Nome inválido: use letras, números, ponto, hífen ou barra (ex: feat/login).";
        branchErr.style.display = "block";
      }
      nbInput?.focus();
      return;
    }
    if (branchErr) branchErr.style.display = "none";
    try {
      await api()?.CheckoutBranch(targetRoot, name, true);
      if (targetRepo) targetRepo.branch = name;
      if (isWorkspaceRoot) state.project.branch = name;
      state.branchPopover.open = false;
      renderBranchPopover();
      renderProjectTabsBar();
      updateHeaderAndStatusBar();
      refreshDiffs();
      void loadGitGraph();
      setStatus(`Branch "${name}" criada e ativada.`);
    } catch (err: any) {
      toastError(`criar a branch "${name}"`, err, () => { void doCreate(); });
    }
  };
  nbInput?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      doCreate();
    }
  });
  document.getElementById("btn-create-branch")?.addEventListener("click", doCreate);
  nbInput?.focus();
}

export function setupResizer(id: string, onMove: (dx: number) => void) {
  const res = document.getElementById(id);
  if (!res) return;
  res.addEventListener("mousedown", (e) => {
    e.preventDefault();
    let lastX = e.clientX;
    res.classList.add("dragging");
    const move = (ev: MouseEvent) => {
      const dx = ev.clientX - lastX;
      lastX = ev.clientX;
      onMove(dx);
    };
    const up = () => {
      res.classList.remove("dragging");
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
  });
}

export function chatLayoutExpanded(): boolean {
  if (isCenterGitStageActive()) return false;
  return shouldExpandChat(state.chatExpandedPref, state.editorTabs.length);
}

export function syncChatExpandLayout() {
  const on = chatLayoutExpanded();
  const wb = document.querySelector<HTMLElement>(".workbench");
  wb?.classList.toggle("chat-expanded", on);
  wb?.style.setProperty("--right-pane-w", `${state.rightWidth}px`);
  wb?.style.setProperty("--left-pane-w", `${state.leftWidth}px`);
  const right = document.getElementById("right-pane");
  right?.classList.toggle("chat-reading", on);
  if (right && !right.classList.contains("hidden")) {
    right.style.width = on ? "" : `${state.rightWidth}px`;
  }
  const resR = document.getElementById("resizer-right");
  if (resR) resR.style.display = on || !state.showRightAI ? "none" : "";
  const btn = document.getElementById("btn-expand-chat");
  if (btn) {
    const label = state.chatExpandedPref ? "Recolher Equipe IA" : "Expandir Equipe IA";
    btn.setAttribute("aria-pressed", state.chatExpandedPref ? "true" : "false");
    btn.setAttribute("aria-label", label);
    btn.classList.toggle("is-on", state.chatExpandedPref);
    btn.title = state.editorTabs.length > 0 || isCenterGitStageActive()
      ? "A preferência fica salva. O chat ocupa o centro quando não houver abas abertas (Ctrl+Shift+E)."
      : `${label} (Ctrl+Shift+E)`;
  }
}

export function toggleChatExpanded() {
  state.chatExpandedPref = !state.chatExpandedPref;
  try {
    localStorage.setItem("hivemind.chatExpanded", state.chatExpandedPref ? "1" : "0");
  } catch {
    // preview sem storage
  }
  syncChatExpandLayout();
  if (state.chatExpandedPref && (state.editorTabs.length > 0 || isCenterGitStageActive())) {
    setStatus("Equipe IA ocupa o centro quando o editor estiver sem abas.");
  }
}

export function syncLayoutVisibility() {
  const dock = document.getElementById("left-dock");
  const left = document.getElementById("left-sidebar");
  const resL = document.getElementById("resizer-left");
  const right = document.getElementById("right-pane");
  const resR = document.getElementById("resizer-right");
  if (!isCenterGitStageActive()) {
    clearCenterStage();
  }
  dock?.classList.toggle("is-rail-only", !state.showLeftSidebar);
  left?.classList.toggle("hidden", !state.showLeftSidebar);
  if (resL) resL.style.display = state.showLeftSidebar ? "" : "none";
  right?.classList.toggle("hidden", !state.showRightAI);
  if (resR) resR.style.display = state.showRightAI ? "" : "none";
  syncChatExpandLayout();
}

export function syncActivityButtons() {
  document.querySelectorAll("[data-act]").forEach((el) => {
    const view = (el as HTMLElement).dataset.act;
    el.classList.toggle("active", state.showLeftSidebar && state.sidebarView === view);
  });
  document.getElementById("act-ide-settings")?.classList.toggle("active", state.activeEditorPath === IDE_SETTINGS_TAB_KEY);
}

export function renderFileMenu() {
  const menu = document.getElementById("file-menu-dropdown");
  if (!menu) return;
  if (!state.showFileMenu) {
    menu.style.display = "none";
    return;
  }
  menu.style.display = "block";
  menu.innerHTML = `
    <button class="dropdown-item" id="menu-open-folder-tab">${icons.plus}<span>Abrir Projeto em Nova Aba… (Ctrl+Shift+O)</span></button>
    <button class="dropdown-item" id="menu-open-folder">${icons.folderOpen}<span>Substituir Projeto na Aba Atual…</span></button>
    <button class="dropdown-item" id="menu-run-tests">${icons.play}<span>Testar stack</span></button>
    <button class="dropdown-item" id="menu-open-memory">${icons.memory}<span>Memória do Time (.hivemind/MEMORY.md)</span></button>
    <button class="dropdown-item" id="menu-new-team">${icons.team}<span>Configurar Equipe do Projeto…</span></button>
    <button class="dropdown-item" id="menu-ide-settings">${icons.settings}<span>Configurações da IDE… (Ctrl+,)</span></button>
    <button class="dropdown-item" id="menu-providers-pool">${icons.cloud}<span>Provedores &amp; Contas (Pool)…</span></button>
    <button class="dropdown-item" id="menu-providers-scan">${icons.search}<span>Varredura de Provedores (Diagnóstico)…</span></button>
    <button type="button" class="dropdown-item" id="menu-companion" aria-pressed="${loadSettings().enabled ? "true" : "false"}">${icons.sparkle}<span>${loadSettings().enabled ? "Companion ligado" : "Companion desligado"}</span></button>
    <button type="button" class="dropdown-item" id="menu-orb-show">${icons.sparkle}<span>Mostrar Orb flutuante</span></button>
    <button type="button" class="dropdown-item" id="menu-check-updates">${icons.refresh}<span>Verificar atualizações…</span></button>
    <button type="button" class="dropdown-item danger" id="menu-quit-all">${icons.closeSmall}<span>Sair completamente (IDE + Orb)</span></button>
    <div class="dim" style="padding:6px 10px 3px 10px;border-top:1px solid var(--border);margin-top:4px">Projetos Recentes (clique abre em Nova Aba)</div>
    ${state.recent.length === 0
      ? `<div class="dim" style="padding:6px 10px">Nenhum projeto recente</div>`
      : state.recent.map((r) => `<button class="dropdown-item" data-recent="${escapeHtml(r)}" title="${escapeHtml(r)}">${escapeHtml(r)}</button>`).join("")
    }
  `;
  menu.querySelector("#menu-open-folder-tab")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    openFolderDialog(true);
  });
  menu.querySelector("#menu-open-folder")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    openFolderDialog(false);
  });
  menu.querySelector("#menu-run-tests")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    runStackTest();
  });
  menu.querySelector("#menu-open-memory")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    openTeamMemoryFile();
  });
  menu.querySelector("#menu-new-team")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    void openTeamConfigTab();
  });
  menu.querySelector("#menu-ide-settings")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    void openIdeSettingsTab();
  });
  menu.querySelector("#menu-providers-pool")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    void openIdeSettingsTab("providers", { providersTab: "profiles" });
  });
  menu.querySelector("#menu-providers-scan")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    void openIdeSettingsTab("providers", { providersTab: "scan", triggerScan: true });
  });
  menu.querySelector("#menu-companion")?.addEventListener("click", () => {
    toggleCompanionEnabled();
    state.showFileMenu = false;
    renderFileMenu();
  });
  menu.querySelector("#menu-orb-show")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    void api()?.OrbShow?.();
  });
  menu.querySelector("#menu-check-updates")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    void openUpdatesModal();
  });
  menu.querySelector("#menu-quit-all")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    void api()?.QuitAll?.();
  });
  menu.querySelectorAll("[data-recent]").forEach((el) => {
    el.addEventListener("click", () => openProjectPath((el as HTMLElement).dataset.recent!, true));
  });
}

export function updateHeaderAndStatusBar() {
  const proj = state.project;
  const cmdProj = document.getElementById("title-cmd-proj");
  const stackTag = document.getElementById("title-stack-tag");
  const headerVcs = document.getElementById("header-vcs-label");
  const headerRun = document.getElementById("header-run-label");

  if (cmdProj) {
    cmdProj.textContent = "Buscar";
  }
  const searchBtn = document.getElementById("btn-quick-open");
  if (searchBtn) {
    searchBtn.title = proj
      ? `${proj.name} — Arquivos (Ctrl+P) ou Comandos (Ctrl+K)`
      : "Buscar arquivos (Ctrl+P) ou Comandos (Ctrl+K)";
  }
  if (stackTag) stackTag.style.display = "none";
  if (headerVcs) {
    headerVcs.textContent = proj?.branch || "sem git";
  }
  if (headerRun) {
    const hint = proj?.stack?.testHint || "Testar";
    headerRun.textContent = hint.length > 18 ? "Testar Stack" : hint;
  }
  updateStatusBar();
}

function withQuotaNote(chip: { text: string; color: string; title: string }): { text: string; color: string; title: string } {
  const note = quotaChipNote(state.providerHealth || [], state.aiUsagePage.report);
  if (!note) return chip;
  const color = note.startsWith("aviso") && chip.color === "var(--green)" ? "var(--amber)" : chip.color;
  return { text: `${chip.text} · ${note}`, color, title: `${chip.title} · ${note}` };
}

export function computeUnifiedProviderChip(): { text: string; color: string; title: string } {
  const scan = state.providersScan;
  const health = state.providerHealth || [];
  const configuredHealth = health.filter((p) => p.hasCredential);
  const openCount = configuredHealth.filter((p) => p.state === "open").length;
  const halfCount = configuredHealth.filter((p) => p.state === "half_open").length;

  const connectedNames: string[] = [];
  if (scan && scan.providers?.length > 0) {
    for (const p of scan.providers) {
      if (p.connected) connectedNames.push(p.name);
    }
  } else {
    if (state.agyStatus?.LoggedIn) connectedNames.push("Antigravity (Google)");
    if (state.openCodeStatus?.configured) connectedNames.push("OpenCode");
    for (const h of configuredHealth) {
      if (!connectedNames.some((n) => n.toLowerCase().includes(h.provider.toLowerCase()))) {
        connectedNames.push(h.provider);
      }
    }
    const profiles = state.profilesOverview?.profiles || [];
    for (const pr of profiles) {
      if (pr.ready && !connectedNames.some((n) => n.toLowerCase().includes(pr.provider.toLowerCase()))) {
        connectedNames.push(pr.provider);
      }
    }
  }

  const totalAccounts = scan?.totalAccounts || (state.profilesOverview?.profiles?.length || 0);
  const activeCount = connectedNames.length;

  if (!scan && !state.agyStatus && !state.openCodeStatus && configuredHealth.length === 0) {
    return withQuotaNote({
      text: "provedor: …",
      color: "var(--text-dim)",
      title: "Verificando provedores e contas — clique para abrir o Pool de Provedores",
    });
  }
  if (activeCount === 0) {
    return withQuotaNote({
      text: "provedor: off",
      color: "var(--amber)",
      title: "Nenhum provedor conectado ainda — clique para configurar contas ou fazer varredura",
    });
  }
  if (configuredHealth.length > 0 && openCount === configuredHealth.length) {
    return withQuotaNote({
      text: `provedor: ${openCount} pausa`,
      color: "var(--red)",
      title: `Todos os provedores estão em pausa/cooldown (${connectedNames.join(", ")}) — clique para abrir o Pool de Provedores`,
    });
  }
  if (openCount > 0 || halfCount > 0) {
    const warn = openCount > 0 ? `${openCount} pausa` : `${halfCount} sonda`;
    return withQuotaNote({
      text: `provedor: ${activeCount} (${warn})`,
      color: "var(--amber)",
      title: `Conectados: ${connectedNames.join(", ")} · ${totalAccounts} conta(s) · clique para abrir o Pool de Provedores`,
    });
  }
  return withQuotaNote({
    text: `provedor: ${activeCount}`,
    color: "var(--green)",
    title: `Provedores conectados (${activeCount}): ${connectedNames.join(", ")}${totalAccounts > 0 ? ` · ${totalAccounts} conta(s)` : ""} — clique para abrir o Pool de Provedores`,
  });
}

export function updateStatusBar() {
  const proj = state.project;
  const lane = activeLane();
  const branchEl = document.getElementById("status-branch");
  const stackEl = document.getElementById("status-stack");
  const teamEl = document.getElementById("team-strip-label");
  const tokEl = document.getElementById("status-tokens");
  const envEl = document.getElementById("status-env");

  if (branchEl) {
    const branchName = proj?.branch || "sem git";
    branchEl.innerHTML = `${icons.gitSmall} <span>${escapeHtml(branchName)}</span>`;
    branchEl.title = proj?.branch ? `Branch atual: ${proj.branch} (clique para trocar)` : "Sem repositório Git (clique para inicializar)";
  }
  if (stackEl) {
    const rawStack = proj?.stack?.summary || "";
    const shortStack = rawStack.startsWith("Stack genérica") ? "Stack genérica" : rawStack;
    stackEl.textContent = shortStack;
    stackEl.title = rawStack;
  }
  const perm = proj?.teamConfig?.settings?.permission_mode || "governed";
  if (teamEl) {
    const label = laneDisplayName(lane);
    teamEl.textContent = `Equipe · ${runModeLabel(state.runMode)}`;
    teamEl.title = `Equipe: ${label} (${lane?.teamName || "-"} · ${lane?.agents.length || 0}) · Modo: ${runModeLabel(state.runMode)} · Permissão: ${permissionModeLabel(perm)}`;
  }
  if (tokEl) {
    const sessTok = lane?.sessionTotalTokens || 0;
    const projTok = Math.max(state.sessionsSummary.projectTotalTokens, sessTok);
    tokEl.innerHTML = `${icons.tokens} <span>${formatTokens(sessTok)} tok</span>`;
    tokEl.title = `Sessão atual: ${exactTokens(sessTok)} tok (Entrada: ${exactTokens(lane?.sessionInTokens)} / Saída: ${exactTokens(lane?.sessionOutTokens)}) | Projeto: ${exactTokens(projTok)} tok — Clique para ver histórico`;
  }

  const jiraEl = document.getElementById("status-jira");
  if (jiraEl) {
    const j = state.jiraStatus;
    if (!j) {
      jiraEl.textContent = "jira: …";
      jiraEl.style.color = "";
      jiraEl.title = "Verificando Jira Cloud… (clique para conectar)";
    } else if (j.connected) {
      const pCount = state.jiraProjects.length > 0 ? ` (${state.jiraProjects.length})` : "";
      const profLabel = j.activeProfileName ? j.activeProfileName : "ok";
      jiraEl.textContent = `jira: ${profLabel}${pCount}`;
      jiraEl.style.color = "var(--green)";
      jiraEl.title = `${j.detail}${j.defaultProject ? ` · Projeto padrão: ${j.defaultProject}` : ""} — clique para gerenciar perfis Jira`;
    } else if (j.matchMode === "isolated_none") {
      jiraEl.textContent = j.detectedOrg ? `jira: sem (${j.detectedOrg})` : "jira: off";
      jiraEl.style.color = "var(--text-dim)";
      jiraEl.title = `${j.detail || "Sem Jira para esta org"} — clique para vincular`;
    } else {
      jiraEl.textContent = "jira: off";
      jiraEl.style.color = "var(--text-dim)";
      jiraEl.title = `${j.detail || "Desconectado"} — clique para conectar ao Jira Cloud`;
    }
  }
  const ghostEl = document.getElementById("status-ghost");
  if (ghostEl) {
    const st = state.inlineAIStatus;
    const accSuffix = state.ghostTextAccepted > 0 ? ` (${state.ghostTextAccepted})` : "";
    if (!state.ghostTextEnabled) {
      ghostEl.textContent = `ghost: off${accSuffix}`;
      ghostEl.style.color = "var(--text-dim)";
    } else if (st && st.ghostAvailable) {
      ghostEl.textContent = `ghost: on${accSuffix}`;
      ghostEl.style.color = "var(--green)";
    } else if (st && !st.ghostAvailable) {
      ghostEl.textContent = "ghost: sem http";
      ghostEl.style.color = "var(--text-dim)";
    } else {
      ghostEl.textContent = "ghost: …";
      ghostEl.style.color = "";
    }
    ghostEl.title = ghostButtonTitle();
  }
  const arEl = document.getElementById("status-autoreview");
  if (arEl) {
    const ar = state.autoReview;
    if (!ar || !ar.enabled) {
      arEl.textContent = "auto-rev: off";
      arEl.style.color = "var(--text-dim)";
      arEl.title = "Revisão Automática do Fluxo desligada (clique para abrir Git → Fluxo)";
    } else if (ar.state === "reviewing" && ar.activePr) {
      arEl.textContent = `auto-rev: #${ar.activePr}`;
      arEl.style.color = "#6ea8fe";
      arEl.title = `Revisando PR #${ar.activePr} automaticamente (Sem Merge) — clique para ver o Fluxo`;
    } else if (ar.state === "paused_limit" || ar.state === "error") {
      arEl.textContent = "auto-rev: pausa";
      arEl.style.color = "var(--amber)";
      arEl.title = ar.lastError || "Revisão Automática pausada";
    } else {
      arEl.textContent = `auto-rev: on${ar.queueCount > 0 ? ` (${ar.queueCount})` : ""}`;
      arEl.style.color = "var(--green)";
      arEl.title = `Revisão Automática ativa (Sem Merge) · ${ar.reviewedLastHour}/h — clique para abrir Git → Fluxo`;
    }
  }
  const lspEl = document.getElementById("status-lsp");
  if (lspEl) {
    const ls = state.lspStatus;
    if (!ls) {
      lspEl.textContent = "lsp: …";
      lspEl.style.color = "var(--text-dim)";
    } else if (ls.running) {
      lspEl.textContent = `lsp: ${ls.server}`;
      lspEl.style.color = "var(--green)";
      lspEl.title = `${ls.server} ativo (${ls.root || ""}) — F12/Ctrl+Clique: definição, hover e diagnósticos`;
    } else if (ls.available) {
      lspEl.textContent = `lsp: pronto`;
      lspEl.style.color = "var(--text-dim)";
      lspEl.title = `${ls.server} disponível (${ls.binary || ""}) — inicia automaticamente ao abrir um arquivo .go`;
    } else {
      lspEl.textContent = "lsp: off";
      lspEl.style.color = "var(--text-dim)";
      lspEl.title = ls.error || "gopls não encontrado no PATH";
    }
  }
  const mbEl = document.getElementById("status-mobile");
  if (mbEl) {
    const mb = state.mobileStatus;
    const relay = state.relayStatus;
    const lan = !!mb?.running;
    const relayOnline = !!relay?.online;
    if (!mb && !relay) {
      mbEl.textContent = "mobile: …";
      mbEl.style.color = "var(--text-dim)";
    } else if (lan && relayOnline) {
      mbEl.textContent = "mobile: on+relay";
      mbEl.style.color = "var(--green)";
      mbEl.title = `LAN na porta ${mb?.port} e relay online (${relay?.deviceName || "IDE"})`;
    } else if (relayOnline) {
      mbEl.textContent = "mobile: relay";
      mbEl.style.color = "var(--green)";
      mbEl.title = "Acesso remoto pelo relay — o celular alcança esta IDE de qualquer rede";
    } else if (relay?.enabled) {
      mbEl.textContent = "mobile: relay…";
      mbEl.style.color = "var(--amber)";
      mbEl.title = relay.lastError || "Relay ligado, reconectando";
    } else if (lan) {
      mbEl.textContent = `mobile: on${mb.urls?.length ? ` (${mb.urls.length})` : ""}`;
      mbEl.style.color = "var(--green)";
      mbEl.title = `App Mobile ativo na porta ${mb.port} — clique para abrir o QR de pareamento`;
    } else {
      mbEl.textContent = "mobile: off";
      mbEl.style.color = "var(--text-dim)";
      mbEl.title = "App Mobile desligado — clique para ligar a LAN ou o relay";
    }
  }
  const provEl = document.getElementById("status-providers");
  if (provEl) {
    const summary = computeUnifiedProviderChip();
    provEl.textContent = summary.text;
    provEl.style.color = summary.color;
    provEl.title = summary.title;
  }
  const quotaBadgeEl = document.getElementById("header-quota-badge");
  const quotaBtnEl = document.getElementById("btn-header-ai-usage");
  if (quotaBadgeEl) {
    const lowest = lowestRemainingQuotaPct(state.providerHealth || [], state.aiUsagePage.report);
    if (lowest) {
      const pct = Math.max(0, Math.min(100, Math.round(lowest.remainingPct)));
      quotaBadgeEl.hidden = false;
      quotaBadgeEl.textContent = `${pct}%`;
      quotaBadgeEl.classList.toggle("is-danger", pct <= 20);
      quotaBadgeEl.classList.toggle("is-warn", pct > 20 && pct <= 35);
      if (quotaBtnEl) {
        quotaBtnEl.title = `Cotas IA — menor cota livre: ${pct}% (${lowest.source}). Clique para ver detalhes.`;
      }
    } else {
      quotaBadgeEl.hidden = true;
      quotaBadgeEl.textContent = "";
      quotaBadgeEl.classList.remove("is-danger", "is-warn");
      if (quotaBtnEl) {
        quotaBtnEl.title = "Cotas, tokens restantes e saúde dos provedores de IA";
      }
    }
  }
  const probEl = document.getElementById("status-problems");
  if (probEl) {
    const c = countProblems();
    probEl.textContent = `✕ ${c.errors} · ▲ ${c.warnings}`;
    probEl.style.color = c.errors > 0 ? "var(--red)" : c.warnings > 0 ? "var(--amber)" : "";
    probEl.title = `${c.errors} erro(s), ${c.warnings} aviso(s) — clique para abrir Problemas (Ctrl+Shift+M)`;
  }
  updateResidentStatusChip();
  if (envEl) {
    envEl.textContent = state.goVersion || "";
    envEl.title = `${state.goVersion} · ${state.os}`;
  }
}
