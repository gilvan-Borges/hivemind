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
  ProjectInfo,
  ProjectIssue,
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
  project: null as ProjectInfo | null,
  recent: [] as string[],
  models: [] as AgyModel[],
  agyStatus: null as AgyStatus | null,
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
};

let laneSeq = 1;
let chatFlushRaf = 0;
let editorHighlightRaf = 0;
let mentionTimer = 0;
let quickOpenTimer = 0;
let globalSearchTimer = 0;
let explorerFilterTimer = 0;

function hasDirtyTabs(): boolean {
  return state.editorTabs.some((t) => t.kind === "file" && t.dirty);
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
        <span class="status-chip" id="status-branch" style="cursor:pointer" title="Clique para trocar ou criar branch Git">${icons.gitSmall} sem git</span>
        <span id="status-stack"></span>
        <span id="status-team">Equipe: -</span>
        <span class="status-chip" id="status-tokens" style="cursor:pointer" title="Clique para abrir Sessões & Consumo de Tokens">${icons.tokens} 0 tok</span>
        <span id="status-agy" class="dim" style="cursor:pointer" title="Clique para abrir Doctor (agy)">agy: verificando…</span>
        <button class="ghost mini" id="status-term-btn" style="height:20px;padding:0 6px" title="Abrir/fechar Terminal (Ctrl+J)">${icons.terminal}<span>Terminal</span></button>
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
    setStatus(`Erro ao abrir MEMORY.md: ${err?.message || err}`);
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
    setStatus(`Erro ao listar branches: ${err?.message || err}`);
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
    if (hasDirtyTabs()) {
      askConfirm({
        title: "Alterações não salvas",
        body: "Existem abas editadas sem salvar. Deseja fechar a HiveMind IDE mesmo assim?",
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
          updateHeaderAndStatusBar();
          refreshDiffs();
          refreshExplorerTree();
          await reloadCleanOpenTabs();
          setStatus(`Branch alterada para ${targetBranch}.`);
        } catch (err: any) {
          setStatus(`Erro ao trocar branch: ${err?.message || err}`);
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
    if (!name || !state.project) return;
    try {
      await api()?.CheckoutBranch(state.project.root, name, true);
      state.project.branch = name;
      state.branchPopover.open = false;
      renderBranchPopover();
      updateHeaderAndStatusBar();
      refreshDiffs();
      setStatus(`Branch "${name}" criada e ativada.`);
    } catch (err: any) {
      setStatus(`Erro ao criar branch: ${err?.message || err}`);
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
    <button class="dropdown-item" id="menu-open-folder">${icons.folderOpen}<span>Abrir Pasta…</span></button>
    <button class="dropdown-item" id="menu-open-memory">${icons.memory}<span>Memória do Time (.hivemind/MEMORY.md)</span></button>
    <button class="dropdown-item" id="menu-new-team">${icons.settings}<span>Configurar Equipe…</span></button>
    <button class="dropdown-item" id="menu-doctor">${icons.bolt}<span>Doctor — Antigravity CLI (agy)</span></button>
    <div class="dim" style="padding:6px 10px 3px 10px;border-top:1px solid var(--border);margin-top:4px">Projetos Recentes</div>
    ${state.recent.length === 0
      ? `<div class="dim" style="padding:6px 10px">Nenhum projeto recente</div>`
      : state.recent.map((r) => `<button class="dropdown-item" data-recent="${escapeHtml(r)}" title="${escapeHtml(r)}">${escapeHtml(r)}</button>`).join("")
    }
  `;
  menu.querySelector("#menu-open-folder")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    openFolderDialog();
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
  menu.querySelector("#menu-doctor")?.addEventListener("click", () => {
    state.showFileMenu = false;
    renderFileMenu();
    openDoctorModal();
  });
  menu.querySelectorAll("[data-recent]").forEach((el) => {
    el.addEventListener("click", () => openProjectPath((el as HTMLElement).dataset.recent!));
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
  if (teamEl) teamEl.textContent = `Equipe: ${lane?.teamName || "-"} (${lane?.agents.length || 0}) · ${runModeLabel(state.runMode)}`;
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
    const lane = state.lanes.find((l) => l.id === payload.laneId);
    if (!lane) return;
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
      state.sessionsSummary.projectInputTokens += inTok;
      state.sessionsSummary.projectOutputTokens += outTok;
      state.sessionsSummary.projectTotalTokens += totTok;

      const ag = matchAgentRuntime(lane.agents, ev.author, ev.label);
      if (ag) {
        ag.inTokens += inTok;
        ag.outTokens += outTok;
        ag.tokens += totTok;
        ag.calls += 1;
      }
      updateStatusBar();
    } else if (ev.kind === "plan_ready") {
      markToolsDone(lane);
      lane.messages.push({
        id: `plan-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        kind: "plan",
        label: ev.label || "Plano pronto para aprovação",
        text: ev.text || "",
      });
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
      } else {
        markToolsDone(lane);
        lane.messages.push({
          id: `tool-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          kind: "tool",
          label: toolLabel,
          text: baseText,
          active: true,
        });
      }
    } else if (ev.kind === "tool_result") {
      markToolsDone(lane);
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
      refreshDiffsAndSummarize(lane);
      refreshSessions();
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

  try {
    const init = await backend.GetInitialState();
    state.recent = init.recentProjects || [];
    state.models = init.models || [];
    if (init.agyStatus?.Found) state.agyStatus = init.agyStatus;
    state.goVersion = init.goVersion || "";
    state.os = init.os || "windows";
    applyProject(init.initialProject);
    if (!init.initialProject?.hasTeam) {
      state.showWizard = true;
      renderModalLayer();
    }
    bootstrapAsync(init.initialProject?.root);
  } catch (err: any) {
    setStatus(`Erro ao iniciar: ${err?.message || err}`);
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

  if (rootDir) {
    backend.GitHubStatus(rootDir).then((gh) => {
      state.ghStatus = gh;
      if (state.sidebarView === "git" && state.showLeftSidebar) {
        renderLeftSidebar();
      }
    }).catch(() => {});

    refreshSessions();
  }
}

async function refreshSessions() {
  const backend = api();
  if (!backend || !state.project) return;
  try {
    const sum = await backend.ListSessions(state.project.root);
    state.sessionsSummary = {
      sessions: sum?.sessions || [],
      projectInputTokens: Number(sum?.projectInputTokens || 0),
      projectOutputTokens: Number(sum?.projectOutputTokens || 0),
      projectTotalTokens: Number(sum?.projectTotalTokens || 0),
    };
    updateStatusBar();
    updateMissionBoardIfOpen();
    if (state.sidebarView === "sessions" && state.showLeftSidebar) {
      renderLeftSidebar();
    }
  } catch {
    // ignore empty workspace sessions
  }
}

function markToolsDone(lane: TeamLane) {
  for (const m of lane.messages) {
    if (m.kind === "tool") m.active = false;
  }
}

function updateAgentStatus(lane: TeamLane, author: string, label: string | undefined, status: AgentRuntime["status"], tool: string) {
  const ag = matchAgentRuntime(lane.agents, author, label);
  if (ag) {
    ag.status = status;
    ag.tool = tool;
  }
}

function applyProject(proj: ProjectInfo) {
  state.project = proj;
  state.expandedDirs.clear();
  state.dirCache.clear();
  state.dirCache.set("", proj.files || []);
  state.explorerFilter = "";
  state.explorerFilterResults = [];
  state.editorTabs = [];
  state.activeEditorPath = "";
  state.diffs = [];
  state.ghStatus = null;
  state.textSearch = { query: "", results: [], searching: false };
  state.issues = { list: [], loading: false, loadedOnce: false };
  state.sessionsSummary = {
    sessions: [],
    projectInputTokens: 0,
    projectOutputTokens: 0,
    projectTotalTokens: 0,
  };

  const activeTeam = proj.teamConfig?.active || proj.teamConfig?.teams?.[0]?.name || "principal";
  const firstLane = createLane(activeTeam);
  state.lanes = [firstLane];
  state.activeLaneId = firstLane.id;

  updateHeaderAndStatusBar();
  renderLeftSidebar();
  renderEditorTabs();
  renderEditorBody();
  renderTerminalDrawer();
  renderRightPane();
  setStatus(`Projeto aberto: ${proj.root}`);
  refreshDiffs();
}

async function openFolderDialog() {
  const backend = api();
  if (!backend) return;
  const dir = await backend.ChooseDirectory();
  if (!dir) return;
  await openProjectPath(dir);
}

async function openProjectPath(dir: string) {
  const backend = api();
  if (!backend) return;
  const doOpen = async () => {
    try {
      const proj = await backend.OpenProject(dir);
      state.recent = await backend.GetRecentProjects();
      state.showFileMenu = false;
      renderFileMenu();
      applyProject(proj);
      bootstrapAsync(proj.root);
      if (!proj.hasTeam) {
        state.showWizard = true;
        renderModalLayer();
      }
    } catch (err: any) {
      setStatus(`Erro ao abrir pasta: ${err?.message || err}`);
    }
  };
  if (hasDirtyTabs()) {
    askConfirm({
      title: "Trocar de Projeto com Edições Abertas",
      body: "Existem arquivos modificados não salvos no editor. Deseja abrir outra pasta e descartar as edições em memória?",
      confirmLabel: "Abrir Pasta",
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
        setStatus(`Erro ao descartar: ${err?.message || err}`);
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
    setStatus(`Erro ao ler ${relPath}: ${err?.message || err}`);
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
    setStatus(`Erro ao salvar: ${err?.message || err}`);
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
    if (state.sidebarView === "git" && state.showLeftSidebar) {
      renderLeftSidebar();
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

function renderTreeNodes(nodes: FileNode[], depth = 0, filter = ""): string {
  const q = filter.trim().toLowerCase();
  return nodes
    .map((n) => {
      const pad = 8 + depth * 14;
      if (n.isDir) {
        const open = state.expandedDirs.has(n.relPath);
        const children = state.dirCache.get(n.relPath) ?? n.children ?? [];
        const childrenHtml = open ? renderTreeNodes(children, depth + 1, filter) : "";
        if (q && !n.name.toLowerCase().includes(q) && !childrenHtml) {
          return "";
        }
        return `
          <div class="tree-row" data-dir="${escapeHtml(n.relPath)}" style="padding-left:${pad}px">
            <span class="tree-chevron">${open ? icons.chevronDown : icons.chevronRight}</span>
            <span class="tree-folder-icon">${open ? icons.folderOpen : icons.folderClosed}</span>
            <span class="tree-name">${escapeHtml(n.name)}</span>
            <span class="tree-actions">
              <button class="tree-act-btn" data-tree-newfile="${escapeHtml(n.relPath)}" title="Novo arquivo nesta pasta">${icons.plus}</button>
              <button class="tree-act-btn" data-tree-rename="${escapeHtml(n.relPath)}" title="Renomear">${icons.editPen}</button>
              <button class="tree-act-btn" data-tree-delete="${escapeHtml(n.relPath)}" title="Excluir">${icons.trash}</button>
            </span>
          </div>
          ${childrenHtml}
        `;
      }
      if (q && !n.name.toLowerCase().includes(q) && !n.relPath.toLowerCase().includes(q)) {
        return "";
      }
      const isAct = state.activeEditorPath === n.relPath ? "active" : "";
      return `
        <div class="tree-row ${isAct}" data-file="${escapeHtml(n.relPath)}" data-name="${escapeHtml(n.name)}" style="padding-left:${pad + 14}px">
          ${fileBadge(n.name)}
          <span class="tree-name">${escapeHtml(n.name)}</span>
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

function buildModelSelectOptions(currentModel: string): string {
  const cur = currentModel || "agy";
  const hasCurInList = state.models.some((m) => `agy:${m.slug}` === cur || m.slug === cur);
  const head = !hasCurInList ? `<option value="${escapeHtml(cur)}" selected>${escapeHtml(cur)}</option>` : "";
  if (state.models.length === 0) {
    return head || `<option value="agy" selected>agy (padrão)</option>`;
  }
  return (
    head +
    state.models
      .map((m) => {
        const val = `agy:${m.slug}`;
        const sel = val === cur || m.slug === cur ? "selected" : "";
        return `<option value="${escapeHtml(val)}" ${sel}>${escapeHtml(m.slug)}</option>`;
      })
      .join("")
  );
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
    setStatus(`Erro ao trocar modelo: ${err?.message || err}`);
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
        <button class="mini" id="btn-team-memory" style="width:100%;margin-bottom:8px;justify-content:flex-start">
          ${icons.memory}
          <span>Memória do Time (.hivemind/MEMORY.md)</span>
        </button>
        <div class="dim" style="margin-bottom:8px">
          Fluxo: Chefe → Dev → QA → Fecho${maxTok > 0 ? ` · Limite: ${exactTokens(maxTok)} tok` : ""}
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
        setStatus(`Erro ao carregar sessão: ${err?.message || err}`);
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
        setStatus(`Erro ao excluir sessão: ${err?.message || err}`);
      }
    });
  });
}

function renderExplorerTreeHtml(): string {
  const rootNodes = state.dirCache.get("") ?? state.project?.files ?? [];
  if (!state.project) return `<div class="dim" style="padding:8px">Abra uma pasta</div>`;
  const q = state.explorerFilter.trim();
  if (q && state.explorerFilterResults.length > 0) {
    return state.explorerFilterResults
      .map((rel) => {
        const name = rel.split("/").pop() || rel;
        const dirPart = rel.includes("/") ? rel.slice(0, rel.lastIndexOf("/")) : "";
        const isAct = state.activeEditorPath === rel ? "active" : "";
        return `
          <div class="tree-row ${isAct}" data-file="${escapeHtml(rel)}" data-name="${escapeHtml(name)}" style="padding-left:10px" title="${escapeHtml(rel)}">
            ${fileBadge(name)}
            <span class="tree-name">${escapeHtml(name)}${dirPart ? ` <span class="dim" style="font-size:10.5px">· ${escapeHtml(dirPart)}</span>` : ""}</span>
          </div>
        `;
      })
      .join("");
  }
  const html = renderTreeNodes(rootNodes, 0, q);
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
  bindAgentModelSelects(document.getElementById("team-agents-list"), "data-sb-agent-model");
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
      setStatus(`Erro ao inicializar Git: ${err?.message || err}`);
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
      setStatus(`Erro no commit/push: ${err?.message || err}`);
    }
  });
  document.getElementById("btn-open-pr")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      const url = await backend.OpenPR(state.project.root, state.commitMsgInput, "", state.project.teamConfig?.settings?.base_branch || "main");
      setStatus(`PR aberto: ${url}`);
    } catch (err: any) {
      setStatus(`Erro ao abrir PR: ${err?.message || err}`);
    }
  });
  document.getElementById("btn-comment-jira")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      await backend.CommentJira(state.project.root, state.jiraKeyInput, state.commitMsgInput || "Atualização pela HiveMind IDE.");
      setStatus(`Comentário enviado ao Jira ${state.jiraKeyInput}.`);
    } catch (err: any) {
      setStatus(`Erro no Jira: ${err?.message || err}`);
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
      setStatus(`Erro ao buscar issues: ${err?.message || err}`);
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
    body.innerHTML = `
      <div class="editor-breadcrumb">
        <span class="dim">${escapeHtml(activeTab.diff.path)}</span>
        <span style="color:var(--green)">+${activeTab.diff.added}</span>
        <span style="color:var(--red)">-${activeTab.diff.removed}</span>
      </div>
      <div class="diff-lines">
        ${activeTab.diff.lines.map((ln) => `<div class="diff-line ${ln.kind}">${escapeHtml(ln.text)}</div>`).join("")}
      </div>
    `;
    return;
  }

  body.innerHTML = `
    <div class="editor-breadcrumb">
      <span class="dim">${escapeHtml(activeTab.relPath)}</span>
      <div class="spacer"></div>
      <button class="mini" id="btn-send-selection" title="Envia as linhas selecionadas para o chat do Chefe (Ctrl+L)">${icons.send}<span>Enviar p/ Chefe (Ctrl+L)</span></button>
      <button class="mini primary" id="btn-save-file" ${activeTab.dirty ? "" : "disabled"}>${icons.save}<span>Salvar (Ctrl+S)</span></button>
    </div>
    <div class="editor-live-wrap">
      <pre class="line-gutter" id="editor-gutter">${renderLineNumbers(activeTab.content)}</pre>
      <div class="editor-code-stack">
        <pre class="code-highlight" id="editor-highlight"><code id="editor-highlight-code">${highlightCode(activeTab.content, activeTab.name)}</code></pre>
        <textarea class="code-overlay-input" id="editor-textarea" spellcheck="false" wrap="off"></textarea>
      </div>
    </div>
  `;

  const ta = document.getElementById("editor-textarea") as HTMLTextAreaElement | null;
  const pre = document.getElementById("editor-highlight");
  const gutter = document.getElementById("editor-gutter");
  const codeEl = document.getElementById("editor-highlight-code");

  if (ta) {
    ta.value = activeTab.content;
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
      if (editorHighlightRaf) cancelAnimationFrame(editorHighlightRaf);
      editorHighlightRaf = requestAnimationFrame(() => {
        editorHighlightRaf = 0;
        if (codeEl) codeEl.innerHTML = highlightCode(activeTab.content, activeTab.name);
        if (gutter) gutter.textContent = renderLineNumbers(activeTab.content);
      });
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
          <button class="primary mini" id="btn-send-run" ${lane?.running ? "disabled" : ""}>${icons.send}<span>Enviar</span></button>
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
  const modes: Array<{ id: RunMode; label: string; icon: string; tip: string }> = [
    { id: "auto", label: "Auto", icon: icons.sparkle, tip: "Triagem inteligente: responde direto ou aciona o Squad" },
    { id: "direct", label: "Direto", icon: icons.bolt, tip: "1 Agente (Chefe) sem delegar — rápido e econômico em tokens" },
    { id: "plan", label: "Planejar", icon: icons.plan, tip: "Gera plano + divisão de arquivos e pausa para aprovação" },
    { id: "squad", label: "Squad", icon: icons.swarm, tip: "Força pipeline completo: Chefe → Devs Paralelos → QA → Fecho" },
  ];
  bar.innerHTML = `
    ${modes.map((m) => `
      <button class="mode-chip ${state.runMode === m.id ? "active" : ""}" data-run-mode="${m.id}" title="${escapeHtml(m.tip)}">
        ${m.icon}
        <span>${m.label}</span>
      </button>
    `).join("")}
    <div class="spacer"></div>
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
      <div class="lane-pill ${l.id === lane?.id ? "active" : ""}" data-lane="${l.id}">
        ${l.running ? `<span class="lane-dot"></span>` : ""}
        <span>${escapeHtml(l.teamName)}</span>
        ${l.sessionTotalTokens > 0 ? `<span class="dim" style="font-size:10px">${formatTokens(l.sessionTotalTokens)}</span>` : ""}
      </div>
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
  return `${m.kind}|${m.active ? 1 : 0}|${m.approved ? 1 : 0}|${m.label}|${m.tokens || 0}|${m.images?.length || 0}|${m.diffItems?.length || 0}|${m.text.length}|${m.text.slice(-16)}`;
}

function createMessageElement(m: ChatMessage): HTMLElement {
  const div = document.createElement("div");
  div.className = `msg-card role-${m.kind}`;
  div.dataset.msgId = m.id;
  updateMessageElement(div, m);
  return div;
}

function updateMessageElement(el: HTMLElement, m: ChatMessage) {
  const digest = messageDigest(m);
  if (el.dataset.digest === digest) return;
  el.dataset.digest = digest;

  if (m.kind === "tool") {
    el.innerHTML = `
      <span class="msg-badge badge-tool">${m.active ? icons.spinner : icons.check}<span>${escapeHtml(m.label)}</span></span>
      <span class="msg-body">${escapeHtml(m.text)}</span>
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
        setStatus(`Erro no commit: ${err?.message || err}`);
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
  flushChatUpdates();
  updateMissionBoardIfOpen();
  if (state.sidebarView === "team" && state.showLeftSidebar) updateTeamStatusPills();

  try {
    const payloadImgs = attached.map((x) => ({ name: x.name, mimeType: x.mimeType, dataBase64: x.dataBase64 }));
    const res = backend.StartRunWithOptions
      ? await backend.StartRunWithOptions(
          lane.id,
          state.project.root,
          lane.teamName,
          text,
          state.jiraKeyInput.trim(),
          lane.sessionId,
          effectiveMode,
          payloadImgs
        )
      : await backend.StartRunWithImages(
          lane.id,
          state.project.root,
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
    setStatus(`Erro na execução: ${err?.message || err}`);
  } finally {
    lane.running = false;
    lane.stage = "";
    markToolsDone(lane);
    await refreshExplorerTree();
    await reloadCleanOpenTabs();
    await refreshDiffsAndSummarize(lane);
    await refreshSessions();
    flushChatUpdates();
    updateMissionBoardIfOpen();
    if (state.sidebarView === "team" && state.showLeftSidebar) updateTeamStatusPills();
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
    { id: "memory", label: "Abrir Memória Compartilhada (.hivemind/MEMORY.md)", run: () => openTeamMemoryFile() },
    { id: "sessions", label: "Ver Sessões Salvas & Consumo de Tokens", run: () => { openSidebarView("sessions"); refreshSessions(); } },
    { id: "search", label: "Busca Global no Código", shortcut: "Ctrl+Shift+F", run: () => { openSidebarView("search"); (document.getElementById("global-search-input") as HTMLInputElement | null)?.focus(); } },
    { id: "test", label: `Rodar Testes da Stack (${state.project?.stack?.testHint || "git status"})`, run: () => { if (!state.terminal.open) { state.terminal.open = true; renderTerminalDrawer(); } runTerminalCommand(state.project?.stack?.testHint || "git status"); } },
    { id: "term", label: "Alternar Terminal Integrado", shortcut: "Ctrl+J", run: () => toggleTerminal() },
    { id: "wizard", label: "Configurar Equipe (.hivemind/team.yaml)", run: () => { state.showWizard = true; renderModalLayer(); } },
    { id: "doctor", label: "Doctor — Verificar Antigravity CLI (agy)", run: () => openDoctorModal() },
    { id: "open-folder", label: "Abrir Outra Pasta de Projeto…", run: () => openFolderDialog() },
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
            <div>${escapeHtml(en.output)}</div>
          </div>
        `).join("")
      }
    </div>
    <div class="term-input-bar">
      <span class="dim">$</span>
      <input id="term-cmd-input" placeholder="Executar comando no projeto (Enter)…" value="${escapeHtml(state.terminal.cmdInput)}" ${state.terminal.running ? "disabled" : ""} />
      <button class="primary mini" id="term-send" ${state.terminal.running ? "disabled" : ""}>Executar</button>
    </div>
  `;

  const outBox = document.getElementById("terminal-output");
  if (outBox) outBox.scrollTop = outBox.scrollHeight;

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
          errEl.textContent = err?.message || String(err);
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

  if (state.showWizard) {
    const settings = state.project?.teamConfig?.settings;
    const activeTeamName = state.project?.teamConfig?.active || "principal";
    const existingMaxTok = settings?.max_tokens || 0;
    const modelOpts = state.models.length > 0
      ? state.models.map((m) => `<option value="agy:${escapeHtml(m.slug)}">${escapeHtml(m.name)} (${escapeHtml(m.slug)})</option>`).join("")
      : `<option value="agy">agy (padrão)</option>`;

    layer.innerHTML = `
      <div class="modal-backdrop">
        <div class="modal-box">
          <h3 class="modal-title">Montar Equipe HiveMind (.hivemind/team.yaml)</h3>
          <div class="form-grid">
            <label>Nome da equipe:</label>
            <input id="wiz-name" value="${escapeHtml(activeTeamName)}" />

            <label>Chefe da equipe:</label>
            <select id="wiz-chief">
              <option value="architect">Arquiteto</option>
              <option value="project_manager">Gerente de Projeto</option>
            </select>

            <label>Qtd. Desenvolvedores:</label>
            <input id="wiz-devs" type="number" min="1" max="6" value="1" />

            <label>Modelo padrão:</label>
            <select id="wiz-model">${modelOpts}</select>

            <label>Modelo do Chefe:</label>
            <select id="wiz-model-chief"><option value="">(usar padrão)</option>${modelOpts}</select>

            <label>Modelo do Dev:</label>
            <select id="wiz-model-dev"><option value="">(usar padrão)</option>${modelOpts}</select>

            <label>Modelo do QA:</label>
            <select id="wiz-model-qa"><option value="">(usar padrão)</option>${modelOpts}</select>

            <label>Limite tokens/turno:</label>
            <input id="wiz-maxtokens" type="number" min="0" step="1000" value="${existingMaxTok}" placeholder="0 = sem limite (ex: 50000)" />

            <label>Worktree por Dev:</label>
            <input id="wiz-worktrees" type="checkbox" ${settings?.worktrees !== false ? "checked" : ""} />

            <label>Auto-commit após QA:</label>
            <input id="wiz-autocommit" type="checkbox" ${settings?.auto_commit ? "checked" : ""} />

            <label>Auto-PR (GitHub gh):</label>
            <input id="wiz-autopr" type="checkbox" ${settings?.auto_pr ? "checked" : ""} />

            <label>Jira Cloud:</label>
            <div style="display:flex;gap:8px;align-items:center">
              <input id="wiz-jira" type="checkbox" ${settings?.jira_enabled ? "checked" : ""} />
              <input id="wiz-jira-key" placeholder="Projeto Jira (ex: PROJ)" value="${escapeHtml(settings?.jira_project || "")}" style="flex:1" />
            </div>
          </div>
          <div class="dim" style="margin-top:10px">Nota: O init grava apenas em .hivemind/team.yaml e não altera o .gitignore do projeto.</div>
          <div id="wiz-error" style="color:var(--red);font-size:12px;margin-top:8px;display:none"></div>
          <div class="modal-footer">
            <button id="wiz-cancel">Cancelar</button>
            <button class="primary" id="wiz-save">Salvar Equipe</button>
          </div>
        </div>
      </div>
    `;

    document.getElementById("wiz-cancel")?.addEventListener("click", () => {
      state.showWizard = false;
      renderModalLayer();
    });
    document.getElementById("wiz-save")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend || !state.project) return;
      const val = (id: string) => (document.getElementById(id) as HTMLInputElement | HTMLSelectElement)?.value || "";
      const chk = (id: string) => (document.getElementById(id) as HTMLInputElement)?.checked || false;
      const devCount = parseInt(val("wiz-devs"), 10) || 1;
      const maxTokens = Math.max(0, parseInt(val("wiz-maxtokens"), 10) || 0);
      const defaultModel = val("wiz-model") || "agy";
      const agentModels: Record<string, string> = {};
      if (val("wiz-model-chief")) agentModels["chefe"] = val("wiz-model-chief");
      if (val("wiz-model-qa")) agentModels["qa"] = val("wiz-model-qa");
      if (val("wiz-model-dev")) {
        if (devCount === 1) agentModels["dev"] = val("wiz-model-dev");
        else {
          for (let i = 1; i <= devCount; i++) agentModels[`dev_${i}`] = val("wiz-model-dev");
        }
      }
      try {
        const cfg = await backend.SaveTeamWizard(state.project.root, {
          teamName: val("wiz-name") || "principal",
          chiefRole: val("wiz-chief") || "architect",
          devCount,
          defaultModel,
          agentModels,
          worktrees: chk("wiz-worktrees"),
          autoCommit: chk("wiz-autocommit"),
          autoPR: chk("wiz-autopr"),
          jiraEnabled: chk("wiz-jira"),
          jiraProject: val("wiz-jira-key"),
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
        const msg = `Erro ao salvar equipe: ${err?.message || err}`;
        setStatus(msg);
        const errEl = document.getElementById("wiz-error");
        if (errEl) {
          errEl.textContent = msg;
          errEl.style.display = "block";
        }
      }
    });
    return;
  }

  layer.innerHTML = "";
}

boot();
