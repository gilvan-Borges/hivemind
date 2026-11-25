// Dados sintéticos só para captura do layout (?layout=d4). Não roda com o runtime Wails.
import type { FileNode, ProjectInfo, TeamFile } from "./types";
import { applyProject } from "./boot";
import { updateGitActivityBadge } from "./git";
import { renderEditorBody, renderEditorTabs } from "./editor";
import { renderLeftSidebar } from "./explorer";
import { renderRightPane } from "./chat";
import { renderProjectTabsBar, updateHeaderAndStatusBar } from "./shell";
import { state } from "./state";

const team: TeamFile = {
  version: 1,
  active: "principal",
  settings: {
    permission_mode: "full_access",
    jira_enabled: true,
    jira_project: "PROJ",
    auto_commit: false,
    auto_pr: false,
    base_branch: "main",
    max_tokens: 200000,
  },
  teams: [
    {
      name: "principal",
      agents: [
        { name: "Chefe", role: "chief", provider: "acme", model: "acme-chief" },
        { name: "Dev", role: "dev", provider: "acme", model: "acme-dev" },
        { name: "QA", role: "qa", provider: "acme", model: "acme-qa" },
      ],
    },
  ],
};

function file(name: string, rel: string): FileNode {
  return { name, path: rel, relPath: rel, isDir: false };
}

function dir(name: string, rel: string, children: FileNode[]): FileNode {
  return { name, path: rel, relPath: rel, isDir: true, hasChildren: true, children };
}

function makeProject(name: string, branch: string): ProjectInfo {
  const root = `/workspace/acme/${name}`;
  return {
    root,
    name,
    branch,
    stack: {
      root,
      name,
      stacks: ["go", "typescript"],
      buildTools: ["go"],
      testHint: "go test ./...",
      summary: "Go · TypeScript",
    },
    hasTeam: true,
    teamConfig: team,
    files: [
      dir("src", "src", [file("main.go", "src/main.go"), file("layout.go", "src/layout.go")]),
      dir("docs", "docs", [file("README.md", "docs/README.md")]),
    ],
  };
}

export function maybeSeedLayoutPreview(): void {
  if (typeof location === "undefined") return;
  if (new URLSearchParams(location.search).get("layout") !== "d4") return;

  applyProject(makeProject("app-mobile", "main"), true);
  applyProject(makeProject("org-alpha", "feat/layout"), true);

  state.expandedDirs.add("src");
  state.expandedDirs.add("docs");
  state.editorTabs = [
    {
      kind: "file",
      relPath: "src/main.go",
      name: "main.go",
      content: "package main\n\nfunc main() {\n\tprintln(\"acme\")\n}\n",
      dirty: true,
    },
  ];
  state.activeEditorPath = "src/main.go";
  state.diffs = [{ path: "src/layout.go", added: 12, removed: 3, lines: [] }];
  state.buildProblems = [
    { source: "go", severity: "error", file: "src/layout.go", line: 12, col: 2, message: "undefined: layout" },
    { source: "go", severity: "warning", file: "src/main.go", line: 4, col: 1, message: "should use fmt" },
  ];
  state.jiraKeyInput = "PROJ-123";
  state.jiraStatus = {
    configured: true,
    connected: true,
    source: "profile",
    baseUrl: "https://acme.example.com",
    email: "conta1@example.com",
    tokenMasked: "sk-test-fake-0000",
    defaultProject: "PROJ",
    user: "conta1",
    detail: "Conectado (exemplo)",
    activeProfileName: "acme",
  };
  state.agyStatus = { Found: true, Path: "", LoggedIn: true, Detail: "exemplo" };
  state.ghostTextEnabled = true;
  state.inlineAIStatus = { ghostAvailable: true, editAvailable: true, ghostProvider: "acme" };
  state.goVersion = "go1.22";
  state.os = "linux";

  const lane = state.lanes[0];
  if (lane) {
    lane.running = true;
    lane.stage = "dev";
    lane.doneStages = ["chief"];
    lane.turnInTokens = 1800;
    lane.turnOutTokens = 510;
    lane.turnTotalTokens = 2310;
    lane.sessionInTokens = 14000;
    lane.sessionOutTokens = 4420;
    lane.sessionTotalTokens = 18420;
    lane.messages = [
      {
        id: "preview-user",
        kind: "user",
        label: "Você",
        text: "Deixa o shell da IDE mais arejado, sem tirar atalho nenhum.",
      },
      {
        id: "preview-chief",
        kind: "chief",
        label: "Chefe",
        text: "O Dev está separando a faixa da equipe do chat. Quadro e tokens ficam numa linha só, e o que saiu da barra de título continua na paleta.",
        modelLabel: "acme-chief",
      },
    ];
  }

  renderProjectTabsBar();
  updateHeaderAndStatusBar();
  updateGitActivityBadge();
  renderLeftSidebar();
  renderEditorTabs();
  renderEditorBody();
  renderRightPane();
}
