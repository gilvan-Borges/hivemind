import { icons } from "./icons";
import type { AIUsageReport, AgyModel, AgyStatus, AutoReviewStatus, BottomDrawerTab, ConfirmDialogState, EditorTab, FileDiff, FileNode, GitGraphCommit, GitHubStatus, GitSubTab, ImageAttachment, InlineAIStatus, InlineEditResponse, JiraConnectorStatus, JiraProject, LSPStatus, MobileBridgeStatus, MobilePairing, OpenCodeStatus, OpenRouterModelInfo, PermissionMode, ProblemItem, ProjectInfo, ProjectIssue, ProjectPullRequest, ProjectWorkspaceContext, RunMode, SessionSummary, SidebarView, SkillAssignment, SkillInfo, TeamLane, TerminalEntry, TextSearchMatch, WailsBackendApp } from "./types";
import { renderModalLayer } from "./modals";

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

export const api = () => window.go?.main?.App;

export const MISSION_TAB_KEY = "mission:swarm";

export const TEAM_CONFIG_TAB_KEY = "team:config";

export interface TeamConfigDraft {
  workspace: string;
  teamName: string;
  chiefRole: string;
  permissionMode: PermissionMode;
  defaultModel: string;
  maxDynamicDevs: number;
  chiefModel: string;
  devModel: string;
  qaModel: string;
  baseBranch: string;
  homologBranch: string;
  maxTokens: number;
  worktrees: boolean;
  autoCommit: boolean;
  autoPR: boolean;
  jiraEnabled: boolean;
  jiraProject: string;
  skillAssignments: SkillAssignment[];
  skillFilter: string;
}

export const state = {
  projectTabs: [] as ProjectWorkspaceContext[],
  activeProjectId: "",
  project: null as ProjectInfo | null,
  recent: [] as string[],
  models: [] as AgyModel[],
  agyStatus: null as AgyStatus | null,
  openCodeGoModels: [] as AgyModel[],
  openCodeModels: [] as AgyModel[],
  openRouterModels: [] as OpenRouterModelInfo[],
  openCodeStatus: null as OpenCodeStatus | null,
  ghStatus: null as GitHubStatus | null,
  jiraStatus: null as JiraConnectorStatus | null,
  jiraProjects: [] as JiraProject[],
  jiraProjectsLoading: false,
  planMenuOpen: false,
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
  teamConfigDraft: null as TeamConfigDraft | null,
  showOpenCodeModal: false,
  showGitHubModal: false,
  showJiraModal: false,
  showAPIKeysModal: false,
  showMobileModal: false,
  mobileStatus: null as MobileBridgeStatus | null,
  mobilePairing: null as MobilePairing | null,
  aiUsageModal: {
    open: false,
    loading: false,
    report: null as AIUsageReport | null,
    editingProvider: "",
  },
  doctorModal: null as AgyStatus | null,
  apiKeysStatus: {} as Record<string, string>,
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
  issuesFilterQuery: "",
  issuesStatusFilter: "",
  prs: {
    list: [] as ProjectPullRequest[],
    loading: false,
    loadedOnce: false,
  },
  gitGraph: {
    commits: [] as GitGraphCommit[],
    loading: false,
    loadedOnce: false,
    allBranches: true,
    collapsed: false,
  },
  changesCollapsed: false,
  gitMoreMenuOpen: false,
  commitMenuOpen: false,
  showFlowResponded: false,
  autoReview: null as AutoReviewStatus | null,
  autoReviewConfigOpen: false,
  gitTab: "changes" as GitSubTab,
  skills: [] as SkillInfo[],
  skillEditor: null as null | { mode: "create" | "edit"; name: string; body: string },
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
  bottomTab: "terminal" as BottomDrawerTab,
  lspStatus: null as LSPStatus | null,
  lspDiagnostics: {} as Record<string, ProblemItem[]>,
  buildProblems: [] as ProblemItem[],
  problemsFilter: "all" as "all" | "error" | "warning",
  problemsChecking: false,
  problemsLastCmd: "",
  diffViewMode: "unified" as "unified" | "split",
  ghostTextEnabled: true,
  ghostTextAccepted: 0,
  inlineAIStatus: null as InlineAIStatus | null,
  inlineReviewEnabled: true,
  inlineReviewDiff: null as FileDiff | null,
  inlineEdit: {
    open: false,
    relPath: "",
    from: 0,
    to: 0,
    startLine: 1,
    endLine: 1,
    selectedText: "",
    instruction: "",
    loading: false,
    error: "",
    result: null as InlineEditResponse | null,
  },
  editorFind: {
    open: false,
    showReplace: false,
    query: "",
    replaceText: "",
    matchIndex: 0,
    matches: [] as Array<{ start: number; end: number }>,
  },
};

export let laneSeq = 1;

export let projectSeq = 1;

export function nextLaneSeq(): number {
  return laneSeq++;
}

export function nextProjectSeq(): number {
  return projectSeq++;
}

export function normalizePathKey(p: string): string {
  return (p || "").trim().replace(/\\/g, "/").toLowerCase();
}

export function hasDirtyTabs(): boolean {
  return state.editorTabs.some((t) => t.kind === "file" && t.dirty);
}

export function hasAnyProjectDirtyTabs(): boolean {
  if (hasDirtyTabs()) return true;
  return state.projectTabs.some((ctx) =>
    ctx.id !== state.activeProjectId && ctx.editorTabs.some((t) => t.kind === "file" && t.dirty)
  );
}

export function saveActiveToProjectContext() {
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
  ctx.prs = state.prs;
  ctx.gitGraph = state.gitGraph;
  ctx.gitTab = state.gitTab;
  ctx.autoReview = state.autoReview;
  ctx.draftObjective = state.draftObjective;
  ctx.draftImages = state.draftImages;
  ctx.jiraKeyInput = state.jiraKeyInput;
  ctx.commitMsgInput = state.commitMsgInput;
  ctx.terminal = state.terminal;
  ctx.lspStatus = state.lspStatus;
  ctx.lspDiagnostics = state.lspDiagnostics;
  ctx.buildProblems = state.buildProblems;
}

export function loadFromProjectContext(ctx: ProjectWorkspaceContext) {
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
  state.prs = ctx.prs;
  state.gitGraph = ctx.gitGraph;
  state.gitTab = ctx.gitTab;
  state.autoReview = ctx.autoReview ?? null;
  state.draftObjective = ctx.draftObjective;
  state.draftImages = ctx.draftImages;
  state.jiraKeyInput = ctx.jiraKeyInput;
  state.commitMsgInput = ctx.commitMsgInput;
  state.terminal = ctx.terminal;
  state.lspStatus = ctx.lspStatus ?? null;
  state.lspDiagnostics = ctx.lspDiagnostics ?? {};
  state.buildProblems = ctx.buildProblems ?? [];
  state.mentionResults = [];
  state.branchPopover.open = false;
}

export function findLaneWithContext(laneId: string): { lane: TeamLane; ctx?: ProjectWorkspaceContext; isActiveProject: boolean } | null {
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

export function askConfirm(dialog: ConfirmDialogState) {
  state.confirmModal = dialog;
  renderModalLayer();
}

export function setStatus(msg: string) {
  state.statusText = msg;
  const el = document.getElementById("status-text");
  if (el) el.textContent = msg;
}

export type ToastKind = "info" | "success" | "error";

export function toast(text: string, kind: ToastKind = "info", opts?: { timeoutMs?: number; retry?: () => void }): void {
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

export function toastError(what: string, err: any, retry?: () => void): void {
  const reason = err instanceof Error ? err.message : typeof err === "string" ? err : err?.message || "";
  const clean = String(reason || "").trim();
  toast(`Não foi possível ${what}${clean ? `: ${clean}` : "."}`, "error", retry ? { retry } : undefined);
}
