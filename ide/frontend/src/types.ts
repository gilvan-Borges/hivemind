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

export interface CursorCLIStatus {
  found: boolean;
  path: string;
  hasKey: boolean;
  detail: string;
  sandbox?: string;
  sandboxWarning?: string;
}

export interface OpenRouterModelInfo {
  id: string;
  name: string;
  contextLength?: number;
  promptPrice?: number;
  completionPrice?: number;
  discountPct?: number;
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

export interface AutoReviewSettings {
  enabled: boolean;
  poll_interval_seconds?: number;
  run_tests?: boolean;
  review_action?: "full_review" | "comment_only";
  include_drafts?: boolean;
  max_prs_per_hour?: number;
  max_tokens_per_pr?: number;
  model?: string;
}

export interface AutoReviewEntry {
  prNumber: number;
  title: string;
  author?: string;
  headRef?: string;
  baseRef?: string;
  status: "reviewed" | "error" | "canceled";
  summary?: string;
  tokens?: number;
  durationMs?: number;
  sessionId?: string;
  reviewedAt: string;
  error?: string;
}

export interface AutoReviewStatus {
  workspace: string;
  enabled: boolean;
  state: "idle" | "polling" | "reviewing" | "paused_limit" | "error";
  config: AutoReviewSettings;
  activePr?: number;
  activePrTitle?: string;
  queueCount: number;
  reviewedLastHour: number;
  lastPollAt?: string;
  nextPollAt?: string;
  lastError?: string;
  history?: AutoReviewEntry[];
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
  inline_model?: string;
  auto_review?: AutoReviewSettings;
  shell_unsandboxed?: boolean;
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

export interface WorkspaceGitRepo {
  root: string;
  name: string;
  relPath: string;
  branch: string;
  isRoot: boolean;
  diffs: FileDiff[];
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

export type RunMode = "auto" | "direct" | "plan" | "plan:sdd" | "plan:sdd-jira" | "squad" | "race";

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
  riskTier?: string;
  images?: string[];
  diffItems?: DiffSummaryItem[];
  tokens?: number;
  modelLabel?: string;
  modelProvider?: string;
  modelFullId?: string;
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
  routeModel?: string;
  routeProvider?: string;
  routeFriendly?: string;
  routeTier?: string;
  routeReasons?: string[];
  status: "idle" | "running" | "done" | "error";
  tool?: string;
  runningSince?: number;
  inTokens: number;
  outTokens: number;
  tokens: number;
  calls: number;
  costMicroUsd?: number;
}

export interface TeamLane {
  id: string;
  teamName: string;
  label?: string;
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
  turnCostMicroUsd: number;
  sessionCostMicroUsd: number;
  costByRole: Record<string, number>;
  viewMode?: "chat" | "pty";
}

export interface TerminalEntry {
  id: string;
  command: string;
  output: string;
  exitCode: number | null;
  durationMs: number;
  running: boolean;
}

export type IdeSettingsSection = "providers" | "agent-hive" | "limits" | "integrations" | "general";

export interface EditorTab {
  kind: "file" | "diff" | "mission" | "team-config" | "commit" | "resident" | "ai-usage" | "ide-settings";
  relPath: string;
  name: string;
  content: string;
  dirty: boolean;
  diff?: FileDiff;
  repoRoot?: string;
  workspaceRelPath?: string;
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

export interface JiraProject {
  key: string;
  name: string;
  projectType?: string;
}

export interface JiraProfile {
  id: string;
  name: string;
  base_url: string;
  email: string;
  token: string;
  default_project?: string;
  org_patterns?: string[];
  is_default?: boolean;
}

export interface JiraProfileSummary {
  id: string;
  name: string;
  baseUrl: string;
  email: string;
  tokenMasked: string;
  defaultProject: string;
  orgPatterns?: string[];
  isDefault?: boolean;
  hasToken: boolean;
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
  activeProfileId?: string;
  activeProfileName?: string;
  matchMode?: string;
  matchedRule?: string;
  bindingMode?: string;
  detectedOrg?: string;
  suggestedPatterns?: string[];
  profiles?: JiraProfileSummary[];
}

export interface OrgIdentity {
  workspace: string;
  gitRemoteUrl?: string;
  gitHost?: string;
  gitOrg?: string;
  gitRepo?: string;
  workspaceLeaf?: string;
  workspaceParent?: string;
  suggestedPatterns?: string[];
}

export interface ProviderProfileSummary {
  id: string;
  provider: string;
  name: string;
  authMode?: string;
  command?: string;
  envMasked?: Record<string, string>;
  configValues?: Record<string, string>;
  orgPatterns?: string[];
  isDefault?: boolean;
  ready: boolean;
  readyDetail?: string;
}

export interface SaveProfileRequest {
  id: string;
  provider: string;
  name: string;
  authMode?: string;
  command?: string;
  env?: Record<string, string>;
  orgPatterns?: string[];
  isDefault?: boolean;
  bindWorkspace?: string;
}

export interface ResolvedProviderInfo {
  provider: string;
  profileId?: string;
  profileName?: string;
  matchMode: string;
  matchedRule?: string;
  bindingMode?: string;
}

export interface ProfilesOverview {
  identity: OrgIdentity;
  profiles: ProviderProfileSummary[];
  resolved: Record<string, ResolvedProviderInfo>;
}

export interface ProviderScanAccount {
  id?: string;
  name: string;
  source: "pool" | "global" | "env" | "cli" | string;
  active: boolean;
  ready: boolean;
  maskedHint?: string;
}

export interface ProviderScanItem {
  id: string;
  name: string;
  found: boolean;
  connected: boolean;
  statusLabel: string;
  source?: string;
  path?: string;
  detail: string;
  modelCount: number;
  modelsSample?: string[];
  accounts: ProviderScanAccount[];
}

export interface ProvidersScanReport {
  scannedAt: string;
  totalProviders: number;
  connectedProviders: number;
  totalAccounts: number;
  providers: ProviderScanItem[];
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

export interface ProjectSubtask {
  key: string;
  summary: string;
  status?: string;
  done: boolean;
  url?: string;
}

export interface ProjectIssue {
  source: string;
  key: string;
  title: string;
  body?: string;
  status?: string;
  issueType?: string;
  isSubtask?: boolean;
  parentKey?: string;
  parentTitle?: string;
  parentUrl?: string;
  subtaskTotal?: number;
  subtaskDone?: number;
  subtaskPending?: number;
  subtasks?: ProjectSubtask[];
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
  mentionedMe?: boolean;
  lastActivityAt?: string;
  lastActivityAuthor?: string;
  lastActivityKind?: string;
  lastActivitySnippet?: string;
}

export interface GitHubNotificationEvent {
  kind: "mention" | "my_pr_comment" | "my_pr_changes" | "my_pr_approved" | "review_requested" | "pr_reply" | "pr_commit" | string;
  prNumber: number;
  title: string;
  author?: string;
  reason?: string;
  snippet?: string;
  url?: string;
  isMine?: boolean;
}

export interface GitHubSyncEvent {
  workspace: string;
  prs: ProjectPullRequest[];
  events?: GitHubNotificationEvent[];
  syncedAt: string;
}

export type GitSubTab = "changes" | "prs_issues" | "flow" | "worktrees";

export interface WorktreeInfo {
  path: string;
  branch?: string;
  head?: string;
  agent?: string;
  sizeBytes: number;
  isMain?: boolean;
  detached?: boolean;
  prunable?: boolean;
  orphan?: boolean;
  locked?: boolean;
}

export interface WorktreeCheckpoint {
  id: string;
  ref: string;
  sha: string;
  label?: string;
  hasDiff?: boolean;
  createdAt: string;
}

export interface WorktreePruneResult {
  prunedRefs: number;
  removedOrphans?: string[];
}

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
  costMicroUsd?: number;
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
  shellUnsandboxed?: boolean;
  jiraEnabled: boolean;
  jiraProject: string;
  baseBranch?: string;
  homologBranch?: string;
  maxTokens: number;
  skillAssignments?: SkillAssignment[];
}

export type SidebarView = "explorer" | "search" | "team" | "git" | "sessions" | "skills" | "mcp";

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
  splitLaneIds?: string[];
  runMode: RunMode;
  diffs: FileDiff[];
  gitRepos?: WorkspaceGitRepo[];
  activeGitRepoRoot?: string;
  collapsedGitRepos?: Record<string, boolean>;
  commitMsgByRepo?: Record<string, string>;
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
    repoRoot?: string;
  };
  gitTab: GitSubTab;
  autoReview?: AutoReviewStatus | null;
  githubUnreadCount?: number;
  githubLastSyncAt?: string;
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
  lspStatus?: LSPStatus | null;
  lspDiagnostics?: Record<string, ProblemItem[]>;
  buildProblems?: ProblemItem[];
}

export interface SkillInfo {
  name: string;
  description: string;
  source: "native" | "project";
  path: string;
  updatedAt: string;
  sizeBytes: number;
  enabled?: boolean;
}

export interface MCPServerInfo {
  name: string;
  description: string;
  transport: string;
  enabled: boolean;
  builtin: boolean;
  status: string;
  detail?: string;
  tools?: string[];
}

export interface UsageWindow {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
  calls: number;
  oldestAt?: string;
  costMicroUsd?: number;
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

export interface AgyAccountQuota {
  profileId: string;
  name: string;
  email?: string;
  quota: AgyQuota;
}

export interface RoleUsageCost {
  role: string;
  calls: number;
  totalTokens: number;
  costMicroUsd: number;
}

export interface AgentUsageCost {
  agent: string;
  role?: string;
  provider?: string;
  model?: string;
  calls: number;
  totalTokens: number;
  costMicroUsd: number;
}

export interface AIUsageReport {
  generatedAt: string;
  agyQuota?: AgyQuota;
  agyAccounts?: AgyAccountQuota[];
  openCodeQuota: OpenCodeQuota;
  providers: AIProviderUsage[];
  costSummary5hMicroUsd?: number;
  costSummaryWeeklyMicroUsd?: number;
  costSummaryMonthlyMicroUsd?: number;
  costByRole?: RoleUsageCost[];
  costByAgent?: AgentUsageCost[];
}

export interface InlineAIStatus {
  ghostAvailable: boolean;
  ghostProvider?: string;
  ghostModel?: string;
  ghostReason?: string;
  editAvailable: boolean;
  editProvider?: string;
  editModel?: string;
}

export interface InlineCompleteResponse {
  completion: string;
  model?: string;
  provider?: string;
  inputTokens?: number;
  outputTokens?: number;
}

export interface InlineEditResponse {
  replacement: string;
  diffLines: DiffLine[];
  added: number;
  removed: number;
  model?: string;
  provider?: string;
  inputTokens?: number;
  outputTokens?: number;
}

export interface ProblemItem {
  source: string;
  severity: "error" | "warning" | "info" | string;
  file: string;
  line: number;
  col: number;
  endLine?: number;
  endCol?: number;
  code?: string;
  message: string;
}

export interface ProblemsCheckResult {
  command: string;
  exitCode: number;
  durationMs: number;
  output: string;
  problems: ProblemItem[];
}

export interface LSPStatus {
  available: boolean;
  running: boolean;
  server: string;
  binary?: string;
  root?: string;
  error?: string;
}

export interface LSPDiagnosticsEvent {
  workspace: string;
  relPath: string;
  diagnostics: ProblemItem[];
}

export interface LSPLocation {
  relPath: string;
  absPath: string;
  line: number;
  col: number;
  endLine?: number;
  endCol?: number;
}

export interface LSPHover {
  contents: string;
  line?: number;
  col?: number;
  endLine?: number;
  endCol?: number;
}

export interface PTYEvent {
  termId: string;
  workspace: string;
  data?: string;
  exited?: boolean;
  exitCode?: number;
}

export interface DesignModePage {
  index: number;
  title?: string;
  url: string;
}

export interface DesignModeElement {
  url?: string;
  selector: string;
  tag?: string;
  id?: string;
  classes?: string[];
  textSnippet?: string;
  outerHtml?: string;
  computedStyles?: Record<string, string>;
  boundingBox?: Record<string, number>;
}

export type BottomDrawerTab = "terminal" | "problems";

export interface MobileBridgeStatus {
  enabled: boolean;
  running: boolean;
  port: number;
  urls: string[];
  host: string;
  version: string;
}

export interface MobilePairing {
  uri: string;
  urls: string[];
  host: string;
  qrDataUrl: string;
  exp: number;
}

export interface RelayStatus {
  enabled: boolean;
  loggedIn: boolean;
  email: string;
  role?: string;
  baseUrl: string;
  deviceId: string;
  deviceName: string;
  linked: boolean;
  online: boolean;
  lastError: string;
  e2e: boolean;
}

export interface RelayLinkCode {
  code: string;
  expiresAt: string;
  uri: string;
  qrDataUrl: string;
}

export interface JevPolicy {
  version: number;
  enabled: boolean;
  classifyEnabled?: boolean;
  weights: { quality: number; cost: number; latency: number };
  autoEscalateUsd: number;
  maxEscalateRatio: number;
  dailyUsd?: Record<string, number>;
  allowDataClass?: string[];
}

export interface ProviderHealthView {
  provider: string;
  state: "closed" | "open" | "half_open" | string;
  reason?: string;
  until?: string;
  dailySpentMicroUsd?: number;
  dailyCapMicroUsd?: number;
  quotaRemainingPct?: number;
  quotaStale?: boolean;
  hasCredential: boolean;
}

export interface ExternalSession {
  id: string;
  source: string;
  state: "running" | "completed" | "failed" | "unknown" | string;
  pid: number;
  createMs: number;
  title?: string;
  actions: string[];
}

export interface ResidentStatus {
  state: string;
  enabled?: boolean;
  orbEnabled?: boolean;
  jevFailover?: boolean;
  readOnly: boolean;
  accountId: string;
  workstream: string;
  provider: string;
  model: string;
  memoryDir?: string;
  auditPath?: string;
  lastError?: string;
  sandboxAvailable?: boolean;
  sandboxMechanism?: string;
  sandboxReason?: string;
  vaultAvailable?: boolean;
  vaultReason?: string;
  allowedBinaries?: string[];
  allowedHosts?: string[];
  vaultSecrets?: string[];
  eventTriggers?: boolean;
  experimentalUnisolated?: boolean;
  isolatedSandbox?: boolean;
  experimentalWarning?: string;
  isolatedBrowser?: boolean;
  isolatedBrowserReason?: string;
  isolatedDesktop?: boolean;
  isolatedDesktopReason?: string;
}

export interface ResidentTurnResult {
  sessionId: string;
  workstream: string;
  finalText: string;
  provider: string;
  model: string;
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
  toolCalls: number;
  promptBuildMs: number;
}

export interface ResidentMemoryFact {
  id: string;
  workstream: string;
  text: string;
  pinned?: boolean;
  source?: string;
  createdAt: string;
}

export interface ResidentAuditEntry {
  ts: string;
  phase: string;
  accountId: string;
  deviceId?: string;
  origin: string;
  workstream?: string;
  tool: string;
  tier: string;
  decision: string;
  approvalId?: string;
  sandbox?: string;
  targetClass?: string;
  targetHash?: string;
  outcome?: string;
  errorClass?: string;
  reason?: string;
}

export interface ResidentLocalMessage {
  role: "user" | "model";
  text: string;
  at: string;
}

export interface WailsBackendApp {
  GetInitialState(): Promise<InitialStatePayload>;
  BootstrapAgy(force: boolean): Promise<{ status: AgyStatus; models: AgyModel[] }>;
  BootstrapOpenCode(force: boolean): Promise<{ status: OpenCodeStatus; goModels?: AgyModel[]; models: AgyModel[] }>;
  SaveOpenCodeConfig(apiKey: string, baseURL: string): Promise<{ status: OpenCodeStatus; goModels?: AgyModel[]; models: AgyModel[] }>;
  ChooseDirectory(): Promise<string>;
  OpenProject(dir: string): Promise<ProjectInfo>;
  CloseProject?(workspace: string): Promise<boolean>;
  AppVersion?(): Promise<string>;
  CheckForUpdates?(): Promise<UpdateCheckResult>;
  DownloadUpdate?(versionStr: string, artifactName: string, downloadURL: string, sha256Hex: string, size: number): Promise<string>;
  GetRecentProjects(): Promise<string[]>;
  ListFiles(workspace: string): Promise<FileNode[]>;
  ListDirectory(workspace: string, relDir: string): Promise<FileNode[]>;
  SearchFiles(workspace: string, query: string): Promise<string[]>;
  ReadFile(workspace: string, relOrAbs: string): Promise<string>;
  SaveFile(workspace: string, relOrAbs: string, content: string): Promise<void>;
  ListModels(): Promise<AgyModel[]>;
  DoctorAgy(): Promise<AgyStatus>;
  ScanAllProviders?(workspace: string): Promise<ProvidersScanReport>;
  SaveTeamWizard(workspace: string, input: WizardInputPayload): Promise<TeamFile>;
  SaveSkillAssignments(workspace: string, assignments: SkillAssignment[]): Promise<TeamFile>;
  SetActiveTeam(workspace: string, teamName: string): Promise<TeamFile>;
  DeleteTeam(workspace: string, teamName: string): Promise<TeamFile>;
  UpdateAgentModel(workspace: string, teamName: string, agentName: string, modelSpec: string): Promise<TeamFile>;
  UpdatePermissionMode(workspace: string, permissionMode: string): Promise<TeamFile>;
  UpdateProjectJira?(workspace: string, jiraProjectKey: string): Promise<TeamFile>;
  RespondApproval(approvalID: string, approved: boolean): Promise<boolean>;
  EnsureTeamMemory(workspace: string): Promise<string>;
  GetDiff(workspace: string): Promise<FileDiff[]>;
  GetWorkspaceGitRepos?(workspace: string): Promise<WorkspaceGitRepo[]>;
  CommitChanges(workspace: string, message: string, push: boolean): Promise<string>;
  OpenPR(workspace: string, title: string, body: string, baseBranch: string): Promise<string>;
  CommentJira(workspace: string, issueKey: string, comment: string): Promise<void>;
  DiscardChanges(workspace: string): Promise<void>;
  DiscardFile(workspace: string, relPath: string): Promise<void>;
  StageFile(workspace: string, relPath: string, stage: boolean): Promise<void>;
  RunGitAction(workspace: string, action: string): Promise<string>;
  RunGitActionTimeout?(workspace: string, action: string, timeoutSec: number): Promise<string>;
  CancelGitAction?(workspace: string): Promise<boolean>;
  ListGitGraph(workspace: string, limit: number, allBranches: boolean): Promise<GitGraphCommit[]>;
  GetCommitDetail(workspace: string, hash: string): Promise<CommitDetail>;
  RevertHunk(workspace: string, relPath: string, hunkHeader: string, hunkLines: string[]): Promise<void>;
  StageHunk?(workspace: string, relPath: string, hunkHeader: string, hunkLines: string[]): Promise<void>;
  ApplyHunk?(workspace: string, relPath: string, hunkHeader: string, hunkLines: string[]): Promise<void>;
  GetFileUnstagedDiff?(workspace: string, relPath: string): Promise<FileDiff | null>;
  GetInlineAIStatus?(workspace: string): Promise<InlineAIStatus>;
  InlineComplete?(workspace: string, relPath: string, prefix: string, suffix: string, model: string): Promise<InlineCompleteResponse>;
  InlineEdit?(workspace: string, relPath: string, instruction: string, selectedText: string, startLine: number, endLine: number, beforeCtx: string, afterCtx: string, model: string): Promise<InlineEditResponse>;
  CancelInlineAI?(): Promise<void>;
  GetAutoReviewStatus?(workspace: string): Promise<AutoReviewStatus>;
  SetAutoReviewConfig?(workspace: string, cfg: AutoReviewSettings): Promise<AutoReviewStatus>;
  TriggerAutoReviewPoll?(workspace: string): Promise<AutoReviewStatus>;
  StopActiveAutoReview?(workspace: string): Promise<boolean>;
  GitHubStatus(workspace: string): Promise<GitHubStatus>;
  SaveGitHubConfig?(token: string, baseURL: string, workspace: string): Promise<GitHubStatus>;
  JiraStatus?(): Promise<JiraConnectorStatus>;
  JiraStatusForWorkspace?(workspace: string): Promise<JiraConnectorStatus>;
  ListJiraProjects?(): Promise<JiraProject[]>;
  ListJiraProjectsForWorkspace?(workspace: string): Promise<JiraProject[]>;
  ImportJiraFromMCP?(workspace: string): Promise<JiraConnectorStatus>;
  SaveJiraConfig?(baseURL: string, email: string, token: string, defaultProject: string, clear: boolean): Promise<JiraConnectorStatus>;
  SaveJiraProfile?(profile: JiraProfile, bindWorkspace: string): Promise<JiraConnectorStatus>;
  DeleteJiraProfile?(profileID: string, workspace: string): Promise<JiraConnectorStatus>;
  BindJiraWorkspace?(workspace: string, target: string): Promise<JiraConnectorStatus>;
  GetProviderProfilesOverview?(workspace: string): Promise<ProfilesOverview>;
  SaveProviderProfile?(req: SaveProfileRequest): Promise<ProfilesOverview>;
  DeleteProviderProfile?(profileID: string, workspace: string): Promise<ProfilesOverview>;
  BindProviderWorkspace?(workspace: string, provider: string, profileID: string): Promise<ProfilesOverview>;
  LoginAgyProfile?(profileID: string): Promise<string>;
  ConnectNewAgyGoogleAccount?(workspace: string): Promise<ProfilesOverview>;
  SubmitAgyOAuthCode?(code: string, workspace: string): Promise<ProfilesOverview>;
  CancelAgyOAuthLogin?(workspace: string): Promise<ProfilesOverview>;
  ActivateAgyAccount?(profileID: string, workspace: string): Promise<ProfilesOverview>;
  MobileBridgeStatus?(): Promise<MobileBridgeStatus>;
  SetMobileBridgeEnabled?(on: boolean): Promise<MobileBridgeStatus>;
  RotateMobileToken?(): Promise<MobileBridgeStatus>;
  MobilePairing?(): Promise<MobilePairing>;
  RelayStatus?(): Promise<RelayStatus>;
  RelayLogin?(baseURL: string, email: string, password: string): Promise<RelayStatus>;
  RelayChangePassword?(current: string, next: string): Promise<RelayStatus>;
  RelayCreateAccount?(email: string, password: string): Promise<void>;
  RelayLogout?(): Promise<RelayStatus>;
  RelaySetEnabled?(on: boolean): Promise<RelayStatus>;
  RelayCreateQRLogin?(): Promise<RelayLinkCode>;
  RelayCreateLinkCode?(): Promise<RelayLinkCode>;
  RelayClaimLinkCode?(code: string): Promise<RelayStatus>;
  RelayUnlink?(): Promise<RelayStatus>;
  InitGit(workspace: string): Promise<void>;
  WindowMinimize(): Promise<void>;
  WindowToggleMaximize(): Promise<void>;
  WindowClose(): Promise<void>;
  ShowDesktopNotification?(id: string, title: string, body: string, sound: boolean): Promise<void>;
  PlayAlertSound?(mood: string): Promise<boolean>;
  StartRun(laneID: string, workspace: string, teamName: string, objective: string, jiraKey: string, sessionID: string): Promise<RunResultPayload>;
  StartRunWithImages(laneID: string, workspace: string, teamName: string, objective: string, jiraKey: string, sessionID: string, images: Array<{ name: string; mimeType: string; dataBase64: string }>): Promise<RunResultPayload>;
  StartRunWithOptions(laneID: string, workspace: string, teamName: string, objective: string, jiraKey: string, sessionID: string, runMode: string, images: Array<{ name: string; mimeType: string; dataBase64: string }>): Promise<RunResultPayload>;
  CancelRun(laneID: string): Promise<boolean>;
  RunCommand(workspace: string, command: string): Promise<{ command: string; output: string; exitCode: number; durationMs: number }>;
  CancelCommand(workspace: string): Promise<boolean>;
  StartPTY?(termID: string, workspace: string, cols: number, rows: number): Promise<string>;
  WritePTY?(termID: string, data: string): Promise<void>;
  ResizePTY?(termID: string, cols: number, rows: number): Promise<void>;
  RestartPTY?(termID: string, workspace: string, cols: number, rows: number): Promise<void>;
  ClosePTY?(termID: string): Promise<void>;
  LSPStatus?(workspace: string): Promise<LSPStatus>;
  LSPDidOpen?(workspace: string, relPath: string, text: string): Promise<void>;
  LSPDidChange?(workspace: string, relPath: string, text: string): Promise<void>;
  LSPDidSave?(workspace: string, relPath: string, text: string): Promise<void>;
  LSPDidClose?(workspace: string, relPath: string): Promise<void>;
  LSPDefinition?(workspace: string, relPath: string, line: number, col: number): Promise<LSPLocation[]>;
  LSPHover?(workspace: string, relPath: string, line: number, col: number): Promise<LSPHover>;
  CheckWorkspaceProblems?(workspace: string, customCmd: string): Promise<ProblemsCheckResult>;
  ParseOutputProblems?(workspace: string, rawOutput: string): Promise<ProblemItem[]>;
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
  ListWorktrees?(workspace: string): Promise<WorktreeInfo[]>;
  CreateWorktreeAsync?(workspace: string, agent: string, branch: string): Promise<string>;
  ArchiveWorktree?(workspace: string, wtPath: string): Promise<void>;
  PruneWorktrees?(workspace: string): Promise<WorktreePruneResult>;
  ListWorktreeCheckpoints?(workspace: string): Promise<WorktreeCheckpoint[]>;
  RollbackWorktreeCheckpoint?(workspace: string, wtDir: string, checkpointID: string): Promise<void>;
  ListIssues(workspace: string): Promise<ProjectIssue[]>;
  ListPullRequests(workspace: string): Promise<ProjectPullRequest[]>;
  CloseOwnGitHubItem(workspace: string, kind: "pr" | "issue", number: number): Promise<void>;
  GitBlameFile?(workspace: string, relPath: string): Promise<BlameLine[]>;
  GitFileHistory?(workspace: string, relPath: string, limit: number): Promise<FileCommitEntry[]>;
  ListConflictedFiles?(workspace: string): Promise<ConflictedFile[]>;
  ResolveConflictBlock?(workspace: string, relPath: string, blockIndex: number, choice: "ours" | "theirs" | "both"): Promise<ConflictedFile>;
  MarkConflictResolved?(workspace: string, relPath: string): Promise<void>;
  OpenExternalURL(rawURL: string): Promise<void>;
  GetAIUsage(): Promise<AIUsageReport>;
  SaveAIUsageLimits(provider: string, fiveHourTokens: number, weeklyTokens: number): Promise<AIUsageReport>;
  GetProviderHealth?(): Promise<ProviderHealthView[]>;
  ProbeProviders?(): Promise<ProviderHealthView[]>;
  GetJevPolicy?(): Promise<JevPolicy>;
  SaveJevPolicy?(policy: JevPolicy): Promise<JevPolicy>;
  GetAPIKeysStatus?(): Promise<Record<string, string>>;
  SaveAPIKeys?(entries: Record<string, string>): Promise<Record<string, string>>;
  ListOpenRouterModels?(): Promise<OpenRouterModelInfo[]>;
  ListCursorModels?(): Promise<AgyModel[]>;
  CursorCLIStatus?(): Promise<CursorCLIStatus>;
  ListSkills(workspace: string): Promise<SkillInfo[]>;
  SetNativeSkillEnabled?(name: string, enabled: boolean): Promise<void>;
  ListMCPServers?(workspace: string): Promise<MCPServerInfo[]>;
  SetMCPServerEnabled?(workspace: string, name: string, enabled: boolean, projectOnly: boolean): Promise<MCPServerInfo[]>;
  ReadNativeSkill(name: string): Promise<string>;
  SaveNativeSkill(name: string, body: string): Promise<void>;
  DeleteNativeSkill(name: string): Promise<void>;
  InstallSkillToProject(workspace: string, name: string): Promise<void>;
  RemoveProjectSkill(workspace: string, name: string): Promise<void>;
  ExternalSessions?(): Promise<ExternalSession[]>;
  ResidentStatus?(): Promise<ResidentStatus>;
  ResidentSaveConfig?(provider: string, model: string, workstream: string): Promise<ResidentStatus>;
  ResidentSetEnabled?(on: boolean): Promise<ResidentStatus>;
  ResidentSetOrbEnabled?(on: boolean): Promise<ResidentStatus>;
  ResidentSetJevFailover?(on: boolean): Promise<ResidentStatus>;
  ResidentSetEventTriggers?(on: boolean): Promise<ResidentStatus>;
  ResidentSetExperimentalUnisolated?(on: boolean): Promise<ResidentStatus>;
  ResidentSetIsolatedSandbox?(on: boolean): Promise<ResidentStatus>;
  ResidentSavePolicy?(allowedBinaries: string[], allowedHosts: string[]): Promise<ResidentStatus>;
  ResidentSetSecret?(name: string, value: string): Promise<ResidentStatus>;
  ResidentDeleteSecret?(name: string): Promise<ResidentStatus>;
  ResidentStartTurn?(workspace: string, workstream: string, message: string): Promise<ResidentTurnResult>;
  ResidentCancel?(): Promise<boolean>;
  ResidentSessionMessages?(workstream: string): Promise<ResidentLocalMessage[]>;
  ResidentClearSession?(workstream: string): Promise<void>;
  ResidentGetSoul?(): Promise<string>;
  ResidentSaveSoul?(content: string): Promise<void>;
  ResidentListMemory?(workstream: string): Promise<ResidentMemoryFact[]>;
  ResidentAddMemory?(workstream: string, text: string, pinned: boolean): Promise<ResidentMemoryFact>;
  ResidentDeleteMemory?(id: string): Promise<boolean>;
  ResidentAuditLog?(limit: number): Promise<ResidentAuditEntry[]>;
  ResidentListRoutines?(): Promise<ResidentRoutine[]>;
  ResidentSaveRoutine?(expr: string, timezone: string, text: string, workstream: string, classes: string[]): Promise<ResidentRoutine>;
  ResidentSetRoutineEnabled?(id: string, enabled: boolean): Promise<void>;
  ResidentDeleteRoutine?(id: string): Promise<void>;
  ResidentReviewSkill?(name: string): Promise<string>;
  OrbShow?(): Promise<void>;
  OrbHide?(): Promise<void>;
  OrbRunning?(): Promise<boolean>;
  OrbSetMood?(mood: string): Promise<void>;
  OrbPushNotice?(
    id: string,
    title: string,
    body: string,
    mood: string,
    surface: string,
    projectId: string,
    laneId: string,
    messageId: string
  ): Promise<void>;
  OrbDismissNotice?(id: string): Promise<void>;
  StartLanePTY?(termID: string, workspace: string, sessionID: string, cols: number, rows: number): Promise<string>;
  DesignModeListPages?(workspace: string): Promise<DesignModePage[]>;
  DesignModePickElement?(workspace: string, pageIdx: number, selector: string): Promise<DesignModeElement>;
  QuitAll?(): Promise<void>;
}

export interface ResidentRoutine {
  id: string;
  expr: string;
  timezone?: string;
  text: string;
  workstream?: string;
  classes?: string[];
  enabled: boolean;
}

export interface UpdateCheckResult {
  available: boolean;
  currentVersion: string;
  latestVersion: string;
  commit?: string;
  buildDate?: string;
  artifactName?: string;
  downloadUrl?: string;
  sha256?: string;
  size?: number;
}

export interface BlameLine {
  line: number;
  commit: string;
  short: string;
  author: string;
  timestamp: string;
  summary: string;
  text: string;
}

export interface FileCommitEntry {
  hash: string;
  short: string;
  author: string;
  timestamp: string;
  subject: string;
}

export interface ConflictBlock {
  index: number;
  startLine: number;
  endLine: number;
  oursLabel: string;
  theirsLabel: string;
  ours: string;
  base?: string;
  theirs: string;
}

export interface ConflictedFile {
  path: string;
  blocks: ConflictBlock[];
}
