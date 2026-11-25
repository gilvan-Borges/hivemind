import "@fontsource-variable/material-symbols-outlined";
import "./styles.css";
import { renderRightPane, renderTokenBar } from "./chat";
import { renderEditorBody } from "./editor";
import { renderLeftSidebar } from "./explorer";
import { createLane, renderPipelineBar } from "./lanes";
import { openIdeSettingsTab } from "./ide-settings";
import { renderModalLayer } from "./modals";
import { mountShell, renderProjectTabsBar, syncActivityButtons, updateHeaderAndStatusBar } from "./shell";
import { state } from "./state";
import { openMissionControlTab } from "./teamconfig";
import type { FileNode, ProjectIssue, ProjectPullRequest } from "./types";

function node(name: string, relPath: string, isDir: boolean, children?: FileNode[]): FileNode {
  return { name, path: relPath, relPath, isDir, children };
}

function seed() {
  const root = "/tmp/acme-web";
  state.project = {
    root,
    name: "acme-web",
    branch: "feat/layout-d4",
    hasTeam: true,
    stack: {
      root,
      name: "acme-web",
      stacks: ["go", "typescript"],
      buildTools: ["go", "npm"],
      testHint: "go test ./...",
      summary: "Go · TypeScript",
    },
    files: [
      node("src", "src", true, [
        node("main.go", "src/main.go", false),
        node("app.go", "src/app.go", false),
        node("review.go", "src/review.go", false),
      ]),
      node("ide", "ide", true, [node("frontend", "ide/frontend", true, [node("styles.css", "ide/frontend/styles.css", false)])]),
      node("README.md", "README.md", false),
    ],
    teamConfig: {
      version: 1,
      active: "principal",
      settings: {
        permission_mode: "full_access",
        jira_enabled: true,
        jira_project: "ACME",
        auto_commit: false,
        auto_pr: false,
        base_branch: "main",
        max_tokens: 200000,
      },
      teams: [
        {
          name: "principal",
          agents: [
            { name: "chefe", role: "architect", provider: "cursor", model: "composer" },
            { name: "dev", role: "developer", provider: "cursor", model: "composer" },
            { name: "qa", role: "qa", provider: "cursor", model: "composer" },
            { name: "fecho", role: "Fecho", provider: "cursor", model: "composer" },
          ],
        },
      ],
    },
  };
  state.expandedDirs = new Set(["src", "ide", "ide/frontend"]);
  state.gitRepos = [
    {
      root,
      name: "acme-web",
      relPath: "",
      branch: "feat/layout-d4",
      isRoot: true,
      diffs: [{ path: "src/app.go", status: "M", added: 12, removed: 3, lines: [] }],
    },
  ];
  state.activeGitRepoRoot = root;
  state.ghStatus = {
    loggedIn: true,
    user: "ana",
    detail: "Conector de exemplo",
    remote: "https://github.com/acme/acme-web",
    connectorLabel: "API",
  } as typeof state.ghStatus;
  state.jiraStatus = {
    configured: true,
    connected: true,
    source: "config",
    baseUrl: "https://acme.atlassian.net",
    email: "ana@example.com",
    tokenMasked: "•••",
    defaultProject: "ACME",
    user: "ana",
    detail: "Conectado como ana",
    activeProfileName: "Acme",
    bindingMode: "",
    detectedOrg: "acme",
    profiles: [{ id: "acme", name: "Acme", baseUrl: "https://acme.atlassian.net", email: "ana@example.com", tokenMasked: "•••", hasToken: true, defaultProject: "ACME", orgPatterns: ["acme"], isDefault: true }],
  };
  state.jiraProjects = [{ key: "ACME", name: "Acme Web" }];
  state.runMode = "auto";
  const lane = createLane("principal");
  lane.running = true;
  lane.stage = "dev";
  lane.doneStages = ["chief"];
  lane.sessionTotalTokens = 18420;
  lane.turnTotalTokens = 2360;
  lane.turnInTokens = 1800;
  lane.turnOutTokens = 560;
  lane.sessionInTokens = 14000;
  lane.sessionOutTokens = 4420;
  lane.messages = [
    { id: "u1", kind: "user", label: "Você", text: "Deixa o layout da IDE mais arejado, sem esconder função. PRs, Jira e Fluxo no centro." },
    { id: "c1", kind: "chief", label: "chefe", text: "A trilha segue no topo. O chat fica só com a conversa e o composer. GitHub, Jira e Fluxo abrem no centro." },
    { id: "d1", kind: "chief", label: "dev", text: "Cards de PR com detalhes, +/− e comentários. Tasks do Jira com subtasks e Resolver c/ Equipe." },
  ];
  state.lanes = [lane];
  state.activeLaneId = lane.id;
  state.sessionsSummary.projectTotalTokens = 18420;
  state.projectTabs = [
    { id: "p1", project: state.project, lanes: state.lanes, editorTabs: [] },
    {
      id: "p2",
      project: { ...state.project, name: "app-mobile", branch: "main", root: "/tmp/app-mobile" },
      lanes: [],
      editorTabs: [],
    },
  ] as unknown as typeof state.projectTabs;
  state.activeProjectId = "p1";

  const pr = (over: Partial<ProjectPullRequest>): ProjectPullRequest => ({
    number: 1,
    title: "PR de exemplo",
    url: "https://example.com/acme/acme-web/pull/1",
    state: "open",
    isDraft: false,
    author: "dev-beta",
    headRef: "feat/exemplo",
    baseRef: "main",
    ...over,
  });
  state.prs = {
    loadedOnce: true,
    loading: false,
    list: [
      pr({
        number: 42,
        title: "Deixa a revisão do fluxo mais fácil de ler",
        author: "dev-beta",
        needsAttention: true,
        reviewRequested: true,
        attentionReason: "Sua revisão",
        lastActivityAuthor: "ana",
        lastActivitySnippet: "Pode extrair esse helper para o pacote core?",
        headRef: "feat/fluxo",
        baseRef: "main",
      }),
      pr({
        number: 38,
        title: "Ajusta o composer da equipe",
        isMine: true,
        needsAttention: true,
        attentionReason: "Ajustes",
        author: "ana",
        headRef: "feat/composer",
      }),
      pr({
        number: 31,
        title: "Comentários no conector de exemplo",
        needsAttention: true,
        myResponse: "commented",
        attentionReason: "Nova atividade",
        author: "dev-beta",
        lastActivitySnippet: "Falta cobrir o estado vazio.",
      }),
      pr({
        number: 27,
        title: "Documenta o atalho da paleta",
        isMine: true,
        author: "ana",
        myResponse: "commented",
        reviewDecision: "REVIEW_REQUIRED",
      }),
    ],
  };
  state.prDetails[42] = {
    open: true,
    loading: false,
    error: "",
    data: {
      number: 42,
      changedFiles: 4,
      additions: 52,
      deletions: 11,
      totalEvents: 2,
      events: [
        { kind: "review_comment", author: "reviewer", body: "Favor tratar o retorno vazio antes de seguir.", path: "src/review.go", line: 27 },
        { kind: "comment", author: "ana", body: "Concordo. Vou responder com o ajuste no helper." },
      ],
    },
  };
  state.showFlowResponded = true;
  state.autoReview = {
    workspace: root,
    enabled: true,
    state: "idle",
    queueCount: 2,
    reviewedLastHour: 1,
    config: { enabled: true, poll_interval_seconds: 120, run_tests: true, review_action: "full_review", max_prs_per_hour: 6 },
    history: [
      { prNumber: 19, title: "Revisão do README de exemplo", status: "reviewed", summary: "ok", tokens: 900, reviewedAt: "2026-11-25T10:00:00Z" },
    ],
  };
  const issue = (over: Partial<ProjectIssue>): ProjectIssue => ({
    source: "jira",
    key: "ACME-1",
    title: "Task de exemplo",
    ...over,
  });
  state.issues = {
    loadedOnce: true,
    loading: false,
    list: [
      issue({
        key: "ACME-120",
        title: "Unificar o conector e as tasks na mesma área",
        status: "Em andamento",
        subtaskTotal: 3,
        subtaskDone: 1,
        subtaskPending: 2,
        subtasks: [
          { key: "ACME-121", summary: "Faixa do conector", status: "Concluído", done: true },
          { key: "ACME-122", summary: "Cards com progresso", status: "A fazer", done: false },
          { key: "ACME-123", summary: "Resolver c/ Equipe", status: "A fazer", done: false },
        ],
      }),
      issue({ key: "ACME-118", title: "Estado vazio das tasks sem ruído", status: "A fazer", subtaskTotal: 0 }),
      issue({ source: "github", key: "#14", title: "Issue de exemplo no GitHub", status: "open", isMine: true, url: "https://example.com/acme/acme-web/issues/14" }),
    ],
  };
  state.expandedIssueSubtasks.add("ACME-120");
  state.goVersion = "go1.22";
  state.os = "linux";
  state.statusText = "Pronto";
}

async function show(screen: string) {
  seed();
  mountShell();
  const modern = Boolean(document.getElementById("center-stage"));
  if (screen === "github") state.gitTab = "prs_issues";
  if (screen === "jira") state.gitTab = modern ? "jira" : "prs_issues";
  if (screen === "fluxo") state.gitTab = "flow";
  renderProjectTabsBar();
  updateHeaderAndStatusBar();
  syncActivityButtons();
  renderPipelineBar();
  renderTokenBar();
  if (screen === "github" || screen === "jira" || screen === "fluxo") {
    state.sidebarView = "git";
    state.showLeftSidebar = true;
    renderLeftSidebar();
  } else {
    renderLeftSidebar();
    renderEditorBody();
  }
  renderRightPane();
  if (screen === "quadro") openMissionControlTab();
  if (screen === "configuracoes") await openIdeSettingsTab("general");
  if (screen === "jira" && !modern) {
    state.showJiraModal = true;
    renderModalLayer();
  }
  await document.fonts.ready;
  document.documentElement.dataset.previewReady = screen;
}

const screen = new URLSearchParams(location.search).get("screen") || "principal";
void show(screen);
