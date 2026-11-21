import { describe, expect, it } from "vitest";
import {
  createEmptyConflictState,
  createEmptyInspectState,
  renderConflictsSectionHtml,
  renderFileGitInspectPanelHtml,
} from "./git-history";

describe("git-history renderers", () => {
  it("renders loading, empty, error, blame and history states cleanly", () => {
    const loadingHtml = renderFileGitInspectPanelHtml({
      ...createEmptyInspectState(),
      mode: "blame",
      relPath: "main.go",
      loading: true,
    });
    expect(loadingHtml).toContain("Carregando blame");

    const errorHtml = renderFileGitInspectPanelHtml({
      ...createEmptyInspectState(),
      mode: "history",
      relPath: "main.go",
      error: "git log falhou",
    });
    expect(errorHtml).toContain("btn-git-inspect-retry");
    expect(errorHtml).toContain("git log falhou");

    const emptyHtml = renderFileGitInspectPanelHtml({
      ...createEmptyInspectState(),
      mode: "history",
      relPath: "new.go",
    });
    expect(emptyHtml).toContain("Nenhum commit encontrado");

    const blameHtml = renderFileGitInspectPanelHtml({
      ...createEmptyInspectState(),
      mode: "blame",
      relPath: "main.go",
      blame: [
        {
          line: 1,
          commit: "1234567890abcdef",
          short: "12345678",
          author: "Dev Synth",
          timestamp: "2026-11-21T00:00:00Z",
          summary: "chore: init",
          text: "package main",
        },
      ],
    });
    expect(blameHtml).toContain("12345678");
    expect(blameHtml).toContain("Dev Synth");
    expect(blameHtml).toContain("package main");
  });

  it("renders conflict blocks and ready-to-mark-resolved actions", () => {
    expect(renderConflictsSectionHtml(createEmptyConflictState())).toBe("");

    const unresolvedHtml = renderConflictsSectionHtml({
      ...createEmptyConflictState(),
      files: [
        {
          path: "src/app.ts",
          blocks: [
            {
              index: 0,
              startLine: 4,
              endLine: 8,
              oursLabel: "HEAD",
              theirsLabel: "feat/x",
              ours: "const x = 1;",
              theirs: "const x = 2;",
            },
          ],
        },
      ],
    });
    expect(unresolvedHtml).toContain("Conflitos de Merge");
    expect(unresolvedHtml).toContain("Manter atual");
    expect(unresolvedHtml).toContain("Aceitar recebida");
    expect(unresolvedHtml).toContain("Manter ambas");
    expect(unresolvedHtml).not.toContain("data-mark-resolved");

    const readyHtml = renderConflictsSectionHtml({
      ...createEmptyConflictState(),
      files: [{ path: "src/app.ts", blocks: [] }],
    });
    expect(readyHtml).toContain("data-mark-resolved=\"src/app.ts\"");
  });
});
