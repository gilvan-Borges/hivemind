import "./styles.css";
import { highlightCode } from "./highlighter";

interface FileNode {
  name: string;
  path: string;
  relPath: string;
  isDir: boolean;
  children?: FileNode[];
}

interface AgyModel {
  slug: string;
  name: string;
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
  editing: boolean;
  diff?: FileDiff;
}

declare global {
  interface Window {
    go?: {
      main?: {
        App?: {
          GetInitialState(): Promise<any>;
          ChooseDirectory(): Promise<string>;
          OpenProject(dir: string): Promise<ProjectInfo>;
          GetRecentProjects(): Promise<string[]>;
          ListFiles(workspace: string): Promise<FileNode[]>;
          SearchFiles(workspace: string, query: string): Promise<string[]>;
          ReadFile(workspace: string, relOrAbs: string): Promise<string>;
          SaveFile(workspace: string, relOrAbs: string, content: string): Promise<void>;
          ListModels(): Promise<AgyModel[]>;
          DoctorAgy(): Promise<{ Found: boolean; Path: string; LoggedIn: boolean; Detail: string }>;
          SaveTeamWizard(workspace: string, input: any): Promise<TeamFile>;
          SetActiveTeam(workspace: string, teamName: string): Promise<TeamFile>;
          GetDiff(workspace: string): Promise<FileDiff[]>;
          CommitChanges(workspace: string, message: string, push: boolean): Promise<string>;
          OpenPR(workspace: string, title: string, body: string, baseBranch: string): Promise<string>;
          CommentJira(workspace: string, issueKey: string, comment: string): Promise<void>;
          DiscardChanges(workspace: string): Promise<void>;
          StartRun(laneID: string, workspace: string, teamName: string, objective: string, jiraKey: string, sessionID: string): Promise<any>;
          CancelRun(laneID: string): Promise<boolean>;
          GitHubStatus(workspace: string): Promise<{ installed: boolean; loggedIn: boolean; user: string; remote: string; detail: string }>;
          InitGit(workspace: string): Promise<void>;
          WindowMinimize(): Promise<void>;
          WindowToggleMaximize(): Promise<void>;
          WindowClose(): Promise<void>;
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
  goVersion: "",
  os: "windows",
  statusText: "Pronto",
  sidebarView: "explorer" as "explorer" | "team" | "git",
  showLeftSidebar: true,
  showRightAI: true,
  leftWidth: 260,
  rightWidth: 410,
  expandedDirs: new Set<string>(),
  editorTabs: [] as EditorTab[],
  activeEditorPath: "",
  lanes: [] as TeamLane[],
  activeLaneId: "",
  diffs: [] as FileDiff[],
  ghStatus: null as null | { installed: boolean; loggedIn: boolean; user: string; remote: string; detail: string },
  showFileMenu: false,
  showWizard: false,
  doctorModal: null as null | { Found: boolean; Path: string; LoggedIn: boolean; Detail: string },
  mentionResults: [] as string[],
  draftObjective: "",
  jiraKeyInput: "",
  commitMsgInput: "",
};

let laneSeq = 1;

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
        text: `Fluxo ativo: Chefe → Dev → QA → Chefe. Stack: ${state.project?.stack.summary ?? "detectando..."}. Use @arquivo para anexar contexto ou /ajuda para comandos.`,
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

async function boot() {
  render();
  const backend = api();
  if (!backend) {
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
      } else {
        lane.messages.push({
          id: `${Date.now()}-${Math.random()}`,
          kind: "tool",
          label: toolLabel,
          text: baseText,
        });
      }
    } else if (ev.kind === "text" && !ev.thought && ev.text) {
      updateAgentStatus(lane, ev.author, "running", "", ev.tokens);
      const last = lane.messages[lane.messages.length - 1];
      const msgLabel = ev.label || ev.author || "Chefe";
      if (last && last.kind === "chief" && last.label === msgLabel && ev.partial) {
        last.text += ev.text;
      } else if (!ev.partial && last && last.kind === "chief" && last.label === msgLabel) {
        last.text = ev.text;
      } else {
        lane.messages.push({
          id: `${Date.now()}-${Math.random()}`,
          kind: "chief",
          label: msgLabel,
          text: ev.text,
        });
      }
    } else if (ev.kind === "error") {
      lane.messages.push({
        id: `${Date.now()}-${Math.random()}`,
        kind: "error",
        label: "Erro",
        text: ev.text || "Erro desconhecido",
      });
    } else if (ev.kind === "finish") {
      lane.agents.forEach((a) => {
        if (a.status === "running") a.status = "done";
        a.tool = "";
      });
      lane.messages.push({
        id: `${Date.now()}-${Math.random()}`,
        kind: "system",
        label: "Sistema",
        text: ev.text || "Execução concluída.",
      });
      refreshDiffs();
    }
    renderRightPaneOnly();
    if (state.sidebarView === "team") renderLeftSidebarOnly();
  });

  try {
    const init = await backend.GetInitialState();
    state.recent = init.recentProjects || [];
    state.models = init.models || [];
    state.goVersion = init.goVersion || "";
    state.os = init.os || "windows";
    applyProject(init.initialProject);
    if (!init.initialProject?.hasTeam) {
      state.showWizard = true;
    }
    render();
  } catch (err: any) {
    setStatus(`Erro ao iniciar: ${err?.message || err}`);
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
  state.editorTabs = [];
  state.activeEditorPath = "";
  state.diffs = [];
  const activeTeam = proj.teamConfig?.active || proj.teamConfig?.teams?.[0]?.name || "principal";
  const firstLane = createLane(activeTeam);
  state.lanes = [firstLane];
  state.activeLaneId = firstLane.id;
  setStatus(`Projeto aberto: ${proj.root}`);
  refreshDiffs();
  api()?.GitHubStatus(proj.root).then((gh) => {
    state.ghStatus = gh;
    if (state.sidebarView === "git") renderLeftSidebarOnly();
  }).catch(() => {});
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
    applyProject(proj);
    if (!proj.hasTeam) state.showWizard = true;
    render();
  } catch (err: any) {
    setStatus(`Erro ao abrir pasta: ${err?.message || err}`);
  }
}

async function openFileInEditor(relPath: string, name: string) {
  const existing = state.editorTabs.find((t) => t.kind === "file" && t.relPath === relPath);
  if (existing) {
    state.activeEditorPath = existing.relPath;
    render();
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
      editing: false,
    });
    state.activeEditorPath = relPath;
    render();
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
    render();
    return;
  }
  state.editorTabs.push({
    kind: "diff",
    relPath: key,
    name: `Diff: ${diff.path.split("/").pop()}`,
    content: "",
    dirty: false,
    editing: false,
    diff,
  });
  state.activeEditorPath = key;
  render();
}

async function saveActiveEditor() {
  const tab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
  const backend = api();
  if (!tab || !backend || !state.project) return;
  try {
    await backend.SaveFile(state.project.root, tab.relPath, tab.content);
    tab.dirty = false;
    setStatus(`Salvo: ${tab.relPath}`);
    render();
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
    if (state.sidebarView === "git") renderLeftSidebarOnly();
    const badge = document.getElementById("git-badge");
    if (badge) {
      badge.textContent = String(state.diffs.length);
      badge.style.display = state.diffs.length > 0 ? "inline-block" : "none";
    }
  } catch {
    // ignore non-git
  }
}

async function sendToChief() {
  const lane = activeLane();
  const backend = api();
  if (!lane || !backend || !state.project || lane.running) return;
  const text = state.draftObjective.trim();
  if (!text) return;

  if (text === "/ajuda") {
    lane.messages.push({
      id: `sys-${Date.now()}`,
      kind: "system",
      label: "Sistema",
      text: "Comandos:\n• @arquivo — anexa caminho do arquivo\n• /ajuda — exibe esta ajuda\n• Fluxo: Chefe → Dev → QA → Chefe",
    });
    state.draftObjective = "";
    renderRightPaneOnly();
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
  lane.running = true;
  lane.agents.forEach((a) => {
    a.status = "idle";
    a.tool = "";
  });
  setStatus(`Executando equipe "${lane.teamName}"...`);
  renderRightPaneOnly();
  if (state.sidebarView === "team") renderLeftSidebarOnly();

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
    refreshDiffs();
    renderRightPaneOnly();
    if (state.sidebarView === "team") renderLeftSidebarOnly();
  }
}

async function cancelActiveLane() {
  const lane = activeLane();
  const backend = api();
  if (!lane || !backend || !lane.running) return;
  await backend.CancelRun(lane.id);
  lane.messages.push({
    id: `sys-${Date.now()}`,
    kind: "system",
    label: "Sistema",
    text: "Execução cancelada pelo usuário.",
  });
  setStatus("Execução cancelada.");
  renderRightPaneOnly();
}

function renderTree(nodes: FileNode[], depth = 0): string {
  return nodes
    .map((n) => {
      const pad = 8 + depth * 14;
      if (n.isDir) {
        const open = state.expandedDirs.has(n.relPath);
        const icon = open ? "▾" : "▸";
        const childrenHtml = open && n.children ? renderTree(n.children, depth + 1) : "";
        return `
          <div class="tree-row" data-dir="${escapeHtml(n.relPath)}" style="padding-left:${pad}px">
            <span class="tree-icon">${icon}</span>
            <span>${escapeHtml(n.name)}</span>
          </div>
          ${childrenHtml}
        `;
      }
      const isAct = state.activeEditorPath === n.relPath ? "active" : "";
      return `
        <div class="tree-row ${isAct}" data-file="${escapeHtml(n.relPath)}" data-name="${escapeHtml(n.name)}" style="padding-left:${pad}px">
          <span class="tree-icon">·</span>
          <span>${escapeHtml(n.name)}</span>
        </div>
      `;
    })
    .join("");
}

function renderLeftSidebarHtml(): string {
  const proj = state.project;
  const lane = activeLane();
  const teams = proj?.teamConfig?.teams || [];

  if (state.sidebarView === "team") {
    return `
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
          <button class="mini" id="btn-new-team-sb">+ Novo</button>
        </div>
        <div class="dim" style="margin-bottom:8px">Fluxo: Chefe → Dev → QA → Chefe</div>
        ${(lane?.agents || []).map((a) => `
          <div class="agent-card">
            <div style="min-width:0">
              <div class="agent-name">${escapeHtml(a.name)} · <span class="dim">${escapeHtml(roleLabelPt(a.role))}</span></div>
              <div class="agent-meta">${escapeHtml(a.model)}${a.tool ? ` · ${escapeHtml(a.tool)}` : ""}${a.tokens ? ` · ${a.tokens} tok` : ""}</div>
            </div>
            <span class="status-pill ${a.status}">${a.status === "idle" ? "pronto" : a.status === "running" ? "rodando" : a.status === "done" ? "ok" : "erro"}</span>
          </div>
        `).join("")}
      </div>
    `;
  }

  if (state.sidebarView === "git") {
    const gh = state.ghStatus;
    return `
      <div class="sidebar-header">
        <span class="eyebrow">GIT & GITHUB (${state.diffs.length})</span>
        <div class="spacer"></div>
        <button class="mini" id="btn-refresh-diff">↻</button>
      </div>
      <div class="sidebar-body" style="display:flex;flex-direction:column;gap:8px">
        <div class="gh-card">
          <div>
            <span class="gh-dot ${gh?.loggedIn ? "ok" : "off"}"></span>
            <strong>GitHub:</strong> ${escapeHtml(gh?.detail || "Verificando...")}
          </div>
          ${gh?.remote ? `<div class="dim" style="word-break:break-all">${escapeHtml(gh.remote)}</div>` : ""}
          ${!proj?.branch ? `
            <div class="dim">Esta pasta ainda não é um repositório Git.</div>
            <button class="primary mini" id="btn-init-git">Inicializar Git (git init)</button>
          ` : `
            <div class="dim">Branch atual: <strong>${escapeHtml(proj.branch)}</strong></div>
          `}
        </div>
        <input id="commit-msg-input" placeholder="Mensagem de commit..." value="${escapeHtml(state.commitMsgInput)}" />
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:6px">
          <button class="primary mini" id="btn-commit">Commitar</button>
          <button class="success mini" id="btn-commit-push">Commit + Push</button>
          <button class="mini" id="btn-open-pr">Abrir PR</button>
          <button class="mini" id="btn-comment-jira">Comentar Jira</button>
        </div>
        <button class="danger mini" id="btn-discard">Descartar Alterações</button>
        <div class="eyebrow" style="margin-top:4px">ARQUIVOS ALTERADOS</div>
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
    `;
  }

  return `
    <div class="sidebar-header">
      <span class="eyebrow">EXPLORADOR</span>
      <div class="spacer"></div>
      <button class="mini" id="btn-refresh-tree" title="Atualizar">↻</button>
    </div>
    <div class="sidebar-body">
      ${proj ? renderTree(proj.files) : `<div class="dim" style="padding:8px">Abra uma pasta</div>`}
    </div>
  `;
}

function renderRightPaneHtml(): string {
  const lane = activeLane();
  return `
    <div class="sidebar-header">
      <span class="eyebrow">CHEFE · AI</span>
      <div class="spacer"></div>
      <button class="mini" id="btn-new-session">Nova Sessão</button>
      <button class="mini" id="btn-add-lane">+ Equipe Paralela</button>
    </div>
    <div class="lane-bar">
      ${state.lanes.map((l) => `
        <div class="lane-pill ${l.id === lane?.id ? "active" : ""}" data-lane="${l.id}">
          <span>${l.running ? "● " : ""}${escapeHtml(l.teamName)}</span>
        </div>
      `).join("")}
    </div>
    <div class="chat-messages" id="chat-messages">
      ${(lane?.messages || []).map((m) => `
        <div class="msg-card role-${m.kind}">
          <span class="msg-badge badge-${m.kind}">${escapeHtml(m.label)}</span>
          <div class="msg-body">${escapeHtml(m.text)}</div>
        </div>
      `).join("")}
    </div>
    <div class="composer-wrap">
      ${state.mentionResults.length > 0 ? `
        <div class="mention-popup">
          ${state.mentionResults.map((f) => `<button class="dropdown-item" data-mention="${escapeHtml(f)}">@${escapeHtml(f)}</button>`).join("")}
        </div>
      ` : ""}
      <div class="composer-box">
        <textarea id="chat-input" rows="3" placeholder="Converse com o Chefe… (@arquivo, /ajuda, Enter envia)">${escapeHtml(state.draftObjective)}</textarea>
        <div class="composer-footer">
          <input id="jira-key-input" style="width:110px" placeholder="Jira (PROJ-1)" value="${escapeHtml(state.jiraKeyInput)}" />
          <div class="spacer"></div>
          <button class="danger mini" id="btn-cancel-run" ${lane?.running ? "" : "disabled"}>Cancelar</button>
          <button class="primary mini" id="btn-send-run" ${lane?.running ? "disabled" : ""}>Enviar</button>
        </div>
      </div>
    </div>
  `;
}

function renderLineNumbers(text: string): string {
  const count = Math.max(1, text.split("\n").length);
  let out = "";
  for (let i = 1; i <= count; i++) out += i + "\n";
  return out;
}

function render() {
  const rootEl = document.getElementById("app");
  if (!rootEl) return;

  const proj = state.project;
  const activeTab = state.editorTabs.find((t) => t.relPath === state.activeEditorPath);
  const lane = activeLane();

  rootEl.innerHTML = `
    <div class="ide-shell">
      <header class="titlebar" id="titlebar">
        <span class="brand-logo">◆ HIVEMIND</span>
        <div class="dropdown">
          <button class="ghost titlebar-interactive" id="btn-file-menu">Arquivo ▾</button>
          ${state.showFileMenu ? `
            <div class="dropdown-menu">
              <button class="dropdown-item" id="menu-open-folder">Abrir Pasta...</button>
              <div class="dim" style="padding:6px 10px 2px 10px;border-top:1px solid var(--border);margin-top:4px">Recentes</div>
              ${state.recent.length === 0
                ? `<div class="dim" style="padding:6px 10px">Nenhum projeto recente</div>`
                : state.recent.map((r) => `<button class="dropdown-item" data-recent="${escapeHtml(r)}" title="${escapeHtml(r)}">${escapeHtml(r)}</button>`).join("")
              }
            </div>
          ` : ""}
        </div>
        <button class="ghost titlebar-interactive" id="btn-new-team">Nova Equipe</button>
        <button class="ghost titlebar-interactive" id="btn-doctor">Doctor (agy)</button>
        <div class="spacer"></div>
        ${proj ? `<div class="title-project-pill"><strong>${escapeHtml(proj.name)}</strong> · ${escapeHtml(proj.stack.summary)}${proj.branch ? ` · ${escapeHtml(proj.branch)}` : ""}</div>` : ""}
        <div class="spacer"></div>
        <button class="ghost mini titlebar-interactive" id="btn-toggle-left" title="Alternar barra lateral">◧</button>
        <button class="ghost mini titlebar-interactive" id="btn-toggle-right" title="Alternar painel AI">◨</button>
        <div class="win-controls">
          <button class="win-btn" id="win-min" title="Minimizar">—</button>
          <button class="win-btn" id="win-max" title="Maximizar / Restaurar">□</button>
          <button class="win-btn close" id="win-close" title="Fechar">✕</button>
        </div>
      </header>

      <div class="workbench">
        <nav class="activity-bar">
          <button class="act-btn ${state.sidebarView === "explorer" && state.showLeftSidebar ? "active" : ""}" data-act="explorer" title="Explorador">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M3 7v10a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2h-6l-2-2H5a2 2 0 0 0-2 2z"/></svg>
          </button>
          <button class="act-btn ${state.sidebarView === "team" && state.showLeftSidebar ? "active" : ""}" data-act="team" title="Equipe & Agentes">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>
          </button>
          <button class="act-btn ${state.sidebarView === "git" && state.showLeftSidebar ? "active" : ""}" data-act="git" title="Git / Diffs / PR / Jira">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M6 21V9a9 9 0 0 0 9 9"/></svg>
            <span class="act-badge" id="git-badge" style="display:${state.diffs.length > 0 ? "inline-block" : "none"}">${state.diffs.length}</span>
          </button>
          <div class="spacer"></div>
          <button class="act-btn" id="act-wizard" title="Configurar Equipe">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
          </button>
        </nav>

        ${state.showLeftSidebar ? `
          <aside class="left-sidebar" id="left-sidebar" style="width:${state.leftWidth}px">
            ${renderLeftSidebarHtml()}
          </aside>
          <div class="resizer-v" id="resizer-left"></div>
        ` : ""}

        <section class="center-area">
          <div class="editor-tabs">
            ${state.editorTabs.map((t) => `
              <div class="editor-tab ${t.relPath === state.activeEditorPath ? "active" : ""}" data-tab="${escapeHtml(t.relPath)}">
                <span>${escapeHtml(t.name)}${t.dirty ? " ●" : ""}</span>
                <span class="close-x" data-close-tab="${escapeHtml(t.relPath)}">✕</span>
              </div>
            `).join("")}
          </div>
          ${activeTab ? (
            activeTab.kind === "diff" && activeTab.diff ? `
              <div class="editor-breadcrumb">
                <span class="dim">${escapeHtml(activeTab.diff.path)}</span>
                <span style="color:var(--green)">+${activeTab.diff.added}</span>
                <span style="color:var(--red)">-${activeTab.diff.removed}</span>
              </div>
              <div class="diff-lines">
                ${activeTab.diff.lines.map((ln) => `<div class="diff-line ${ln.kind}">${escapeHtml(ln.text)}</div>`).join("")}
              </div>
            ` : `
              <div class="editor-breadcrumb">
                <span class="dim">${escapeHtml(activeTab.relPath)}</span>
                <div class="spacer"></div>
                <button class="mini" id="btn-toggle-edit">${activeTab.editing ? "Visualizar (Syntax)" : "Editar"}</button>
                <button class="mini primary" id="btn-save-file" ${activeTab.dirty ? "" : "disabled"}>Salvar (Ctrl+S)</button>
              </div>
              <div class="editor-surface">
                ${activeTab.editing
                  ? `<textarea class="code-textarea" id="editor-textarea">${escapeHtml(activeTab.content)}</textarea>`
                  : `<pre class="line-gutter">${renderLineNumbers(activeTab.content)}</pre><pre class="code-highlight"><code>${highlightCode(activeTab.content, activeTab.name)}</code></pre>`
                }
              </div>
            `
          ) : `
            <div class="empty-welcome">
              <div style="font-size:20px;color:var(--accent);font-weight:700">HiveMind IDE</div>
              <div>Selecione um arquivo no Explorador ou converse com o Chefe no painel à direita.</div>
              <div class="shortcut-grid">
                <span class="dim">Fluxo do Time</span><span>Chefe → Dev → QA → Chefe</span>
                <span class="dim">Mencionar arquivo</span><span class="kbd">@arquivo</span>
                <span class="dim">Salvar no editor</span><span class="kbd">Ctrl + S</span>
              </div>
            </div>
          `}
        </section>

        ${state.showRightAI ? `
          <div class="resizer-v" id="resizer-right"></div>
          <aside class="right-ai-pane" id="right-pane" style="width:${state.rightWidth}px">
            ${renderRightPaneHtml()}
          </aside>
        ` : ""}
      </div>

      <footer class="status-bar">
        <span class="status-chip">${proj?.branch ? `⎇ ${escapeHtml(proj.branch)}` : "sem git"}</span>
        <span>${proj ? escapeHtml(proj.stack.summary) : ""}</span>
        <span>Equipe: ${escapeHtml(lane?.teamName || "-")} (${lane?.agents.length || 0})</span>
        <span id="status-text">${escapeHtml(state.statusText)}</span>
        <div class="spacer"></div>
        <span>${escapeHtml(state.goVersion)} · ${escapeHtml(state.os)}</span>
      </footer>
    </div>
    ${state.showWizard ? renderWizardModal() : ""}
    ${state.doctorModal ? renderDoctorModal() : ""}
  `;

  bindAllEvents();
  scrollChatIfAuto();
}

function renderLeftSidebarOnly() {
  const el = document.getElementById("left-sidebar");
  if (!el) return;
  el.innerHTML = renderLeftSidebarHtml();
  bindLeftSidebarEvents();
}

function renderRightPaneOnly() {
  const el = document.getElementById("right-pane");
  if (!el) return;
  el.innerHTML = renderRightPaneHtml();
  bindRightEvents();
  scrollChatIfAuto();
}

function scrollChatIfAuto() {
  const box = document.getElementById("chat-messages");
  const lane = activeLane();
  if (box && lane?.autoScroll) {
    box.scrollTop = box.scrollHeight;
  }
}

function renderWizardModal(): string {
  const modelOpts = state.models.length > 0
    ? state.models.map((m) => `<option value="agy:${escapeHtml(m.slug)}">${escapeHtml(m.name)} (${escapeHtml(m.slug)})</option>`).join("")
    : `<option value="agy">agy (padrão)</option>`;

  return `
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
}

function renderDoctorModal(): string {
  const d = state.doctorModal!;
  return `
    <div class="modal-backdrop">
      <div class="modal-box">
        <h3 class="modal-title">Doctor — Antigravity CLI (agy)</h3>
        <p><strong>Encontrado:</strong> ${d.Found ? "Sim" : "Não"}</p>
        <p><strong>Caminho:</strong> ${escapeHtml(d.Path || "-")}</p>
        <p><strong>Autenticado:</strong> ${d.LoggedIn ? "Sim (Google Pro)" : "Não"}</p>
        <p><strong>Detalhe:</strong> ${escapeHtml(d.Detail)}</p>
        <p><strong>Modelos (${state.models.length}):</strong></p>
        <div style="max-height:150px;overflow:auto;background:var(--bg);padding:8px;border-radius:6px;font-size:12px">
          ${state.models.length === 0 ? "Nenhum modelo carregado." : state.models.map((m) => `<div>${escapeHtml(m.slug)} — <span class="dim">${escapeHtml(m.name)}</span></div>`).join("")}
        </div>
        <div class="modal-footer">
          <button class="primary" id="doc-close">Fechar</button>
        </div>
      </div>
    </div>
  `;
}

function bindAllEvents() {
  document.getElementById("win-min")?.addEventListener("click", () => api()?.WindowMinimize());
  document.getElementById("win-max")?.addEventListener("click", () => api()?.WindowToggleMaximize());
  document.getElementById("win-close")?.addEventListener("click", () => api()?.WindowClose());

  document.getElementById("btn-toggle-left")?.addEventListener("click", () => {
    state.showLeftSidebar = !state.showLeftSidebar;
    render();
  });
  document.getElementById("btn-toggle-right")?.addEventListener("click", () => {
    state.showRightAI = !state.showRightAI;
    render();
  });

  document.getElementById("btn-file-menu")?.addEventListener("click", () => {
    state.showFileMenu = !state.showFileMenu;
    render();
  });
  document.getElementById("menu-open-folder")?.addEventListener("click", openFolderDialog);
  document.querySelectorAll("[data-recent]").forEach((el) => {
    el.addEventListener("click", () => openProjectPath((el as HTMLElement).dataset.recent!));
  });

  document.getElementById("btn-new-team")?.addEventListener("click", () => {
    state.showWizard = true;
    render();
  });
  document.getElementById("act-wizard")?.addEventListener("click", () => {
    state.showWizard = true;
    render();
  });
  document.getElementById("btn-doctor")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend) return;
    state.doctorModal = await backend.DoctorAgy();
    if (state.doctorModal.LoggedIn) {
      state.models = (await backend.ListModels()) || state.models;
    }
    render();
  });
  document.getElementById("doc-close")?.addEventListener("click", () => {
    state.doctorModal = null;
    render();
  });

  document.querySelectorAll("[data-act]").forEach((el) => {
    el.addEventListener("click", () => {
      const view = (el as HTMLElement).dataset.act as "explorer" | "team" | "git";
      if (state.sidebarView === view && state.showLeftSidebar) {
        state.showLeftSidebar = false;
      } else {
        state.sidebarView = view;
        state.showLeftSidebar = true;
      }
      render();
    });
  });

  document.querySelectorAll("[data-tab]").forEach((el) => {
    el.addEventListener("click", () => {
      state.activeEditorPath = (el as HTMLElement).dataset.tab!;
      render();
    });
  });
  document.querySelectorAll("[data-close-tab]").forEach((el) => {
    el.addEventListener("click", (ev) => {
      ev.stopPropagation();
      const path = (el as HTMLElement).dataset.closeTab!;
      state.editorTabs = state.editorTabs.filter((t) => t.relPath !== path);
      if (state.activeEditorPath === path) {
        state.activeEditorPath = state.editorTabs[0]?.relPath || "";
      }
      render();
    });
  });

  document.getElementById("btn-toggle-edit")?.addEventListener("click", () => {
    const tab = state.editorTabs.find((t) => t.relPath === state.activeEditorPath);
    if (tab) {
      tab.editing = !tab.editing;
      render();
    }
  });
  document.getElementById("btn-save-file")?.addEventListener("click", saveActiveEditor);
  const ta = document.getElementById("editor-textarea") as HTMLTextAreaElement | null;
  ta?.addEventListener("input", () => {
    const tab = state.editorTabs.find((t) => t.relPath === state.activeEditorPath);
    if (tab) {
      tab.content = ta.value;
      tab.dirty = true;
      const btn = document.getElementById("btn-save-file") as HTMLButtonElement | null;
      if (btn) btn.disabled = false;
    }
  });
  ta?.addEventListener("keydown", (e) => {
    if (e.ctrlKey && e.key.toLowerCase() === "s") {
      e.preventDefault();
      saveActiveEditor();
    }
  });

  // Resizers
  setupResizer("resizer-left", (dx) => {
    state.leftWidth = Math.max(180, Math.min(480, state.leftWidth + dx));
    const el = document.getElementById("left-sidebar");
    if (el) el.style.width = `${state.leftWidth}px`;
  });
  setupResizer("resizer-right", (dx) => {
    state.rightWidth = Math.max(300, Math.min(680, state.rightWidth - dx));
    const el = document.getElementById("right-pane");
    if (el) el.style.width = `${state.rightWidth}px`;
  });

  document.getElementById("wiz-cancel")?.addEventListener("click", () => {
    state.showWizard = false;
    render();
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
      setStatus(`Equipe "${cfg.active}" salva em .hivemind/team.yaml.`);
      render();
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

  bindLeftSidebarEvents();
  bindRightEvents();
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

function bindLeftSidebarEvents() {
  const lane = activeLane();
  document.getElementById("btn-refresh-tree")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    state.project.files = await backend.ListFiles(state.project.root);
    renderLeftSidebarOnly();
  });
  document.querySelectorAll("[data-dir]").forEach((el) => {
    el.addEventListener("click", () => {
      const rel = (el as HTMLElement).dataset.dir!;
      if (state.expandedDirs.has(rel)) state.expandedDirs.delete(rel);
      else state.expandedDirs.add(rel);
      renderLeftSidebarOnly();
    });
  });
  document.querySelectorAll("[data-file]").forEach((el) => {
    el.addEventListener("click", () => {
      const ds = (el as HTMLElement).dataset;
      openFileInEditor(ds.file!, ds.name!);
    });
  });

  document.getElementById("btn-config-team")?.addEventListener("click", () => {
    state.showWizard = true;
    render();
  });
  document.getElementById("btn-new-team-sb")?.addEventListener("click", () => {
    state.showWizard = true;
    render();
  });
  const teamSel = document.getElementById("team-select") as HTMLSelectElement | null;
  teamSel?.addEventListener("change", async () => {
    if (!lane || !state.project) return;
    lane.teamName = teamSel.value;
    lane.agents = buildAgentRuntimes(lane.teamName);
    await api()?.SetActiveTeam(state.project.root, lane.teamName);
    renderLeftSidebarOnly();
    renderRightPaneOnly();
  });

  document.getElementById("btn-refresh-diff")?.addEventListener("click", refreshDiffs);
  document.getElementById("btn-init-git")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      await backend.InitGit(state.project.root);
      const updated = await backend.OpenProject(state.project.root);
      state.project.branch = updated.branch;
      state.ghStatus = await backend.GitHubStatus(state.project.root);
      setStatus("Repositório Git inicializado (branch main).");
      render();
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

function bindRightEvents() {
  const lane = activeLane();
  document.getElementById("btn-add-lane")?.addEventListener("click", () => {
    const teams = state.project?.teamConfig?.teams || [];
    const used = new Set(state.lanes.map((l) => l.teamName));
    const next = teams.find((t) => !used.has(t.name))?.name || lane?.teamName || "principal";
    const created = createLane(next);
    state.lanes.push(created);
    state.activeLaneId = created.id;
    renderRightPaneOnly();
    if (state.sidebarView === "team") renderLeftSidebarOnly();
  });
  document.querySelectorAll("[data-lane]").forEach((el) => {
    el.addEventListener("click", () => {
      state.activeLaneId = (el as HTMLElement).dataset.lane!;
      renderRightPaneOnly();
      if (state.sidebarView === "team") renderLeftSidebarOnly();
    });
  });
  document.getElementById("btn-new-session")?.addEventListener("click", () => {
    if (!lane || lane.running) return;
    lane.sessionId = "";
    lane.messages = [{
      id: `sys-${Date.now()}`,
      kind: "system",
      label: "Sistema",
      text: "Nova sessão iniciada com o Chefe.",
    }];
    renderRightPaneOnly();
  });

  const chatBox = document.getElementById("chat-messages");
  chatBox?.addEventListener("scroll", () => {
    if (!lane || !chatBox) return;
    const atBottom = chatBox.scrollHeight - chatBox.scrollTop - chatBox.clientHeight < 24;
    lane.autoScroll = atBottom;
  });

  const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
  chatInput?.addEventListener("input", async () => {
    state.draftObjective = chatInput.value;
    const m = state.draftObjective.match(/@([^\s@]*)$/);
    const backend = api();
    if (m && backend && state.project) {
      state.mentionResults = (await backend.SearchFiles(state.project.root, m[1])) || [];
      renderRightPaneOnly();
      const restored = document.getElementById("chat-input") as HTMLTextAreaElement | null;
      restored?.focus();
    } else if (state.mentionResults.length > 0) {
      state.mentionResults = [];
      renderRightPaneOnly();
    }
  });
  chatInput?.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      sendToChief();
    }
  });
  document.querySelectorAll("[data-mention]").forEach((el) => {
    el.addEventListener("click", () => {
      const f = (el as HTMLElement).dataset.mention!;
      state.draftObjective = state.draftObjective.replace(/@([^\s@]*)$/, `@${f} `);
      state.mentionResults = [];
      renderRightPaneOnly();
    });
  });

  const jiraInput = document.getElementById("jira-key-input") as HTMLInputElement | null;
  jiraInput?.addEventListener("input", () => {
    state.jiraKeyInput = jiraInput.value;
  });

  document.getElementById("btn-send-run")?.addEventListener("click", sendToChief);
  document.getElementById("btn-cancel-run")?.addEventListener("click", cancelActiveLane);
}

boot();
