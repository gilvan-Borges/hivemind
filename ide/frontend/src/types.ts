export interface FileNode {
  name: string;
  path: string;
  relPath: string;
  isDir: boolean;
  hasChildren?: boolean;
  children?: FileNode[];
}

export interface AgyModel {
  slug: string;
  name: string;
}

export interface AgyStatus {
  Found: boolean;
  Path: string;
  LoggedIn: boolean;
  Detail: string;
}

export interface OpenCodeStatus {
  configured: boolean;
  source: string;
  baseUrl: string;
  keyMasked: string;
  goCount?: number;
  zenCount?: number;
  modelCount: number;
  detail: string;
}

export interface TeamAgent {
  name: string;
  role: string;
  provider: string;
  model: string;
}

export interface TeamSpec {
  name: string;
  agents: TeamAgent[];
}

export type PermissionMode = "governed" | "auto_edit" | "full_access";

export type SkillTargetRole = "all" | "chief" | "dev" | "qa";

export interface SkillAssignment {
  name: string;
  source?: "native" | "project" | "";
  roles: SkillTargetRole[];
}

export interface TeamSettings {
  permission_mode?: PermissionMode;
  jira_enabled: boolean;
  jira_project?: string;
  worktrees?: boolean;
  auto_commit: boolean;
  auto_pr: boolean;
  homolog_branch?: string;
  protected_branches?: string[];
  require_main_merge_approval?: boolean;
  base_branch: string;
  max_dynamic_devs?: number;
  max_tokens?: number;
  in_turn_keep_tool_results?: number;
  review_model?: string;
  skill_assignments?: SkillAssignment[];
}

export interface TeamFile {
  version: number;
  active: string;
  settings: TeamSettings;
  teams: TeamSpec[];
}

export interface ProjectStack {
  root: string;
  name: string;
  stacks: string[];
  buildTools: string[];
  testHint: string;
  summary: string;
}

export interface ProjectInfo {
  root: string;
  name: string;
  branch: string;
  stack: ProjectStack;
  hasTeam: boolean;
  teamConfig: TeamFile;
  files: FileNode[];
}

export interface DiffLine {
  kind: "header" | "hunk" | "added" | "removed" | "context";
  text: string;
}

export interface FileDiff {
  path: string;
  status?: string;
  staged?: boolean;
  added: number;
  removed: number;
  lines: DiffLine[];
}

export interface GitGraphCommit {
  hash: string;
  short: string;
  parents: string[];
  author: string;
  date: string;
  subject: string;
  refs?: string[];
}

export interface CommitDetail {
  hash: string;
  short: string;
  author: string;
  date: string;
  subject: string;
  body?: string;
  files: FileDiff[];
}

export interface ImageAttachment {
  id: string;
  name: string;
  mimeType: string;
  dataBase64: string;
}

export interface DiffSummaryItem {
  path: string;
  added: number;
  removed: number;
}

export type RunMode = "auto" | "direct" | "plan" | "plan:sdd" | "plan:sdd-jira" | "squad";

export interface ChatMessage {
  id: string;
  kind: "user" | "chief" | "system" | "tool" | "thinking" | "error" | "handoff" | "diff" | "plan" | "approval";
  label: string;
  text: string;
  active?: boolean;
  solo?: boolean;
  approved?: boolean;
  resolved?: "approved" | "denied";
  approvalId?: string;
  gate?: "docs" | "jira";
  images?: string[];
  diffItems?: DiffSummaryItem[];
  tokens?: number;
  toolName?: string;
  toolDetail?: string;
  toolOutput?: string;
  startedAt?: number;
  durationMs?: number;
  stepCount?: number;
}

export interface AgentRuntime {
  name: string;
  role: string;
  model: string;
  status: "idle" | "running" | "done" | "error";
  tool?: string;
  inTokens: number;
  outTokens: number;
  tokens: number;
  calls: number;
}

export interface TeamLane {
  id: string;
  teamName: string;
  sessionId: string;
  running: boolean;
  canceledByUser?: boolean;
  stage: "" | "chief" | "dev" | "qa" | "closing";
  doneStages: string[];
  messages: ChatMessage[];
  agents: AgentRuntime[];
  autoScroll: boolean;
  turnInTokens: number;
  turnOutTokens: number;
  turnTotalTokens: number;
  sessionInTokens: number;
  sessionOutTokens: number;
  sessionTotalTokens: number;
}

export interface TerminalEntry {
  id: string;
  command: string;
  output: string;
  exitCode: number | null;
  durationMs: number;
  running: boolean;
}

export interface EditorTab {
  kind: "file" | "diff" | "mission" | "team-config" | "commit";
  relPath: string;
  name: string;
  content: string;
  dirty: boolean;
  diff?: FileDiff;
  commitDetail?: CommitDetail;
  mdView?: "preview" | "source";
}

export interface GitHubStatus {
  installed: boolean;
  loggedIn: boolean;
  user: string;
  remote: string;
  detail: string;
  mode?: string;
  source?: string;
  connectorLabel?: string;
  tokenMasked?: string;
  baseUrl?: string;
}

export interface JiraConnectorStatus {
  configured: boolean;
  connected: boolean;
  source: string;
  baseUrl: string;
  email: string;
  tokenMasked: string;
  defaultProject: string;
  user: string;
  detail: string;
}

export interface TokenStat {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
  calls: number;
}

export interface SessionSummary {
  id: string;
  updatedAt: string;
  title: string;
  jiraKey?: string;
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
}

export interface WorkspaceSessionsSummary {
  sessions: SessionSummary[];
  projectInputTokens: number;
  projectOutputTokens: number;
  projectTotalTokens: number;
}

export interface RestoredMessage {
  id: string;
  kind: string;
  label: string;
  text: string;
  tokens?: number;
}

export interface SessionDetail {
  id: string;
  title: string;
  jiraKey?: string;
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
  agentUsage: Record<string, TokenStat>;
  messages: RestoredMessage[];
}

export interface TextSearchMatch {
  relPath: string;
  line: number;
  preview: string;
}

export interface ProjectIssue {
  source: string;
  key: string;
  title: string;
  body?: string;
  status?: string;
  author?: string;
  url?: string;
  isMine?: boolean;
}

export interface ProjectPullRequest {
  number: number;
  title: string;
  body?: string;
  url: string;
  state: string;
  isDraft: boolean;
  author?: string;
  headRef?: string;
  baseRef?: string;
  updatedAt?: string;
  reviewDecision?: string;
  requestedReviewers?: string[];
  isMine?: boolean;
  reviewRequested?: boolean;
  myResponse?: "" | "approved" | "changes_requested" | "commented";
  needsAttention?: boolean;
  attentionReason?: string;
}

export type GitSubTab = "changes" | "prs_issues" | "flow";

export interface CommandAction {
  id: string;
  label: string;
  shortcut?: string;
  run: () => void;
}

export interface ConfirmDialogState {
  title: string;
  body: string;
  confirmLabel: string;
  danger?: boolean;
  onConfirm: () => void | Promise<void>;
}

export interface InitialStatePayload {
  initialWorkspace: string;
  initialProject: ProjectInfo;
  recentProjects: string[];
  agyStatus: AgyStatus;
  models: AgyModel[];
  openCodeStatus?: OpenCodeStatus;
  openCodeGoModels?: AgyModel[];
  openCodeModels?: AgyModel[];
  goVersion: string;
  os: string;
}

export interface RunResultPayload {
  SessionID?: string;
  sessionID?: string;
  Summary?: string;
  IntegrationBranch?: string;
  integrationBranch?: string;
  inputTokens?: number;
  outputTokens?: number;
  totalTokens?: number;
}

export interface WizardInputPayload {
  teamName: string;
  chiefRole: string;
  devCount: number;
  maxDynamicDevs?: number;
  defaultModel: string;
  agentModels?: Record<string, string>;
  permissionMode?: PermissionMode;
  worktrees: boolean;
  autoCommit: boolean;
  autoPR: boolean;
  jiraEnabled: boolean;
  jiraProject: string;
  baseBranch?: string;
  homologBranch?: string;
  maxTokens: number;
  skillAssignments?: SkillAssignment[];
}

export type SidebarView = "explorer" | "search" | "team" | "git" | "sessions" | "skills";

export interface ProjectWorkspaceContext {
  id: string;
  project: ProjectInfo;
  sidebarView: SidebarView;
  expandedDirs: Set<string>;
  dirCache: Map<string, FileNode[]>;
  explorerFilter: string;
  explorerFilterResults: string[];
  editorTabs: EditorTab[];
  activeEditorPath: string;
  lanes: TeamLane[];
  activeLaneId: string;
  runMode: RunMode;
  diffs: FileDiff[];
  ghStatus: GitHubStatus | null;
  textSearch: {
    query: string;
    results: TextSearchMatch[];
    searching: boolean;
  };
  sessionsSummary: {
    sessions: SessionSummary[];
    projectInputTokens: number;
    projectOutputTokens: number;
    projectTotalTokens: number;
  };
  issues: {
    list: ProjectIssue[];
    loading: boolean;
    loadedOnce: boolean;
  };
  prs: {
    list: ProjectPullRequest[];
    loading: boolean;
    loadedOnce: boolean;
  };
  gitGraph: {
    commits: GitGraphCommit[];
    loading: boolean;
    loadedOnce: boolean;
    allBranches: boolean;
    collapsed: boolean;
  };
  gitTab: GitSubTab;
  draftObjective: string;
  draftImages: ImageAttachment[];
  jiraKeyInput: string;
  commitMsgInput: string;
  terminal: {
    open: boolean;
    running: boolean;
    cmdInput: string;
    entries: TerminalEntry[];
  };
}

export interface SkillInfo {
  name: string;
  description: string;
  source: "native" | "project";
  path: string;
  updatedAt: string;
  sizeBytes: number;
}

export interface UsageWindow {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
  calls: number;
  oldestAt?: string;
}

export interface RemoteQuotaWindow {
  percent: number;
  resetsAt?: string;
  status?: string;
  hasData: boolean;
}

export interface OpenCodeQuota {
  available: boolean;
  detail?: string;
  rolling5h: RemoteQuotaWindow;
  weekly: RemoteQuotaWindow;
  monthly: RemoteQuotaWindow;
}

export interface ProviderLimits {
  fiveHourTokens?: number;
  weeklyTokens?: number;
}

export interface AIProviderUsage {
  provider: string;
  label: string;
  window5h: UsageWindow;
  windowWeekly: UsageWindow;
  windowMonthly: UsageWindow;
  limits: ProviderLimits;
}

export interface AgyQuotaBucket {
  id: string;
  name: string;
  window: string;
  usedPercent: number;
  remainingPercent: number;
  resetTime?: string;
  description?: string;
}

export interface AgyQuotaGroup {
  name: string;
  description?: string;
  buckets: AgyQuotaBucket[];
}

export interface AgyQuota {
  available: boolean;
  detail?: string;
  groups?: AgyQuotaGroup[];
}

export interface AIUsageReport {
  generatedAt: string;
  agyQuota?: AgyQuota;
  openCodeQuota: OpenCodeQuota;
  providers: AIProviderUsage[];
}

export interface WailsBackendApp {
  GetInitialState(): Promise<InitialStatePayload>;
  BootstrapAgy(force: boolean): Promise<{ status: AgyStatus; models: AgyModel[] }>;
  BootstrapOpenCode(force: boolean): Promise<{ status: OpenCodeStatus; goModels?: AgyModel[]; models: AgyModel[] }>;
  SaveOpenCodeConfig(apiKey: string, baseURL: string): Promise<{ status: OpenCodeStatus; goModels?: AgyModel[]; models: AgyModel[] }>;
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
  SaveTeamWizard(workspace: string, input: WizardInputPayload): Promise<TeamFile>;
  SaveSkillAssignments(workspace: string, assignments: SkillAssignment[]): Promise<TeamFile>;
  SetActiveTeam(workspace: string, teamName: string): Promise<TeamFile>;
  DeleteTeam(workspace: string, teamName: string): Promise<TeamFile>;
  UpdateAgentModel(workspace: string, teamName: string, agentName: string, modelSpec: string): Promise<TeamFile>;
  UpdatePermissionMode(workspace: string, permissionMode: string): Promise<TeamFile>;
  RespondApproval(approvalID: string, approved: boolean): Promise<boolean>;
  EnsureTeamMemory(workspace: string): Promise<string>;
  GetDiff(workspace: string): Promise<FileDiff[]>;
  CommitChanges(workspace: string, message: string, push: boolean): Promise<string>;
  OpenPR(workspace: string, title: string, body: string, baseBranch: string): Promise<string>;
  CommentJira(workspace: string, issueKey: string, comment: string): Promise<void>;
  DiscardChanges(workspace: string): Promise<void>;
  DiscardFile(workspace: string, relPath: string): Promise<void>;
  StageFile(workspace: string, relPath: string, stage: boolean): Promise<void>;
  RunGitAction(workspace: string, action: string): Promise<string>;
  ListGitGraph(workspace: string, limit: number, allBranches: boolean): Promise<GitGraphCommit[]>;
  GetCommitDetail(workspace: string, hash: string): Promise<CommitDetail>;
  RevertHunk(workspace: string, relPath: string, hunkHeader: string, hunkLines: string[]): Promise<void>;
  GitHubStatus(workspace: string): Promise<GitHubStatus>;
  SaveGitHubConfig?(token: string, baseURL: string, workspace: string): Promise<GitHubStatus>;
  JiraStatus?(): Promise<JiraConnectorStatus>;
  SaveJiraConfig?(baseURL: string, email: string, token: string, defaultProject: string, clear: boolean): Promise<JiraConnectorStatus>;
  InitGit(workspace: string): Promise<void>;
  WindowMinimize(): Promise<void>;
  WindowToggleMaximize(): Promise<void>;
  WindowClose(): Promise<void>;
  StartRun(laneID: string, workspace: string, teamName: string, objective: string, jiraKey: string, sessionID: string): Promise<RunResultPayload>;
  StartRunWithImages(laneID: string, workspace: string, teamName: string, objective: string, jiraKey: string, sessionID: string, images: Array<{ name: string; mimeType: string; dataBase64: string }>): Promise<RunResultPayload>;
  StartRunWithOptions(laneID: string, workspace: string, teamName: string, objective: string, jiraKey: string, sessionID: string, runMode: string, images: Array<{ name: string; mimeType: string; dataBase64: string }>): Promise<RunResultPayload>;
  CancelRun(laneID: string): Promise<boolean>;
  RunCommand(workspace: string, command: string): Promise<{ command: string; output: string; exitCode: number; durationMs: number }>;
  CancelCommand(workspace: string): Promise<boolean>;
  ListSessions(workspace: string): Promise<WorkspaceSessionsSummary>;
  LoadSession(workspace: string, sessionID: string): Promise<SessionDetail>;
  DeleteSession(workspace: string, sessionID: string): Promise<void>;
  SearchText(workspace: string, query: string): Promise<TextSearchMatch[]>;
  CreateFile(workspace: string, relPath: string): Promise<void>;
  CreateDir(workspace: string, relPath: string): Promise<void>;
  RenamePath(workspace: string, oldRel: string, newRel: string): Promise<void>;
  DeletePath(workspace: string, relPath: string): Promise<void>;
  ListBranches(workspace: string): Promise<string[]>;
  CheckoutBranch(workspace: string, branch: string, create: boolean): Promise<void>;
  ListIssues(workspace: string): Promise<ProjectIssue[]>;
  ListPullRequests(workspace: string): Promise<ProjectPullRequest[]>;
  CloseOwnGitHubItem(workspace: string, kind: "pr" | "issue", number: number): Promise<void>;
  OpenExternalURL(rawURL: string): Promise<void>;
  GetAIUsage(): Promise<AIUsageReport>;
  SaveAIUsageLimits(provider: string, fiveHourTokens: number, weeklyTokens: number): Promise<AIUsageReport>;
  ListSkills(workspace: string): Promise<SkillInfo[]>;
  ReadNativeSkill(name: string): Promise<string>;
  SaveNativeSkill(name: string, body: string): Promise<void>;
  DeleteNativeSkill(name: string): Promise<void>;
  InstallSkillToProject(workspace: string, name: string): Promise<void>;
  RemoveProjectSkill(workspace: string, name: string): Promise<void>;
}
