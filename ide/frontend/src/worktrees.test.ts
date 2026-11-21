import { describe, expect, it } from "vitest";
import { state } from "./state";
import { formatBytesCompact, renderWorktreesTabHtml } from "./worktrees";

describe("worktrees panel", () => {
  it("formata tamanhos de worktrees em bytes legíveis", () => {
    expect(formatBytesCompact(0)).toBe("0 B");
    expect(formatBytesCompact(2048)).toBe("2.0 KB");
    expect(formatBytesCompact(5 * 1024 * 1024)).toBe("5.0 MB");
  });

  it("renderiza os 4 estados (carregando, erro com retry, vazio e dados com rollback)", () => {
    state.worktrees = { loading: true, error: "", loadedOnce: false, list: [], checkpoints: [], creating: false, createError: "", progress: null, newAgent: "dev_1", newBranch: "" };
    expect(renderWorktreesTabHtml()).toContain("Carregando worktrees");

    state.worktrees.loading = false;
    state.worktrees.error = "falha de rede";
    const errHtml = renderWorktreesTabHtml();
    expect(errHtml).toContain("falha de rede");
    expect(errHtml).toContain("Tentar novamente");

    state.worktrees.error = "";
    state.worktrees.loadedOnce = true;
    expect(renderWorktreesTabHtml()).toContain("Nenhuma worktree encontrada");

    state.worktrees.list = [
      { path: "/tmp/wt/dev_1", agent: "dev_1", branch: "feat/teste", sizeBytes: 4096 },
      { path: "/tmp/wt/orphan", agent: "orphan", orphan: true, sizeBytes: 1024 },
    ];
    state.worktrees.checkpoints = [
      { id: "cp-1", ref: "refs/hivemind/checkpoints/cp-1", sha: "abcdef123456", label: "antes dev", createdAt: "2026-11-21T00:00:00Z" },
    ];
    const dataHtml = renderWorktreesTabHtml();
    expect(dataHtml).toContain("feat/teste");
    expect(dataHtml).toContain("Órfã");
    expect(dataHtml).toContain("Arquivar");
    expect(dataHtml).toContain("Rollback");
  });
});
