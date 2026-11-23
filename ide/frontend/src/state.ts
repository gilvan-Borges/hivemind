import { icons } from "./icons";
import type { AIUsageReport, AgyModel, AgyStatus, AutoReviewStatus, BottomDrawerTab, ConfirmDialogState, CursorCLIStatus, EditorTab, FileDiff, FileNode, GitGraphCommit, GitHubStatus, GitSubTab, IdeSettingsSection, ImageAttachment, InlineAIStatus, InlineEditResponse, JiraConnectorStatus, JiraProject, LSPStatus, MCPServerInfo, MobileBridgeStatus, MobilePairing, OpenCodeStatus, OpenRouterModelInfo, PermissionMode, ProblemItem, ProfilesOverview, ProjectInfo, ProjectIssue, ProjectPullRequest, ProjectWorkspaceContext, ProviderHealthView, ProvidersScanReport, PullRequestActivityDetail, RelayLinkCode, RelayStatus, RunMode, SessionSummary, SidebarView, SkillAssignment, SkillInfo, TeamLane, TerminalEntry, TextSearchMatch, UpdateCheckResult, WailsBackendApp, WorkspaceGitRepo } from "./types";
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
      InitializeNotifications?(): Promise<void>;
      IsNotificationAvailable?(): Promise<boolean>;
      RequestNotificationAuthorization?(): Promise<boolean>;
      SendNotification?(opts: { id: string; title: string; body?: string }): Promise<void>;
      WindowIsMinimised?(): Promise<boolean>;
      WindowUnminimise?(): void;
      WindowShow?(): void;
    };
  }
}

export const api = () => (typeof window === "undefined" ? undefined : window.go?.main?.App);

export function publishComposerMode() {
  const root = state.project?.root?.trim();
  const mode = state.runMode;
  if (!root || !mode) return;
  const fn = api()?.SetComposerMode;
  if (!fn) return;
  void fn(root, mode).catch(() => {});
}

export function setRunMode(mode: RunMode) {
  state.runMode = mode;
  publishComposerMode();
}

export const MISSION_TAB_KEY = "mission:swarm";

export const TEAM_CONFIG_TAB_KEY = "team:config";

export const AI_USAGE_TAB_KEY = "ai:usage";

export const IDE_SETTINGS_TAB_KEY = "ide:settings";

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
  shellUnsandboxed: boolean;
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
  cursorModels: [] as string[],
  cursorCLI: null as CursorCLIStatus | null,
  openCodeStatus: null as OpenCodeStatus | null,
  ghStatus: null as GitHubStatus | null,
  jiraStatus: null as JiraConnectorStatus | null,
  jiraProjects: [] as JiraProject[],
  jiraProjectsLoading: false,
  planMenuOpen: false,
  composerMenuOpen: "" as "" | "mode" | "perm",
  goVersion: "",
  os: "windows",
  statusText: "Pronto",
  sidebarView: "explorer" as SidebarView,
  showLeftSidebar: true,
  showRightAI: true,
  chatExpandedPref: false,
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
  ideSettingsSection: "providers" as IdeSettingsSection,
  orbRunning: false,
  appVersion: "",
  showGitHubModal: false,
  showJiraModal: false,
  showAPIKeysModal: false,
  showMobileModal: false,
  showUpdatesModal: false,
  updateCheckState: {
    status: "idle" as "idle" | "checking" | "ready" | "error",
    version: "",
    result: null as UpdateCheckResult | null,
    downloading: false,
    downloadedPath: "",
    error: "",
  },
  mobileStatus: null as MobileBridgeStatus | null,
  mobilePairing: null as MobilePairing | null,
  relayStatus: null as RelayStatus | null,
  relayLink: null as RelayLinkCode | null,
  relayDraft: {
    baseUrl: "",
    email: "",
    password: "",
    claim: "",
    currentPassword: "",
    newPassword: "",
    newUserEmail: "",
    newUserPassword: "",
  },
  aiUsagePage: {
    loading: false,
    report: null as AIUsageReport | null,
    editingProvider: "",
  },
  providerHealth: [] as ProviderHealthView[],
  providerHealthProbing: false,
  providersScan: null as ProvidersScanReport | null,
  providersScanning: false,
  providersScanExpanded: {} as Record<string, boolean>,
  apiKeysStatus: {} as Record<string, string>,
  apiKeysTab: "profiles" as "profiles" | "global" | "scan",
  agyOAuthWaitingCode: false,
  agyOAuthSubmitting: false,
  agyOAuthCodeInput: "",
  jiraEditingProfileId: null as string | null,
  profilesOverview: null as ProfilesOverview | null,
  profileEditor: {
    open: false,
    id: "",
    provider: "agy",
    authMode: "live",
  },
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
  issuesSourceFilter: "" as "" | "jira" | "github",
  expandedIssueSubtasks: new Set<string>(),
  prs: {
    list: [] as ProjectPullRequest[],
    loading: false,
    loadedOnce: false,
  },
  prsCollapsed: false,
  issuesCollapsed: false,
  prDetails: {} as Record<number, { open: boolean; loading: boolean; error: string; data?: PullRequestActivityDetail }>,
  gitGraph: {
    commits: [] as GitGraphCommit[],
    loading: false,
    loadedOnce: false,
    allBranches: true,
    collapsed: false,
    repoRoot: "",
  } as ProjectWorkspaceContext["gitGraph"],
  gitRepos: [] as WorkspaceGitRepo[],
  activeGitRepoRoot: "",
  collapsedGitRepos: {} as Record<string, boolean>,
  commitMsgByRepo: {} as Record<string, string>,
  changesCollapsed: false,
  gitMoreMenuOpen: false,
  gitOp: {
    running: false,
    action: "",
    label: "",
    log: "",
    result: "",
    error: "",
  },
  commitMenuOpen: false,
  commitMenuRepoRoot: "",
  showFlowResponded: false,
  autoReview: null as AutoReviewStatus | null,
  autoReviewConfigOpen: false,
  githubUnreadCount: 0,
  githubLastSyncAt: "",
  gitTab: "changes" as GitSubTab,
  worktrees: {
    loading: false,
    error: "",
    loadedOnce: false,
    list: [] as import("./types").WorktreeInfo[],
    checkpoints: [] as import("./types").WorktreeCheckpoint[],
    creating: false,
    createError: "",
    progress: null as null | { step: string; pct: number },
    newAgent: "dev_1",
    newBranch: "",
  },
  skills: [] as SkillInfo[],
  mcpServers: [] as MCPServerInfo[],
  skillEditor: null as null | { mode: "create" | "edit"; name: string; body: string },
  branchPopover: {
    open: false,
    anchorTop: false,
    branches: [] as string[],
    newBranch: "",
    repoRoot: "",
  },
  mentionResults: [] as string[],
  draftObjective: "",
  draftImages: [] as ImageAttachment[],
  imagePreviewUrl: "",
  jiraKeyInput: "",
  commitMsgInput: "",
  splitLaneIds: [] as string[],
  designMode: {
    open: false,
    loadingPages: false,
    picking: false,
    error: "",
    pages: [] as import("./types").DesignModePage[],
    pageIdx: -1,
    selectorInput: "",
    selected: null as import("./types").DesignModeElement | null,
  },
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
  fileGitInspect: {
    mode: "none" as "none" | "blame" | "history",
    relPath: "",
    loading: false,
    error: "",
    blame: [] as import("./types").BlameLine[],
    history: [] as import("./types").FileCommitEntry[],
  },
  gitConflicts: {
    loading: false,
    error: "",
    files: [] as import("./types").ConflictedFile[],
    busyKey: "",
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
  ctx.splitLaneIds = [...state.splitLaneIds];
  ctx.runMode = state.runMode;
  ctx.diffs = state.diffs;
  ctx.gitRepos = state.gitRepos;
  ctx.activeGitRepoRoot = state.activeGitRepoRoot;
  ctx.collapsedGitRepos = state.collapsedGitRepos;
  ctx.commitMsgByRepo = state.commitMsgByRepo;
  ctx.ghStatus = state.ghStatus;
  ctx.textSearch = state.textSearch;
  ctx.sessionsSummary = state.sessionsSummary;
  ctx.issues = state.issues;
  ctx.prs = state.prs;
  ctx.gitGraph = state.gitGraph;
  ctx.gitTab = state.gitTab;
  ctx.autoReview = state.autoReview;
  ctx.githubUnreadCount = state.githubUnreadCount;
  ctx.githubLastSyncAt = state.githubLastSyncAt;
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
  state.splitLaneIds = Array.isArray(ctx.splitLaneIds) ? [...ctx.splitLaneIds] : [];
  state.designMode.open = false;
  state.runMode = ctx.runMode;
  state.diffs = ctx.diffs;
  state.gitRepos = ctx.gitRepos ?? [];
  state.activeGitRepoRoot = ctx.activeGitRepoRoot ?? "";
  state.collapsedGitRepos = ctx.collapsedGitRepos ?? {};
  state.commitMsgByRepo = ctx.commitMsgByRepo ?? {};
  state.commitMenuOpen = false;
  state.commitMenuRepoRoot = "";
  state.ghStatus = ctx.ghStatus;
  state.textSearch = ctx.textSearch;
  state.sessionsSummary = ctx.sessionsSummary;
  state.issues = ctx.issues;
  state.prs = ctx.prs;
  state.prDetails = {};
  state.gitGraph = ctx.gitGraph;
  state.gitTab = ctx.gitTab;
  state.autoReview = ctx.autoReview ?? null;
  state.githubUnreadCount = ctx.githubUnreadCount ?? 0;
  state.githubLastSyncAt = ctx.githubLastSyncAt ?? "";
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
  publishComposerMode();
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
