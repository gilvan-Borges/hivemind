import { describe, expect, it } from "vitest";
import { isLSPFile, mergeProblems } from "./lsp";
import type { ProblemItem } from "./types";

describe("Fase 4 — LSP & Problems Panel helpers", () => {
  it("recognizes Go files for gopls LSP", () => {
    expect(isLSPFile("ide/app.go")).toBe(true);
    expect(isLSPFile("internal/tools/tools_test.go")).toBe(true);
    expect(isLSPFile("ide/frontend/src/lsp.ts")).toBe(false);
    expect(isLSPFile("README.md")).toBe(false);
  });

  it("merges, deduplicates, and sorts LSP + build problems by severity then file/line", () => {
    const lspMap: Record<string, ProblemItem[]> = {
      "ide/app.go": [
        { source: "gopls", severity: "warning", file: "ide/app.go", line: 30, col: 2, message: "unused var" },
        { source: "gopls", severity: "error", file: "ide/app.go", line: 12, col: 5, message: "undefined: x" },
      ],
    };
    const buildList: ProblemItem[] = [
      // Duplicate of gopls error (should collapse)
      { source: "go", severity: "error", file: "ide\\app.go", line: 12, col: 5, message: "undefined: x" },
      { source: "tsc", severity: "error", file: "ide/frontend/src/a.ts", line: 8, col: 1, code: "TS2339", message: "Property missing" },
      { source: "gopls", severity: "info", file: "ide/app.go", line: 5, col: 1, message: "hint" },
    ];

    const merged = mergeProblems(lspMap, buildList);
    expect(merged).toHaveLength(4);
    expect(merged[0]).toMatchObject({ severity: "error", file: "ide/app.go", line: 12 });
    expect(merged[1]).toMatchObject({ severity: "error", file: "ide/frontend/src/a.ts", line: 8 });
    expect(merged[2]).toMatchObject({ severity: "warning", file: "ide/app.go", line: 30 });
    expect(merged[3]).toMatchObject({ severity: "info", file: "ide/app.go", line: 5 });
  });
});
