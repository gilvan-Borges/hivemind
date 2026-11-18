import "./styles.css";
import { highlightCode } from "./highlighter";
import { fileBadge, icons } from "./icons";
import { renderMarkdownLite } from "./markdown";
import type {
  AgentRuntime,
  AgyModel,
  AgyStatus,
  ChatMessage,
  CommandAction,
  ConfirmDialogState,
  EditorTab,
  FileDiff,
  FileNode,
  GitHubStatus,
  ImageAttachment,
  OpenCodeStatus,
  PermissionMode,
  ProjectInfo,
  ProjectIssue,
  ProjectWorkspaceContext,
  RunMode,
  SessionSummary,
  SidebarView,
  TeamLane,
  TerminalEntry,
  TextSearchMatch,
  WailsBackendApp,
} from "./types";
import {
  escapeHtml,
  exactTokens,
  formatTokens,
  matchAgentRuntime,
  permissionModeLabel,
  roleLabelPt,
  runModeLabel,
  safeImageSrc,
} from "./utils";

declare global {
  interface Window {
    go?: {
      main?: {
        App?: WailsBackendApp;
      };
    };
    runtime?: {
      EventsOn(eventName: string, cb: (data: any) => void): void;
    };
  }
}

const api = () => window.go?.main?.App;
const MISSION_TAB_KEY = "mission:swarm";

const state = {
  projectTabs: [] as ProjectWorkspaceContext[],
  activeProjectId: "",
  project: null as ProjectInfo | null,
  recent: [] as string[],
  models: [] as AgyModel[],
  agyStatus: null as AgyStatus | null,
  openCodeGoModels: [] as AgyModel[],
  openCodeModels: [] as AgyModel[],
  openCodeStatus: null as OpenCodeStatus | null,
  ghStatus: null as GitHubStatus | null,
  goVersion: "",
  os: "windows",
  statusText: "Pronto",
  sidebarView: "explorer" as SidebarView,
  showLeftSidebar: true,
  showRightAI: true,
  leftWidth: 268,
  rightWidth: 435,
  expandedDirs: new Set<string>(),
  dirCache: new Map<string, FileNode[]>(),
  explorerFilter: "",
  explorerFilterResults: [] as string[],
  editorTabs: [] as EditorTab[],
  activeEditorPath: "",
  lanes: [] as TeamLane[],
  activeLaneId: "",
  runMode: "auto" as RunMode,
  diffs: [] as FileDiff[],
  showFileMenu: false,
  showWizard: false,
  showOpenCodeModal: false,
  doctorModal: null as AgyStatus | null,
  confirmModal: null as ConfirmDialogState | null,
  pathModal: null as null | {
    mode: "newFile" | "newDir" | "rename" | "delete";
    targetRel: string;
    value: string;
  },
  quickOpen: {
    open: false,
    mode: "files" as "files" | "commands",
    query: "",
    results: [] as string[],
    selected: 0,
  },
  textSearch: {
    query: "",
    results: [] as TextSearchMatch[],
    searching: false,
  },
  sessionsSummary: {
    sessions: [] as SessionSummary[],
    projectInputTokens: 0,
    projectOutputTokens: 0,
    projectTotalTokens: 0,
  },
  issues: {
    list: [] as ProjectIssue[],
    loading: false,
    loadedOnce: false,
  },
  branchPopover: {
    open: false,
    anchorTop: false,
    branches: [] as string[],
    newBranch: "",
  },
  mentionResults: [] as string[],
  draftObjective: "",
  draftImages: [] as ImageAttachment[],
  imagePreviewUrl: "",
  jiraKeyInput: "",
  commitMsgInput: "",
  terminal: {
    open: false,
    running: false,
    cmdInput: "",
    entries: [] as TerminalEntry[],
  },
  diffViewMode: "unified" as "unified" | "split",
  editorFind: {
    open: false,
    showReplace: false,
    query: "",
    replaceText: "",
    matchIndex: 0,
    matches: [] as Array<{ start: number; end: number }>,
  },
};

let laneSeq = 1;
let projectSeq = 1;
let chatFlushRaf = 0;
let editorHighlightRaf = 0;
let editorHighlightTimer = 0;
let lastRenderedGutterLines = -1;
let mentionTimer = 0;
let quickOpenTimer = 0;
let globalSearchTimer = 0;
let explorerFilterTimer = 0;

function normalizePathKey(p: string): string {
  return (p || "").trim().replace(/\\/g, "/").toLowerCase();
}

function hasDirtyTabs(): boolean {
  return state.editorTabs.some((t) => t.kind === "file" && t.dirty);
}

function hasAnyProjectDirtyTabs(): boolean {
  if (hasDirtyTabs()) return true;
  return state.projectTabs.some((ctx) =>
    ctx.id !== state.activeProjectId && ctx.editorTabs.some((t) => t.kind === "file" && t.dirty)
  );
}

function saveActiveToProjectContext() {
  if (!state.project || !state.activeProjectId) return;
  const ctx = state.projectTabs.find((p) => p.id === state.activeProjectId);
  if (!ctx) return;
  ctx.project = state.project;
  ctx.sidebarView = state.sidebarView;
  ctx.expandedDirs = state.expandedDirs;
  ctx.dirCache = state.dirCache;
  ctx.explorerFilter = state.explorerFilter;
  ctx.explorerFilterResults = state.explorerFilterResults;
  ctx.editorTabs = state.editorTabs;
  ctx.activeEditorPath = state.activeEditorPath;
  ctx.lanes = state.lanes;
  ctx.activeLaneId = state.activeLaneId;
  ctx.runMode = state.runMode;
  ctx.diffs = state.diffs;
  ctx.ghStatus = state.ghStatus;
  ctx.textSearch = state.textSearch;
  ctx.sessionsSummary = state.sessionsSummary;
  ctx.issues = state.issues;
  ctx.draftObjective = state.draftObjective;
  ctx.draftImages = state.draftImages;
  ctx.jiraKeyInput = state.jiraKeyInput;
  ctx.commitMsgInput = state.commitMsgInput;
  ctx.terminal = state.terminal;
}

function loadFromProjectContext(ctx: ProjectWorkspaceContext) {
  state.activeProjectId = ctx.id;
  state.project = ctx.project;
  state.sidebarView = ctx.sidebarView;
  state.expandedDirs = ctx.expandedDirs;
  state.dirCache = ctx.dirCache;
  state.explorerFilter = ctx.explorerFilter;
  state.explorerFilterResults = ctx.explorerFilterResults;
  state.editorTabs = ctx.editorTabs;
  state.activeEditorPath = ctx.activeEditorPath;
  state.lanes = ctx.lanes;
  state.activeLaneId = ctx.activeLaneId;
  state.runMode = ctx.runMode;
  state.diffs = ctx.diffs;
  state.ghStatus = ctx.ghStatus;
  state.textSearch = ctx.textSearch;
  state.sessionsSummary = ctx.sessionsSummary;
  state.issues = ctx.issues;
  state.draftObjective = ctx.draftObjective;
  state.draftImages = ctx.draftImages;
  state.jiraKeyInput = ctx.jiraKeyInput;
  state.commitMsgInput = ctx.commitMsgInput;
  state.terminal = ctx.terminal;
  state.mentionResults = [];
  state.branchPopover.open = false;
}

function findLaneWithContext(laneId: string): { lane: TeamLane; ctx?: ProjectWorkspaceContext; isActiveProject: boolean } | null {
  saveActiveToProjectContext();
  for (const ctx of state.projectTabs) {
    const found = ctx.lanes.find((l) => l.id === laneId);
    if (found) {
      return { lane: found, ctx, isActiveProject: ctx.id === state.activeProjectId };
    }
  }
  const activeFound = state.lanes.find((l) => l.id === laneId);
  if (activeFound) {
    return { lane: activeFound, isActiveProject: true };
  }
  return null;
}

function switchProjectTab(projectId: string) {
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
  const gitBadge = document.getElementById("git-badge");
  if (gitBadge) {
    gitBadge.textContent = String(state.diffs.length);
    gitBadge.style.display = state.diffs.length > 0 ? "inline-block" : "none";
  }
  setStatus(`Projeto ativo: ${target.project.name} (${target.project.root})`);
}

function closeProjectTab(projectId: string) {
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

function renderProjectTabsBar() {
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
        <div class="project-tab ${isAct ? "active" : ""}" data-proj-tab="${escapeHtml(ctx.id)}" title="${escapeHtml(ctx.project.root)} — Contexto e Chat 100% isolados">
          ${icons.folderOpen}
          <span>${escapeHtml(ctx.project.name)}</span>
          <span class="proj-branch-tag">${escapeHtml(branch)}</span>
          ${isRunning ? `<span class="proj-run-dot" title="Equipe executando em segundo plano neste projeto"></span>` : ""}
          ${isDirty ? `<span class="proj-dirty-dot" title="Arquivos com edições não salvas">${icons.dot}</span>` : ""}
          ${state.projectTabs.length > 1
            ? `<span class="proj-close-x" data-close-proj="${escapeHtml(ctx.id)}" title="Fechar aba deste projeto">${icons.closeSmall}</span>`
            : ""
          }
        </div>
      `;
    }).join("")}
    <button class="project-tab-add" id="btn-add-project-tab" title="Abrir outro projeto em uma nova aba isolada (Ctrl+Shift+O)">
      ${icons.plus}
      <span>Novo Projeto em Aba</span>
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

function askConfirm(dialog: ConfirmDialogState) {
  state.confirmModal = dialog;
  renderModalLayer();
}

function buildAgentRuntimes(teamName: string): AgentRuntime[] {
  const teams = state.project?.teamConfig?.teams || [];
  const t = teams.find((x) => x.name === teamName) ?? teams[0];
  if (!t) return [];
  return (t.agents || []).map((a) => ({
    name: a.name,
    role: a.role,
    model: a.model,
    status: "idle",
    inTokens: 0,
    outTokens: 0,
    tokens: 0,
    calls: 0,
  }));
}

function createLane(teamName: string): TeamLane {
  const id = `lane-${laneSeq++}`;
  return {
    id,
    teamName,
    sessionId: "",
    running: false,
    stage: "",
    doneStages: [],
    autoScroll: true,
    turnInTokens: 0,
    turnOutTokens: 0,
    turnTotalTokens: 0,
    sessionInTokens: 0,
    sessionOutTokens: 0,
    sessionTotalTokens: 0,
    agents: buildAgentRuntimes(teamName),
    messages: [
      {
        id: `sys-${Date.now()}`,
        kind: "system",
        label: "Sistema",
        text: `Arquitetura **Chefe → Devs Paralelos → QA → Fecho** pronta (\`${state.project?.stack.summary ?? "detectando..."}\`). Escolha o modo acima (**Auto**, **Direto**, **Planejar** ou **Squad**), cole prints com \`Ctrl+V\` ou envie seleção com \`Ctrl+L\`.`,
      },
    ],
  };
}

function activeLane(): TeamLane | undefined {
  return state.lanes.find((l) => l.id === state.activeLaneId) ?? state.lanes[0];
}

function setStatus(msg: string) {
  state.statusText = msg;
  const el = document.getElementById("status-text");
  if (el) el.textContent = msg;
}

type ToastKind = "info" | "success" | "error";

function toast(text: string, kind: ToastKind = "info", opts?: { timeoutMs?: number; retry?: () => void }): void {
  let layer = document.getElementById("toast-layer");
  if (!layer) {
    layer = document.createElement("div");
    layer.id = "toast-layer";
    layer.className = "toast-layer";
    document.body.appendChild(layer);
  }
  const box = document.createElement("div");
  box.className = `toast ${kind}`;
  box.setAttribute("role", kind === "error" ? "alert" : "status");
  const msg = document.createElement("span");
  msg.className = "toast-msg";
  msg.textContent = text;
  box.appendChild(msg);
  if (opts?.retry) {
    const retryBtn = document.createElement("button");
    retryBtn.className = "mini toast-retry";
    retryBtn.textContent = "Tentar novamente";
    retryBtn.addEventListener("click", () => {
      box.remove();
      opts.retry!();
    });
    box.appendChild(retryBtn);
  }
  const closeBtn = document.createElement("button");
  closeBtn.className = "toast-close";
  closeBtn.setAttribute("aria-label", "Fechar aviso");
  closeBtn.innerHTML = icons.closeSmall;
  closeBtn.addEventListener("click", () => box.remove());
  box.appendChild(closeBtn);
  layer.appendChild(box);
  // Com caminho de recuperação disponível, o aviso fica até a pessoa decidir.
  if (!opts?.retry) {
    window.setTimeout(() => box.remove(), opts?.timeoutMs ?? (kind === "error" ? 8000 : 5000));
  }
}

function toastError(what: string, err: any, retry?: () => void): void {
  const reason = err instanceof Error ? err.message : typeof err === "string" ? err : err?.message || "";
  const clean = String(reason || "").trim();
  toast(`Não foi possível ${what}${clean ? `: ${clean}` : "."}`, "error", retry ? { retry } : undefined);
}

function mountShell() {
  const rootEl = document.getElementById("app");
  if (!rootEl) return;
  rootEl.innerHTML = `
    <div class="ide-shell">
      <header class="titlebar" id="titlebar">
        <span class="brand-logo">${icons.brand}<span>HiveMind</span></span>
        <div class="dropdown">
          <button class="ghost titlebar-interactive" id="btn-file-menu">Arquivo</button>
          <div class="dropdown-menu" id="file-menu-dropdown" style="display:none"></div>
        </div>
        <button class="jb-vcs-pill titlebar-interactive" id="btn-header-vcs" title="Branch Git (clique para trocar ou criar)">
          ${icons.gitSmall}
          <span id="header-vcs-label">sem git</span>
        </button>
        <div class="spacer"></div>
        <button class="title-center-cmd titlebar-interactive" id="btn-quick-open" title="Buscar arquivos (Ctrl+P) ou Comandos (Ctrl+K)">
          ${icons.search}
          <span class="cmd-proj" id="title-cmd-proj">Buscar arquivo (Ctrl+P)</span>
          <span class="stack-tag" id="title-stack-tag" style="display:none"></span>
        </button>
        <div class="spacer"></div>
        <button class="jb-swarm-btn titlebar-interactive" id="btn-header-swarm" title="Abrir Quadro Multiagente (Ctrl+Shift+A)">
          ${icons.swarm}
          <span>Quadro</span>
        </button>
        <button class="jb-run-btn titlebar-interactive" id="btn-header-run" title="Rodar comando de teste/build da stack no Terminal">
          ${icons.play}
          <span id="header-run-label">Testar</span>
        </button>
        <button class="icon-btn titlebar-interactive" id="btn-cmd-palette" title="Paleta de Comandos (Ctrl+K)">${icons.command}</button>
        <button class="icon-btn titlebar-interactive" id="btn-toggle-term" title="Alternar Terminal (Ctrl+J)">${icons.terminal}</button>
        <button class="icon-btn titlebar-interactive" id="btn-toggle-left" title="Alternar barra lateral">${icons.sidebarLeft}</button>
        <button class="icon-btn titlebar-interactive" id="btn-toggle-right" title="Alternar painel AI">${icons.sidebarRight}</button>
        <div class="win-controls">
          <button class="win-btn" id="win-min" title="Minimizar">${icons.winMin}</button>
          <button class="win-btn" id="win-max" title="Maximizar / Restaurar">${icons.winMax}</button>
          <button class="win-btn close" id="win-close" title="Fechar">${icons.winClose}</button>
        </div>
      </header>

      <div class="project-tabs-bar" id="project-tabs-bar"></div>

      <div class="workbench">
        <nav class="activity-bar" id="activity-bar">
          <button class="act-btn active" data-act="explorer" title="Explorador de Arquivos">${icons.explorer}</button>
          <button class="act-btn" data-act="search" title="Busca Global no Projeto (Ctrl+Shift+F)">${icons.searchLarge}</button>
          <button class="act-btn" data-act="team" title="Equipe & Modelos">${icons.team}</button>
          <button class="act-btn" data-act="git" title="Git / Diffs / PR / Issues">
            ${icons.gitBranch}
            <span class="act-badge" id="git-badge" style="display:none">0</span>
          </button>
          <button class="act-btn" data-act="sessions" title="Sessões Salvas & Consumo de Tokens">${icons.sessions}</button>
          <div class="spacer"></div>
          <button class="act-btn" id="act-mission" title="Quadro Multiagente (Ctrl+Shift+A)">${icons.swarm}</button>
          <button class="act-btn" id="act-wizard" title="Configurar Equipe (.hivemind/team.yaml)">${icons.settings}</button>
        </nav>

        <aside class="left-sidebar" id="left-sidebar" style="width:${state.leftWidth}px"></aside>
        <div class="resizer-v" id="resizer-left"></div>

        <section class="center-area" id="center-area">
          <div class="editor-tabs" id="editor-tabs"></div>
          <div class="editor-body-wrap" id="editor-body"></div>
          <div class="terminal-drawer hidden" id="terminal-drawer"></div>
        </section>

        <div class="resizer-v" id="resizer-right"></div>
        <aside class="right-ai-pane" id="right-pane" style="width:${state.rightWidth}px"></aside>
      </div>

      <footer class="status-bar">
        <button type="button" class="status-chip" id="status-branch" title="Clique para trocar ou criar branch Git">${icons.gitSmall} sem git</button>
        <span id="status-stack"></span>
        <span id="status-team">Equipe: -</span>
        <button type="button" class="status-chip" id="status-tokens" title="Clique para abrir Sessões & Consumo de Tokens">${icons.tokens} 0 tok</button>
        <button type="button" id="status-agy" class="dim plain-chip" title="Clique para abrir Doctor (agy)">agy: verificando…</button>
        <button type="button" id="status-opencode" class="dim plain-chip" title="Clique para configurar Token OpenCode Zen (API portátil)">opencode: …</button>
        <button type="button" class="ghost mini" id="status-term-btn" title="Abrir/fechar Terminal (Ctrl+J)">${icons.terminal}<span>Terminal</span></button>
        <span id="status-text">Pronto</span>
        <div class="spacer"></div>
        <span id="status-env"></span>
      </footer>
    </div>
    <div id="branch-popover-layer"></div>
    <div id="quick-open-layer"></div>
    <div id="modal-layer"></div>
  `;

  bindStaticShellEvents();
}

function openSidebarView(view: SidebarView) {
  state.sidebarView = view;
  state.showLeftSidebar = true;
  syncActivityButtons();
  syncLayoutVisibility();
  renderLeftSidebar();
}

function openMissionControlTab() {
  const existing = state.editorTabs.find((t) => t.relPath === MISSION_TAB_KEY);
  if (!existing) {
    state.editorTabs.unshift({
      kind: "mission",
      relPath: MISSION_TAB_KEY,
      name: "Quadro Multiagente",
      content: "",
      dirty: false,
    });
  }
  state.activeEditorPath = MISSION_TAB_KEY;
  renderEditorTabs();
  renderEditorBody();
}

async function openTeamMemoryFile() {
  const backend = api();
  if (!backend || !state.project) return;
  try {
    const rel = await backend.EnsureTeamMemory(state.project.root);
    await refreshExplorerTree();
    await openFileInEditor(rel, "MEMORY.md");
    setStatus("Memória compartilhada (.hivemind/MEMORY.md) aberta no editor.");
  } catch (err: any) {
    toastError("abrir a memória da equipe (.hivemind/MEMORY.md)", err);
  }
}

async function toggleBranchPopover(anchorTop: boolean) {
  if (!state.project) return;
  if (!state.project.branch) {
    openSidebarView("git");
    return;
  }
  if (state.branchPopover.open && state.branchPopover.anchorTop === anchorTop) {
    state.branchPopover.open = false;
    renderBranchPopover();
    return;
  }
  try {
    const branches = (await api()?.ListBranches(state.project.root)) || [];
    state.branchPopover.branches = branches;
    state.branchPopover.newBranch = "";
    state.branchPopover.anchorTop = anchorTop;
    state.branchPopover.open = true;
    renderBranchPopover();
  } catch (err: any) {
    toastError("listar as branches do Git", err);
  }
}

async function openDoctorModal() {
  const backend = api();
  if (!backend) return;
  setStatus("Verificando agy…");
  const status = await backend.DoctorAgy();
  state.agyStatus = status;
  if (status.LoggedIn) {
    state.models = (await backend.ListModels()) || state.models;
  }
  state.doctorModal = status;
  updateStatusBar();
  renderModalLayer();
  setStatus("Pronto");
}

async function openOpenCodeModal() {
  state.showOpenCodeModal = true;
  renderModalLayer();
  const backend = api();
  if (!backend?.BootstrapOpenCode) return;
  try {
    const res = await backend.BootstrapOpenCode(true);
    state.openCodeStatus = res.status;
    if (res.goModels && res.goModels.length > 0) {
      state.openCodeGoModels = res.goModels;
    }
    if (res.models && res.models.length > 0) {
      state.openCodeModels = res.models;
    }
    updateStatusBar();
    renderModalLayer();
  } catch {
    // keep modal open for token entry
  }
}

function bindStaticShellEvents() {
  const titlebar = document.getElementById("titlebar");
  titlebar?.addEventListener("dblclick", (e) => {
    const target = e.target as HTMLElement;
    if (target.closest(".titlebar-interactive") || target.closest(".win-controls") || target.closest("button")) {
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
  document.getElementById("btn-toggle-left")?.addEventListener("click", () => {
    state.showLeftSidebar = !state.showLeftSidebar;
    syncLayoutVisibility();
  });
  document.getElementById("btn-toggle-right")?.addEventListener("click", () => {
    state.showRightAI = !state.showRightAI;
    syncLayoutVisibility();
  });

  document.getElementById("btn-header-swarm")?.addEventListener("click", () => openMissionControlTab());
  document.getElementById("act-mission")?.addEventListener("click", () => openMissionControlTab());

  document.getElementById("btn-header-run")?.addEventListener("click", () => {
    const cmd = state.project?.stack?.testHint || "git status";
    if (!state.terminal.open) {
      state.terminal.open = true;
      renderTerminalDrawer();
    }
    runTerminalCommand(cmd);
  });

  document.getElementById("btn-cmd-palette")?.addEventListener("click", () => openCommandPalette());
  document.getElementById("status-agy")?.addEventListener("click", () => openDoctorModal());
  document.getElementById("status-opencode")?.addEventListener("click", () => openOpenCodeModal());

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
    if (state.showFileMenu) {
      state.showFileMenu = false;
      renderFileMenu();
    }
    if (state.branchPopover.open) {
      const target = e.target as HTMLElement;
      if (!target.closest("#branch-popover-layer") && !target.closest("#status-branch") && !target.closest("#btn-header-vcs")) {
        state.branchPopover.open = false;
        renderBranchPopover();
      }
    }
  });

  document.getElementById("act-wizard")?.addEventListener("click", () => {
    state.showWizard = true;
    renderModalLayer();
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
      }
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
    state.leftWidth = Math.max(190, Math.min(480, state.leftWidth + dx));
    const el = document.getElementById("left-sidebar");
    if (el) el.style.width = `${state.leftWidth}px`;
  });
  setupResizer("resizer-right", (dx) => {
    state.rightWidth = Math.max(310, Math.min(680, state.rightWidth - dx));
    const el = document.getElementById("right-pane");
    if (el) el.style.width = `${state.rightWidth}px`;
  });

  document.addEventListener("keydown", (e) => {
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
      openCommandPalette();
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
      } else if (state.showOpenCodeModal) {
        state.showOpenCodeModal = false;
        renderModalLayer();
      } else if (state.doctorModal) {
        state.doctorModal = null;
        renderModalLayer();
      } else if (state.showWizard) {
        state.showWizard = false;
        renderModalLayer();
      }
    }
  });
}

function renderBranchPopover() {
  const layer = document.getElementById("branch-popover-layer");
  if (!layer) return;
  if (!state.branchPopover.open || !state.project) {
    layer.innerHTML = "";
    return;
  }
  const cur = state.project.branch;
  const posStyle = state.branchPopover.anchorTop
    ? "top:44px;bottom:auto;left:110px"
    : "bottom:30px;left:12px";

  layer.innerHTML = `
    <div class="branch-popover" style="${posStyle}">
      <div class="eyebrow">${icons.gitSmall}<span>Branches Git</span></div>
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
      if (!state.project || targetBranch === state.project.branch) {
        state.branchPopover.open = false;
        renderBranchPopover();
        return;
      }
      const performSwitch = async () => {
        if (!state.project) return;
        try {
          await api()?.CheckoutBranch(state.project.root, targetBranch, false);
          state.project.branch = targetBranch;
          state.branchPopover.open = false;
          renderBranchPopover();
          renderProjectTabsBar();
          updateHeaderAndStatusBar();
          refreshDiffs();
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
      await api()?.CheckoutBranch(state.project.root, name, true);
      state.project.branch = name;
      state.branchPopover.open = false;
      renderBranchPopover();
      renderProjectTabsBar();
      updateHeaderAndStatusBar();
      refreshDiffs();
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

function setupResizer(id: string, onMove: (dx: number) => void) {
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

function syncLayoutVisibility() {
  const left = document.getElementById("left-sidebar");
  const resL = document.getElementById("resizer-left");
  const right = document.getElementById("right-pane");
  const resR = document.getElementById("resizer-right");
  left?.classList.toggle("hidden", !state.showLeftSidebar);
  if (resL) resL.style.display = state.showLeftSidebar ? "" : "none";
  right?.classList.toggle("hidden", !state.showRightAI);
  if (resR) resR.style.display = state.showRightAI ? "" : "none";
}

function syncActivityButtons() {
  document.querySelectorAll("[data-act]").forEach((el) => {
    const view = (el as HTMLElement).dataset.act;
    el.classList.toggle("active", state.showLeftSidebar && state.sidebarView === view);
  });
}

function renderFileMenu() {
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
    <button class="dropdown-item" id="menu-open-memory">${icons.memory}<span>Memória do Time (.hivemind/MEMORY.md)</span></button>
    <button class="dropdown-item" id="menu-new-team">${icons.settings}<span>Configurar Equipe…</span></button>
    <button class="dropdown-item" id="menu-opencode">${icons.bolt}<span>Token OpenCode Zen (API portátil)…</span></button>
    <button class="dropdown-item" id="menu-doctor">${icons.bolt}<span>Doctor — Antigravity CLI (agy)</span></button>
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
  menu.querySelector("#menu-open-memory")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    openTeamMemoryFile();
  });
  menu.querySelector("#menu-new-team")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    state.showWizard = true;
    renderModalLayer();
  });
  menu.querySelector("#menu-opencode")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    openOpenCodeModal();
  });
  menu.querySelector("#menu-doctor")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    openDoctorModal();
  });
  menu.querySelectorAll("[data-recent]").forEach((el) => {
    el.addEventListener("click", () => openProjectPath((el as HTMLElement).dataset.recent!, true));
  });
}

function updateHeaderAndStatusBar() {
  const proj = state.project;
  const cmdProj = document.getElementById("title-cmd-proj");
  const stackTag = document.getElementById("title-stack-tag");
  const headerVcs = document.getElementById("header-vcs-label");
  const headerRun = document.getElementById("header-run-label");

  if (cmdProj) {
    cmdProj.textContent = proj ? `${proj.name} — Arquivos (Ctrl+P) · Comandos (Ctrl+K)` : "Buscar arquivo (Ctrl+P)";
  }
  if (stackTag) {
    if (proj?.stack?.summary) {
      stackTag.textContent = proj.stack.summary;
      stackTag.style.display = "inline-block";
    } else {
      stackTag.style.display = "none";
    }
  }
  if (headerVcs) {
    headerVcs.textContent = proj?.branch || "sem git";
  }
  if (headerRun) {
    const hint = proj?.stack?.testHint || "Testar";
    headerRun.textContent = hint.length > 18 ? "Testar Stack" : hint;
  }
  updateStatusBar();
}

function updateStatusBar() {
  const proj = state.project;
  const lane = activeLane();
  const branchEl = document.getElementById("status-branch");
  const stackEl = document.getElementById("status-stack");
  const teamEl = document.getElementById("status-team");
  const tokEl = document.getElementById("status-tokens");
  const agyEl = document.getElementById("status-agy");
  const envEl = document.getElementById("status-env");

  if (branchEl) {
    branchEl.innerHTML = `${icons.gitSmall} <span>${escapeHtml(proj?.branch || "sem git")}</span>`;
  }
  if (stackEl) stackEl.textContent = proj?.stack?.summary || "";
  const perm = proj?.teamConfig?.settings?.permission_mode || "governed";
  if (teamEl) teamEl.textContent = `Equipe: ${lane?.teamName || "-"} (${lane?.agents.length || 0}) · ${runModeLabel(state.runMode)} · ${permissionModeLabel(perm)}`;
  if (tokEl) {
    const sessTok = lane?.sessionTotalTokens || 0;
    const projTok = Math.max(state.sessionsSummary.projectTotalTokens, sessTok);
    tokEl.innerHTML = `${icons.tokens} <span>Sessão: ${formatTokens(sessTok)} · Total: ${formatTokens(projTok)} tok</span>`;
    tokEl.title = `Sessão atual: ${exactTokens(sessTok)} tok (Entrada: ${exactTokens(lane?.sessionInTokens)} / Saída: ${exactTokens(lane?.sessionOutTokens)}) | Projeto: ${exactTokens(projTok)} tok — Clique para ver histórico`;
  }
  if (agyEl) {
    if (!state.agyStatus) {
      agyEl.textContent = "agy: verificando…";
      agyEl.style.color = "";
    } else if (state.agyStatus.LoggedIn) {
      agyEl.textContent = `agy: ok (${state.models.length} modelos)`;
      agyEl.style.color = "var(--green)";
    } else {
      agyEl.textContent = `agy: ${state.agyStatus.Detail}`;
      agyEl.style.color = "var(--amber)";
    }
  }
  const ocEl = document.getElementById("status-opencode");
  if (ocEl) {
    const oc = state.openCodeStatus;
    if (!oc) {
      ocEl.textContent = "opencode: …";
      ocEl.style.color = "";
    } else if (oc.configured) {
      ocEl.textContent = `opencode: Go ${state.openCodeGoModels.length} · Zen ${state.openCodeModels.length}`;
      ocEl.style.color = "var(--green)";
      ocEl.title = oc.detail;
    } else {
      ocEl.textContent = `opencode: configurar token (Go ${state.openCodeGoModels.length} · Zen ${state.openCodeModels.length})`;
      ocEl.style.color = "var(--text-dim)";
      ocEl.title = oc.detail || "Clique para configurar OPENCODE_API_KEY";
    }
  }
  if (envEl) envEl.textContent = `${state.goVersion} · ${state.os}`;
}

async function boot() {
  mountShell();
  const backend = api();
  if (!backend) {
    renderLeftSidebar();
    renderEditorTabs();
    renderEditorBody();
    renderRightPane();
    setStatus("Modo preview (sem runtime Wails)");
    return;
  }

  window.runtime?.EventsOn("hivemind:lane-event", (payload: any) => {
    const hit = findLaneWithContext(payload.laneId);
    if (!hit) return;
    const { lane, ctx, isActiveProject } = hit;
    const targetSummary = isActiveProject
      ? state.sessionsSummary
      : (ctx?.sessionsSummary ?? state.sessionsSummary);
    const ev = payload.event;
    if (ev.stage) {
      if (lane.stage && lane.stage !== ev.stage && !lane.doneStages.includes(lane.stage)) {
        lane.doneStages.push(lane.stage);
      }
      lane.stage = ev.stage;
    }

    if (ev.kind === "stage") {
      // stage updated above
    } else if (ev.kind === "usage") {
      const inTok = Number(ev.inputTokens || 0);
      const outTok = Number(ev.outputTokens || 0);
      const totTok = Number(ev.tokens || inTok + outTok);
      lane.turnInTokens += inTok;
      lane.turnOutTokens += outTok;
      lane.turnTotalTokens += totTok;
      lane.sessionInTokens += inTok;
      lane.sessionOutTokens += outTok;
      lane.sessionTotalTokens += totTok;
      targetSummary.projectInputTokens += inTok;
      targetSummary.projectOutputTokens += outTok;
      targetSummary.projectTotalTokens += totTok;

      const ag = matchAgentRuntime(lane.agents, ev.author, ev.label);
      if (ag) {
        ag.inTokens += inTok;
        ag.outTokens += outTok;
        ag.tokens += totTok;
        ag.calls += 1;
      }
      if (isActiveProject) updateStatusBar();
    } else if (ev.kind === "plan_ready") {
      markToolsDone(lane);
      lane.messages.push({
        id: `plan-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        kind: "plan",
        label: ev.label || "Plano pronto para aprovação",
        text: ev.text || "",
      });
    } else if (ev.kind === "approval_required") {
      markToolsDone(lane);
      lane.messages.push({
        id: `appr-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        kind: "approval",
        label: ev.label || "Permissão Solicitada pelo Chefe",
        text: ev.text || "",
      });
    } else if (ev.kind === "devs_scaled") {
      if (Array.isArray(ev.devs) && ev.devs.length > 0) {
        syncDynamicDevsInLane(lane, ev.devs.map(String));
      }
    } else if (ev.kind === "handoff") {
      markToolsDone(lane);
      lane.messages.push({
        id: `ho-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        kind: "handoff",
        label: ev.label || "Handoff da Equipe",
        text: ev.text || "",
      });
    } else if (ev.kind === "tool_call") {
      updateAgentStatus(lane, ev.author, ev.label, "running", ev.toolName);
      const last = lane.messages[lane.messages.length - 1];
      const toolLabel = ev.label || ev.author || "Agente";
      const baseText = `${ev.toolName}`;
      if (last && last.kind === "tool" && last.label === toolLabel && last.text.startsWith(baseText)) {
        const match = last.text.match(/×(\d+)$/);
        const count = match ? parseInt(match[1], 10) + 1 : 2;
        last.text = `${baseText} ×${count}`;
        last.active = true;
        if (ev.detail) {
          last.toolDetail = last.toolDetail ? `${last.toolDetail}\n${ev.detail}` : ev.detail;
        }
        if (!last.startedAt) last.startedAt = Date.now();
      } else {
        markToolsDone(lane);
        lane.messages.push({
          id: `tool-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          kind: "tool",
          label: toolLabel,
          text: baseText,
          active: true,
          toolName: ev.toolName,
          toolDetail: ev.detail || "",
          startedAt: Date.now(),
        });
      }
    } else if (ev.kind === "tool_result") {
      markToolsDone(lane, ev.detail);
    } else if (ev.kind === "text" && !ev.thought && ev.text) {
      markToolsDone(lane);
      updateAgentStatus(lane, ev.author, ev.label, "running", "");
      const last = lane.messages[lane.messages.length - 1];
      const msgLabel = ev.label || ev.author || "Chefe";
      const evTok = !ev.partial && ev.tokens ? Number(ev.tokens) : undefined;
      if (last && last.kind === "chief" && last.label === msgLabel && ev.partial) {
        last.text += ev.text;
      } else if (!ev.partial && last && last.kind === "chief" && last.label === msgLabel) {
        last.text = ev.text;
        if (evTok) last.tokens = evTok;
      } else {
        lane.messages.push({
          id: `chief-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          kind: "chief",
          label: msgLabel,
          text: ev.text,
          tokens: evTok,
        });
      }
    } else if (ev.kind === "error") {
      markToolsDone(lane);
      lane.messages.push({
        id: `err-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        kind: "error",
        label: "Erro",
        text: ev.text || "Erro desconhecido",
      });
    } else if (ev.kind === "finish") {
      markToolsDone(lane);
      if (lane.stage && !lane.doneStages.includes(lane.stage)) {
        lane.doneStages.push(lane.stage);
      }
      lane.stage = "";
      lane.agents.forEach((a) => {
        if (a.status === "running") a.status = "done";
        a.tool = "";
      });
      const tokSuffix = lane.turnTotalTokens > 0
        ? ` · ${exactTokens(lane.turnTotalTokens)} tokens no turno (Entrada: ${exactTokens(lane.turnInTokens)} / Saída: ${exactTokens(lane.turnOutTokens)})`
        : "";
      lane.messages.push({
        id: `sys-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        kind: "system",
        label: "Sistema",
        text: `${ev.text || "Execução concluída."}${tokSuffix}`,
      });
      if (isActiveProject) {
        refreshDiffsAndSummarize(lane);
        refreshSessions();
      }
    }

    if (!isActiveProject) {
      renderProjectTabsBar();
      return;
    }

    if (lane.id === activeLane()?.id) {
      scheduleChatFlush();
      updateMissionBoardIfOpen();
    } else {
      renderLaneBarOnly();
    }
    if (state.sidebarView === "team" && state.showLeftSidebar) {
      updateTeamStatusPills();
    }
  });

  window.runtime?.EventsOn("hivemind:terminal-output", (payload: any) => {
    const wsKey = normalizePathKey(payload?.workspace || "");
    const chunk = String(payload?.chunk || "");
    if (!wsKey || !chunk) return;
    saveActiveToProjectContext();
    const targetCtx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === wsKey);
    const term = targetCtx
      ? targetCtx.terminal
      : (state.project && normalizePathKey(state.project.root) === wsKey ? state.terminal : null);
    if (!term) return;
    const runningEntry = [...term.entries].reverse().find((e) => e.running);
    if (!runningEntry) return;
    if (runningEntry.output === "Executando…") {
      runningEntry.output = chunk;
    } else {
      runningEntry.output += chunk;
    }
    if (state.project && normalizePathKey(state.project.root) === wsKey && state.terminal.open) {
      const liveNode = document.querySelector<HTMLElement>(`[data-term-out="${CSS.escape(runningEntry.id)}"]`);
      if (liveNode) {
        liveNode.textContent = runningEntry.output;
        const outBox = document.getElementById("terminal-output");
        if (outBox) outBox.scrollTop = outBox.scrollHeight;
      }
    }
  });

  try {
    const init = await backend.GetInitialState();
    state.recent = init.recentProjects || [];
    state.models = init.models || [];
    if (init.agyStatus?.Found) state.agyStatus = init.agyStatus;
    if (init.openCodeGoModels && init.openCodeGoModels.length > 0) state.openCodeGoModels = init.openCodeGoModels;
    if (init.openCodeModels && init.openCodeModels.length > 0) state.openCodeModels = init.openCodeModels;
    if (init.openCodeStatus && (init.openCodeStatus.configured || init.openCodeStatus.modelCount > 0)) {
      state.openCodeStatus = init.openCodeStatus;
    }
    state.goVersion = init.goVersion || "";
    state.os = init.os || "windows";
    applyProject(init.initialProject);
    if (!init.initialProject?.hasTeam) {
      state.showWizard = true;
      renderModalLayer();
    }
    bootstrapAsync(init.initialProject?.root);
  } catch (err: any) {
    toastError("iniciar a conexão com o backend (reinicie o app se o problema persistir)", err);
  }
}

function bootstrapAsync(rootDir?: string) {
  const backend = api();
  if (!backend) return;

  backend.BootstrapAgy(false).then((res) => {
    state.agyStatus = res.status;
    if (res.models && res.models.length > 0) {
      state.models = res.models;
    }
    updateStatusBar();
    updateMissionBoardIfOpen();
    if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
    if (state.showWizard || state.doctorModal) renderModalLayer();
  }).catch(() => {});

  backend.BootstrapOpenCode?.(false).then((res) => {
    state.openCodeStatus = res.status;
    if (res.goModels && res.goModels.length > 0) {
      state.openCodeGoModels = res.goModels;
    }
    if (res.models && res.models.length > 0) {
      state.openCodeModels = res.models;
    }
    updateStatusBar();
    updateMissionBoardIfOpen();
    if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
    if (state.showWizard || state.showOpenCodeModal) renderModalLayer();
  }).catch(() => {});

  if (rootDir) {
    const targetKey = normalizePathKey(rootDir);
    backend.GitHubStatus(rootDir).then((gh) => {
      const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
      if (ctx) ctx.ghStatus = gh;
      if (state.project && normalizePathKey(state.project.root) === targetKey) {
        state.ghStatus = gh;
        if (state.sidebarView === "git" && state.showLeftSidebar) {
          renderLeftSidebar();
        }
      }
    }).catch(() => {});

    refreshSessions(rootDir);
  }
}

async function refreshSessions(targetRoot?: string) {
  const backend = api();
  const root = targetRoot || state.project?.root;
  if (!backend || !root) return;
  const targetKey = normalizePathKey(root);
  try {
    const sum = await backend.ListSessions(root);
    const summaryObj = {
      sessions: sum?.sessions || [],
      projectInputTokens: Number(sum?.projectInputTokens || 0),
      projectOutputTokens: Number(sum?.projectOutputTokens || 0),
      projectTotalTokens: Number(sum?.projectTotalTokens || 0),
    };
    const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
    if (ctx) ctx.sessionsSummary = summaryObj;
    if (state.project && normalizePathKey(state.project.root) === targetKey) {
      state.sessionsSummary = summaryObj;
      updateStatusBar();
      updateMissionBoardIfOpen();
      if (state.sidebarView === "sessions" && state.showLeftSidebar) {
        renderLeftSidebar();
      }
    }
  } catch {
    // ignore empty workspace sessions
  }
}

function markToolsDone(lane: TeamLane, resultDetail?: string) {
  const now = Date.now();
  for (const m of lane.messages) {
    if (m.kind === "tool" && m.active) {
      m.active = false;
      if (m.startedAt && !m.durationMs) {
        m.durationMs = Math.max(1, now - m.startedAt);
      }
      if (resultDetail) {
        m.toolOutput = m.toolOutput ? `${m.toolOutput}\n${resultDetail}` : resultDetail;
      }
    }
  }
}

function syncDynamicDevsInLane(lane: TeamLane, devNames: string[]) {
  if (!devNames || devNames.length === 0) return;
  const existingByName = new Map<string, AgentRuntime>();
  for (const a of lane.agents) {
    existingByName.set(a.name.toLowerCase(), a);
  }
  const baseDev = lane.agents.find((a) => a.role === "developer");
  const baseDevModel = baseDev?.model || "agy";

  const beforeQA: AgentRuntime[] = [];
  const qaAndAfter: AgentRuntime[] = [];
  for (const a of lane.agents) {
    if (a.role === "developer") continue;
    if (a.role === "qa") {
      qaAndAfter.push(a);
    } else {
      beforeQA.push(a);
    }
  }

  const nextDevs: AgentRuntime[] = devNames.map((name) => {
    const prev = existingByName.get(name.toLowerCase());
    if (prev) return prev;
    return {
      name,
      role: "developer",
      model: baseDevModel,
      status: "running",
      inTokens: 0,
      outTokens: 0,
      tokens: 0,
      calls: 0,
    };
  });

  lane.agents = [...beforeQA, ...nextDevs, ...qaAndAfter];
}

function updateAgentStatus(lane: TeamLane, author: string, label: string | undefined, status: AgentRuntime["status"], tool: string) {
  const ag = matchAgentRuntime(lane.agents, author, label);
  if (ag) {
    ag.status = status;
    ag.tool = tool;
  }
}

function applyProject(proj: ProjectInfo, inNewTab = true) {
  saveActiveToProjectContext();
  const targetKey = normalizePathKey(proj.root);
  const alreadyOpen = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
  if (alreadyOpen) {
    alreadyOpen.project = proj;
    loadFromProjectContext(alreadyOpen);
    renderProjectTabsBar();
    updateHeaderAndStatusBar();
    renderLeftSidebar();
    renderEditorTabs();
    renderEditorBody();
    renderTerminalDrawer();
    renderRightPane();
    setStatus(`Projeto em foco: ${proj.root}`);
    refreshDiffs();
    return;
  }

  const activeTeam = proj.teamConfig?.active || proj.teamConfig?.teams?.[0]?.name || "principal";
  const tempPrevProj = state.project;
  state.project = proj;
  const firstLane = createLane(activeTeam);
  state.project = tempPrevProj;

  const dirCache = new Map<string, FileNode[]>();
  dirCache.set("", proj.files || []);

  const newCtx: ProjectWorkspaceContext = {
    id: (inNewTab || !state.activeProjectId) ? `proj-${projectSeq++}` : state.activeProjectId,
    project: proj,
    sidebarView: "explorer",
    expandedDirs: new Set<string>(),
    dirCache,
    explorerFilter: "",
    explorerFilterResults: [],
    editorTabs: [],
    activeEditorPath: "",
    lanes: [firstLane],
    activeLaneId: firstLane.id,
    runMode: "auto",
    diffs: [],
    ghStatus: null,
    textSearch: { query: "", results: [], searching: false },
    sessionsSummary: {
      sessions: [],
      projectInputTokens: 0,
      projectOutputTokens: 0,
      projectTotalTokens: 0,
    },
    issues: { list: [], loading: false, loadedOnce: false },
    draftObjective: "",
    draftImages: [],
    jiraKeyInput: "",
    commitMsgInput: "",
    terminal: { open: false, running: false, cmdInput: "", entries: [] },
  };

  if (inNewTab || state.projectTabs.length === 0) {
    state.projectTabs.push(newCtx);
  } else {
    const idx = state.projectTabs.findIndex((p) => p.id === state.activeProjectId);
    if (idx >= 0) state.projectTabs[idx] = newCtx;
    else state.projectTabs.push(newCtx);
  }

  loadFromProjectContext(newCtx);
  syncActivityButtons();
  syncLayoutVisibility();
  renderProjectTabsBar();
  updateHeaderAndStatusBar();
  renderLeftSidebar();
  renderEditorTabs();
  renderEditorBody();
  renderTerminalDrawer();
  renderRightPane();
  setStatus(`Projeto aberto em aba isolada: ${proj.root}`);
  refreshDiffs();
}

async function openFolderDialog(inNewTab = true) {
  const backend = api();
  if (!backend) return;
  const dir = await backend.ChooseDirectory();
  if (!dir) return;
  await openProjectPath(dir, inNewTab);
}

async function openProjectPath(dir: string, inNewTab = true) {
  const backend = api();
  if (!backend) return;
  const targetKey = normalizePathKey(dir);
  const existingTab = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
  if (existingTab) {
    state.showFileMenu = false;
    renderFileMenu();
    switchProjectTab(existingTab.id);
    return;
  }
  const doOpen = async () => {
    try {
      const proj = await backend.OpenProject(dir);
      state.recent = await backend.GetRecentProjects();
      state.showFileMenu = false;
      renderFileMenu();
      applyProject(proj, inNewTab);
      bootstrapAsync(proj.root);
      if (!proj.hasTeam) {
        state.showWizard = true;
        renderModalLayer();
      }
    } catch (err: any) {
      toastError("abrir a pasta do projeto", err);
    }
  };
  if (!inNewTab && hasDirtyTabs()) {
    askConfirm({
      title: "Substituir Projeto na Aba Atual",
      body: "Existem arquivos modificados não salvos nesta aba de projeto. Deseja substituir mesmo assim? (Dica: use 'Abrir Projeto em Nova Aba' para manter ambos abertos)",
      confirmLabel: "Substituir Projeto",
      danger: true,
      onConfirm: doOpen,
    });
    return;
  }
  await doOpen();
}

async function reloadCleanOpenTabs() {
  const backend = api();
  if (!backend || !state.project) return;
  let activeChanged = false;
  for (const tab of state.editorTabs) {
    if (tab.kind !== "file" || tab.dirty) continue;
    try {
      const fresh = await backend.ReadFile(state.project.root, tab.relPath);
      if (fresh !== tab.content) {
        tab.content = fresh;
        if (tab.relPath === state.activeEditorPath) activeChanged = true;
      }
    } catch {
      // File may have been removed or moved
    }
  }
  if (activeChanged) {
    renderEditorBody();
  }
}

function promptDiscardChanges() {
  if (!state.project) return;
  askConfirm({
    title: "Descartar Todas as Alterações",
    body: "Isso reverterá arquivos rastreados para HEAD e removerá arquivos novos não rastreados (mantendo .hivemind/). Deseja continuar?",
    confirmLabel: "Descartar Alterações",
    danger: true,
    onConfirm: async () => {
      if (!state.project) return;
      try {
        await api()?.DiscardChanges(state.project.root);
        setStatus("Alterações descartadas.");
        await refreshExplorerTree();
        await reloadCleanOpenTabs();
        await refreshDiffs();
      } catch (err: any) {
        toastError("descartar as alterações", err);
      }
    },
  });
}

function jumpEditorToLine(targetLine?: number) {
  if (!targetLine || targetLine <= 0) return;
  const ta = document.getElementById("editor-textarea") as HTMLTextAreaElement | null;
  const pre = document.getElementById("editor-highlight");
  const gutter = document.getElementById("editor-gutter");
  if (!ta) return;

  const lines = ta.value.split("\n");
  let charOffset = 0;
  const clamped = Math.min(targetLine, lines.length);
  for (let i = 0; i < clamped - 1; i++) {
    charOffset += lines[i].length + 1;
  }
  const lineEnd = charOffset + (lines[clamped - 1]?.length || 0);
  ta.focus();
  ta.setSelectionRange(charOffset, lineEnd);
  const topPx = Math.max(0, (clamped - 1) * 20 - 80);
  ta.scrollTop = topPx;
  if (pre) pre.scrollTop = topPx;
  if (gutter) gutter.scrollTop = topPx;
}

async function openFileInEditor(relPath: string, name: string, targetLine?: number) {
  const existing = state.editorTabs.find((t) => t.kind === "file" && t.relPath === relPath);
  if (existing) {
    state.activeEditorPath = existing.relPath;
    renderEditorTabs();
    renderEditorBody();
    highlightActiveTreeRow();
    jumpEditorToLine(targetLine);
    return;
  }
  const backend = api();
  if (!backend || !state.project) return;
  try {
    const content = await backend.ReadFile(state.project.root, relPath);
    state.editorTabs.push({
      kind: "file",
      relPath,
      name,
      content,
      dirty: false,
    });
    state.activeEditorPath = relPath;
    renderEditorTabs();
    renderEditorBody();
    highlightActiveTreeRow();
    jumpEditorToLine(targetLine);
  } catch (err: any) {
    toastError(`abrir o arquivo ${relPath}`, err);
  }
}

function sendEditorSelectionToChief() {
  const activeTab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
  if (!activeTab) return;
  const ta = document.getElementById("editor-textarea") as HTMLTextAreaElement | null;
  let snippet = `@${activeTab.relPath} `;
  if (ta) {
    const start = ta.selectionStart ?? 0;
    const end = ta.selectionEnd ?? 0;
    const startLine = ta.value.slice(0, start).split("\n").length;
    if (end > start) {
      const selected = ta.value.slice(start, end);
      const endLine = ta.value.slice(0, end).split("\n").length;
      const ext = activeTab.name.includes(".") ? activeTab.name.split(".").pop() || "" : "";
      snippet = `@${activeTab.relPath}:L${startLine}-L${endLine}\n\`\`\`${ext}\n${selected}\n\`\`\`\n`;
    } else {
      snippet = `@${activeTab.relPath}:L${startLine} `;
    }
  }
  const prefix = state.draftObjective && !state.draftObjective.endsWith("\n") && !state.draftObjective.endsWith(" ")
    ? state.draftObjective + "\n"
    : state.draftObjective;
  state.draftObjective = prefix + snippet;
  state.showRightAI = true;
  syncLayoutVisibility();
  const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
  if (chatInput) {
    chatInput.value = state.draftObjective;
    chatInput.focus();
    chatInput.selectionStart = chatInput.selectionEnd = chatInput.value.length;
  }
  setStatus(`Trecho de ${activeTab.name} anexado ao chat.`);
}

function openDiffInEditor(diff: FileDiff) {
  const key = `diff:${diff.path}`;
  const existing = state.editorTabs.find((t) => t.relPath === key);
  if (existing) {
    existing.diff = diff;
    state.activeEditorPath = key;
    renderEditorTabs();
    renderEditorBody();
    return;
  }
  state.editorTabs.push({
    kind: "diff",
    relPath: key,
    name: `Diff: ${diff.path.split("/").pop()}`,
    content: "",
    dirty: false,
    diff,
  });
  state.activeEditorPath = key;
  renderEditorTabs();
  renderEditorBody();
}

async function saveActiveEditor() {
  const tab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
  const backend = api();
  if (!tab || !backend || !state.project) return;
  try {
    await backend.SaveFile(state.project.root, tab.relPath, tab.content);
    tab.dirty = false;
    syncActiveTabDirtyUI(tab);
    setStatus(`Salvo: ${tab.relPath}`);
    refreshDiffs();
  } catch (err: any) {
    toastError("salvar o arquivo", err, () => { void saveActiveEditor(); });
  }
}

async function refreshDiffsAndSummarize(lane: TeamLane) {
  await refreshDiffs();
  if (state.diffs.length === 0) return;
  const last = lane.messages[lane.messages.length - 1];
  if (last && last.kind === "diff") return;
  const totalAdd = state.diffs.reduce((s, d) => s + d.added, 0);
  const totalRem = state.diffs.reduce((s, d) => s + d.removed, 0);
  lane.messages.push({
    id: `diff-${Date.now()}`,
    kind: "diff",
    label: `${state.diffs.length} arquivo(s) alterado(s) (+${totalAdd} / -${totalRem})`,
    text: "",
    diffItems: state.diffs.map((d) => ({ path: d.path, added: d.added, removed: d.removed })),
  });
  if (lane.id === activeLane()?.id) {
    scheduleChatFlush();
  }
}

function getGitStatusMap(): { files: Map<string, "M" | "U">; dirs: Set<string> } {
  const files = new Map<string, "M" | "U">();
  const dirs = new Set<string>();
  for (const d of state.diffs) {
    const p = (d.path || "").replace(/\\/g, "/");
    if (!p) continue;
    const isNew = d.lines.some((ln) => ln.kind === "header" && ln.text.startsWith("new file mode"));
    files.set(p, isNew ? "U" : "M");
    const parts = p.split("/");
    for (let i = 1; i < parts.length; i++) {
      dirs.add(parts.slice(0, i).join("/"));
    }
  }
  return { files, dirs };
}

async function refreshDiffs() {
  const backend = api();
  if (!backend || !state.project) return;
  try {
    state.diffs = (await backend.GetDiff(state.project.root)) || [];
    const badge = document.getElementById("git-badge");
    if (badge) {
      badge.textContent = String(state.diffs.length);
      badge.style.display = state.diffs.length > 0 ? "inline-block" : "none";
    }
    // Sync any open diff tabs with updated diff objects
    let activeDiffChanged = false;
    for (const tab of [...state.editorTabs]) {
      if (tab.kind !== "diff" || !tab.diff) continue;
      const updated = state.diffs.find((d) => d.path === tab.diff?.path);
      if (updated) {
        tab.diff = updated;
        if (state.activeEditorPath === tab.relPath) activeDiffChanged = true;
      } else {
        state.editorTabs = state.editorTabs.filter((t) => t.relPath !== tab.relPath);
        if (state.activeEditorPath === tab.relPath) {
          state.activeEditorPath = state.editorTabs[0]?.relPath || "";
          activeDiffChanged = true;
        }
      }
    }
    if (activeDiffChanged) {
      renderEditorTabs();
      renderEditorBody();
    }
    if (state.sidebarView === "git" && state.showLeftSidebar) {
      renderLeftSidebar();
    } else if (state.sidebarView === "explorer" && state.showLeftSidebar) {
      const treeBody = document.getElementById("explorer-tree-body");
      if (treeBody) {
        treeBody.innerHTML = renderExplorerTreeHtml();
        bindTreeRowClicks();
      }
    }
  } catch {
    // ignore non-git
  }
}

async function refreshExplorerTree() {
  const backend = api();
  if (!backend || !state.project) return;
  const rootNodes = await backend.ListDirectory(state.project.root, "");
  state.dirCache.clear();
  state.dirCache.set("", rootNodes || []);
  for (const dir of [...state.expandedDirs]) {
    try {
      const ch = await backend.ListDirectory(state.project.root, dir);
      state.dirCache.set(dir, ch || []);
    } catch {
      state.expandedDirs.delete(dir);
    }
  }
  const body = document.getElementById("explorer-tree-body");
  if (body) {
    body.innerHTML = renderExplorerTreeHtml();
    bindTreeRowClicks();
  }
}

function renderTreeNodes(
  nodes: FileNode[],
  depth = 0,
  filter = "",
  gitMap = getGitStatusMap()
): string {
  const q = filter.trim().toLowerCase();
  return nodes
    .map((n) => {
      const pad = 8 + depth * 14;
      if (n.isDir) {
        const open = state.expandedDirs.has(n.relPath);
        const children = state.dirCache.get(n.relPath) ?? n.children ?? [];
        const childrenHtml = open ? renderTreeNodes(children, depth + 1, filter, gitMap) : "";
        if (q && !n.name.toLowerCase().includes(q) && !childrenHtml) {
          return "";
        }
        const hasModChild = gitMap.dirs.has(n.relPath);
        return `
          <div class="tree-row ${hasModChild ? "git-mod" : ""}" data-dir="${escapeHtml(n.relPath)}" style="padding-left:${pad}px">
            <span class="tree-chevron">${open ? icons.chevronDown : icons.chevronRight}</span>
            <span class="tree-folder-icon">${open ? icons.folderOpen : icons.folderClosed}</span>
            <span class="tree-name">${escapeHtml(n.name)}</span>
            ${hasModChild ? `<span class="tree-dir-dot" title="Contém arquivos modificados"></span>` : ""}
            <span class="tree-actions">
              <button class="tree-act-btn" data-tree-newfile="${escapeHtml(n.relPath)}" title="Novo arquivo nesta pasta">${icons.plus}</button>
              <button class="tree-act-btn" data-tree-rename="${escapeHtml(n.relPath)}" title="Renomear">${icons.editPen}</button>
              <button class="tree-act-btn" data-tree-delete="${escapeHtml(n.relPath)}" title="Excluir">${icons.trash}</button>
            </span>
          </div>
          ${childrenHtml ? `<div style="border-left:1px solid rgba(255,255,255,0.06);margin-left:${pad + 6}px">${childrenHtml}</div>` : ""}
        `;
      }
      if (q && !n.name.toLowerCase().includes(q) && !n.relPath.toLowerCase().includes(q)) {
        return "";
      }
      const isAct = state.activeEditorPath === n.relPath ? "active" : "";
      const gitSt = gitMap.files.get(n.relPath);
      const gitCls = gitSt === "U" ? "git-add" : gitSt === "M" ? "git-mod" : "";
      const gitBadge = gitSt === "U"
        ? `<span class="tree-git-badge add" title="Arquivo novo / não rastreado">U</span>`
        : gitSt === "M"
          ? `<span class="tree-git-badge mod" title="Arquivo modificado">M</span>`
          : "";
      return `
        <div class="tree-row ${isAct} ${gitCls}" data-file="${escapeHtml(n.relPath)}" data-name="${escapeHtml(n.name)}" style="padding-left:${pad + 14}px">
          ${fileBadge(n.name)}
          <span class="tree-name">${escapeHtml(n.name)}</span>
          ${gitBadge}
          <span class="tree-actions">
            <button class="tree-act-btn" data-tree-rename="${escapeHtml(n.relPath)}" title="Renomear">${icons.editPen}</button>
            <button class="tree-act-btn" data-tree-delete="${escapeHtml(n.relPath)}" title="Excluir">${icons.trash}</button>
          </span>
        </div>
      `;
    })
    .join("");
}

function highlightActiveTreeRow() {
  document.querySelectorAll("#left-sidebar [data-file]").forEach((el) => {
    el.classList.toggle("active", (el as HTMLElement).dataset.file === state.activeEditorPath);
  });
}

function buildModelSelectOptions(currentModel: string, includeDefaultBlank = false): string {
  const cur = (currentModel || "").trim();
  const blank = includeDefaultBlank ? `<option value="" ${!cur ? "selected" : ""}>(usar padrão)</option>` : "";

  const inAgy = state.models.some((m) => `agy:${m.slug}` === cur || m.slug === cur);
  const inOpenCodeGo = state.openCodeGoModels.some(
    (m) => `opencode-go:${m.slug}` === cur || `opencode-go/${m.slug}` === cur
  );
  const inOpenCodeZen = state.openCodeModels.some(
    (m) => `opencode:${m.slug}` === cur || `opencode/${m.slug}` === cur
  );
  const effectiveCur =
    cur ||
    (!includeDefaultBlank
      ? state.openCodeStatus?.configured && state.openCodeGoModels.length > 0
        ? `opencode-go:${state.openCodeGoModels[0].slug}`
        : state.openCodeStatus?.configured && state.openCodeModels.length > 0
          ? `opencode:${state.openCodeModels[0].slug}`
          : "agy"
      : "");
  const effectiveInList =
    inAgy ||
    inOpenCodeGo ||
    inOpenCodeZen ||
    (!cur &&
      state.openCodeStatus?.configured &&
      (state.openCodeGoModels.length > 0 || state.openCodeModels.length > 0));
  const head =
    effectiveCur && !effectiveInList
      ? `<option value="${escapeHtml(effectiveCur)}" selected>${escapeHtml(effectiveCur)}</option>`
      : "";

  const groups: string[] = [];
  if (state.openCodeGoModels.length > 0) {
    const goLabel = state.openCodeStatus?.configured
      ? `OpenCode Go — Assinatura (${state.openCodeGoModels.length})`
      : `OpenCode Go — Configure o token (${state.openCodeGoModels.length})`;
    const opts = state.openCodeGoModels
      .map((m) => {
        const val = `opencode-go:${m.slug}`;
        const sel = effectiveCur === val || cur === `opencode-go/${m.slug}` ? "selected" : "";
        return `<option value="${escapeHtml(val)}" ${sel}>opencode-go:${escapeHtml(m.slug)}</option>`;
      })
      .join("");
    groups.push(`<optgroup label="${escapeHtml(goLabel)}">${opts}</optgroup>`);
  }
  if (state.openCodeModels.length > 0) {
    const ocLabel = state.openCodeStatus?.configured
      ? `OpenCode Zen — Pay-as-you-go / Free (${state.openCodeModels.length})`
      : `OpenCode Zen — Sem token (modelos -free / configure o token)`;
    const opts = state.openCodeModels
      .map((m) => {
        const val = `opencode:${m.slug}`;
        const sel = effectiveCur === val || cur === `opencode/${m.slug}` ? "selected" : "";
        return `<option value="${escapeHtml(val)}" ${sel}>opencode:${escapeHtml(m.slug)}</option>`;
      })
      .join("");
    groups.push(`<optgroup label="${escapeHtml(ocLabel)}">${opts}</optgroup>`);
  }
  if (state.models.length > 0) {
    const opts = state.models
      .map((m) => {
        const val = `agy:${m.slug}`;
        const sel = effectiveCur === val || cur === m.slug ? "selected" : "";
        return `<option value="${escapeHtml(val)}" ${sel}>agy:${escapeHtml(m.slug)}</option>`;
      })
      .join("");
    groups.push(`<optgroup label="Antigravity CLI (agy)">${opts}</optgroup>`);
  }

  if (groups.length === 0) {
    return blank + (head || `<option value="agy" selected>agy (padrão)</option>`);
  }
  return blank + head + groups.join("");
}

async function handleInlineModelChange(agentName: string, newModel: string) {
  const backend = api();
  const lane = activeLane();
  if (!backend || !state.project || !lane || !newModel) return;
  try {
    const cfg = await backend.UpdateAgentModel(state.project.root, lane.teamName, agentName, newModel);
    state.project.teamConfig = cfg;
    for (const ag of lane.agents) {
      if (ag.name === agentName) ag.model = newModel;
    }
    if (state.sidebarView === "team" && state.showLeftSidebar) updateTeamStatusPills();
    updateMissionBoardIfOpen();
    setStatus(`Modelo de ${agentName} atualizado para ${newModel}.`);
  } catch (err: any) {
    toastError(`trocar o modelo do agente "${agentName}"`, err);
  }
}

function renderLeftSidebar() {
  const sidebar = document.getElementById("left-sidebar");
  if (!sidebar) return;

  const proj = state.project;
  const lane = activeLane();
  const teams = proj?.teamConfig?.teams || [];

  if (state.sidebarView === "search") {
    sidebar.innerHTML = `
      <div class="sidebar-header">
        <span class="eyebrow">Busca no Código</span>
        <div class="spacer"></div>
        <span class="dim" style="font-size:10.5px">Ctrl+Shift+F</span>
      </div>
      <div class="sidebar-filter" style="display:flex;gap:5px">
        <input id="global-search-input" placeholder="Buscar texto nos arquivos…" value="${escapeHtml(state.textSearch.query)}" style="flex:1" />
        <button class="mini primary" id="btn-run-global-search">${icons.search}</button>
      </div>
      <div class="sidebar-body" id="global-search-results">
        ${renderGlobalSearchResultsHtml()}
      </div>
    `;
    bindGlobalSearchEvents();
    return;
  }

  if (state.sidebarView === "sessions") {
    const sum = state.sessionsSummary;
    const projTotal = Math.max(sum.projectTotalTokens, lane?.sessionTotalTokens || 0);
    sidebar.innerHTML = `
      <div class="sidebar-header">
        <span class="eyebrow">Sessões & Tokens (${sum.sessions.length})</span>
        <div class="spacer"></div>
        <button class="icon-btn" id="btn-refresh-sessions" title="Atualizar sessões">${icons.refresh}</button>
      </div>
      <div class="sidebar-body">
        <div class="gh-card" style="margin-bottom:10px">
          <div class="dim" style="color:var(--accent);font-weight:600">Consumo acumulado do projeto</div>
          <div style="display:flex;align-items:center;gap:6px;font-size:18px;font-weight:700;font-family:var(--mono);color:var(--text)">
            ${icons.bolt}
            <span>${exactTokens(projTotal)}</span>
            <span class="dim" style="font-size:11px;font-weight:400">tokens</span>
          </div>
          <div class="dim" style="font-family:var(--mono);font-size:11px">
            Entrada: ${exactTokens(sum.projectInputTokens)} · Saída: ${exactTokens(sum.projectOutputTokens)}
          </div>
        </div>
        <div class="dim" style="font-weight:600;margin-bottom:6px">Histórico salvo (.hivemind/sessions)</div>
        ${sum.sessions.length === 0
          ? `<div class="dim">Nenhuma sessão salva neste projeto ainda. Envie uma mensagem ao Chefe para iniciar.</div>`
          : sum.sessions.map((s) => `
            <div class="session-card ${lane?.sessionId === s.id ? "active" : ""}">
              <div class="session-title" title="${escapeHtml(s.title)}">${escapeHtml(s.title)}</div>
              <div class="session-meta">
                <span>${escapeHtml(s.updatedAt)}${s.jiraKey ? ` · <strong>${escapeHtml(s.jiraKey)}</strong>` : ""}</span>
                <span class="tok-pill" title="Entrada: ${exactTokens(s.inputTokens)} | Saída: ${exactTokens(s.outputTokens)}">${icons.bolt}<span>${exactTokens(s.totalTokens)} tok</span></span>
              </div>
              <div class="dim" style="font-family:var(--mono);font-size:10.5px">
                Entrada: ${exactTokens(s.inputTokens)} · Saída: ${exactTokens(s.outputTokens)}
              </div>
              <div style="display:flex;gap:6px;margin-top:2px">
                <button class="mini primary" style="flex:1" data-resume-session="${escapeHtml(s.id)}">Retomar Sessão</button>
                <button class="mini danger" data-delete-session="${escapeHtml(s.id)}" title="Excluir sessão">${icons.trash}</button>
              </div>
            </div>
          `).join("")
        }
      </div>
    `;
    bindSessionsSidebarEvents();
    return;
  }

  if (state.sidebarView === "team") {
    const maxTok = proj?.teamConfig?.settings?.max_tokens || 0;
    const permMode = (proj?.teamConfig?.settings?.permission_mode || "governed") as PermissionMode;
    sidebar.innerHTML = `
      <div class="sidebar-header">
        <span class="eyebrow">Equipe (${lane?.agents.length || 0})</span>
        <div class="spacer"></div>
        <button class="mini" id="btn-open-mission-sb" title="Abrir Quadro Multiagente">${icons.swarm}<span>Quadro</span></button>
        <button class="mini" id="btn-config-team">Wizard</button>
      </div>
      <div class="sidebar-body">
        <div style="display:flex;gap:6px;align-items:center;margin-bottom:8px">
          <select id="team-select" style="flex:1" ${lane?.running ? "disabled" : ""}>
            ${teams.map((t) => `<option value="${escapeHtml(t.name)}" ${t.name === lane?.teamName ? "selected" : ""}>${escapeHtml(t.name)}</option>`).join("")}
          </select>
          <button class="mini" id="btn-new-team-sb">${icons.plus}<span>Novo</span></button>
        </div>
        <div style="margin-bottom:8px">
          <div class="dim" style="font-size:10.5px;margin-bottom:3px">Permissão & Autonomia (estilo Claude Code / GPT):</div>
          <select id="sb-permission-select" style="width:100%;font-size:11.5px;padding:4px 6px">
            <option value="governed" ${permMode === "governed" ? "selected" : ""}>Governado (Worktree + PR + Pede permissão p/ main)</option>
            <option value="auto_edit" ${permMode === "auto_edit" ? "selected" : ""}>Auto-Edição (Edições e comandos locais livres)</option>
            <option value="full_access" ${permMode === "full_access" ? "selected" : ""}>Full Acesso & Permissão Total</option>
          </select>
        </div>
        <button class="mini" id="btn-team-memory" style="width:100%;margin-bottom:8px;justify-content:flex-start">
          ${icons.memory}
          <span>Memória do Time (.hivemind/MEMORY.md)</span>
        </button>
        <div class="dim" style="margin-bottom:8px">
          Fluxo: Chefe → 1..${proj?.teamConfig?.settings?.max_dynamic_devs || 5} Devs dinâmicos (worktree) → QA → Fecho${maxTok > 0 ? ` · Limite: ${exactTokens(maxTok)} tok` : ""}
        </div>
        <div id="team-agents-list">
          ${renderAgentCardsHtml(lane?.agents || [])}
        </div>
      </div>
    `;
    bindTeamSidebarEvents();
    return;
  }

  if (state.sidebarView === "git") {
    const gh = state.ghStatus;
    sidebar.innerHTML = `
      <div class="sidebar-header">
        <span class="eyebrow">Git & Issues (${state.diffs.length})</span>
        <div class="spacer"></div>
        <button class="icon-btn" id="btn-refresh-diff" title="Atualizar diffs">${icons.refresh}</button>
      </div>
      <div class="sidebar-body" style="display:flex;flex-direction:column;gap:8px">
        <div class="gh-card">
          <div>
            <span class="gh-dot ${gh?.loggedIn ? "ok" : "off"}"></span>
            <strong>GitHub:</strong> ${escapeHtml(gh?.detail || "Verificando…")}
          </div>
          ${gh?.remote ? `<div class="dim" style="word-break:break-all">${escapeHtml(gh.remote)}</div>` : ""}
          ${!proj?.branch ? `
            <div class="dim">Esta pasta ainda não é um repositório Git.</div>
            <button class="primary mini" id="btn-init-git">Inicializar Git (git init)</button>
          ` : `
            <div class="dim">Branch ativa: <strong>${escapeHtml(proj.branch)}</strong></div>
          `}
        </div>
        <input id="commit-msg-input" placeholder="Mensagem de commit…" value="${escapeHtml(state.commitMsgInput)}" />
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:6px">
          <button class="primary mini" id="btn-commit">${icons.commit}<span>Commitar</span></button>
          <button class="success mini" id="btn-commit-push">Commit + Push</button>
          <button class="mini" id="btn-open-pr">${icons.pr}<span>Abrir PR</span></button>
          <button class="mini" id="btn-comment-jira">Comentar Jira</button>
        </div>
        <button class="danger mini" id="btn-discard">${icons.trash}<span>Descartar Alterações</span></button>

        <div class="dim" style="font-weight:600;margin-top:4px">Arquivos alterados</div>
        <div id="git-diff-files">
          ${state.diffs.length === 0
            ? `<div class="dim">Sem alterações pendentes</div>`
            : state.diffs.map((d) => `
              <div class="diff-file-item ${state.activeEditorPath === "diff:" + d.path ? "active" : ""}" data-open-diff="${escapeHtml(d.path)}">
                <span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${escapeHtml(d.path)}</span>
                <span><span style="color:var(--green)">+${d.added}</span> <span style="color:var(--red)">-${d.removed}</span></span>
              </div>
            `).join("")
          }
        </div>

        <div style="display:flex;align-items:center;justify-content:space-between;margin-top:6px">
          <span class="dim" style="font-weight:600">Issues abertas (Jira / GitHub)</span>
          <button class="mini" id="btn-load-issues" ${state.issues.loading ? "disabled" : ""}>
            ${state.issues.loading ? icons.spinner : icons.refresh}
            <span>Carregar</span>
          </button>
        </div>
        <div id="git-issues-list">
          ${!state.issues.loadedOnce
            ? `<div class="dim">Clique em "Carregar" para buscar issues abertas do Jira ou GitHub.</div>`
            : state.issues.list.length === 0
              ? `<div class="dim">Nenhuma issue aberta encontrada.</div>`
              : state.issues.list.map((iss, idx) => `
                <div class="issue-card">
                  <div style="display:flex;align-items:center;justify-content:space-between;gap:6px">
                    <span class="tok-pill">${escapeHtml(iss.source.toUpperCase())} · ${escapeHtml(iss.key)}</span>
                    ${iss.status ? `<span class="dim" style="font-size:10.5px">${escapeHtml(iss.status)}</span>` : ""}
                  </div>
                  <div style="font-size:12px;font-weight:600;color:var(--text)">${escapeHtml(iss.title)}</div>
                  <button class="mini primary" data-solve-issue="${idx}">${icons.send}<span>Resolver c/ Equipe</span></button>
                </div>
              `).join("")
          }
        </div>
      </div>
    `;
    bindGitSidebarEvents();
    return;
  }

  sidebar.innerHTML = `
    <div class="sidebar-header">
      <span class="eyebrow">Explorador</span>
      <div class="spacer"></div>
      <button class="icon-btn" id="btn-new-file" title="Novo arquivo">${icons.newFile}</button>
      <button class="icon-btn" id="btn-new-folder" title="Nova pasta">${icons.newFolder}</button>
      <button class="icon-btn" id="btn-refresh-tree" title="Atualizar arquivos">${icons.refresh}</button>
    </div>
    <div class="sidebar-filter">
      <input id="explorer-filter" placeholder="Filtrar arquivos…" value="${escapeHtml(state.explorerFilter)}" />
    </div>
    <div class="sidebar-body" id="explorer-tree-body">
      ${renderExplorerTreeHtml()}
    </div>
  `;
  bindExplorerEvents();
}

function renderGlobalSearchResultsHtml(): string {
  if (state.textSearch.searching) {
    return `<div class="dim" style="padding:8px">${icons.spinner} Buscando…</div>`;
  }
  if (!state.textSearch.query.trim()) {
    return `<div class="dim" style="padding:8px">Digite um termo para buscar em todo o código do projeto.</div>`;
  }
  if (state.textSearch.results.length === 0) {
    return `<div class="dim" style="padding:8px">Nenhum resultado encontrado.</div>`;
  }
  return state.textSearch.results
    .map((m, i) => `
      <div class="search-hit" data-search-hit="${i}">
        <div class="search-hit-file">${escapeHtml(m.relPath)}:${m.line}</div>
        <div class="search-hit-line">${escapeHtml(m.preview)}</div>
      </div>
    `)
    .join("");
}

function bindGlobalSearchEvents() {
  const input = document.getElementById("global-search-input") as HTMLInputElement | null;
  const runSearch = async () => {
    const q = (input?.value || "").trim();
    state.textSearch.query = input?.value || "";
    if (!q || !state.project) {
      state.textSearch.results = [];
      state.textSearch.searching = false;
      const box = document.getElementById("global-search-results");
      if (box) box.innerHTML = renderGlobalSearchResultsHtml();
      return;
    }
    state.textSearch.searching = true;
    const box = document.getElementById("global-search-results");
    if (box) box.innerHTML = renderGlobalSearchResultsHtml();
    try {
      const res = (await api()?.SearchText(state.project.root, q)) || [];
      state.textSearch.results = res;
    } catch {
      state.textSearch.results = [];
    } finally {
      state.textSearch.searching = false;
      const updatedBox = document.getElementById("global-search-results");
      if (updatedBox) {
        updatedBox.innerHTML = renderGlobalSearchResultsHtml();
        bindGlobalSearchHits();
      }
    }
  };

  input?.addEventListener("input", () => {
    state.textSearch.query = input.value;
    if (globalSearchTimer) window.clearTimeout(globalSearchTimer);
    globalSearchTimer = window.setTimeout(runSearch, 220);
  });
  input?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      if (globalSearchTimer) window.clearTimeout(globalSearchTimer);
      runSearch();
    }
  });
  document.getElementById("btn-run-global-search")?.addEventListener("click", runSearch);
  bindGlobalSearchHits();
}

function bindGlobalSearchHits() {
  const box = document.getElementById("global-search-results");
  if (!box) return;
  box.querySelectorAll("[data-search-hit]").forEach((el) => {
    el.addEventListener("click", () => {
      const idx = parseInt((el as HTMLElement).dataset.searchHit || "0", 10);
      const hit = state.textSearch.results[idx];
      if (!hit) return;
      const name = hit.relPath.split("/").pop() || hit.relPath;
      openFileInEditor(hit.relPath, name, hit.line);
    });
  });
}

function bindSessionsSidebarEvents() {
  document.getElementById("btn-refresh-sessions")?.addEventListener("click", () => refreshSessions());
  document.querySelectorAll("[data-resume-session]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const sid = (btn as HTMLElement).dataset.resumeSession!;
      const backend = api();
      const lane = activeLane();
      if (!backend || !state.project || !lane || lane.running) return;
      try {
        const detail = await backend.LoadSession(state.project.root, sid);
        lane.sessionId = detail.id;
        lane.turnInTokens = 0;
        lane.turnOutTokens = 0;
        lane.turnTotalTokens = 0;
        lane.sessionInTokens = Number(detail.inputTokens || 0);
        lane.sessionOutTokens = Number(detail.outputTokens || 0);
        lane.sessionTotalTokens = Number(detail.totalTokens || 0);
        if (detail.jiraKey) {
          state.jiraKeyInput = detail.jiraKey;
          const jk = document.getElementById("jira-key-input") as HTMLInputElement | null;
          if (jk) jk.value = detail.jiraKey;
        }
        lane.agents = buildAgentRuntimes(lane.teamName);
        if (detail.agentUsage) {
          for (const [author, st] of Object.entries(detail.agentUsage)) {
            const ag = matchAgentRuntime(lane.agents, author);
            if (ag) {
              ag.inTokens += Number(st.inputTokens || 0);
              ag.outTokens += Number(st.outputTokens || 0);
              ag.tokens += Number(st.totalTokens || 0);
              ag.calls += Number(st.calls || 0);
            }
          }
        }
        lane.messages = [
          {
            id: `sys-res-${Date.now()}`,
            kind: "system",
            label: "Sessão Retomada",
            text: `Sessão \`${detail.id}\` restaurada — **${exactTokens(detail.totalTokens)} tokens** acumulados (Entrada: ${exactTokens(detail.inputTokens)} / Saída: ${exactTokens(detail.outputTokens)}).`,
          },
          ...(detail.messages || []).map((m): ChatMessage => ({
            id: m.id || `msg-${Math.random().toString(36).slice(2, 8)}`,
            kind: (m.kind === "user" || m.kind === "tool" ? m.kind : "chief"),
            label: m.label || "Chefe",
            text: m.text || "",
            tokens: m.tokens ? Number(m.tokens) : undefined,
          })),
        ];
        state.showRightAI = true;
        syncLayoutVisibility();
        rebuildChatMessagesFull();
        renderTokenBar();
        updateStatusBar();
        updateMissionBoardIfOpen();
        renderLeftSidebar();
        setStatus(`Sessão ${detail.id} retomada.`);
      } catch (err: any) {
        toastError("carregar a sessão salva", err);
      }
    });
  });

  document.querySelectorAll("[data-delete-session]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const sid = (btn as HTMLElement).dataset.deleteSession!;
      const backend = api();
      const lane = activeLane();
      if (!backend || !state.project) return;
      try {
        await backend.DeleteSession(state.project.root, sid);
        if (lane && lane.sessionId === sid) {
          lane.sessionId = "";
        }
        await refreshSessions();
        setStatus(`Sessão ${sid} excluída.`);
      } catch (err: any) {
        toastError("excluir a sessão", err);
      }
    });
  });
}

function renderExplorerTreeHtml(): string {
  const rootNodes = state.dirCache.get("") ?? state.project?.files ?? [];
  if (!state.project) return `<div class="dim" style="padding:8px">Abra uma pasta</div>`;
  const gitMap = getGitStatusMap();
  const q = state.explorerFilter.trim();
  if (q && state.explorerFilterResults.length > 0) {
    return state.explorerFilterResults
      .map((rel) => {
        const name = rel.split("/").pop() || rel;
        const dirPart = rel.includes("/") ? rel.slice(0, rel.lastIndexOf("/")) : "";
        const isAct = state.activeEditorPath === rel ? "active" : "";
        const gitSt = gitMap.files.get(rel);
        const gitCls = gitSt === "U" ? "git-add" : gitSt === "M" ? "git-mod" : "";
        const gitBadge = gitSt === "U"
          ? `<span class="tree-git-badge add">U</span>`
          : gitSt === "M"
            ? `<span class="tree-git-badge mod">M</span>`
            : "";
        return `
          <div class="tree-row ${isAct} ${gitCls}" data-file="${escapeHtml(rel)}" data-name="${escapeHtml(name)}" style="padding-left:10px" title="${escapeHtml(rel)}">
            ${fileBadge(name)}
            <span class="tree-name">${escapeHtml(name)}${dirPart ? ` <span class="dim" style="font-size:10.5px">· ${escapeHtml(dirPart)}</span>` : ""}</span>
            ${gitBadge}
          </div>
        `;
      })
      .join("");
  }
  const html = renderTreeNodes(rootNodes, 0, q, gitMap);
  return html || `<div class="dim" style="padding:8px">Nenhum arquivo correspondente</div>`;
}

function renderAgentCardsHtml(agents: AgentRuntime[]): string {
  const lane = activeLane();
  return agents.map((a) => `
    <div class="agent-card" data-agent-card="${escapeHtml(a.name)}">
      <div class="agent-card-top">
        <div class="agent-name">${escapeHtml(a.name)} · <span class="dim">${escapeHtml(roleLabelPt(a.role))}</span></div>
        <span class="status-pill ${a.status}">${a.status === "idle" ? "pronto" : a.status === "running" ? "rodando" : a.status === "done" ? "ok" : "erro"}</span>
      </div>
      <select data-sb-agent-model="${escapeHtml(a.name)}" style="width:100%;padding:3px 6px;font-size:11px;font-family:var(--mono)" ${lane?.running ? "disabled" : ""}>
        ${buildModelSelectOptions(a.model)}
      </select>
      ${a.tool ? `<div class="agent-meta" style="color:var(--green)">Ferramenta: ${escapeHtml(a.tool)}</div>` : ""}
      ${a.tokens > 0
        ? `<div class="agent-meta" style="color:var(--accent);font-family:var(--mono)" title="Entrada: ${exactTokens(a.inTokens)} | Saída: ${exactTokens(a.outTokens)} | Chamadas: ${a.calls}">${exactTokens(a.tokens)} tok (↑${formatTokens(a.inTokens)} ↓${formatTokens(a.outTokens)} · ${a.calls}x)</div>`
        : ""
      }
    </div>
  `).join("");
}

function bindAgentModelSelects(container: ParentNode | null, attrName: string) {
  if (!container) return;
  container.querySelectorAll<HTMLSelectElement>(`[${attrName}]`).forEach((sel) => {
    sel.addEventListener("change", () => {
      const agName = sel.getAttribute(attrName) || "";
      if (agName) handleInlineModelChange(agName, sel.value);
    });
  });
}

function updateTeamStatusPills() {
  const list = document.getElementById("team-agents-list");
  const lane = activeLane();
  if (!list || !lane) return;
  list.innerHTML = renderAgentCardsHtml(lane.agents);
  bindAgentModelSelects(list, "data-sb-agent-model");
}

function bindExplorerEvents() {
  const filterIn = document.getElementById("explorer-filter") as HTMLInputElement | null;
  filterIn?.addEventListener("input", () => {
    state.explorerFilter = filterIn.value;
    const q = state.explorerFilter.trim();
    if (explorerFilterTimer) window.clearTimeout(explorerFilterTimer);
    if (!q) {
      state.explorerFilterResults = [];
      const body = document.getElementById("explorer-tree-body");
      if (body) {
        body.innerHTML = renderExplorerTreeHtml();
        bindTreeRowClicks();
      }
      return;
    }
    explorerFilterTimer = window.setTimeout(async () => {
      if (state.project) {
        state.explorerFilterResults = (await api()?.SearchFiles(state.project.root, q)) || [];
      }
      const body = document.getElementById("explorer-tree-body");
      if (body) {
        body.innerHTML = renderExplorerTreeHtml();
        bindTreeRowClicks();
      }
    }, 60);
  });

  document.getElementById("btn-new-file")?.addEventListener("click", () => {
    if (!state.project) return;
    state.pathModal = { mode: "newFile", targetRel: "", value: "" };
    renderModalLayer();
  });
  document.getElementById("btn-new-folder")?.addEventListener("click", () => {
    if (!state.project) return;
    state.pathModal = { mode: "newDir", targetRel: "", value: "" };
    renderModalLayer();
  });
  document.getElementById("btn-refresh-tree")?.addEventListener("click", refreshExplorerTree);

  bindTreeRowClicks();
}

function bindTreeRowClicks() {
  const body = document.getElementById("explorer-tree-body");
  if (!body) return;

  body.querySelectorAll("[data-tree-newfile]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const folder = (btn as HTMLElement).dataset.treeNewfile!;
      state.pathModal = { mode: "newFile", targetRel: folder, value: `${folder}/` };
      renderModalLayer();
    });
  });

  body.querySelectorAll("[data-tree-rename]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const rel = (btn as HTMLElement).dataset.treeRename!;
      state.pathModal = { mode: "rename", targetRel: rel, value: rel };
      renderModalLayer();
    });
  });

  body.querySelectorAll("[data-tree-delete]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const rel = (btn as HTMLElement).dataset.treeDelete!;
      state.pathModal = { mode: "delete", targetRel: rel, value: rel };
      renderModalLayer();
    });
  });

  body.querySelectorAll("[data-dir]").forEach((el) => {
    el.addEventListener("click", async () => {
      const rel = (el as HTMLElement).dataset.dir!;
      if (state.expandedDirs.has(rel)) {
        state.expandedDirs.delete(rel);
      } else {
        state.expandedDirs.add(rel);
        if (!state.dirCache.has(rel) && state.project) {
          const backend = api();
          if (backend) {
            const children = await backend.ListDirectory(state.project.root, rel);
            state.dirCache.set(rel, children || []);
          }
        }
      }
      body.innerHTML = renderExplorerTreeHtml();
      bindTreeRowClicks();
    });
  });

  body.querySelectorAll("[data-file]").forEach((el) => {
    el.addEventListener("click", () => {
      const ds = (el as HTMLElement).dataset;
      openFileInEditor(ds.file!, ds.name!);
    });
  });
}

function bindTeamSidebarEvents() {
  const lane = activeLane();
  document.getElementById("btn-open-mission-sb")?.addEventListener("click", () => openMissionControlTab());
  document.getElementById("btn-team-memory")?.addEventListener("click", () => openTeamMemoryFile());
  document.getElementById("btn-config-team")?.addEventListener("click", () => {
    state.showWizard = true;
    renderModalLayer();
  });
  document.getElementById("btn-new-team-sb")?.addEventListener("click", () => {
    state.showWizard = true;
    renderModalLayer();
  });
  const teamSel = document.getElementById("team-select") as HTMLSelectElement | null;
  teamSel?.addEventListener("change", async () => {
    if (!lane || !state.project) return;
    lane.teamName = teamSel.value;
    lane.agents = buildAgentRuntimes(lane.teamName);
    await api()?.SetActiveTeam(state.project.root, lane.teamName);
    updateTeamStatusPills();
    updateMissionBoardIfOpen();
    renderLaneBarOnly();
    updateStatusBar();
  });
  const permSel = document.getElementById("sb-permission-select") as HTMLSelectElement | null;
  permSel?.addEventListener("change", () => {
    setProjectPermissionMode(permSel.value as PermissionMode);
  });
  bindAgentModelSelects(document.getElementById("team-agents-list"), "data-sb-agent-model");
}

async function setProjectPermissionMode(mode: PermissionMode) {
  const backend = api();
  if (!backend || !state.project) return;
  try {
    if (backend.UpdatePermissionMode) {
      const cfg = await backend.UpdatePermissionMode(state.project.root, mode);
      state.project.teamConfig = cfg;
    } else if (state.project.teamConfig?.settings) {
      state.project.teamConfig.settings.permission_mode = mode;
    }
    renderComposerModeBar();
    updateStatusBar();
    updateMissionBoardIfOpen();
    if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
    setStatus(`Modo de permissão atualizado para: ${permissionModeLabel(mode)}.`);
  } catch (err: any) {
    toastError("atualizar o nível de permissão", err);
  }
}

function bindGitSidebarEvents() {
  document.getElementById("btn-refresh-diff")?.addEventListener("click", refreshDiffs);
  document.getElementById("btn-init-git")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      await backend.InitGit(state.project.root);
      const updated = await backend.OpenProject(state.project.root);
      state.project.branch = updated.branch;
      state.ghStatus = await backend.GitHubStatus(state.project.root);
      updateHeaderAndStatusBar();
      renderLeftSidebar();
      setStatus("Repositório Git inicializado (branch main).");
    } catch (err: any) {
      toastError("inicializar o repositório Git", err);
    }
  });
  document.querySelectorAll("[data-open-diff]").forEach((el) => {
    el.addEventListener("click", () => {
      const p = (el as HTMLElement).dataset.openDiff!;
      const d = state.diffs.find((x) => x.path === p);
      if (d) openDiffInEditor(d);
    });
  });
  const commitIn = document.getElementById("commit-msg-input") as HTMLInputElement | null;
  commitIn?.addEventListener("input", () => {
    state.commitMsgInput = commitIn.value;
  });
  document.getElementById("btn-commit")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      const msg = await backend.CommitChanges(state.project.root, state.commitMsgInput, false);
      setStatus(msg);
      state.commitMsgInput = "";
      refreshDiffs();
    } catch (err: any) {
      setStatus(`Erro no commit: ${err?.message || err}`);
    }
  });
  document.getElementById("btn-commit-push")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      const msg = await backend.CommitChanges(state.project.root, state.commitMsgInput, true);
      setStatus(msg);
      state.commitMsgInput = "";
      refreshDiffs();
    } catch (err: any) {
      toastError("publicar as alterações (commit e push)", err);
    }
  });
  document.getElementById("btn-open-pr")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      const url = await backend.OpenPR(state.project.root, state.commitMsgInput, "", state.project.teamConfig?.settings?.base_branch || "main");
      setStatus(`PR aberto: ${url}`);
    } catch (err: any) {
      toastError("abrir o Pull Request", err);
    }
  });
  document.getElementById("btn-comment-jira")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      await backend.CommentJira(state.project.root, state.jiraKeyInput, state.commitMsgInput || "Atualização pela HiveMind IDE.");
      setStatus(`Comentário enviado ao Jira ${state.jiraKeyInput}.`);
    } catch (err: any) {
      toastError("criar a issue no Jira", err);
    }
  });
  document.getElementById("btn-discard")?.addEventListener("click", () => {
    promptDiscardChanges();
  });

  document.getElementById("btn-load-issues")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    state.issues.loading = true;
    renderLeftSidebar();
    try {
      state.issues.list = (await backend.ListIssues(state.project.root)) || [];
      state.issues.loadedOnce = true;
    } catch (err: any) {
      toastError("buscar as issues do GitHub", err);
    } finally {
      state.issues.loading = false;
      renderLeftSidebar();
    }
  });

  document.querySelectorAll("[data-solve-issue]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const idx = parseInt((btn as HTMLElement).dataset.solveIssue || "0", 10);
      const iss = state.issues.list[idx];
      if (!iss) return;
      if (iss.source === "jira") {
        state.jiraKeyInput = iss.key;
        const jk = document.getElementById("jira-key-input") as HTMLInputElement | null;
        if (jk) jk.value = iss.key;
      }
      const bodyPart = iss.body ? `\n\nDescrição:\n${iss.body.slice(0, 1200)}` : "";
      state.draftObjective = `Resolver issue ${iss.key} — ${iss.title}${bodyPart}`;
      state.showRightAI = true;
      syncLayoutVisibility();
      const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
      if (chatInput) {
        chatInput.value = state.draftObjective;
        chatInput.focus();
      }
      setStatus(`Issue ${iss.key} carregada no chat do Chefe.`);
    });
  });
}

function renderEditorTabs() {
  const tabsEl = document.getElementById("editor-tabs");
  if (!tabsEl) return;
  tabsEl.innerHTML = state.editorTabs.map((t) => {
    const tabIcon = t.kind === "mission" ? icons.swarm : t.kind === "diff" ? icons.gitSmall : fileBadge(t.name);
    return `
      <div class="editor-tab ${t.relPath === state.activeEditorPath ? "active" : ""}" data-tab="${escapeHtml(t.relPath)}">
        ${tabIcon}
        <span>${escapeHtml(t.name)}</span>
        <span class="dirty-dot" data-dirty-for="${escapeHtml(t.relPath)}" style="display:${t.dirty ? "inline-flex" : "none"}">${icons.dot}</span>
        <span class="close-x" data-close-tab="${escapeHtml(t.relPath)}">${icons.closeSmall}</span>
      </div>
    `;
  }).join("");

  tabsEl.querySelectorAll("[data-tab]").forEach((el) => {
    el.addEventListener("click", () => {
      state.activeEditorPath = (el as HTMLElement).dataset.tab!;
      renderEditorTabs();
      renderEditorBody();
      highlightActiveTreeRow();
    });
  });
  tabsEl.querySelectorAll("[data-close-tab]").forEach((el) => {
    el.addEventListener("click", (ev) => {
      ev.stopPropagation();
      const path = (el as HTMLElement).dataset.closeTab!;
      const closing = state.editorTabs.find((t) => t.relPath === path);
      const doClose = () => {
        state.editorTabs = state.editorTabs.filter((t) => t.relPath !== path);
        if (state.activeEditorPath === path) {
          state.activeEditorPath = state.editorTabs[0]?.relPath || "";
        }
        renderEditorTabs();
        renderEditorBody();
        highlightActiveTreeRow();
      };
      if (closing?.kind === "file" && closing.dirty) {
        askConfirm({
          title: "Fechar Aba sem Salvar",
          body: `O arquivo "${closing.name}" possui alterações não salvas. Deseja fechá-lo sem salvar?`,
          confirmLabel: "Fechar sem salvar",
          danger: true,
          onConfirm: doClose,
        });
        return;
      }
      doClose();
    });
  });
}

function syncActiveTabDirtyUI(tab: EditorTab) {
  const saveBtn = document.getElementById("btn-save-file") as HTMLButtonElement | null;
  if (saveBtn) saveBtn.disabled = !tab.dirty;
  document.querySelectorAll<HTMLElement>("[data-dirty-for]").forEach((dot) => {
    if (dot.dataset.dirtyFor === tab.relPath) {
      dot.style.display = tab.dirty ? "inline-flex" : "none";
    }
  });
  renderProjectTabsBar();
}

function renderLineNumbers(text: string): string {
  const count = Math.max(1, text.split("\n").length);
  const lines: string[] = [];
  for (let i = 1; i <= count; i++) lines.push(String(i));
  return lines.join("\n");
}

function renderMissionBoardHtml(): string {
  const lane = activeLane();
  const agents = lane?.agents || [];
  const sessTotal = Math.max(1, lane?.sessionTotalTokens || 0);
  const projTotal = Math.max(state.sessionsSummary.projectTotalTokens, lane?.sessionTotalTokens || 0);

  return `
    <div class="mission-board" id="mission-board-root">
      <div class="mission-top-card">
        <div>
          <div style="display:flex;align-items:center;gap:8px;font-size:15px;font-weight:700;color:var(--text)">
            ${icons.swarm}
            <span>Quadro de Controle Multiagente — ${escapeHtml(lane?.teamName || "principal")}</span>
          </div>
          <div class="dim" style="margin-top:4px" id="mc-header-summary">
            Modo ativo: <strong>${escapeHtml(runModeLabel(state.runMode))}</strong> · Sessão: <strong>${exactTokens(lane?.sessionTotalTokens)} tok</strong> · Projeto: <strong>${exactTokens(projTotal)} tok</strong>
          </div>
        </div>
        <div style="display:flex;gap:7px;flex-wrap:wrap">
          <button class="mini" id="mc-open-memory">${icons.memory}<span>Memória (.hivemind/MEMORY.md)</span></button>
          <button class="mini" id="mc-open-sessions">${icons.sessions}<span>Histórico de Sessões</span></button>
          <button class="mini primary" id="mc-open-wizard">${icons.settings}<span>Configurar Equipe</span></button>
        </div>
      </div>

      <div class="mission-grid">
        ${agents.map((a) => {
          const pct = lane?.sessionTotalTokens ? Math.min(100, Math.round((a.tokens / sessTotal) * 100)) : 0;
          return `
            <div class="mc-agent-card ${a.status}" data-mc-card="${escapeHtml(a.name)}">
              <div style="display:flex;align-items:center;justify-content:space-between;gap:8px">
                <div>
                  <div style="font-size:13px;font-weight:700;color:var(--text)">${escapeHtml(a.name)}</div>
                  <div class="dim">${escapeHtml(roleLabelPt(a.role))}</div>
                </div>
                <span class="status-pill ${a.status}" data-mc-pill="${escapeHtml(a.name)}">
                  ${a.status === "running" ? `${icons.spinner} rodando` : a.status === "done" ? "concluído" : a.status === "error" ? "erro" : "pronto"}
                </span>
              </div>

              <div>
                <div class="dim" style="font-size:10.5px;margin-bottom:3px">Modelo LLM (troca imediata):</div>
                <select data-mc-agent-model="${escapeHtml(a.name)}" style="width:100%;font-family:var(--mono);font-size:11.5px;padding:5px 7px" ${lane?.running ? "disabled" : ""}>
                  ${buildModelSelectOptions(a.model)}
                </select>
              </div>

              <div style="display:flex;align-items:center;justify-content:space-between;font-size:11px;font-family:var(--mono)">
                <span class="dim" data-mc-activity="${escapeHtml(a.name)}">${a.tool ? `Ativo: ${escapeHtml(a.tool)}` : `${a.calls} chamada(s)`}</span>
                <span style="color:var(--accent)" data-mc-tok="${escapeHtml(a.name)}">${exactTokens(a.tokens)} tok (${pct}%)</span>
              </div>
              <div class="mc-tok-track">
                <div class="mc-tok-fill" data-mc-fill="${escapeHtml(a.name)}" style="width:${pct}%"></div>
              </div>
              <div class="dim" style="font-size:10.5px;font-family:var(--mono)" data-mc-inout="${escapeHtml(a.name)}">
                Entrada: ${exactTokens(a.inTokens)} · Saída: ${exactTokens(a.outTokens)}
              </div>
            </div>
          `;
        }).join("")}
      </div>
    </div>
  `;
}

function bindMissionBoardEvents() {
  const root = document.getElementById("mission-board-root");
  if (!root) return;
  document.getElementById("mc-open-memory")?.addEventListener("click", () => openTeamMemoryFile());
  document.getElementById("mc-open-sessions")?.addEventListener("click", () => {
    openSidebarView("sessions");
    refreshSessions();
  });
  document.getElementById("mc-open-wizard")?.addEventListener("click", () => {
    state.showWizard = true;
    renderModalLayer();
  });
  bindAgentModelSelects(root, "data-mc-agent-model");
}

function updateMissionBoardIfOpen() {
  if (state.activeEditorPath !== MISSION_TAB_KEY) return;
  const body = document.getElementById("editor-body");
  if (!body) return;
  const root = document.getElementById("mission-board-root");
  const lane = activeLane();
  const agents = lane?.agents || [];
  const cards = root?.querySelectorAll("[data-mc-card]");
  if (!root || !cards || cards.length !== agents.length) {
    body.innerHTML = renderMissionBoardHtml();
    bindMissionBoardEvents();
    return;
  }
  const sessTotal = Math.max(1, lane?.sessionTotalTokens || 0);
  const projTotal = Math.max(state.sessionsSummary.projectTotalTokens, lane?.sessionTotalTokens || 0);
  const summaryEl = document.getElementById("mc-header-summary");
  if (summaryEl) {
    summaryEl.innerHTML = `Modo ativo: <strong>${escapeHtml(runModeLabel(state.runMode))}</strong> · Sessão: <strong>${exactTokens(lane?.sessionTotalTokens)} tok</strong> · Projeto: <strong>${exactTokens(projTotal)} tok</strong>`;
  }
  for (const a of agents) {
    const pct = lane?.sessionTotalTokens ? Math.min(100, Math.round((a.tokens / sessTotal) * 100)) : 0;
    const esc = CSS.escape(a.name);
    const card = root.querySelector<HTMLElement>(`[data-mc-card="${esc}"]`);
    if (card) card.className = `mc-agent-card ${a.status}`;
    const pill = root.querySelector<HTMLElement>(`[data-mc-pill="${esc}"]`);
    if (pill) {
      pill.className = `status-pill ${a.status}`;
      pill.innerHTML = a.status === "running" ? `${icons.spinner} rodando` : a.status === "done" ? "concluído" : a.status === "error" ? "erro" : "pronto";
    }
    const sel = root.querySelector<HTMLSelectElement>(`[data-mc-agent-model="${esc}"]`);
    if (sel) sel.disabled = !!lane?.running;
    const act = root.querySelector<HTMLElement>(`[data-mc-activity="${esc}"]`);
    if (act) act.textContent = a.tool ? `Ativo: ${a.tool}` : `${a.calls} chamada(s)`;
    const tok = root.querySelector<HTMLElement>(`[data-mc-tok="${esc}"]`);
    if (tok) tok.textContent = `${exactTokens(a.tokens)} tok (${pct}%)`;
    const fill = root.querySelector<HTMLElement>(`[data-mc-fill="${esc}"]`);
    if (fill) fill.style.width = `${pct}%`;
    const inout = root.querySelector<HTMLElement>(`[data-mc-inout="${esc}"]`);
    if (inout) inout.textContent = `Entrada: ${exactTokens(a.inTokens)} · Saída: ${exactTokens(a.outTokens)}`;
  }
}

interface DiffHunkParsed {
  header: string;
  oldStart: number;
  newStart: number;
  lines: Array<{ kind: string; text: string }>;
}

function parseDiffHunks(diff: FileDiff): { headers: string[]; hunks: DiffHunkParsed[] } {
  const headers: string[] = [];
  const hunks: DiffHunkParsed[] = [];
  let cur: DiffHunkParsed | null = null;
  for (const ln of diff.lines) {
    if (ln.kind === "header") {
      if (!cur) headers.push(ln.text);
      continue;
    }
    if (ln.kind === "hunk") {
      const m = ln.text.match(/@@\s*-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s*@@/);
      cur = {
        header: ln.text,
        oldStart: m ? parseInt(m[1], 10) : 1,
        newStart: m ? parseInt(m[2], 10) : 1,
        lines: [],
      };
      hunks.push(cur);
      continue;
    }
    if (!cur) {
      cur = { header: "@@ -1 +1 @@", oldStart: 1, newStart: 1, lines: [] };
      hunks.push(cur);
    }
    cur.lines.push(ln);
  }
  return { headers, hunks };
}

function renderUnifiedDiffHtml(hunks: DiffHunkParsed[]): string {
  if (hunks.length === 0) {
    return `<div class="dim" style="padding:12px">Sem blocos de diff para exibir.</div>`;
  }
  return hunks
    .map(
      (h, idx) => `
      <div class="diff-hunk-bar">
        <span>${escapeHtml(h.header)}</span>
        <button class="diff-hunk-revert" data-revert-hunk="${idx}" title="Reverter somente este bloco (git apply --reverse)">Reverter bloco</button>
      </div>
      ${h.lines.map((ln) => `<div class="diff-line ${ln.kind}">${escapeHtml(ln.text)}</div>`).join("")}
    `
    )
    .join("");
}

function renderSplitDiffHtml(hunks: DiffHunkParsed[]): string {
  if (hunks.length === 0) {
    return `<div class="dim" style="padding:12px">Sem blocos de diff para exibir.</div>`;
  }
  const sections: string[] = [];
  for (let hIdx = 0; hIdx < hunks.length; hIdx++) {
    const h = hunks[hIdx];
    let oldLine = h.oldStart;
    let newLine = h.newStart;
    const rows: string[] = [];
    let i = 0;
    while (i < h.lines.length) {
      const ln = h.lines[i];
      if (ln.kind === "context") {
        const body = ln.text.startsWith(" ") ? ln.text.slice(1) : ln.text;
        rows.push(`
          <tr>
            <td class="diff-split-num">${oldLine++}</td>
            <td class="diff-split-code">${escapeHtml(body)}</td>
            <td class="diff-split-num">${newLine++}</td>
            <td class="diff-split-code">${escapeHtml(body)}</td>
          </tr>
        `);
        i++;
        continue;
      }
      const removedBatch: string[] = [];
      const addedBatch: string[] = [];
      while (i < h.lines.length && h.lines[i].kind === "removed") {
        removedBatch.push(h.lines[i].text.slice(1));
        i++;
      }
      while (i < h.lines.length && h.lines[i].kind === "added") {
        addedBatch.push(h.lines[i].text.slice(1));
        i++;
      }
      const maxLen = Math.max(removedBatch.length, addedBatch.length);
      if (maxLen === 0) {
        i++;
        continue;
      }
      for (let k = 0; k < maxLen; k++) {
        const hasOld = k < removedBatch.length;
        const hasNew = k < addedBatch.length;
        rows.push(`
          <tr>
            <td class="diff-split-num">${hasOld ? oldLine++ : ""}</td>
            <td class="diff-split-code ${hasOld ? "removed" : "empty"}">${hasOld ? escapeHtml(removedBatch[k]) : ""}</td>
            <td class="diff-split-num">${hasNew ? newLine++ : ""}</td>
            <td class="diff-split-code ${hasNew ? "added" : "empty"}">${hasNew ? escapeHtml(addedBatch[k]) : ""}</td>
          </tr>
        `);
      }
    }
    sections.push(`
      <div class="diff-hunk-bar">
        <span>${escapeHtml(h.header)}</span>
        <button class="diff-hunk-revert" data-revert-hunk="${hIdx}" title="Reverter somente este bloco (git apply --reverse)">Reverter bloco</button>
      </div>
      <table class="diff-split-table"><tbody>${rows.join("")}</tbody></table>
    `);
  }
  return `<div class="diff-split-wrap">${sections.join("")}</div>`;
}

function countLinesFast(text: string): number {
  let count = 1;
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10) count++;
  }
  return count;
}

function openEditorFind(showReplace: boolean) {
  const ta = document.getElementById("editor-textarea") as HTMLTextAreaElement | null;
  if (ta) {
    const sel = ta.value.slice(ta.selectionStart || 0, ta.selectionEnd || 0);
    if (sel && !sel.includes("\n") && sel.length <= 120) {
      state.editorFind.query = sel;
    }
  }
  state.editorFind.open = true;
  state.editorFind.showReplace = showReplace;
  renderEditorFindBar();
  recomputeEditorFindMatches(false);
  const input = document.getElementById("editor-find-query") as HTMLInputElement | null;
  if (input) {
    input.focus();
    input.select();
  }
}

function recomputeEditorFindMatches(selectMatch = true) {
  const ta = document.getElementById("editor-textarea") as HTMLTextAreaElement | null;
  const q = state.editorFind.query;
  state.editorFind.matches = [];
  if (!ta || !q) {
    state.editorFind.matchIndex = 0;
    updateEditorFindCounter();
    return;
  }
  const hay = ta.value.toLowerCase();
  const needle = q.toLowerCase();
  let pos = 0;
  while (pos <= hay.length - needle.length) {
    const idx = hay.indexOf(needle, pos);
    if (idx === -1) break;
    state.editorFind.matches.push({ start: idx, end: idx + needle.length });
    pos = idx + Math.max(1, needle.length);
    if (state.editorFind.matches.length >= 2000) break;
  }
  if (state.editorFind.matches.length === 0) {
    state.editorFind.matchIndex = 0;
  } else if (state.editorFind.matchIndex >= state.editorFind.matches.length) {
    state.editorFind.matchIndex = 0;
  }
  updateEditorFindCounter();
  if (selectMatch && state.editorFind.matches.length > 0) {
    focusCurrentEditorMatch();
  }
}

function updateEditorFindCounter() {
  const counterEl = document.getElementById("editor-find-count");
  if (!counterEl) return;
  const total = state.editorFind.matches.length;
  if (!state.editorFind.query) {
    counterEl.textContent = "0 de 0";
  } else if (total === 0) {
    counterEl.textContent = "Sem ocorr.";
  } else {
    counterEl.textContent = `${state.editorFind.matchIndex + 1} de ${total}`;
  }
}

function focusCurrentEditorMatch() {
  const ta = document.getElementById("editor-textarea") as HTMLTextAreaElement | null;
  const pre = document.getElementById("editor-highlight");
  const gutter = document.getElementById("editor-gutter");
  const m = state.editorFind.matches[state.editorFind.matchIndex];
  if (!ta || !m) return;
  const lineNum = countLinesFast(ta.value.slice(0, m.start));
  const topPx = Math.max(0, (lineNum - 1) * 20 - 90);
  ta.setSelectionRange(m.start, m.end);
  ta.scrollTop = topPx;
  if (pre) pre.scrollTop = topPx;
  if (gutter) gutter.scrollTop = topPx;
}

function stepEditorFind(delta: number) {
  const total = state.editorFind.matches.length;
  if (total === 0) return;
  state.editorFind.matchIndex = (state.editorFind.matchIndex + delta + total) % total;
  updateEditorFindCounter();
  focusCurrentEditorMatch();
}

function replaceEditorCurrentMatch() {
  const ta = document.getElementById("editor-textarea") as HTMLTextAreaElement | null;
  const m = state.editorFind.matches[state.editorFind.matchIndex];
  if (!ta || !m) return;
  const rep = state.editorFind.replaceText;
  ta.value = ta.value.slice(0, m.start) + rep + ta.value.slice(m.end);
  ta.dispatchEvent(new Event("input"));
  recomputeEditorFindMatches(true);
}

function replaceEditorAllMatches() {
  const ta = document.getElementById("editor-textarea") as HTMLTextAreaElement | null;
  const q = state.editorFind.query;
  if (!ta || !q || state.editorFind.matches.length === 0) return;
  const count = state.editorFind.matches.length;
  const rep = state.editorFind.replaceText;
  let out = "";
  let lastIdx = 0;
  for (const m of state.editorFind.matches) {
    out += ta.value.slice(lastIdx, m.start) + rep;
    lastIdx = m.end;
  }
  out += ta.value.slice(lastIdx);
  ta.value = out;
  ta.dispatchEvent(new Event("input"));
  recomputeEditorFindMatches(false);
  setStatus(`${count} ocorrência(s) substituída(s).`);
}

function renderEditorFindBar() {
  const layer = document.getElementById("editor-find-layer");
  if (!layer) return;
  if (!state.editorFind.open) {
    layer.innerHTML = "";
    return;
  }
  const total = state.editorFind.matches.length;
  const countLabel = !state.editorFind.query
    ? "0 de 0"
    : total === 0
      ? "Sem ocorr."
      : `${state.editorFind.matchIndex + 1} de ${total}`;

  layer.innerHTML = `
    <div class="editor-find-bar">
      <div class="editor-find-row">
        <button class="mini ghost" id="ef-toggle-replace" title="Mostrar/ocultar Substituir (Ctrl+H)" style="padding:2px 5px">
          ${state.editorFind.showReplace ? icons.chevronDown : icons.chevronRight}
        </button>
        <input class="editor-find-input" id="editor-find-query" placeholder="Localizar no arquivo (Ctrl+F)…" value="${escapeHtml(state.editorFind.query)}" />
        <span class="editor-find-count" id="editor-find-count">${countLabel}</span>
        <button class="mini" id="ef-prev" title="Anterior (Shift+Enter)">↑</button>
        <button class="mini" id="ef-next" title="Próximo (Enter)">↓</button>
        <button class="icon-btn" id="ef-close" title="Fechar (Esc)">${icons.closeSmall}</button>
      </div>
      ${state.editorFind.showReplace ? `
        <div class="editor-find-row" style="padding-left:24px">
          <input class="editor-find-input" id="editor-find-replace" placeholder="Substituir por…" value="${escapeHtml(state.editorFind.replaceText)}" />
          <button class="mini" id="ef-replace-one" title="Substituir atual">Substituir</button>
          <button class="mini primary" id="ef-replace-all" title="Substituir todas">Tudo</button>
        </div>
      ` : ""}
    </div>
  `;

  const qIn = document.getElementById("editor-find-query") as HTMLInputElement | null;
  const rIn = document.getElementById("editor-find-replace") as HTMLInputElement | null;
  qIn?.addEventListener("input", () => {
    state.editorFind.query = qIn.value;
    state.editorFind.matchIndex = 0;
    recomputeEditorFindMatches(true);
  });
  qIn?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      stepEditorFind(e.shiftKey ? -1 : 1);
    } else if (e.key === "Escape") {
      e.preventDefault();
      state.editorFind.open = false;
      renderEditorFindBar();
      (document.getElementById("editor-textarea") as HTMLTextAreaElement | null)?.focus();
    }
  });
  rIn?.addEventListener("input", () => {
    state.editorFind.replaceText = rIn.value;
  });
  rIn?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      replaceEditorCurrentMatch();
    }
  });
  document.getElementById("ef-toggle-replace")?.addEventListener("click", () => {
    state.editorFind.showReplace = !state.editorFind.showReplace;
    renderEditorFindBar();
  });
  document.getElementById("ef-prev")?.addEventListener("click", () => stepEditorFind(-1));
  document.getElementById("ef-next")?.addEventListener("click", () => stepEditorFind(1));
  document.getElementById("ef-replace-one")?.addEventListener("click", () => replaceEditorCurrentMatch());
  document.getElementById("ef-replace-all")?.addEventListener("click", () => replaceEditorAllMatches());
  document.getElementById("ef-close")?.addEventListener("click", () => {
    state.editorFind.open = false;
    renderEditorFindBar();
  });
}

function renderEditorBody() {
  const body = document.getElementById("editor-body");
  if (!body) return;
  const activeTab = state.editorTabs.find((t) => t.relPath === state.activeEditorPath);

  if (!activeTab) {
    const proj = state.project;
    body.innerHTML = `
      <div class="empty-welcome">
        <div class="welcome-hero-badge">${icons.brand}<span>Multi-Agent Dark Islands IDE</span></div>
        <div style="font-size:22px;color:var(--text);font-weight:700;letter-spacing:-0.3px">HiveMind IDE</div>
        <div class="dim">${proj ? `${escapeHtml(proj.name)} · ${escapeHtml(proj.stack.summary)}` : "Abra uma pasta para iniciar"}</div>
        <div class="welcome-actions">
          <button class="primary" id="welcome-mission">${icons.swarm}<span>Quadro Multiagente</span></button>
          <button id="welcome-quick-open">${icons.search}<span>Buscar Arquivo (Ctrl+P)</span></button>
          <button id="welcome-cmd-palette">${icons.command}<span>Comandos (Ctrl+K)</span></button>
          <button id="welcome-memory">${icons.memory}<span>Memória do Time</span></button>
        </div>
        <div class="shortcut-grid">
          <span class="dim">Paleta de comandos</span><span class="kbd">Ctrl + K</span>
          <span class="dim">Quadro Multiagente</span><span class="kbd">Ctrl + Shift + A</span>
          <span class="dim">Busca rápida de arquivo</span><span class="kbd">Ctrl + P</span>
          <span class="dim">Busca global no código</span><span class="kbd">Ctrl + Shift + F</span>
          <span class="dim">Localizar / Substituir</span><span class="kbd">Ctrl + F / Ctrl + H</span>
          <span class="dim">Enviar seleção ao Chefe</span><span class="kbd">Ctrl + L</span>
          <span class="dim">Terminal integrado</span><span class="kbd">Ctrl + J</span>
        </div>
      </div>
    `;
    document.getElementById("welcome-mission")?.addEventListener("click", () => openMissionControlTab());
    document.getElementById("welcome-quick-open")?.addEventListener("click", () => openQuickOpen());
    document.getElementById("welcome-cmd-palette")?.addEventListener("click", () => openCommandPalette());
    document.getElementById("welcome-memory")?.addEventListener("click", () => openTeamMemoryFile());
    return;
  }

  if (activeTab.kind === "mission") {
    body.innerHTML = renderMissionBoardHtml();
    bindMissionBoardEvents();
    return;
  }

  if (activeTab.kind === "diff" && activeTab.diff) {
    const diff = activeTab.diff;
    const { hunks } = parseDiffHunks(diff);
    const isSplit = state.diffViewMode === "split";
    body.innerHTML = `
      <div class="editor-breadcrumb">
        <span class="dim">${escapeHtml(diff.path)}</span>
        <span style="color:var(--green)">+${diff.added}</span>
        <span style="color:var(--red)">-${diff.removed}</span>
        <div class="spacer"></div>
        <button class="mini ${!isSplit ? "primary" : ""}" id="btn-diff-unified">Unificado</button>
        <button class="mini ${isSplit ? "primary" : ""}" id="btn-diff-split">Lado a Lado</button>
        <button class="mini" id="btn-diff-open-file" title="Abrir arquivo no editor">Abrir Arquivo</button>
        <button class="mini danger" id="btn-diff-revert-file" title="Reverter todas as alterações deste arquivo">Reverter Arquivo</button>
      </div>
      ${isSplit ? renderSplitDiffHtml(hunks) : `<div class="diff-lines">${renderUnifiedDiffHtml(hunks)}</div>`}
    `;
    document.getElementById("btn-diff-unified")?.addEventListener("click", () => {
      state.diffViewMode = "unified";
      renderEditorBody();
    });
    document.getElementById("btn-diff-split")?.addEventListener("click", () => {
      state.diffViewMode = "split";
      renderEditorBody();
    });
    document.getElementById("btn-diff-open-file")?.addEventListener("click", () => {
      const name = diff.path.split("/").pop() || diff.path;
      openFileInEditor(diff.path, name);
    });
    document.getElementById("btn-diff-revert-file")?.addEventListener("click", () => {
      if (!state.project) return;
      askConfirm({
        title: `Reverter "${diff.path}"`,
        body: `Deseja descartar todas as alterações de "${diff.path}" (restaurando para HEAD ou removendo se for arquivo novo)?`,
        confirmLabel: "Reverter Arquivo",
        danger: true,
        onConfirm: async () => {
          if (!state.project) return;
          try {
            await api()?.DiscardFile(state.project.root, diff.path);
            setStatus(`Arquivo revertido: ${diff.path}`);
            await refreshExplorerTree();
            await reloadCleanOpenTabs();
            await refreshDiffs();
          } catch (err: any) {
            toastError(`reverter o arquivo ${diff.path}`, err);
          }
        },
      });
    });
    body.querySelectorAll("[data-revert-hunk]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        if (!state.project) return;
        const idx = parseInt((btn as HTMLElement).dataset.revertHunk || "0", 10);
        const h = hunks[idx];
        if (!h) return;
        try {
          await api()?.RevertHunk(
            state.project.root,
            diff.path,
            h.header,
            h.lines.map((l) => l.text)
          );
          setStatus(`Bloco revertido em ${diff.path}.`);
          await reloadCleanOpenTabs();
          await refreshDiffs();
        } catch (err: any) {
          toastError(`reverter o bloco em ${diff.path}`, err);
        }
      });
    });
    return;
  }

  lastRenderedGutterLines = countLinesFast(activeTab.content);
  body.innerHTML = `
    <div class="editor-breadcrumb">
      <span class="dim">${escapeHtml(activeTab.relPath)}</span>
      <div class="spacer"></div>
      <button class="mini" id="btn-editor-find" title="Localizar e Substituir no arquivo (Ctrl+F / Ctrl+H)">${icons.search}<span>Localizar (Ctrl+F)</span></button>
      <button class="mini" id="btn-send-selection" title="Envia as linhas selecionadas para o chat do Chefe (Ctrl+L)">${icons.send}<span>Enviar p/ Chefe (Ctrl+L)</span></button>
      <button class="mini primary" id="btn-save-file" ${activeTab.dirty ? "" : "disabled"}>${icons.save}<span>Salvar (Ctrl+S)</span></button>
    </div>
    <div class="editor-live-wrap">
      <div id="editor-find-layer"></div>
      <pre class="line-gutter" id="editor-gutter">${renderLineNumbers(activeTab.content)}</pre>
      <div class="editor-code-stack">
        <pre class="code-highlight" id="editor-highlight"><code id="editor-highlight-code">${highlightCode(activeTab.content, activeTab.name)}</code></pre>
        <textarea class="code-overlay-input" id="editor-textarea" spellcheck="false" wrap="off"></textarea>
      </div>
    </div>
  `;

  if (state.editorFind.open) {
    renderEditorFindBar();
  }

  const ta = document.getElementById("editor-textarea") as HTMLTextAreaElement | null;
  const pre = document.getElementById("editor-highlight");
  const gutter = document.getElementById("editor-gutter");
  const codeEl = document.getElementById("editor-highlight-code");

  if (ta) {
    ta.value = activeTab.content;
    if (state.editorFind.open && state.editorFind.query) {
      recomputeEditorFindMatches(false);
    }
    ta.addEventListener("scroll", () => {
      if (pre) {
        pre.scrollTop = ta.scrollTop;
        pre.scrollLeft = ta.scrollLeft;
      }
      if (gutter) gutter.scrollTop = ta.scrollTop;
    });
    ta.addEventListener("input", () => {
      activeTab.content = ta.value;
      activeTab.dirty = true;
      syncActiveTabDirtyUI(activeTab);

      const newLineCount = countLinesFast(activeTab.content);
      if (gutter && newLineCount !== lastRenderedGutterLines) {
        lastRenderedGutterLines = newLineCount;
        gutter.textContent = renderLineNumbers(activeTab.content);
      }

      if (editorHighlightRaf) cancelAnimationFrame(editorHighlightRaf);
      if (editorHighlightTimer) window.clearTimeout(editorHighlightTimer);

      const applyHighlight = () => {
        editorHighlightRaf = 0;
        editorHighlightTimer = 0;
        if (codeEl) codeEl.innerHTML = highlightCode(activeTab.content, activeTab.name);
      };

      if (activeTab.content.length >= 40000) {
        editorHighlightTimer = window.setTimeout(applyHighlight, 120);
      } else {
        editorHighlightRaf = requestAnimationFrame(applyHighlight);
      }
    });
    ta.addEventListener("keydown", (e) => {
      if (e.key === "Tab") {
        e.preventDefault();
        const start = ta.selectionStart;
        const end = ta.selectionEnd;
        ta.setRangeText("  ", start, end, "end");
        ta.dispatchEvent(new Event("input"));
      }
    });
  }

  document.getElementById("btn-editor-find")?.addEventListener("click", () => openEditorFind(false));
  document.getElementById("btn-send-selection")?.addEventListener("click", sendEditorSelectionToChief);
  document.getElementById("btn-save-file")?.addEventListener("click", saveActiveEditor);
}

function renderRightPane() {
  const right = document.getElementById("right-pane");
  if (!right) return;
  const lane = activeLane();

  right.innerHTML = `
    <div class="sidebar-header">
      <span class="eyebrow">${icons.sparkle}<span>Orquestrador · AI</span></span>
      <div class="spacer"></div>
      <button class="mini" id="btn-new-session">Nova Sessão</button>
      <button class="mini" id="btn-add-lane">${icons.plus}<span>Equipe Paralela</span></button>
    </div>
    <div class="lane-bar" id="lane-bar"></div>
    <div class="pipeline-bar" id="pipeline-bar"></div>
    <div class="token-bar" id="token-bar"></div>
    <div class="chat-messages" id="chat-messages"></div>
    <div class="composer-wrap">
      <div class="mention-popup" id="mention-popup" style="display:none"></div>
      <div class="composer-box">
        <div class="composer-mode-bar" id="composer-mode-bar"></div>
        <div class="composer-images" id="composer-images" style="display:none"></div>
        <textarea id="chat-input" rows="3" placeholder="Descreva a tarefa para a equipe… (Ctrl+V cola print, Ctrl+L envia seleção, @arquivo, /ajuda)"></textarea>
        <div class="composer-footer">
          <input type="file" id="chat-image-input" accept="image/*" multiple style="display:none" />
          <button class="mini" id="btn-attach-image" title="Anexar foto/print (ou cole com Ctrl+V)">${icons.image}<span>Foto</span></button>
          <input id="jira-key-input" style="width:94px" placeholder="Jira (PROJ-1)" value="${escapeHtml(state.jiraKeyInput)}" />
          <span class="dim" id="composer-team-label"></span>
          <div class="spacer"></div>
          <button class="danger mini" id="btn-cancel-run" ${lane?.running ? "" : "disabled"}>${icons.stop}<span>Parar</span></button>
          <button class="primary composer-send" id="btn-send-run" ${lane?.running ? "disabled" : ""}>${icons.send}<span>Enviar</span></button>
        </div>
      </div>
    </div>
  `;

  const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
  if (chatInput) chatInput.value = state.draftObjective;

  renderComposerModeBar();
  renderLaneBarOnly();
  renderPipelineBar();
  renderTokenBar();
  renderComposerImages();
  rebuildChatMessagesFull();
  bindRightStaticEvents();
}

function renderComposerModeBar() {
  const bar = document.getElementById("composer-mode-bar");
  if (!bar) return;
  const perm = (state.project?.teamConfig?.settings?.permission_mode || "governed") as PermissionMode;
  const isFull = perm === "full_access";
  const modes: Array<{ id: RunMode; label: string; icon: string; tip: string }> = [
    { id: "auto", label: "Auto", icon: icons.sparkle, tip: "Triagem inteligente: responde direto ou aciona o Squad em worktree" },
    { id: "direct", label: "Direto", icon: icons.bolt, tip: "1 Agente (Chefe) sem delegar — rápido e econômico em tokens" },
    { id: "plan", label: "Planejar", icon: icons.plan, tip: "Gera plano + divisão de arquivos e pausa para aprovação" },
    { id: "squad", label: "Squad", icon: icons.swarm, tip: "Força pipeline completo em worktree: Chefe → Devs → QA → Fecho" },
  ];
  bar.innerHTML = `
    ${modes.map((m) => `
      <button class="mode-chip ${state.runMode === m.id ? "active" : ""}" data-run-mode="${m.id}" title="${escapeHtml(m.tip)}">
        ${m.icon}
        <span>${m.label}</span>
      </button>
    `).join("")}
    <div class="spacer"></div>
    <button class="mode-chip ${isFull ? "active" : ""}" id="btn-composer-perm" title="Alternar nível de permissão e autonomia (Governado / Auto-Edição / Full Acesso)">
      ${icons.bolt}
      <span>${escapeHtml(permissionModeLabel(perm))}</span>
    </button>
    <button class="mode-chip" id="btn-composer-memory" title="Abrir Memória Compartilhada (.hivemind/MEMORY.md)">
      ${icons.memory}
      <span>Memória</span>
    </button>
  `;
  bar.querySelectorAll("[data-run-mode]").forEach((btn) => {
    btn.addEventListener("click", () => {
      state.runMode = (btn as HTMLElement).dataset.runMode as RunMode;
      renderComposerModeBar();
      updateStatusBar();
      updateMissionBoardIfOpen();
    });
  });
  bar.querySelector("#btn-composer-perm")?.addEventListener("click", () => {
    const order: PermissionMode[] = ["governed", "auto_edit", "full_access"];
    const next = order[(order.indexOf(perm) + 1) % order.length];
    setProjectPermissionMode(next);
  });
  bar.querySelector("#btn-composer-memory")?.addEventListener("click", () => openTeamMemoryFile());
}

function renderTokenBar() {
  const bar = document.getElementById("token-bar");
  const lane = activeLane();
  if (!bar || !lane) return;
  const maxTok = state.project?.teamConfig?.settings?.max_tokens || 0;
  const warnBudget = maxTok > 0 && lane.turnTotalTokens >= maxTok * 0.8;

  bar.innerHTML = `
    <span class="tok-pill" title="Turno atual — Entrada: ${exactTokens(lane.turnInTokens)} | Saída: ${exactTokens(lane.turnOutTokens)}">
      ${icons.bolt}
      <span>Turno: ${formatTokens(lane.turnTotalTokens)}</span>
      <span class="dim">(↑${formatTokens(lane.turnInTokens)} ↓${formatTokens(lane.turnOutTokens)})</span>
    </span>
    <span class="tok-pill" title="Total desta sessão (${escapeHtml(lane.sessionId || "nova")}) — Entrada: ${exactTokens(lane.sessionInTokens)} | Saída: ${exactTokens(lane.sessionOutTokens)}">
      <span>Sessão: ${formatTokens(lane.sessionTotalTokens)} tok</span>
    </span>
    ${maxTok > 0 ? `<span class="tok-pill ${warnBudget ? "warn" : ""}" title="Limite configurado em .hivemind/team.yaml">Limite: ${formatTokens(lane.turnTotalTokens)}/${formatTokens(maxTok)}</span>` : ""}
    <div class="spacer"></div>
    <button class="ghost mini" id="btn-open-sessions-bar" style="padding:1px 6px;font-size:11px" title="Ver todas as sessões salvas e total do projeto">Histórico</button>
  `;
  bar.querySelector("#btn-open-sessions-bar")?.addEventListener("click", () => {
    openSidebarView("sessions");
    refreshSessions();
  });
}

function renderLaneBarOnly() {
  const bar = document.getElementById("lane-bar");
  const lane = activeLane();
  if (bar) {
    bar.innerHTML = state.lanes.map((l) => `
      <button type="button" class="lane-pill ${l.id === lane?.id ? "active" : ""}" data-lane="${l.id}">
        ${l.running ? `<span class="lane-dot"></span>` : ""}
        <span>${escapeHtml(l.teamName)}</span>
        ${l.sessionTotalTokens > 0 ? `<span class="dim" style="font-size:10px">${formatTokens(l.sessionTotalTokens)}</span>` : ""}
      </button>
    `).join("");
    bar.querySelectorAll("[data-lane]").forEach((el) => {
      el.addEventListener("click", () => {
        state.activeLaneId = (el as HTMLElement).dataset.lane!;
        renderLaneBarOnly();
        renderPipelineBar();
        renderTokenBar();
        rebuildChatMessagesFull();
        syncComposerButtons();
        updateStatusBar();
        updateMissionBoardIfOpen();
        if ((state.sidebarView === "team" || state.sidebarView === "sessions") && state.showLeftSidebar) {
          renderLeftSidebar();
        }
      });
    });
  }
  const teamLbl = document.getElementById("composer-team-label");
  if (teamLbl && lane) {
    teamLbl.textContent = lane.teamName;
  }
}

function renderPipelineBar() {
  const bar = document.getElementById("pipeline-bar");
  const lane = activeLane();
  if (!bar) return;
  const steps: Array<{ id: "chief" | "dev" | "qa" | "closing"; name: string }> = [
    { id: "chief", name: "Chefe" },
    { id: "dev", name: "Dev" },
    { id: "qa", name: "QA" },
    { id: "closing", name: "Fecho" },
  ];
  bar.innerHTML = steps
    .map((s, idx) => {
      const isAct = lane?.running && lane.stage === s.id;
      const isDone = lane?.doneStages?.includes(s.id) && !isAct;
      const cls = isAct ? "active" : isDone ? "done" : "";
      const iconHtml = isAct ? icons.spinner : isDone ? icons.check : "";
      return `${idx > 0 ? `<span class="pipe-sep">${icons.arrowRight}</span>` : ""}<span class="pipe-step ${cls}">${iconHtml}<span>${s.name}</span></span>`;
    })
    .join("");
}

function renderComposerImages() {
  const wrap = document.getElementById("composer-images");
  if (!wrap) return;
  if (state.draftImages.length === 0) {
    wrap.style.display = "none";
    wrap.innerHTML = "";
    return;
  }
  wrap.style.display = "flex";
  wrap.innerHTML = state.draftImages
    .map((img) => {
      const safeSrc = safeImageSrc(img.dataBase64);
      if (!safeSrc) return "";
      return `
        <div class="img-thumb-wrap" title="${escapeHtml(img.name)}">
          <img src="${escapeHtml(safeSrc)}" data-preview-img="${escapeHtml(safeSrc)}" alt="${escapeHtml(img.name)}" />
          <button class="img-thumb-remove" data-remove-img="${escapeHtml(img.id)}" title="Remover imagem" aria-label="Remover imagem">${icons.closeSmall}</button>
        </div>
      `;
    })
    .join("");
  wrap.querySelectorAll("[data-remove-img]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = (btn as HTMLElement).dataset.removeImg!;
      state.draftImages = state.draftImages.filter((x) => x.id !== id);
      renderComposerImages();
    });
  });
  wrap.querySelectorAll("[data-preview-img]").forEach((imgEl) => {
    imgEl.addEventListener("click", () => {
      state.imagePreviewUrl = (imgEl as HTMLElement).dataset.previewImg || "";
      renderModalLayer();
    });
  });
}

function attachImageFiles(files: FileList | File[]) {
  const list = Array.from(files).filter((f) => f.type.startsWith("image/"));
  if (list.length === 0) return;
  for (const file of list) {
    if (state.draftImages.length >= 8) break;
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === "string") {
        state.draftImages.push({
          id: `img-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
          name: file.name || "print.png",
          mimeType: file.type || "image/png",
          dataBase64: reader.result,
        });
        renderComposerImages();
      }
    };
    reader.readAsDataURL(file);
  }
}

function messageDigest(m: ChatMessage): string {
  return `${m.kind}|${m.active ? 1 : 0}|${m.approved ? 1 : 0}|${m.resolved || ""}|${m.label}|${m.tokens || 0}|${m.durationMs || 0}|${m.toolDetail?.length || 0}|${m.toolOutput?.length || 0}|${m.images?.length || 0}|${m.diffItems?.length || 0}|${m.text.length}|${m.text.slice(-16)}`;
}

function createMessageElement(m: ChatMessage): HTMLElement {
  const div = document.createElement("div");
  div.className = `msg-card role-${m.kind}`;
  div.dataset.msgId = m.id;
  updateMessageElement(div, m);
  return div;
}

function formatToolDuration(ms?: number): string {
  if (!ms || ms <= 0) return "";
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

function updateMessageElement(el: HTMLElement, m: ChatMessage) {
  const digest = messageDigest(m);
  if (el.dataset.digest === digest) return;
  el.dataset.digest = digest;

  if (m.kind === "tool") {
    const durLabel = formatToolDuration(m.durationMs);
    const hasDrawer = Boolean(m.toolDetail || m.toolOutput);
    const shortArg = m.toolDetail ? m.toolDetail.split("\n")[0].slice(0, 72) : "";
    if (!hasDrawer) {
      el.innerHTML = `
        <span class="msg-badge badge-tool">${m.active ? icons.spinner : icons.check}<span>${escapeHtml(m.label)}</span></span>
        <span class="msg-body">${escapeHtml(m.text)}</span>
        ${durLabel ? `<span class="tool-duration">${escapeHtml(durLabel)}</span>` : ""}
      `;
      return;
    }
    const wasOpen = el.querySelector("details")?.hasAttribute("open") ?? false;
    const drawerLines: string[] = [];
    if (m.toolDetail) drawerLines.push(`Args: ${m.toolDetail}`);
    if (m.toolOutput) drawerLines.push(`Saída: ${m.toolOutput}`);
    el.innerHTML = `
      <details class="tool-details" ${wasOpen ? "open" : ""}>
        <summary>
          <span class="msg-badge badge-tool">${m.active ? icons.spinner : icons.check}<span>${escapeHtml(m.label)}</span></span>
          <span class="msg-body">${escapeHtml(m.text)}${shortArg ? ` <span class="dim">· ${escapeHtml(shortArg)}</span>` : ""}</span>
          ${durLabel ? `<span class="tool-duration">${escapeHtml(durLabel)}</span>` : ""}
        </summary>
        <div class="tool-drawer">${escapeHtml(drawerLines.join("\n\n"))}</div>
      </details>
    `;
    return;
  }

  if (m.kind === "plan") {
    el.className = "msg-card role-plan";
    el.innerHTML = `
      <div style="display:flex;align-items:center;justify-content:space-between;gap:8px">
        <span class="msg-badge badge-chief" style="margin-bottom:0">${icons.plan}<span>${escapeHtml(m.label)}</span></span>
        <button class="mini primary" data-approve-plan="${escapeHtml(m.id)}" ${m.approved ? "disabled" : ""}>
          ${m.approved ? icons.check : icons.play}
          <span>${m.approved ? "Squad Acionado" : "Aprovar & Executar Squad"}</span>
        </button>
      </div>
      <div class="msg-body">${renderMarkdownLite(m.text)}</div>
    `;
    el.querySelector("[data-approve-plan]")?.addEventListener("click", () => {
      if (m.approved) return;
      m.approved = true;
      updateMessageElement(el, m);
      executeApprovedPlan();
    });
    return;
  }

  if (m.kind === "approval") {
    el.className = "msg-card role-plan";
    const done = !!m.resolved;
    el.innerHTML = `
      <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap">
        <span class="msg-badge badge-chief" style="margin-bottom:0">${icons.bolt}<span>${escapeHtml(m.label)}</span></span>
        <div style="display:flex;gap:6px">
          <button class="mini primary" data-approve-gate="1" ${done ? "disabled" : ""}>
            ${icons.check}
            <span>${m.resolved === "approved" ? "Autorizado" : "Autorizar"}</span>
          </button>
          <button class="mini danger" data-deny-gate="1" ${done ? "disabled" : ""}>
            <span>${m.resolved === "denied" ? "Negado" : "Negar"}</span>
          </button>
        </div>
      </div>
      <div class="msg-body">${escapeHtml(m.text)}</div>
    `;
    const lane = activeLane();
    el.querySelector("[data-approve-gate]")?.addEventListener("click", async () => {
      if (m.resolved || !lane) return;
      m.resolved = "approved";
      updateMessageElement(el, m);
      await api()?.RespondApproval(lane.id, true);
    });
    el.querySelector("[data-deny-gate]")?.addEventListener("click", async () => {
      if (m.resolved || !lane) return;
      m.resolved = "denied";
      updateMessageElement(el, m);
      await api()?.RespondApproval(lane.id, false);
    });
    return;
  }

  if (m.kind === "handoff") {
    el.className = "handoff-card";
    el.innerHTML = `
      <details open>
        <summary>${icons.team}<span>${escapeHtml(m.label)}</span></summary>
        <div class="handoff-body">${renderMarkdownLite(m.text)}</div>
      </details>
    `;
    return;
  }

  if (m.kind === "diff") {
    const items = m.diffItems || [];
    el.innerHTML = `
      <div style="display:flex;align-items:center;justify-content:space-between;gap:8px">
        <span class="msg-badge badge-tool">${icons.gitSmall}<span>${escapeHtml(m.label)}</span></span>
        <div style="display:flex;gap:5px">
          <button class="mini primary" data-chat-commit="1">${icons.commit}<span>Commitar</span></button>
          <button class="mini danger" data-chat-discard="1">${icons.trash}<span>Descartar</span></button>
        </div>
      </div>
      <div class="diff-summary-files">
        ${items.map((it) => `
          <button class="diff-chip-btn" data-chat-diff-file="${escapeHtml(it.path)}">
            <span>${escapeHtml(it.path)}</span>
            <span style="color:var(--green)">+${it.added}</span>
            <span style="color:var(--red)">-${it.removed}</span>
          </button>
        `).join("")}
      </div>
    `;
    el.querySelectorAll("[data-chat-diff-file]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const p = (btn as HTMLElement).dataset.chatDiffFile!;
        const found = state.diffs.find((d) => d.path === p);
        if (found) openDiffInEditor(found);
      });
    });
    el.querySelector("[data-chat-commit]")?.addEventListener("click", async () => {
      if (!state.project) return;
      try {
        const msg = await api()?.CommitChanges(state.project.root, state.commitMsgInput || "feat: entrega HiveMind IDE", false);
        if (msg) setStatus(msg);
        refreshDiffs();
      } catch (err: any) {
        toastError("criar o commit das alterações", err);
      }
    });
    el.querySelector("[data-chat-discard]")?.addEventListener("click", () => {
      promptDiscardChanges();
    });
    return;
  }

  const bodyHtml = m.kind === "chief" || m.kind === "system" ? renderMarkdownLite(m.text) : escapeHtml(m.text);
  const validImages = (m.images || []).map(safeImageSrc).filter(Boolean);
  const imgsHtml = validImages.length > 0
    ? `<div class="msg-images">${validImages.map((src) => `<img src="${escapeHtml(src)}" data-preview-img="${escapeHtml(src)}" alt="anexo" />`).join("")}</div>`
    : "";
  const tokHtml = m.kind === "chief" && m.tokens && m.tokens > 0
    ? `<div class="msg-tok-footer">${icons.bolt}<span>${exactTokens(m.tokens)} tokens</span></div>`
    : "";
  el.innerHTML = `
    <span class="msg-badge badge-${m.kind}">${escapeHtml(m.label)}</span>
    <div class="msg-body">${bodyHtml}</div>
    ${imgsHtml}
    ${tokHtml}
  `;
  el.querySelectorAll("[data-preview-img]").forEach((imgEl) => {
    imgEl.addEventListener("click", () => {
      state.imagePreviewUrl = (imgEl as HTMLElement).dataset.previewImg || "";
      renderModalLayer();
    });
  });
}

function rebuildChatMessagesFull() {
  const box = document.getElementById("chat-messages");
  const lane = activeLane();
  if (!box || !lane) return;
  box.innerHTML = "";
  const frag = document.createDocumentFragment();
  for (const m of lane.messages) {
    frag.appendChild(createMessageElement(m));
  }
  box.appendChild(frag);
  scrollChatIfAuto();
}

function scheduleChatFlush() {
  if (chatFlushRaf) return;
  chatFlushRaf = requestAnimationFrame(() => {
    chatFlushRaf = 0;
    flushChatUpdates();
  });
}

function flushChatUpdates() {
  const box = document.getElementById("chat-messages");
  const lane = activeLane();
  if (!box || !lane) return;

  for (const m of lane.messages) {
    const existing = box.querySelector<HTMLElement>(`[data-msg-id="${CSS.escape(m.id)}"]`);
    if (existing) {
      updateMessageElement(existing, m);
    } else {
      box.appendChild(createMessageElement(m));
    }
  }
  renderLaneBarOnly();
  renderPipelineBar();
  renderTokenBar();
  syncComposerButtons();
  scrollChatIfAuto();
}

function syncComposerButtons() {
  const lane = activeLane();
  const cancelBtn = document.getElementById("btn-cancel-run") as HTMLButtonElement | null;
  const sendBtn = document.getElementById("btn-send-run") as HTMLButtonElement | null;
  if (cancelBtn) cancelBtn.disabled = !lane?.running;
  if (sendBtn) sendBtn.disabled = !!lane?.running;
}

function scrollChatIfAuto() {
  const box = document.getElementById("chat-messages");
  const lane = activeLane();
  if (box && lane?.autoScroll) {
    box.scrollTop = box.scrollHeight;
  }
}

function renderMentionPopup() {
  const pop = document.getElementById("mention-popup");
  if (!pop) return;
  if (state.mentionResults.length === 0) {
    pop.style.display = "none";
    pop.innerHTML = "";
    return;
  }
  pop.style.display = "block";
  pop.innerHTML = state.mentionResults
    .map((f) => `<button class="dropdown-item" data-mention="${escapeHtml(f)}">@${escapeHtml(f)}</button>`)
    .join("");
  pop.querySelectorAll("[data-mention]").forEach((el) => {
    el.addEventListener("click", () => {
      const f = (el as HTMLElement).dataset.mention!;
      state.draftObjective = state.draftObjective.replace(/@([^\s@]*)$/, `@${f} `);
      state.mentionResults = [];
      const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
      if (chatInput) {
        chatInput.value = state.draftObjective;
        chatInput.focus();
      }
      renderMentionPopup();
    });
  });
}

function bindRightStaticEvents() {
  document.getElementById("btn-add-lane")?.addEventListener("click", () => {
    const lane = activeLane();
    const teams = state.project?.teamConfig?.teams || [];
    const used = new Set(state.lanes.map((l) => l.teamName));
    const next = teams.find((t) => !used.has(t.name))?.name || lane?.teamName || "principal";
    const created = createLane(next);
    state.lanes.push(created);
    state.activeLaneId = created.id;
    renderLaneBarOnly();
    renderPipelineBar();
    renderTokenBar();
    rebuildChatMessagesFull();
    syncComposerButtons();
    updateStatusBar();
    updateMissionBoardIfOpen();
    if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
  });

  document.getElementById("btn-new-session")?.addEventListener("click", () => {
    const lane = activeLane();
    if (!lane || lane.running) return;
    lane.sessionId = "";
    lane.turnInTokens = 0;
    lane.turnOutTokens = 0;
    lane.turnTotalTokens = 0;
    lane.sessionInTokens = 0;
    lane.sessionOutTokens = 0;
    lane.sessionTotalTokens = 0;
    lane.agents = buildAgentRuntimes(lane.teamName);
    lane.messages = [{
      id: `sys-${Date.now()}`,
      kind: "system",
      label: "Sistema",
      text: "Nova sessão iniciada com o Chefe (contador de tokens da sessão zerado).",
    }];
    rebuildChatMessagesFull();
    renderTokenBar();
    renderLaneBarOnly();
    updateStatusBar();
    updateMissionBoardIfOpen();
    if ((state.sidebarView === "team" || state.sidebarView === "sessions") && state.showLeftSidebar) {
      renderLeftSidebar();
    }
  });

  const chatBox = document.getElementById("chat-messages");
  chatBox?.addEventListener("scroll", () => {
    const lane = activeLane();
    if (!lane || !chatBox) return;
    lane.autoScroll = chatBox.scrollHeight - chatBox.scrollTop - chatBox.clientHeight < 28;
  });
  chatBox?.addEventListener("click", (e) => {
    const target = (e.target as HTMLElement)?.closest<HTMLButtonElement>("[data-copy-code]");
    if (!target) return;
    const wrap = target.closest(".md-code-wrap");
    const codeText = wrap?.querySelector("code")?.textContent ?? "";
    if (!codeText) return;
    navigator.clipboard?.writeText(codeText).then(() => {
      const prev = target.textContent;
      target.textContent = "Copiado!";
      window.setTimeout(() => {
        target.textContent = prev || "Copiar";
      }, 1400);
    }).catch(() => {});
  });

  const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
  chatInput?.addEventListener("input", () => {
    state.draftObjective = chatInput.value;
    const m = state.draftObjective.match(/@([^\s@]*)$/);
    if (mentionTimer) window.clearTimeout(mentionTimer);
    if (m && state.project) {
      const q = m[1];
      const root = state.project.root;
      mentionTimer = window.setTimeout(async () => {
        const res = await api()?.SearchFiles(root, q);
        state.mentionResults = res || [];
        renderMentionPopup();
      }, 80);
    } else if (state.mentionResults.length > 0) {
      state.mentionResults = [];
      renderMentionPopup();
    }
  });
  chatInput?.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      sendToChief();
    }
  });
  chatInput?.addEventListener("paste", (e) => {
    const items = e.clipboardData?.items;
    if (!items) return;
    const imgFiles: File[] = [];
    for (let i = 0; i < items.length; i++) {
      if (items[i].type.startsWith("image/")) {
        const f = items[i].getAsFile();
        if (f) imgFiles.push(f);
      }
    }
    if (imgFiles.length > 0) {
      e.preventDefault();
      attachImageFiles(imgFiles);
    }
  });

  const fileInput = document.getElementById("chat-image-input") as HTMLInputElement | null;
  document.getElementById("btn-attach-image")?.addEventListener("click", () => fileInput?.click());
  fileInput?.addEventListener("change", () => {
    if (fileInput.files && fileInput.files.length > 0) {
      attachImageFiles(fileInput.files);
      fileInput.value = "";
    }
  });

  const rightPane = document.getElementById("right-pane");
  rightPane?.addEventListener("dragover", (e) => {
    e.preventDefault();
    rightPane.classList.add("drag-over");
  });
  rightPane?.addEventListener("dragleave", () => {
    rightPane.classList.remove("drag-over");
  });
  rightPane?.addEventListener("drop", (e) => {
    e.preventDefault();
    rightPane.classList.remove("drag-over");
    if (e.dataTransfer?.files && e.dataTransfer.files.length > 0) {
      attachImageFiles(e.dataTransfer.files);
    }
  });

  const jiraInput = document.getElementById("jira-key-input") as HTMLInputElement | null;
  jiraInput?.addEventListener("input", () => {
    state.jiraKeyInput = jiraInput.value;
  });

  document.getElementById("btn-send-run")?.addEventListener("click", () => sendToChief());
  document.getElementById("btn-cancel-run")?.addEventListener("click", cancelActiveLane);
}

async function executeApprovedPlan() {
  const lane = activeLane();
  if (!lane || lane.running) return;
  await sendToChief("Plano aprovado. Prossigam com a implementação paralela e validação de QA conforme o plano registrado.", "squad");
}

async function sendToChief(overrideText?: string, overrideMode?: RunMode) {
  const lane = activeLane();
  const backend = api();
  if (!lane || !backend || !state.project || lane.running) return;
  const isOverride = typeof overrideText === "string";
  const text = (isOverride ? overrideText : state.draftObjective).trim();
  const attached = isOverride ? [] : [...state.draftImages];
  const effectiveMode = overrideMode || state.runMode;
  if (!text && attached.length === 0) return;

  const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;

  if (!isOverride && text === "/ajuda" && attached.length === 0) {
    lane.messages.push({
      id: `sys-${Date.now()}`,
      kind: "system",
      label: "Sistema",
      text: "Modos & Atalhos:\n- **Auto / Direto / Planejar / Squad** — controle se responde em 1 agente, pede aprovação do plano ou roda todos os Devs em paralelo\n- **Quadro (`Ctrl+Shift+A`)** — painel em tempo real dos agentes + troca rápida de modelo LLM\n- **Memória (`.hivemind/MEMORY.md`)** — contexto persistente compartilhado por todo o time\n- `Ctrl+K` — Paleta de Comandos | `Ctrl+P` — Buscar Arquivo | `Ctrl+Shift+F` — Busca no Código | `Ctrl+L` — Enviar seleção",
    });
    state.draftObjective = "";
    if (chatInput) chatInput.value = "";
    flushChatUpdates();
    return;
  }

  lane.messages.push({
    id: `usr-${Date.now()}`,
    kind: "user",
    label: `Usuário · ${runModeLabel(effectiveMode)}`,
    text: text || "(imagem anexada)",
    images: attached.map((x) => x.dataBase64),
  });

  if (!isOverride) {
    state.draftObjective = "";
    state.draftImages = [];
    state.mentionResults = [];
    if (chatInput) chatInput.value = "";
    renderComposerImages();
    renderMentionPopup();
  }

  const runProjectId = state.activeProjectId;
  const runProjectRoot = state.project.root;
  lane.running = true;
  lane.stage = effectiveMode === "squad" ? "dev" : "chief";
  lane.doneStages = [];
  lane.turnInTokens = 0;
  lane.turnOutTokens = 0;
  lane.turnTotalTokens = 0;
  lane.agents.forEach((a) => {
    a.status = "idle";
    a.tool = "";
  });
  setStatus(`Executando "${lane.teamName}" em modo ${runModeLabel(effectiveMode)}…`);
  renderProjectTabsBar();
  flushChatUpdates();
  updateMissionBoardIfOpen();
  if (state.sidebarView === "team" && state.showLeftSidebar) updateTeamStatusPills();

  try {
    const payloadImgs = attached.map((x) => ({ name: x.name, mimeType: x.mimeType, dataBase64: x.dataBase64 }));
    const res = backend.StartRunWithOptions
      ? await backend.StartRunWithOptions(
          lane.id,
          runProjectRoot,
          lane.teamName,
          text,
          state.jiraKeyInput.trim(),
          lane.sessionId,
          effectiveMode,
          payloadImgs
        )
      : await backend.StartRunWithImages(
          lane.id,
          runProjectRoot,
          lane.teamName,
          text,
          state.jiraKeyInput.trim(),
          lane.sessionId,
          payloadImgs
        );
    const sid = res?.SessionID || res?.sessionID;
    if (sid) lane.sessionId = sid;
    setStatus(`Equipe "${lane.teamName}" concluída.`);
  } catch (err: any) {
    lane.messages.push({
      id: `err-${Date.now()}`,
      kind: "error",
      label: "Erro",
      text: err?.message || String(err),
    });
    toastError("executar a tarefa com a equipe", err);
  } finally {
    lane.running = false;
    lane.stage = "";
    markToolsDone(lane);
    renderProjectTabsBar();
    await refreshSessions(runProjectRoot);
    if (state.activeProjectId === runProjectId) {
      await refreshExplorerTree();
      await reloadCleanOpenTabs();
      await refreshDiffsAndSummarize(lane);
      flushChatUpdates();
      updateMissionBoardIfOpen();
      if (state.sidebarView === "team" && state.showLeftSidebar) updateTeamStatusPills();
    }
  }
}

async function cancelActiveLane() {
  const lane = activeLane();
  const backend = api();
  if (!lane || !backend || !lane.running) return;
  await backend.CancelRun(lane.id);
  markToolsDone(lane);
  lane.messages.push({
    id: `sys-${Date.now()}`,
    kind: "system",
    label: "Sistema",
    text: "Execução interrompida pelo usuário.",
  });
  setStatus("Execução interrompida.");
  flushChatUpdates();
}

function getCommandActions(): CommandAction[] {
  return [
    { id: "mission", label: "Abrir Quadro de Controle Multiagente", shortcut: "Ctrl+Shift+A", run: () => openMissionControlTab() },
    { id: "mode-auto", label: "Modo de Execução: Auto (Triagem Inteligente)", run: () => { state.runMode = "auto"; renderComposerModeBar(); updateStatusBar(); } },
    { id: "mode-direct", label: "Modo de Execução: Direto (1 Agente · Rápido)", run: () => { state.runMode = "direct"; renderComposerModeBar(); updateStatusBar(); } },
    { id: "mode-plan", label: "Modo de Execução: Planejar (Aprovação Humana)", run: () => { state.runMode = "plan"; renderComposerModeBar(); updateStatusBar(); } },
    { id: "mode-squad", label: "Modo de Execução: Squad Completo (Paralelo + QA)", run: () => { state.runMode = "squad"; renderComposerModeBar(); updateStatusBar(); } },
    { id: "perm-governed", label: "Permissão: Governado (Worktree + PR + Aprovação p/ main)", run: () => setProjectPermissionMode("governed") },
    { id: "perm-auto", label: "Permissão: Auto-Edição (Edições e comandos locais livres)", run: () => setProjectPermissionMode("auto_edit") },
    { id: "perm-full", label: "Permissão: Full Acesso & Permissão Total (estilo Claude Code / GPT)", run: () => setProjectPermissionMode("full_access") },
    { id: "memory", label: "Abrir Memória Compartilhada (.hivemind/MEMORY.md)", run: () => openTeamMemoryFile() },
    { id: "sessions", label: "Ver Sessões Salvas & Consumo de Tokens", run: () => { openSidebarView("sessions"); refreshSessions(); } },
    { id: "find-file", label: "Localizar e Substituir no Arquivo Atual", shortcut: "Ctrl+F / Ctrl+H", run: () => openEditorFind(true) },
    { id: "diff-toggle", label: `Alternar Modo de Diff (${state.diffViewMode === "split" ? "Lado a Lado → Unificado" : "Unificado → Lado a Lado"})`, run: () => { state.diffViewMode = state.diffViewMode === "split" ? "unified" : "split"; renderEditorBody(); } },
    { id: "search", label: "Busca Global no Código", shortcut: "Ctrl+Shift+F", run: () => { openSidebarView("search"); (document.getElementById("global-search-input") as HTMLInputElement | null)?.focus(); } },
    { id: "test", label: `Rodar Testes da Stack (${state.project?.stack?.testHint || "git status"})`, run: () => { if (!state.terminal.open) { state.terminal.open = true; renderTerminalDrawer(); } runTerminalCommand(state.project?.stack?.testHint || "git status"); } },
    { id: "term", label: "Alternar Terminal Integrado", shortcut: "Ctrl+J", run: () => toggleTerminal() },
    { id: "wizard", label: "Configurar Equipe (.hivemind/team.yaml)", run: () => { state.showWizard = true; renderModalLayer(); } },
    { id: "opencode-token", label: "Configurar Token OpenCode Zen (OPENCODE_API_KEY)", run: () => openOpenCodeModal() },
    { id: "doctor", label: "Doctor — Verificar Antigravity CLI (agy)", run: () => openDoctorModal() },
    { id: "open-folder-tab", label: "Abrir Outro Projeto em Nova Aba Isolada…", shortcut: "Ctrl+Shift+O", run: () => openFolderDialog(true) },
    { id: "open-folder", label: "Substituir Projeto na Aba Atual…", run: () => openFolderDialog(false) },
  ];
}

function openQuickOpen() {
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

function openCommandPalette() {
  state.quickOpen.open = true;
  state.quickOpen.mode = "commands";
  state.quickOpen.query = "";
  state.quickOpen.selected = 0;
  renderQuickOpenModal();
}

function closeQuickOpen() {
  state.quickOpen.open = false;
  const layer = document.getElementById("quick-open-layer");
  if (layer) layer.innerHTML = "";
}

function filteredCommands(): CommandAction[] {
  const q = state.quickOpen.query.trim().toLowerCase();
  const all = getCommandActions();
  if (!q) return all;
  return all.filter((c) => c.label.toLowerCase().includes(q));
}

function renderQuickOpenModal() {
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

function renderQuickOpenList() {
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

function toggleTerminal() {
  state.terminal.open = !state.terminal.open;
  renderTerminalDrawer();
  if (state.terminal.open) {
    (document.getElementById("term-cmd-input") as HTMLInputElement | null)?.focus();
  }
}

function renderTerminalDrawer() {
  const drawer = document.getElementById("terminal-drawer");
  if (!drawer) return;
  drawer.classList.toggle("hidden", !state.terminal.open);
  if (!state.terminal.open) return;

  const hint = state.project?.stack?.testHint || "";
  drawer.innerHTML = `
    <div class="terminal-header">
      <span class="eyebrow">${icons.terminal}<span>Terminal</span></span>
      ${hint ? `<button class="mini success" id="term-run-hint" ${state.terminal.running ? "disabled" : ""}>${icons.play}<span>${escapeHtml(hint)}</span></button>` : ""}
      <button class="mini" id="term-run-status" ${state.terminal.running ? "disabled" : ""}>git status</button>
      ${state.terminal.running ? `<button class="mini danger" id="term-cancel-cmd" title="Interromper comando em execução">${icons.stop}<span>Parar Comando</span></button>` : ""}
      <div class="spacer"></div>
      <button class="mini ghost" id="term-clear">Limpar</button>
      <button class="icon-btn" id="term-close" title="Fechar Terminal (Ctrl+J)">${icons.closeSmall}</button>
    </div>
    <div class="terminal-output" id="terminal-output">
      ${state.terminal.entries.length === 0
        ? `<div class="dim">Digite um comando abaixo ou clique no atalho da stack (${escapeHtml(hint || "ex: git status")}).</div>`
        : state.terminal.entries.map((en) => `
          <div class="term-entry">
            <div class="term-cmd-line">
              <span>$ ${escapeHtml(en.command)}</span>
              ${en.running
                ? icons.spinner
                : `<span class="dim" style="color:${en.exitCode === 0 ? "var(--green)" : "var(--red)"}">[exit ${en.exitCode} · ${en.durationMs}ms]</span>`
              }
            </div>
            <div data-term-out="${escapeHtml(en.id)}">${escapeHtml(en.output)}</div>
          </div>
        `).join("")
      }
    </div>
    <div class="term-input-bar">
      <span class="dim">$</span>
      <input id="term-cmd-input" placeholder="Executar comando no projeto (Enter)…" value="${escapeHtml(state.terminal.cmdInput)}" ${state.terminal.running ? "disabled" : ""} />
      ${state.terminal.running
        ? `<button class="danger mini" id="term-cancel-bottom">${icons.stop}<span>Parar</span></button>`
        : `<button class="primary mini" id="term-send">Executar</button>`
      }
    </div>
  `;

  const outBox = document.getElementById("terminal-output");
  if (outBox) outBox.scrollTop = outBox.scrollHeight;

  const cancelRunningTerm = async () => {
    if (!state.project) return;
    await api()?.CancelCommand(state.project.root);
    setStatus("Comando do terminal interrompido.");
  };
  document.getElementById("term-cancel-cmd")?.addEventListener("click", cancelRunningTerm);
  document.getElementById("term-cancel-bottom")?.addEventListener("click", cancelRunningTerm);

  document.getElementById("term-close")?.addEventListener("click", () => toggleTerminal());
  document.getElementById("term-clear")?.addEventListener("click", () => {
    state.terminal.entries = [];
    renderTerminalDrawer();
  });
  document.getElementById("term-run-hint")?.addEventListener("click", () => {
    if (hint) runTerminalCommand(hint);
  });
  document.getElementById("term-run-status")?.addEventListener("click", () => runTerminalCommand("git status"));

  const cmdIn = document.getElementById("term-cmd-input") as HTMLInputElement | null;
  cmdIn?.addEventListener("input", () => {
    state.terminal.cmdInput = cmdIn.value;
  });
  cmdIn?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      runTerminalCommand(state.terminal.cmdInput);
    }
  });
  document.getElementById("term-send")?.addEventListener("click", () => runTerminalCommand(state.terminal.cmdInput));
}

async function runTerminalCommand(rawCmd: string) {
  const cmd = rawCmd.trim();
  const backend = api();
  if (!cmd || !backend || !state.project || state.terminal.running) return;
  const entry: TerminalEntry = {
    id: `cmd-${Date.now()}`,
    command: cmd,
    output: "Executando…",
    exitCode: null,
    durationMs: 0,
    running: true,
  };
  state.terminal.entries.push(entry);
  state.terminal.cmdInput = "";
  state.terminal.running = true;
  renderTerminalDrawer();

  try {
    const res = await backend.RunCommand(state.project.root, cmd);
    entry.output = res.output || "(sem saída)";
    entry.exitCode = res.exitCode;
    entry.durationMs = res.durationMs;
  } catch (err: any) {
    entry.output = err?.message || String(err);
    entry.exitCode = -1;
  } finally {
    entry.running = false;
    state.terminal.running = false;
    renderTerminalDrawer();
    (document.getElementById("term-cmd-input") as HTMLInputElement | null)?.focus();
    refreshDiffs();
  }
}

function renderModalLayer() {
  const layer = document.getElementById("modal-layer");
  if (!layer) return;

  if (state.confirmModal) {
    const cm = state.confirmModal;
    layer.innerHTML = `
      <div class="modal-backdrop" id="confirm-modal-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:430px">
          <h3 class="modal-title">${escapeHtml(cm.title)}</h3>
          <p style="font-size:12.5px;line-height:1.5;color:var(--text-dim)">${escapeHtml(cm.body)}</p>
          <div class="modal-footer">
            <button id="confirm-modal-cancel">Cancelar</button>
            <button class="${cm.danger ? "danger" : "primary"}" id="confirm-modal-ok">${escapeHtml(cm.confirmLabel)}</button>
          </div>
        </div>
      </div>
    `;
    const closeConfirm = () => {
      state.confirmModal = null;
      renderModalLayer();
    };
    document.getElementById("confirm-modal-cancel")?.addEventListener("click", closeConfirm);
    document.getElementById("confirm-modal-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement).id === "confirm-modal-backdrop") closeConfirm();
    });
    const okBtn = document.getElementById("confirm-modal-ok") as HTMLButtonElement | null;
    okBtn?.focus();
    okBtn?.addEventListener("click", async () => {
      const action = cm.onConfirm;
      closeConfirm();
      await action();
    });
    return;
  }

  if (state.imagePreviewUrl) {
    const safeUrl = safeImageSrc(state.imagePreviewUrl);
    if (!safeUrl) {
      state.imagePreviewUrl = "";
      layer.innerHTML = "";
      return;
    }
    layer.innerHTML = `
      <div class="modal-backdrop" id="img-preview-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:auto;max-width:92vw;padding:12px;text-align:center">
          <img src="${escapeHtml(safeUrl)}" style="max-width:86vw;max-height:80vh;border-radius:6px;display:block" alt="preview" />
          <div class="modal-footer" style="margin-top:10px">
            <button class="primary mini" id="img-preview-close">Fechar</button>
          </div>
        </div>
      </div>
    `;
    document.getElementById("img-preview-close")?.addEventListener("click", () => {
      state.imagePreviewUrl = "";
      renderModalLayer();
    });
    document.getElementById("img-preview-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement).id === "img-preview-backdrop") {
        state.imagePreviewUrl = "";
        renderModalLayer();
      }
    });
    return;
  }

  if (state.pathModal) {
    const pm = state.pathModal;
    const isDelete = pm.mode === "delete";
    const titles: Record<typeof pm.mode, string> = {
      newFile: "Novo Arquivo",
      newDir: "Nova Pasta",
      rename: "Renomear Caminho",
      delete: "Confirmar Exclusão",
    };
    layer.innerHTML = `
      <div class="modal-backdrop" id="path-modal-backdrop">
        <div class="modal-box" style="width:420px">
          <h3 class="modal-title">${titles[pm.mode]}</h3>
          ${isDelete
            ? `<p style="font-size:12.5px">Deseja excluir permanentemente <code>${escapeHtml(pm.targetRel)}</code>?</p>`
            : `<input id="path-modal-input" placeholder="Ex: src/NovoArquivo.java" value="${escapeHtml(pm.value)}" />`
          }
          <div id="path-modal-err" style="color:var(--red);font-size:12px;margin-top:8px;display:none"></div>
          <div class="modal-footer">
            <button id="path-modal-cancel">Cancelar</button>
            <button class="${isDelete ? "danger" : "primary"}" id="path-modal-confirm">${isDelete ? "Excluir" : "Confirmar"}</button>
          </div>
        </div>
      </div>
    `;
    const input = document.getElementById("path-modal-input") as HTMLInputElement | null;
    if (input) {
      input.focus();
      input.selectionStart = input.selectionEnd = input.value.length;
    }

    const closePm = () => {
      state.pathModal = null;
      renderModalLayer();
    };
    document.getElementById("path-modal-cancel")?.addEventListener("click", closePm);

    const submitPm = async () => {
      const backend = api();
      if (!backend || !state.project) return;
      const val = (input?.value || "").trim();
      try {
        if (pm.mode === "newFile") {
          if (!val) return;
          await backend.CreateFile(state.project.root, val);
          closePm();
          await refreshExplorerTree();
          await openFileInEditor(val, val.split("/").pop() || val);
          setStatus(`Arquivo criado: ${val}`);
        } else if (pm.mode === "newDir") {
          if (!val) return;
          await backend.CreateDir(state.project.root, val);
          state.expandedDirs.add(val);
          closePm();
          await refreshExplorerTree();
          setStatus(`Pasta criada: ${val}`);
        } else if (pm.mode === "rename") {
          if (!val || val === pm.targetRel) {
            closePm();
            return;
          }
          await backend.RenamePath(state.project.root, pm.targetRel, val);
          for (const t of state.editorTabs) {
            if (t.relPath === pm.targetRel) {
              t.relPath = val;
              t.name = val.split("/").pop() || val;
            }
          }
          if (state.activeEditorPath === pm.targetRel) state.activeEditorPath = val;
          closePm();
          await refreshExplorerTree();
          renderEditorTabs();
          renderEditorBody();
          setStatus(`Renomeado para ${val}`);
        } else if (pm.mode === "delete") {
          await backend.DeletePath(state.project.root, pm.targetRel);
          state.editorTabs = state.editorTabs.filter((t) => t.relPath !== pm.targetRel && !t.relPath.startsWith(pm.targetRel + "/"));
          if (!state.editorTabs.some((t) => t.relPath === state.activeEditorPath)) {
            state.activeEditorPath = state.editorTabs[0]?.relPath || "";
          }
          closePm();
          await refreshExplorerTree();
          renderEditorTabs();
          renderEditorBody();
          refreshDiffs();
          setStatus(`Excluído: ${pm.targetRel}`);
        }
      } catch (err: any) {
        const errEl = document.getElementById("path-modal-err");
        if (errEl) {
          const action = pm.mode === "newFile" ? "criar o arquivo" : pm.mode === "newDir" ? "criar a pasta" : pm.mode === "rename" ? "renomear o caminho" : "excluir";
          const reason = err instanceof Error ? err.message : String(err);
          errEl.textContent = `Não foi possível ${action}: ${reason}. Verifique o caminho e tente novamente.`;
          errEl.style.display = "block";
        }
      }
    };

    input?.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        submitPm();
      }
    });
    document.getElementById("path-modal-confirm")?.addEventListener("click", submitPm);
    return;
  }

  if (state.doctorModal) {
    const d = state.doctorModal;
    layer.innerHTML = `
      <div class="modal-backdrop">
        <div class="modal-box">
          <h3 class="modal-title">Doctor — Antigravity CLI (agy)</h3>
          <p><strong>Encontrado:</strong> ${d.Found ? "Sim" : "Não"}</p>
          <p><strong>Caminho:</strong> ${escapeHtml(d.Path || "-")}</p>
          <p><strong>Autenticado:</strong> ${d.LoggedIn ? "Sim (Google Pro)" : "Não"}</p>
          <p><strong>Detalhe:</strong> ${escapeHtml(d.Detail)}</p>
          <p><strong>Modelos (${state.models.length}):</strong></p>
          <div style="max-height:160px;overflow:auto;background:var(--bg-deep);padding:8px;border-radius:6px;font-size:12px;border:1px solid var(--border)">
            ${state.models.length === 0 ? "Nenhum modelo carregado." : state.models.map((m) => `<div>${escapeHtml(m.slug)} — <span class="dim">${escapeHtml(m.name)}</span></div>`).join("")}
          </div>
          <div class="modal-footer">
            <button class="primary" id="doc-close">Fechar</button>
          </div>
        </div>
      </div>
    `;
    document.getElementById("doc-close")?.addEventListener("click", () => {
      state.doctorModal = null;
      renderModalLayer();
    });
    return;
  }

  if (state.showOpenCodeModal) {
    const oc = state.openCodeStatus;
    const baseUrl = oc?.baseUrl || "https://opencode.ai/zen/v1";
    const statusBadge = oc?.configured
      ? `<span style="color:var(--green);font-weight:600">Conectado (${escapeHtml(oc.keyMasked)}${oc.source === "env" ? " via OPENCODE_API_KEY" : " salvo localmente"})</span>`
      : `<span style="color:var(--amber)">Sem token salvo (apenas modelos <code>-free</code> liberados)</span>`;

    layer.innerHTML = `
      <div class="modal-backdrop" id="oc-modal-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:520px">
          <h3 class="modal-title">OpenCode Zen — Autenticação via Token (Portátil)</h3>
          <p class="dim" style="font-size:12px;margin-top:-4px;margin-bottom:10px">
            Funciona via HTTPS puro em qualquer máquina sem precisar do CLI do OpenCode instalado.
            O token é salvo fora do repositório Git (em <code>credentials.json</code> do seu usuário) ou lido de <code>OPENCODE_API_KEY</code>.
          </p>
          <div style="font-size:12px;margin-bottom:10px"><strong>Status:</strong> ${statusBadge}</div>
          <div class="form-grid">
            <label>Token OpenCode:</label>
            <input id="oc-api-key" type="password" placeholder="${oc?.configured ? "Cole novo token para substituir…" : "Cole sua chave OpenCode Zen (OPENCODE_API_KEY)"}" autocomplete="off" />

            <label>Base URL:</label>
            <input id="oc-base-url" value="${escapeHtml(baseUrl)}" placeholder="https://opencode.ai/zen/v1" />
          </div>
          <div style="margin-top:10px">
            <div class="dim" style="font-size:11.5px;margin-bottom:4px">
              Catálogo OpenCode Go (${state.openCodeGoModels.length}) + OpenCode Zen (${state.openCodeModels.length}) — use <code>opencode-go:&lt;id&gt;</code> ou <code>opencode:&lt;id&gt;</code>:
            </div>
            <div style="max-height:170px;overflow:auto;background:var(--bg-deep);padding:8px;border-radius:6px;font-size:11.5px;font-family:var(--mono);border:1px solid var(--border)">
              ${state.openCodeGoModels.length === 0 && state.openCodeModels.length === 0
                ? `<span class="dim">Carregando catálogos OpenCode Go e Zen…</span>`
                : [
                    ...(state.openCodeGoModels.length > 0
                      ? [`<div style="color:var(--accent);font-weight:600;margin-bottom:3px">— OpenCode Go (Assinatura: ${state.openCodeGoModels.length}) —</div>`]
                          .concat(state.openCodeGoModels.map((m) => `<div>opencode-go:${escapeHtml(m.slug)}</div>`))
                      : []),
                    ...(state.openCodeModels.length > 0
                      ? [`<div style="color:var(--accent);font-weight:600;margin:6px 0 3px 0">— OpenCode Zen (${state.openCodeModels.length}) —</div>`]
                          .concat(state.openCodeModels.map((m) => `<div>opencode:${escapeHtml(m.slug)}</div>`))
                      : []),
                  ].join("")
              }
            </div>
          </div>
          <div id="oc-modal-err" style="color:var(--red);font-size:12px;margin-top:8px;display:none"></div>
          <div class="modal-footer">
            ${oc?.configured && oc.source !== "env" ? `<button class="danger mini" id="oc-clear-token">Remover Token</button>` : ""}
            <div class="spacer"></div>
            <button id="oc-close">Fechar</button>
            <button class="primary" id="oc-save">Salvar & Testar</button>
          </div>
        </div>
      </div>
    `;

    const closeOc = () => {
      state.showOpenCodeModal = false;
      renderModalLayer();
    };
    document.getElementById("oc-close")?.addEventListener("click", closeOc);
    document.getElementById("oc-modal-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement).id === "oc-modal-backdrop") closeOc();
    });
    document.getElementById("oc-clear-token")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.SaveOpenCodeConfig) return;
      try {
        const res = await backend.SaveOpenCodeConfig("", "");
        state.openCodeStatus = res.status;
        if (res.goModels) state.openCodeGoModels = res.goModels;
        if (res.models) state.openCodeModels = res.models;
        updateStatusBar();
        updateMissionBoardIfOpen();
        if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
        renderModalLayer();
        setStatus("Token do OpenCode removido.");
      } catch (err: any) {
        const errEl = document.getElementById("oc-modal-err");
        if (errEl) {
          const reason = err instanceof Error ? err.message : String(err);
          errEl.textContent = `Não foi possível remover o token: ${reason}`;
          errEl.style.display = "block";
        }
      }
    });
    document.getElementById("oc-save")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.SaveOpenCodeConfig) return;
      const keyIn = (document.getElementById("oc-api-key") as HTMLInputElement | null)?.value.trim() || "";
      const urlIn = (document.getElementById("oc-base-url") as HTMLInputElement | null)?.value.trim() || "";
      if (!keyIn && !state.openCodeStatus?.configured) {
        const errEl = document.getElementById("oc-modal-err");
        if (errEl) {
          errEl.textContent = "Cole seu token do OpenCode antes de salvar.";
          errEl.style.display = "block";
        }
        return;
      }
      try {
        setStatus("Salvando e validando token OpenCode (Go + Zen)…");
        const res = keyIn
          ? await backend.SaveOpenCodeConfig(keyIn, urlIn)
          : await backend.BootstrapOpenCode(true);
        state.openCodeStatus = res.status;
        if (res.goModels && res.goModels.length > 0) state.openCodeGoModels = res.goModels;
        if (res.models && res.models.length > 0) state.openCodeModels = res.models;
        updateStatusBar();
        updateMissionBoardIfOpen();
        if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
        renderModalLayer();
        setStatus(`OpenCode pronto: ${res.status.detail}`);
      } catch (err: any) {
        const errEl = document.getElementById("oc-modal-err");
        if (errEl) {
          const reason = err instanceof Error ? err.message : String(err);
          errEl.textContent = `Não foi possível salvar e testar o token: ${reason}. Confira a chave e a Base URL e tente novamente.`;
          errEl.style.display = "block";
        }
      }
    });
    return;
  }

  if (state.showWizard) {
    const settings = state.project?.teamConfig?.settings;
    const activeTeamName = state.project?.teamConfig?.active || "principal";
    const curTeam = (state.project?.teamConfig?.teams || []).find((t) => t.name === activeTeamName) || state.project?.teamConfig?.teams?.[0];
    const existingChief = curTeam?.agents?.find((a) => a.name === "chefe" || a.role === "architect" || a.role === "project_manager");
    const existingDev = curTeam?.agents?.find((a) => a.role === "developer");
    const existingQA = curTeam?.agents?.find((a) => a.role === "qa");
    const curChiefRole = existingChief?.role === "project_manager" ? "project_manager" : "architect";
    const curMaxDynDevs = settings?.max_dynamic_devs && settings.max_dynamic_devs > 0 ? settings.max_dynamic_devs : 5;
    const existingMaxTok = settings?.max_tokens || 0;
    const curPerm = settings?.permission_mode || "governed";
    const curBase = settings?.base_branch || "main";
    const curHomolog = settings?.homolog_branch || "homolog";
    const autoCommitChecked = settings ? settings.auto_commit === true : false;
    const autoPRChecked = settings ? settings.auto_pr === true : false;
    const buildOptWithSel = (selectedModel?: string, includeDefaultBlank = false) =>
      buildModelSelectOptions(selectedModel || "", includeDefaultBlank);

    layer.innerHTML = `
      <div class="modal-backdrop">
        <div class="modal-box">
          <h3 class="modal-title">Montar Equipe HiveMind (.hivemind/team.yaml)</h3>
          <div class="form-grid">
            <label>Nome da equipe:</label>
            <input id="wiz-name" value="${escapeHtml(activeTeamName)}" />

            <label>Chefe da equipe:</label>
            <select id="wiz-chief">
              <option value="architect" ${curChiefRole === "architect" ? "selected" : ""}>Arquiteto</option>
              <option value="project_manager" ${curChiefRole === "project_manager" ? "selected" : ""}>Gerente de Projeto</option>
            </select>

            <label>Permissão / Autonomia:</label>
            <select id="wiz-permission">
              <option value="governed" ${curPerm === "governed" ? "selected" : ""}>Governado (Worktree + PR + Pede permissão p/ main)</option>
              <option value="auto_edit" ${curPerm === "auto_edit" ? "selected" : ""}>Auto-Edição (Edições e comandos locais livres)</option>
              <option value="full_access" ${curPerm === "full_access" ? "selected" : ""}>Full Acesso & Permissão Total (estilo Claude Code / GPT)</option>
            </select>

            <label>Modelo padrão:</label>
            <select id="wiz-model">${buildOptWithSel(existingChief?.model || existingDev?.model)}</select>
          </div>
          <div class="dim" style="margin-top:10px">Time base: <strong>3 agentes</strong> (Chefe/Arquiteto, Dev e QA). Em tarefas grandes, o Arquiteto dispara devs paralelos sob demanda.</div>
          <details class="wiz-advanced" ${settings ? "open" : ""}>
            <summary>${icons.settings}<span>Avançado — modelos por agente, branches, Git e integrações</span></summary>
            <div class="wiz-advanced-body">
              <label>Máx. Devs dinâmicos:</label>
              <input id="wiz-max-devs" type="number" min="1" max="5" value="${curMaxDynDevs}" title="Padrão: 3 agentes (Chefe, Dev, QA). Para tarefas grandes, o Chefe/Arquiteto pode disparar até N devs paralelos sob demanda." />

              <label>Modelo do Chefe:</label>
              <select id="wiz-model-chief">${buildOptWithSel(existingChief?.model, true)}</select>

              <label>Modelo do Dev:</label>
              <select id="wiz-model-dev">${buildOptWithSel(existingDev?.model, true)}</select>

              <label>Modelo do QA:</label>
              <select id="wiz-model-qa">${buildOptWithSel(existingQA?.model, true)}</select>

              <label>Branch Base / Homolog:</label>
              <div style="display:flex;gap:8px">
                <input id="wiz-base-branch" value="${escapeHtml(curBase)}" placeholder="Base (ex: main)" style="flex:1" title="Branch base principal (exige sua permissão para merge)" />
                <input id="wiz-homolog-branch" value="${escapeHtml(curHomolog)}" placeholder="Homolog (ex: homolog)" style="flex:1" title="Branch de homologação (Chefe mescla automático após aprovação do revisor)" />
              </div>

              <label>Limite tokens/turno:</label>
              <input id="wiz-maxtokens" type="number" min="0" step="1000" value="${existingMaxTok}" placeholder="0 = sem limite (ex: 50000)" />

              <label>Worktree separada:</label>
              <div>
                <input id="wiz-worktrees" type="checkbox" ${settings?.worktrees !== false ? "checked" : ""} />
                <span class="wiz-hint">Recomendado: cada execução acontece numa cópia isolada do projeto.</span>
              </div>

              <label>Commit automático:</label>
              <div>
                <input id="wiz-autocommit" type="checkbox" ${autoCommitChecked ? "checked" : ""} />
                <span class="wiz-hint">Desligado por padrão: o Chefe pergunta antes de commitar.</span>
              </div>

              <label>Criar PR automático:</label>
              <div>
                <input id="wiz-autopr" type="checkbox" ${autoPRChecked ? "checked" : ""} />
                <span class="wiz-hint">Desligado por padrão: o PR só abre quando você pedir.</span>
              </div>

              <label>Jira Cloud:</label>
              <div style="display:flex;gap:8px;align-items:center">
                <input id="wiz-jira" type="checkbox" ${settings?.jira_enabled ? "checked" : ""} />
                <input id="wiz-jira-key" placeholder="Projeto Jira (ex: PROJ)" value="${escapeHtml(settings?.jira_project || "")}" style="flex:1" />
              </div>
            </div>
          </details>
          <div id="wiz-error" style="color:var(--red);font-size:12px;margin-top:8px;display:none"></div>
          <div class="modal-footer">
            <button class="mini" id="wiz-opencode-btn" title="Configurar token portátil do OpenCode Zen (OPENCODE_API_KEY)">${icons.bolt}<span>Token OpenCode…</span></button>
            <div class="spacer"></div>
            <button id="wiz-cancel">Cancelar</button>
            <button class="primary" id="wiz-save">Salvar Equipe</button>
          </div>
        </div>
      </div>
    `;

    document.getElementById("wiz-opencode-btn")?.addEventListener("click", () => {
      state.showWizard = false;
      openOpenCodeModal();
    });
    document.getElementById("wiz-cancel")?.addEventListener("click", () => {
      state.showWizard = false;
      renderModalLayer();
    });
    document.getElementById("wiz-save")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend || !state.project) return;
      const val = (id: string) => (document.getElementById(id) as HTMLInputElement | HTMLSelectElement)?.value || "";
      const chk = (id: string) => (document.getElementById(id) as HTMLInputElement)?.checked || false;
      const maxDynamicDevs = Math.max(1, Math.min(5, parseInt(val("wiz-max-devs"), 10) || 5));
      const maxTokens = Math.max(0, parseInt(val("wiz-maxtokens"), 10) || 0);
      const defaultModel = val("wiz-model") || "agy";
      const agentModels: Record<string, string> = {};
      if (val("wiz-model-chief")) agentModels["chefe"] = val("wiz-model-chief");
      if (val("wiz-model-qa")) agentModels["qa"] = val("wiz-model-qa");
      if (val("wiz-model-dev")) agentModels["dev"] = val("wiz-model-dev");
      try {
        const cfg = await backend.SaveTeamWizard(state.project.root, {
          teamName: val("wiz-name") || "principal",
          chiefRole: val("wiz-chief") || "architect",
          devCount: 1,
          maxDynamicDevs,
          defaultModel,
          agentModels,
          permissionMode: (val("wiz-permission") || "governed") as PermissionMode,
          worktrees: chk("wiz-worktrees"),
          autoCommit: chk("wiz-autocommit"),
          autoPR: chk("wiz-autopr"),
          jiraEnabled: chk("wiz-jira"),
          jiraProject: val("wiz-jira-key"),
          baseBranch: val("wiz-base-branch") || "main",
          homologBranch: val("wiz-homolog-branch") || "homolog",
          maxTokens,
        });
        state.project.hasTeam = true;
        state.project.teamConfig = cfg;
        state.showWizard = false;
        const lane = activeLane();
        if (lane) {
          lane.teamName = cfg.active || "principal";
          lane.agents = buildAgentRuntimes(lane.teamName);
        }
        renderModalLayer();
        renderLaneBarOnly();
        renderTokenBar();
        updateStatusBar();
        updateMissionBoardIfOpen();
        if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
        setStatus(`Equipe "${cfg.active}" salva em .hivemind/team.yaml.`);
      } catch (err: any) {
        const reason = err instanceof Error ? err.message : err?.message || String(err);
        const errEl = document.getElementById("wiz-error");
        if (errEl) {
          errEl.textContent = `Não foi possível salvar a equipe: ${reason}`;
          errEl.style.display = "block";
        }
      }
    });
    return;
  }

  layer.innerHTML = "";
}

boot();
