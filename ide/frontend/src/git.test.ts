import { beforeEach, describe, expect, it } from "vitest";
import { collectTurnEditedFiles, filterTurnDiffs, formatPrRelativeTime, getActiveGitRepo, getGitStatusMap, pickActiveGitRepo, renderGitGraphSectionHtml, renderGitSidebarHtml, renderPrCardHtml, selectActiveGitRepo } from "./git";
import { state } from "./state";
import type { ChatMessage, ProjectInfo, ProjectPullRequest, WorkspaceGitRepo } from "./types";

function makeProject(root = "c:/hivemind"): ProjectInfo {
  return {
    root,
    name: "hivemind",
    branch: "main",
    stack: { summary: "Go" } as any,
    hasTeam: false,
    teamConfig: {} as any,
    files: [],
  };
}

describe("multi-repo source control", () => {
  beforeEach(() => {
    state.project = makeProject();
    state.gitRepos = [];
    state.activeGitRepoRoot = "";
    state.diffs = [];
    state.collapsedGitRepos = {};
    state.commitMsgByRepo = {};
    state.commitMsgInput = "";
    state.gitTab = "changes";
    state.gitGraph = {
      commits: [],
      loading: false,
      loadedOnce: false,
      allBranches: true,
      collapsed: false,
      repoRoot: "",
    };
  });

  it("renders all discovered repos and maps nested diff paths for explorer", () => {
    const repos: WorkspaceGitRepo[] = [
      {
        root: "c:/hivemind",
        name: "hivemind",
        relPath: "",
        branch: "main",
        isRoot: true,
        diffs: [{ path: "ide/app.go", status: "M", added: 3, removed: 1, lines: [] }],
      },
      {
        root: "c:/hivemind/HiveMind-App",
        name: "HiveMind-App",
        relPath: "HiveMind-App",
        branch: "feat/mobile",
        isRoot: false,
        diffs: [{ path: "app/MainActivity.kt", status: "U", added: 10, removed: 0, lines: [] }],
      },
    ];
    state.gitRepos = repos;
    state.activeGitRepoRoot = repos[0].root;
    state.diffs = repos[0].diffs;

    const html = renderGitSidebarHtml(state.project);
    expect(html).toContain("Alterações (2 repos)");
    expect(html).toContain("hivemind");
    expect(html).toContain("HiveMind-App");
    expect(html).toContain("feat/mobile*");
    expect(html).toContain("Alterações (2)");
    expect(html).toContain('scm-graph-repo-pill" title="hivemind">hivemind</span>');

    state.gitRepos = [
      ...repos,
      {
        root: "d:/outro/projeto",
        name: "projeto",
        relPath: "",
        branch: "main",
        isRoot: false,
        diffs: [{ path: "ide/app.go", status: "M", added: 9, removed: 0, lines: [] }],
      },
    ];
    const map = getGitStatusMap();
    expect(map.files.get("ide/app.go")).toBe("M");
    expect(map.files.get("HiveMind-App/app/MainActivity.kt")).toBe("U");
    expect(map.dirs.has("HiveMind-App")).toBe(true);
    expect(map.dirs.has("HiveMind-App/app")).toBe(true);
  });

  it("switches active repo and updates Graph target when another repo is selected", () => {
    const repos: WorkspaceGitRepo[] = [
      { root: "c:/hivemind", name: "hivemind", relPath: "", branch: "main", isRoot: true, diffs: [] },
      { root: "c:/hivemind/HiveMind-App", name: "HiveMind-App", relPath: "HiveMind-App", branch: "main", isRoot: false, diffs: [] },
    ];
    state.gitRepos = repos;
    state.activeGitRepoRoot = repos[0].root;
    state.gitGraph.repoRoot = repos[0].root;
    state.gitGraph.loadedOnce = true;

    const picked = pickActiveGitRepo(repos, "c:/outro-projeto");
    expect(picked?.root).toBe("c:/hivemind");
    const dirtyOther: WorkspaceGitRepo[] = [
      { root: "c:/pai", name: "pai", relPath: "", branch: "main", isRoot: true, diffs: [] },
      { root: "c:/pai/outro", name: "outro", relPath: "outro", branch: "main", isRoot: false, diffs: [{ path: "a.kt", status: "M", added: 1, removed: 0, lines: [] }] },
    ];
    expect(pickActiveGitRepo(dirtyOther, "")?.root).toBe("c:/pai");

    selectActiveGitRepo("c:/hivemind/HiveMind-App");
    const active = getActiveGitRepo();
    expect(active?.name).toBe("HiveMind-App");
    expect(state.gitGraph.repoRoot).toBe("c:/hivemind/HiveMind-App");
    expect(state.gitGraph.loadedOnce).toBe(false);

    const graphHtml = renderGitGraphSectionHtml();
    expect(graphHtml).toContain("HiveMind-App");
  });
});

describe("turn-scoped diff summary filter", () => {
  const dirtyRepoDiffs = [
    { path: "src/app.ts", added: 12, removed: 2 },
    { path: "other/external.ts", added: 40, removed: 0 },
    { path: "README.md", added: 1, removed: 0 },
  ];

  it("retorna vazio quando a conversa atual só leu ou listou arquivos", () => {
    const msgs: ChatMessage[] = [
      { id: "u1", kind: "user", label: "Usuário", text: "Analise o repo" },
      { id: "t1", kind: "tool", label: "Chefe", toolName: "list_dir", toolDetail: ".", text: "list_dir" },
      { id: "t2", kind: "tool", label: "Chefe", toolName: "read_file", toolDetail: "README.md", text: "read_file" },
      { id: "c1", kind: "chief", label: "Arquiteto (Chefe)", text: "Tudo analisado." },
    ];
    expect(collectTurnEditedFiles(msgs).size).toBe(0);
    expect(filterTurnDiffs(dirtyRepoDiffs, msgs)).toEqual([]);
  });

  it("inclui apenas os diffs editados pelo turno (tool write/edit ou marcador hmtool)", () => {
    const msgs: ChatMessage[] = [
      { id: "u1", kind: "user", label: "Usuário", text: "Ajuste app.ts" },
      { id: "t1", kind: "tool", label: "Dev", toolName: "edit_file", toolDetail: "./src/app.ts#L10-L20", text: "edit_file" },
      { id: "c1", kind: "chief", label: "Dev", text: "Aplicado [[hmtool:write:README.md]]" },
    ];
    const filtered = filterTurnDiffs(dirtyRepoDiffs, msgs);
    expect(filtered.map((d) => d.path)).toEqual(["src/app.ts", "README.md"]);
  });

  it("considera apenas edições após a última mensagem de usuário", () => {
    const msgs: ChatMessage[] = [
      { id: "u1", kind: "user", label: "Usuário", text: "Edite app.ts" },
      { id: "t1", kind: "tool", label: "Dev", toolName: "edit_file", toolDetail: "src/app.ts", text: "edit_file" },
      { id: "u2", kind: "user", label: "Usuário", text: "Agora só explique o README" },
      { id: "t2", kind: "tool", label: "Chefe", toolName: "read_file", toolDetail: "README.md", text: "read_file" },
    ];
    expect(filterTurnDiffs(dirtyRepoDiffs, msgs)).toEqual([]);
  });
});

describe("PRs & Issues collapsible sections, details drawer and scroll preservation", () => {
  beforeEach(() => {
    state.project = makeProject();
    state.gitTab = "prs_issues";
    state.prsCollapsed = false;
    state.issuesCollapsed = false;
    state.prDetails = {};
    state.prs = { list: [], loading: false, loadedOnce: true };
    state.issues = { list: [], loading: false, loadedOnce: true };
  });

  const samplePr: ProjectPullRequest = {
    number: 123,
    title: "feat(auth): exemplo sintético",
    url: "https://example.com/acme/repo/pull/123",
    state: "open",
    isDraft: false,
    author: "dev-alpha",
    headRef: "feat/exemplo",
    baseRef: "main",
    lastActivityAuthor: "reviewer",
    lastActivitySnippet: "Pode extrair esse helper para o pacote core?",
  };

  it("permite recolher a seção Pull Requests na aba PRs & Issues", () => {
    state.prs.list = [samplePr];
    const openHtml = renderGitSidebarHtml(state.project);
    expect(openHtml).toContain('id="btn-toggle-prs-section"');
    expect(openHtml).toContain('id="git-prs-list"');
    expect(openHtml).toContain("Pull Requests (1)");

    state.prsCollapsed = true;
    const closedHtml = renderGitSidebarHtml(state.project);
    expect(closedHtml).toContain('id="btn-toggle-prs-section"');
    expect(closedHtml).not.toContain('id="git-prs-list"');
  });

  it("renderPrCardHtml inclui botão Detalhes e exibe últimos eventos e comentários quando expandido", () => {
    const collapsedCard = renderPrCardHtml(samplePr, 0);
    expect(collapsedCard).toContain('data-toggle-pr-details="123"');
    expect(collapsedCard).toContain("Pode extrair esse helper para o pacote core?");
    expect(collapsedCard).not.toContain("pr-details-drawer");

    state.prDetails[123] = {
      open: true,
      loading: false,
      error: "",
      data: {
        number: 123,
        changedFiles: 4,
        additions: 52,
        deletions: 11,
        totalEvents: 2,
        events: [
          {
            kind: "review_comment",
            author: "reviewer",
            body: "Favor tratar retorno nil.",
            path: "src/example.go",
            line: 27,
          },
          {
            kind: "approved",
            author: "maintainer",
            body: "Restante aprovado!",
          },
        ],
      },
    };
    const expandedCard = renderPrCardHtml(samplePr, 0);
    expect(expandedCard).toContain("pr-details-drawer");
    expect(expandedCard).toContain("4 arq");
    expect(expandedCard).toContain("+52");
    expect(expandedCard).toContain("-11");
    expect(expandedCard).toContain("src/example.go:L27");
    expect(expandedCard).toContain("Favor tratar retorno nil.");
    expect(expandedCard).toContain("Aprovou");
    expect(formatPrRelativeTime("2026-11-22T10:40:00-03:00", Date.parse("2026-11-22T10:50:00-03:00"))).toBe("10m");
  });

  it("explorer.ts preserva scrollTop da .sidebar-body ao re-renderizar a mesma aba", async () => {
    const mod = "node:fs/promises";
    const { readFile } = (await import(/* @vite-ignore */ mod)) as {
      readFile: (path: URL, encoding: string) => Promise<string>;
    };
    const explorerSrc = await readFile(new URL("./explorer.ts", import.meta.url), "utf8");
    expect(explorerSrc).toContain("sidebar.dataset.renderedView");
    expect(explorerSrc).toContain("sidebar.dataset.renderedGitTab");
    expect(explorerSrc).toContain("nextBody.scrollTop = prevScrollTop");
  });
});
