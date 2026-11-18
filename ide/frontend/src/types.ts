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

export interface TeamSettings {
  jira_enabled: boolean;
  jira_project?: string;
  worktrees?: boolean;
  auto_commit: boolean;
  auto_pr: boolean;
  base_branch: string;
  max_tokens?: number;
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
  added: number;
  removed: number;
  lines: DiffLine[];
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

export type RunMode = "auto" | "direct" | "plan" | "squad";

export interface ChatMessage {
  id: string;
  kind: "user" | "chief" | "system" | "tool" | "error" | "handoff" | "diff" | "plan";
  label: string;
  text: string;
  active?: boolean;
  approved?: boolean;
  images?: string[];
  diffItems?: DiffSummaryItem[];
  tokens?: number;
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
  kind: "file" | "diff" | "mission";
  relPath: string;
  name: string;
  content: string;
  dirty: boolean;
  diff?: FileDiff;
}

export interface GitHubStatus {
  installed: boolean;
  loggedIn: boolean;
  user: string;
  remote: string;
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
  defaultModel: string;
  agentModels?: Record<string, string>;
  worktrees: boolean;
  autoCommit: boolean;
  autoPR: boolean;
  jiraEnabled: boolean;
  jiraProject: string;
  baseBranch?: string;
  maxTokens: number;
}

export type SidebarView = "explorer" | "search" | "team" | "git" | "sessions";

export interface WailsBackendApp {
  GetInitialState(): Promise<InitialStatePayload>;
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
  SaveTeamWizard(workspace: string, input: WizardInputPayload): Promise<TeamFile>;
  SetActiveTeam(workspace: string, teamName: string): Promise<TeamFile>;
  UpdateAgentModel(workspace: string, teamName: string, agentName: string, modelSpec: string): Promise<TeamFile>;
  EnsureTeamMemory(workspace: string): Promise<string>;
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
  StartRun(laneID: string, workspace: string, teamName: string, objective: string, jiraKey: string, sessionID: string): Promise<RunResultPayload>;
  StartRunWithImages(laneID: string, workspace: string, teamName: string, objective: string, jiraKey: string, sessionID: string, images: Array<{ name: string; mimeType: string; dataBase64: string }>): Promise<RunResultPayload>;
  StartRunWithOptions(laneID: string, workspace: string, teamName: string, objective: string, jiraKey: string, sessionID: string, runMode: string, images: Array<{ name: string; mimeType: string; dataBase64: string }>): Promise<RunResultPayload>;
  CancelRun(laneID: string): Promise<boolean>;
  RunCommand(workspace: string, command: string): Promise<{ command: string; output: string; exitCode: number; durationMs: number }>;
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
}
