import { beforeEach, describe, expect, it } from "vitest";
import { getActiveGitRepo, getGitStatusMap, renderGitGraphSectionHtml, renderGitSidebarHtml, selectActiveGitRepo } from "./git";
import { state } from "./state";
import type { ProjectInfo, WorkspaceGitRepo } from "./types";

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

    selectActiveGitRepo("c:/hivemind/HiveMind-App");
    const active = getActiveGitRepo();
    expect(active?.name).toBe("HiveMind-App");
    expect(state.gitGraph.repoRoot).toBe("c:/hivemind/HiveMind-App");
    expect(state.gitGraph.loadedOnce).toBe(false);

    const graphHtml = renderGitGraphSectionHtml();
    expect(graphHtml).toContain("HiveMind-App");
  });
});
