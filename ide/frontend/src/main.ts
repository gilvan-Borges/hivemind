import "./styles.css";
import { highlightCode } from "./highlighter";
import { fileBadge, icons } from "./icons";
import { renderMarkdownLite } from "./markdown";

interface FileNode {
  name: string;
  path: string;
  relPath: string;
  isDir: boolean;
  hasChildren?: boolean;
  children?: FileNode[];
}

interface AgyModel {
  slug: string;
  name: string;
}

interface AgyStatus {
  Found: boolean;
  Path: string;
  LoggedIn: boolean;
  Detail: string;
}

interface TeamAgent {
  name: string;
  role: string;
  provider: string;
  model: string;
}

interface TeamSpec {
  name: string;
  agents: TeamAgent[];
}

interface TeamSettings {
  jira_enabled: boolean;
  jira_project?: string;
  worktrees?: boolean;
  auto_commit: boolean;
  auto_pr: boolean;
  base_branch: string;
}

interface TeamFile {
  version: number;
  active: string;
  settings: TeamSettings;
  teams: TeamSpec[];
}

interface ProjectStack {
  root: string;
  name: string;
  stacks: string[];
  buildTools: string[];
  testHint: string;
  summary: string;
}

interface ProjectInfo {
  root: string;
  name: string;
  branch: string;
  stack: ProjectStack;
  hasTeam: boolean;
  teamConfig: TeamFile;
  files: FileNode[];
}

interface DiffLine {
  kind: "header" | "hunk" | "added" | "removed" | "context";
  text: string;
}

interface FileDiff {
  path: string;
  added: number;
  removed: number;
  lines: DiffLine[];
}

interface ChatMessage {
  id: string;
  kind: "user" | "chief" | "system" | "tool" | "error";
  label: string;
  text: string;
  active?: boolean;
}

interface AgentRuntime {
  name: string;
  role: string;
  model: string;
  status: "idle" | "running" | "done" | "error";
  tool?: string;
  tokens?: number;
}

interface TeamLane {
  id: string;
  teamName: string;
  sessionId: string;
  running: boolean;
  messages: ChatMessage[];
  agents: AgentRuntime[];
  autoScroll: boolean;
}

interface EditorTab {
  kind: "file" | "diff";
  relPath: string;
  name: string;
  content: string;
  dirty: boolean;
  diff?: FileDiff;
}

interface GitHubStatus {
  installed: boolean;
  loggedIn: boolean;
  user: string;
  remote: string;
  detail: string;
}

declare global {
  interface Window {
    go?: {
      main?: {
        App?: {
          GetInitialState(): Promise<any>;
          BootstrapAgy(force: boolean): Promise<{ status: AgyStatus; models: AgyModel[] }>;
          ChooseDirectory(): Promise<string>;
          OpenProject(dir: string): Promise<ProjectInfo>;
          GetRecentProjects(): Promise<string[]>;
          ListFiles(workspace: string): Promise<FileNode[]>;
          ListDirectory(workspace: string, relDir: string): Promise<FileNode[]>;
          SearchFiles(workspace: string, query: string): Promise<string[]>;
          ReadFile(workspace: string, relOrAbs: string): Promise<string>;
          SaveFile(workspace: string, relOrAbs: string, content: string): Promise<void>;
          ListModels(): Promise<AgyModel[]>;
          DoctorAgy(): Promise<AgyStatus>;
          SaveTeamWizard(workspace: string, input: any): Promise<TeamFile>;
          SetActiveTeam(workspace: string, teamName: string): Promise<TeamFile>;
          GetDiff(workspace: string): Promise<FileDiff[]>;
          CommitChanges(workspace: string, message: string, push: boolean): Promise<string>;
          OpenPR(workspace: string, title: string, body: string, baseBranch: string): Promise<string>;
          CommentJira(workspace: string, issueKey: string, comment: string): Promise<void>;
          DiscardChanges(workspace: string): Promise<void>;
          GitHubStatus(workspace: string): Promise<GitHubStatus>;
          InitGit(workspace: string): Promise<void>;
          WindowMinimize(): Promise<void>;
          WindowToggleMaximize(): Promise<void>;
          WindowClose(): Promise<void>;
          StartRun(laneID: string, workspace: string, teamName: string, objective: string, jiraKey: string, sessionID: string): Promise<any>;
          CancelRun(laneID: string): Promise<boolean>;
        };
      };
    };
    runtime?: {
      EventsOn(eventName: string, cb: (data: any) => void): void;
    };
  }
}

const api = () => window.go?.main?.App;

const state = {
  project: null as ProjectInfo | null,
  recent: [] as string[],
  models: [] as AgyModel[],
  agyStatus: null as AgyStatus | null,
  ghStatus: null as GitHubStatus | null,
  goVersion: "",
  os: "windows",
  statusText: "Pronto",
  sidebarView: "explorer" as "explorer" | "team" | "git",
  showLeftSidebar: true,
  showRightAI: true,
  leftWidth: 260,
  rightWidth: 420,
  expandedDirs: new Set<string>(),
  dirCache: new Map<string, FileNode[]>(),
  explorerFilter: "",
  editorTabs: [] as EditorTab[],
  activeEditorPath: "",
  lanes: [] as TeamLane[],
  activeLaneId: "",
  diffs: [] as FileDiff[],
  showFileMenu: false,
  showWizard: false,
  doctorModal: null as AgyStatus | null,
  quickOpen: {
    open: false,
    query: "",
    results: [] as string[],
    selected: 0,
  },
  mentionResults: [] as string[],
  draftObjective: "",
  jiraKeyInput: "",
  commitMsgInput: "",
};

let laneSeq = 1;
let chatFlushRaf = 0;
let editorHighlightRaf = 0;
let mentionTimer = 0;
let quickOpenTimer = 0;

function escapeHtml(text: string | undefined | null): string {
  return String(text ?? "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function roleLabelPt(role: string): string {
  switch (role) {
    case "architect": return "Arquiteto (Chefe)";
    case "project_manager": return "Gerente de Projeto (Chefe)";
    case "tech_lead": return "Tech Lead";
    case "developer": return "Desenvolvedor";
    case "qa": return "QA";
    default: return role;
  }
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
  }));
}

function createLane(teamName: string): TeamLane {
  const id = `lane-${laneSeq++}`;
  return {
    id,
    teamName,
    sessionId: "",
    running: false,
    autoScroll: true,
    agents: buildAgentRuntimes(teamName),
    messages: [
      {
        id: `sys-${Date.now()}`,
        kind: "system",
        label: "Sistema",
        text: `Fluxo ativo: **Chefe → Dev → QA → Chefe**. Stack: \`${state.project?.stack.summary ?? "detectando..."}\`. Use \`@arquivo\` para anexar arquivos ou \`/ajuda\`.`,
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
        <span class="brand-logo">${icons.brand}<span>HIVEMIND</span></span>
        <div class="dropdown">
          <button class="ghost titlebar-interactive" id="btn-file-menu">Arquivo</button>
          <div class="dropdown-menu" id="file-menu-dropdown" style="display:none"></div>
        </div>
        <button class="ghost titlebar-interactive" id="btn-new-team">Nova Equipe</button>
        <button class="ghost titlebar-interactive" id="btn-doctor">Doctor (agy)</button>
        <div class="spacer"></div>
        <button class="title-center-cmd titlebar-interactive" id="btn-quick-open" title="Abrir arquivo rápido (Ctrl+P)">
          ${icons.search}
          <span class="cmd-proj" id="title-cmd-proj">Abrir arquivo (Ctrl+P)</span>
          <span class="stack-tag" id="title-stack-tag" style="display:none"></span>
        </button>
        <div class="spacer"></div>
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
          <button class="act-btn active" data-act="explorer" title="Explorador">${icons.explorer}</button>
          <button class="act-btn" data-act="team" title="Equipe & Agentes">${icons.team}</button>
          <button class="act-btn" data-act="git" title="Git / Diffs / PR / Jira">
            ${icons.gitBranch}
            <span class="act-badge" id="git-badge" style="display:none">0</span>
          </button>
          <div class="spacer"></div>
          <button class="act-btn" id="act-wizard" title="Configurar Equipe">${icons.settings}</button>
        </nav>

        <aside class="left-sidebar" id="left-sidebar" style="width:${state.leftWidth}px"></aside>
        <div class="resizer-v" id="resizer-left"></div>

        <section class="center-area" id="center-area">
          <div class="editor-tabs" id="editor-tabs"></div>
          <div class="editor-body-wrap" id="editor-body"></div>
        </section>

        <div class="resizer-v" id="resizer-right"></div>
        <aside class="right-ai-pane" id="right-pane" style="width:${state.rightWidth}px"></aside>
      </div>

      <footer class="status-bar">
        <span class="status-chip" id="status-branch">${icons.gitSmall} sem git</span>
        <span id="status-stack"></span>
        <span id="status-team">Equipe: -</span>
        <span id="status-agy" class="dim">agy: verificando…</span>
        <span id="status-text">Pronto</span>
        <div class="spacer"></div>
        <span id="status-env"></span>
      </footer>
    </div>
    <div id="quick-open-layer"></div>
    <div id="modal-layer"></div>
  `;

  bindStaticShellEvents();
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
  document.getElementById("win-close")?.addEventListener("click", () => api()?.WindowClose());

  document.getElementById("btn-toggle-left")?.addEventListener("click", () => {
    state.showLeftSidebar = !state.showLeftSidebar;
    syncLayoutVisibility();
  });
  document.getElementById("btn-toggle-right")?.addEventListener("click", () => {
    state.showRightAI = !state.showRightAI;
    syncLayoutVisibility();
  });

  document.getElementById("btn-file-menu")?.addEventListener("click", (e) => {
    e.stopPropagation();
    state.showFileMenu = !state.showFileMenu;
    renderFileMenu();
  });
  document.addEventListener("click", () => {
    if (state.showFileMenu) {
      state.showFileMenu = false;
      renderFileMenu();
    }
  });

  document.getElementById("btn-new-team")?.addEventListener("click", () => {
    state.showWizard = true;
    renderModalLayer();
  });
  document.getElementById("act-wizard")?.addEventListener("click", () => {
    state.showWizard = true;
    renderModalLayer();
  });
  document.getElementById("btn-doctor")?.addEventListener("click", async () => {
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
  });

  document.getElementById("btn-quick-open")?.addEventListener("click", () => openQuickOpen());

  document.querySelectorAll("[data-act]").forEach((el) => {
    el.addEventListener("click", () => {
      const view = (el as HTMLElement).dataset.act as "explorer" | "team" | "git";
      if (state.sidebarView === view && state.showLeftSidebar) {
        state.showLeftSidebar = false;
      } else {
        state.sidebarView = view;
        state.showLeftSidebar = true;
      }
      syncActivityButtons();
      syncLayoutVisibility();
      if (state.showLeftSidebar) renderLeftSidebar();
    });
  });

  setupResizer("resizer-left", (dx) => {
    state.leftWidth = Math.max(190, Math.min(480, state.leftWidth + dx));
    const el = document.getElementById("left-sidebar");
    if (el) el.style.width = `${state.leftWidth}px`;
  });
  setupResizer("resizer-right", (dx) => {
    state.rightWidth = Math.max(300, Math.min(680, state.rightWidth - dx));
    const el = document.getElementById("right-pane");
    if (el) el.style.width = `${state.rightWidth}px`;
  });

  document.addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "p") {
      e.preventDefault();
      openQuickOpen();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      saveActiveEditor();
      return;
    }
    if (e.key === "Escape") {
      if (state.quickOpen.open) {
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
    <div class="dim" style="padding:6px 10px 3px 10px;border-top:1px solid var(--border);margin-top:4px">Recentes</div>
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
  menu.querySelectorAll("[data-recent]").forEach((el) => {
    el.addEventListener("click", () => openProjectPath((el as HTMLElement).dataset.recent!));
  });
}

function updateHeaderAndStatusBar() {
  const proj = state.project;
  const cmdProj = document.getElementById("title-cmd-proj");
  const stackTag = document.getElementById("title-stack-tag");
  if (cmdProj) {
    cmdProj.textContent = proj ? `${proj.name} — Buscar arquivo (Ctrl+P)` : "Abrir arquivo (Ctrl+P)";
  }
  if (stackTag) {
    if (proj?.stack?.summary) {
      stackTag.textContent = proj.stack.summary;
      stackTag.style.display = "inline-block";
    } else {
      stackTag.style.display = "none";
    }
  }
  updateStatusBar();
}

function updateStatusBar() {
  const proj = state.project;
  const lane = activeLane();
  const branchEl = document.getElementById("status-branch");
  const stackEl = document.getElementById("status-stack");
  const teamEl = document.getElementById("status-team");
  const agyEl = document.getElementById("status-agy");
  const envEl = document.getElementById("status-env");

  if (branchEl) {
    branchEl.innerHTML = `${icons.gitSmall} <span>${escapeHtml(proj?.branch || "sem git")}</span>`;
  }
  if (stackEl) stackEl.textContent = proj?.stack?.summary || "";
  if (teamEl) teamEl.textContent = `Equipe: ${lane?.teamName || "-"} (${lane?.agents.length || 0})`;
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
    if (ev.kind === "tool_call") {
      updateAgentStatus(lane, ev.author, "running", ev.toolName, ev.tokens);
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
      updateAgentStatus(lane, ev.author, "running", "", ev.tokens);
      const last = lane.messages[lane.messages.length - 1];
      const msgLabel = ev.label || ev.author || "Chefe";
      if (last && last.kind === "chief" && last.label === msgLabel && ev.partial) {
        last.text += ev.text;
      } else if (!ev.partial && last && last.kind === "chief" && last.label === msgLabel) {
        last.text = ev.text;
      } else {
        lane.messages.push({
          id: `chief-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          kind: "chief",
          label: msgLabel,
          text: ev.text,
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
      lane.agents.forEach((a) => {
        if (a.status === "running") a.status = "done";
        a.tool = "";
      });
      lane.messages.push({
        id: `sys-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        kind: "system",
        label: "Sistema",
        text: ev.text || "Execução concluída.",
      });
      refreshDiffs();
    }

    if (lane.id === activeLane()?.id) {
      scheduleChatFlush();
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
    if (state.showWizard || state.doctorModal) renderModalLayer();
  }).catch(() => {});

  if (rootDir) {
    backend.GitHubStatus(rootDir).then((gh) => {
      state.ghStatus = gh;
      if (state.sidebarView === "git" && state.showLeftSidebar) {
        renderLeftSidebar();
      }
    }).catch(() => {});
  }
}

function markToolsDone(lane: TeamLane) {
  for (const m of lane.messages) {
    if (m.kind === "tool") m.active = false;
  }
}

function updateAgentStatus(lane: TeamLane, author: string, status: AgentRuntime["status"], tool: string, tokens?: number) {
  const norm = (author || "").toLowerCase();
  for (const a of lane.agents) {
    if (norm.includes(a.name.toLowerCase()) || norm.includes(a.role.toLowerCase())) {
      a.status = status;
      a.tool = tool;
      if (tokens) a.tokens = tokens;
    }
  }
}

function applyProject(proj: ProjectInfo) {
  state.project = proj;
  state.expandedDirs.clear();
  state.dirCache.clear();
  state.dirCache.set("", proj.files || []);
  state.explorerFilter = "";
  state.editorTabs = [];
  state.activeEditorPath = "";
  state.diffs = [];
  state.ghStatus = null;

  const activeTeam = proj.teamConfig?.active || proj.teamConfig?.teams?.[0]?.name || "principal";
  const firstLane = createLane(activeTeam);
  state.lanes = [firstLane];
  state.activeLaneId = firstLane.id;

  updateHeaderAndStatusBar();
  renderLeftSidebar();
  renderEditorTabs();
  renderEditorBody();
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
}

async function openFileInEditor(relPath: string, name: string) {
  const existing = state.editorTabs.find((t) => t.kind === "file" && t.relPath === relPath);
  if (existing) {
    state.activeEditorPath = existing.relPath;
    renderEditorTabs();
    renderEditorBody();
    highlightActiveTreeRow();
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
  } catch (err: any) {
    setStatus(`Erro ao ler ${relPath}: ${err?.message || err}`);
  }
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

function renderLeftSidebar() {
  const sidebar = document.getElementById("left-sidebar");
  if (!sidebar) return;

  const proj = state.project;
  const lane = activeLane();
  const teams = proj?.teamConfig?.teams || [];

  if (state.sidebarView === "team") {
    sidebar.innerHTML = `
      <div class="sidebar-header">
        <span class="eyebrow">EQUIPE (${lane?.agents.length || 0})</span>
        <div class="spacer"></div>
        <button class="mini" id="btn-config-team">Wizard</button>
      </div>
      <div class="sidebar-body">
        <div style="display:flex;gap:6px;align-items:center;margin-bottom:8px">
          <select id="team-select" style="flex:1" ${lane?.running ? "disabled" : ""}>
            ${teams.map((t) => `<option value="${escapeHtml(t.name)}" ${t.name === lane?.teamName ? "selected" : ""}>${escapeHtml(t.name)}</option>`).join("")}
          </select>
          <button class="mini" id="btn-new-team-sb">${icons.plus}<span>Novo</span></button>
        </div>
        <div class="dim" style="margin-bottom:8px">Fluxo: Chefe → Dev → QA → Chefe</div>
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
        <span class="eyebrow">GIT & GITHUB (${state.diffs.length})</span>
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
            <div class="dim">Branch atual: <strong>${escapeHtml(proj.branch)}</strong></div>
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
        <div class="eyebrow" style="margin-top:4px">ARQUIVOS ALTERADOS</div>
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
      </div>
    `;
    bindGitSidebarEvents();
    return;
  }

  sidebar.innerHTML = `
    <div class="sidebar-header">
      <span class="eyebrow">EXPLORADOR</span>
      <div class="spacer"></div>
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

function renderExplorerTreeHtml(): string {
  const rootNodes = state.dirCache.get("") ?? state.project?.files ?? [];
  if (!state.project) return `<div class="dim" style="padding:8px">Abra uma pasta</div>`;
  const html = renderTreeNodes(rootNodes, 0, state.explorerFilter);
  return html || `<div class="dim" style="padding:8px">Nenhum arquivo correspondente</div>`;
}

function renderAgentCardsHtml(agents: AgentRuntime[]): string {
  return agents.map((a) => `
    <div class="agent-card" data-agent-card="${escapeHtml(a.name)}">
      <div style="min-width:0">
        <div class="agent-name">${escapeHtml(a.name)} · <span class="dim">${escapeHtml(roleLabelPt(a.role))}</span></div>
        <div class="agent-meta">${escapeHtml(a.model)}${a.tool ? ` · ${escapeHtml(a.tool)}` : ""}${a.tokens ? ` · ${a.tokens} tok` : ""}</div>
      </div>
      <span class="status-pill ${a.status}">${a.status === "idle" ? "pronto" : a.status === "running" ? "rodando" : a.status === "done" ? "ok" : "erro"}</span>
    </div>
  `).join("");
}

function updateTeamStatusPills() {
  const list = document.getElementById("team-agents-list");
  const lane = activeLane();
  if (!list || !lane) return;
  list.innerHTML = renderAgentCardsHtml(lane.agents);
}

function bindExplorerEvents() {
  const filterIn = document.getElementById("explorer-filter") as HTMLInputElement | null;
  filterIn?.addEventListener("input", () => {
    state.explorerFilter = filterIn.value;
    const body = document.getElementById("explorer-tree-body");
    if (body) {
      body.innerHTML = renderExplorerTreeHtml();
      bindTreeRowClicks();
    }
  });

  document.getElementById("btn-refresh-tree")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    const rootNodes = await backend.ListDirectory(state.project.root, "");
    state.dirCache.clear();
    state.dirCache.set("", rootNodes || []);
    for (const dir of state.expandedDirs) {
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
  });

  bindTreeRowClicks();
}

function bindTreeRowClicks() {
  const body = document.getElementById("explorer-tree-body");
  if (!body) return;
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
    renderLaneBarOnly();
    updateStatusBar();
  });
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
  document.getElementById("btn-discard")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      await backend.DiscardChanges(state.project.root);
      setStatus("Alterações descartadas.");
      refreshDiffs();
    } catch (err: any) {
      setStatus(`Erro ao descartar: ${err?.message || err}`);
    }
  });
}

function renderEditorTabs() {
  const tabsEl = document.getElementById("editor-tabs");
  if (!tabsEl) return;
  tabsEl.innerHTML = state.editorTabs.map((t) => `
    <div class="editor-tab ${t.relPath === state.activeEditorPath ? "active" : ""}" data-tab="${escapeHtml(t.relPath)}">
      ${t.kind === "diff" ? icons.gitSmall : fileBadge(t.name)}
      <span>${escapeHtml(t.name)}</span>
      <span class="dirty-dot" data-dirty-for="${escapeHtml(t.relPath)}" style="display:${t.dirty ? "inline" : "none"}">●</span>
      <span class="close-x" data-close-tab="${escapeHtml(t.relPath)}">${icons.closeSmall}</span>
    </div>
  `).join("");

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
      state.editorTabs = state.editorTabs.filter((t) => t.relPath !== path);
      if (state.activeEditorPath === path) {
        state.activeEditorPath = state.editorTabs[0]?.relPath || "";
      }
      renderEditorTabs();
      renderEditorBody();
      highlightActiveTreeRow();
    });
  });
}

function syncActiveTabDirtyUI(tab: EditorTab) {
  const saveBtn = document.getElementById("btn-save-file") as HTMLButtonElement | null;
  if (saveBtn) saveBtn.disabled = !tab.dirty;
  document.querySelectorAll<HTMLElement>("[data-dirty-for]").forEach((dot) => {
    if (dot.dataset.dirtyFor === tab.relPath) {
      dot.style.display = tab.dirty ? "inline" : "none";
    }
  });
}

function renderLineNumbers(text: string): string {
  const count = Math.max(1, text.split("\n").length);
  const lines: string[] = [];
  for (let i = 1; i <= count; i++) lines.push(String(i));
  return lines.join("\n");
}

function renderEditorBody() {
  const body = document.getElementById("editor-body");
  if (!body) return;
  const activeTab = state.editorTabs.find((t) => t.relPath === state.activeEditorPath);

  if (!activeTab) {
    const proj = state.project;
    body.innerHTML = `
      <div class="empty-welcome">
        <div style="font-size:20px;color:var(--accent);font-weight:700">HiveMind IDE</div>
        <div class="dim">${proj ? `${escapeHtml(proj.name)} · ${escapeHtml(proj.stack.summary)}` : "Abra uma pasta para começar"}</div>
        <div class="welcome-actions">
          <button id="welcome-quick-open">${icons.search}<span>Buscar Arquivo (Ctrl+P)</span></button>
          <button id="welcome-wizard">${icons.settings}<span>Configurar Equipe</span></button>
        </div>
        <div class="shortcut-grid">
          <span class="dim">Fluxo do Time</span><span>Chefe → Dev → QA → Chefe</span>
          <span class="dim">Busca rápida</span><span class="kbd">Ctrl + P</span>
          <span class="dim">Mencionar arquivo</span><span class="kbd">@arquivo</span>
          <span class="dim">Salvar alterações</span><span class="kbd">Ctrl + S</span>
        </div>
      </div>
    `;
    document.getElementById("welcome-quick-open")?.addEventListener("click", () => openQuickOpen());
    document.getElementById("welcome-wizard")?.addEventListener("click", () => {
      state.showWizard = true;
      renderModalLayer();
    });
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

  document.getElementById("btn-save-file")?.addEventListener("click", saveActiveEditor);
}

function renderRightPane() {
  const right = document.getElementById("right-pane");
  if (!right) return;
  const lane = activeLane();

  right.innerHTML = `
    <div class="sidebar-header">
      <span class="eyebrow">CHEFE · AI</span>
      <div class="spacer"></div>
      <button class="mini" id="btn-new-session">Nova Sessão</button>
      <button class="mini" id="btn-add-lane">${icons.plus}<span>Equipe Paralela</span></button>
    </div>
    <div class="lane-bar" id="lane-bar"></div>
    <div class="chat-messages" id="chat-messages"></div>
    <div class="composer-wrap">
      <div class="mention-popup" id="mention-popup" style="display:none"></div>
      <div class="composer-box">
        <textarea id="chat-input" rows="3" placeholder="Converse com o Chefe… (@arquivo, /ajuda, Enter envia)"></textarea>
        <div class="composer-footer">
          <input id="jira-key-input" style="width:105px" placeholder="Jira (PROJ-1)" value="${escapeHtml(state.jiraKeyInput)}" />
          <span class="dim" id="composer-team-label"></span>
          <div class="spacer"></div>
          <button class="danger mini" id="btn-cancel-run" ${lane?.running ? "" : "disabled"}>${icons.stop}<span>Cancelar</span></button>
          <button class="primary mini" id="btn-send-run" ${lane?.running ? "disabled" : ""}>${icons.send}<span>Enviar</span></button>
        </div>
      </div>
    </div>
  `;

  const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
  if (chatInput) chatInput.value = state.draftObjective;

  renderLaneBarOnly();
  rebuildChatMessagesFull();
  bindRightStaticEvents();
}

function renderLaneBarOnly() {
  const bar = document.getElementById("lane-bar");
  const lane = activeLane();
  if (bar) {
    bar.innerHTML = state.lanes.map((l) => `
      <div class="lane-pill ${l.id === lane?.id ? "active" : ""}" data-lane="${l.id}">
        ${l.running ? `<span class="lane-dot"></span>` : ""}
        <span>${escapeHtml(l.teamName)}</span>
      </div>
    `).join("");
    bar.querySelectorAll("[data-lane]").forEach((el) => {
      el.addEventListener("click", () => {
        state.activeLaneId = (el as HTMLElement).dataset.lane!;
        renderLaneBarOnly();
        rebuildChatMessagesFull();
        syncComposerButtons();
        updateStatusBar();
        if (state.sidebarView === "team" && state.showLeftSidebar) {
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

function messageDigest(m: ChatMessage): string {
  return `${m.kind}|${m.active ? 1 : 0}|${m.label}|${m.text.length}|${m.text.slice(-16)}`;
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
  const bodyHtml = m.kind === "chief" || m.kind === "system" ? renderMarkdownLite(m.text) : escapeHtml(m.text);
  el.innerHTML = `
    <span class="msg-badge badge-${m.kind}">${escapeHtml(m.label)}</span>
    <div class="msg-body">${bodyHtml}</div>
  `;
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
    rebuildChatMessagesFull();
    syncComposerButtons();
    updateStatusBar();
    if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
  });

  document.getElementById("btn-new-session")?.addEventListener("click", () => {
    const lane = activeLane();
    if (!lane || lane.running) return;
    lane.sessionId = "";
    lane.messages = [{
      id: `sys-${Date.now()}`,
      kind: "system",
      label: "Sistema",
      text: "Nova sessão iniciada com o Chefe.",
    }];
    rebuildChatMessagesFull();
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

  const jiraInput = document.getElementById("jira-key-input") as HTMLInputElement | null;
  jiraInput?.addEventListener("input", () => {
    state.jiraKeyInput = jiraInput.value;
  });

  document.getElementById("btn-send-run")?.addEventListener("click", sendToChief);
  document.getElementById("btn-cancel-run")?.addEventListener("click", cancelActiveLane);
}

async function sendToChief() {
  const lane = activeLane();
  const backend = api();
  if (!lane || !backend || !state.project || lane.running) return;
  const text = state.draftObjective.trim();
  if (!text) return;

  const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;

  if (text === "/ajuda") {
    lane.messages.push({
      id: `sys-${Date.now()}`,
      kind: "system",
      label: "Sistema",
      text: "Comandos:\n- `@arquivo` — anexa caminho do arquivo\n- `/ajuda` — exibe esta ajuda\n- Fluxo: **Chefe → Dev → QA → Chefe**",
    });
    state.draftObjective = "";
    if (chatInput) chatInput.value = "";
    flushChatUpdates();
    return;
  }

  lane.messages.push({
    id: `usr-${Date.now()}`,
    kind: "user",
    label: "Usuário",
    text,
  });
  state.draftObjective = "";
  state.mentionResults = [];
  if (chatInput) chatInput.value = "";
  renderMentionPopup();

  lane.running = true;
  lane.agents.forEach((a) => {
    a.status = "idle";
    a.tool = "";
  });
  setStatus(`Executando equipe "${lane.teamName}"…`);
  flushChatUpdates();
  if (state.sidebarView === "team" && state.showLeftSidebar) updateTeamStatusPills();

  try {
    const res = await backend.StartRun(
      lane.id,
      state.project.root,
      lane.teamName,
      text,
      state.jiraKeyInput.trim(),
      lane.sessionId
    );
    if (res?.SessionID) lane.sessionId = res.SessionID;
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
    markToolsDone(lane);
    refreshDiffs();
    flushChatUpdates();
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
    text: "Execução cancelada pelo usuário.",
  });
  setStatus("Execução cancelada.");
  flushChatUpdates();
}

function openQuickOpen() {
  state.quickOpen.open = true;
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

function closeQuickOpen() {
  state.quickOpen.open = false;
  const layer = document.getElementById("quick-open-layer");
  if (layer) layer.innerHTML = "";
}

function renderQuickOpenModal() {
  const layer = document.getElementById("quick-open-layer");
  if (!layer) return;
  if (!state.quickOpen.open) {
    layer.innerHTML = "";
    return;
  }
  layer.innerHTML = `
    <div class="quick-open-backdrop" id="qo-backdrop">
      <div class="quick-open-modal">
        <input id="qo-input" placeholder="Buscar arquivo no projeto (Ctrl+P)…" value="${escapeHtml(state.quickOpen.query)}" />
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
    state.quickOpen.query = input.value;
    state.quickOpen.selected = 0;
    if (quickOpenTimer) window.clearTimeout(quickOpenTimer);
    quickOpenTimer = window.setTimeout(async () => {
      if (!state.project) return;
      const res = await api()?.SearchFiles(state.project.root, state.quickOpen.query);
      state.quickOpen.results = res || [];
      renderQuickOpenList();
    }, 50);
  });
  input?.addEventListener("keydown", (e) => {
    const total = state.quickOpen.results.length;
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
      const chosen = state.quickOpen.results[state.quickOpen.selected];
      if (chosen) {
        closeQuickOpen();
        openFileInEditor(chosen, chosen.split("/").pop() || chosen);
      }
    }
  });
  renderQuickOpenList();
}

function renderQuickOpenList() {
  const list = document.getElementById("qo-list");
  if (!list) return;
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

function renderModalLayer() {
  const layer = document.getElementById("modal-layer");
  if (!layer) return;

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
    const modelOpts = state.models.length > 0
      ? state.models.map((m) => `<option value="agy:${escapeHtml(m.slug)}">${escapeHtml(m.name)} (${escapeHtml(m.slug)})</option>`).join("")
      : `<option value="agy">agy (padrão)</option>`;

    layer.innerHTML = `
      <div class="modal-backdrop">
        <div class="modal-box">
          <h3 class="modal-title">Montar Equipe HiveMind (.hivemind/team.yaml)</h3>
          <div class="form-grid">
            <label>Nome da equipe:</label>
            <input id="wiz-name" value="principal" />

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

            <label>Worktree por Dev:</label>
            <input id="wiz-worktrees" type="checkbox" checked />

            <label>Auto-commit após QA:</label>
            <input id="wiz-autocommit" type="checkbox" />

            <label>Auto-PR (GitHub gh):</label>
            <input id="wiz-autopr" type="checkbox" />

            <label>Jira Cloud:</label>
            <div style="display:flex;gap:8px;align-items:center">
              <input id="wiz-jira" type="checkbox" />
              <input id="wiz-jira-key" placeholder="Projeto Jira (ex: PROJ)" style="flex:1" />
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
        updateStatusBar();
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
