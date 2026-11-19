import { describe, expect, it } from "vitest";
import { countLinesFast, parseDiffHunks, renderLineNumbers, renderSplitDiffHtml, renderUnifiedDiffHtml } from "./editor";
import type { FileDiff } from "./types";

function makeDiff(lines: Array<{ kind: string; text: string }>): FileDiff {
  return {
    path: "core/src/PlayerXp.kt",
    name: "PlayerXp.kt",
    added: 2,
    removed: 1,
    lines,
  } as unknown as FileDiff;
}

describe("parseDiffHunks", () => {
  it("separa headers, hunks e linhas", () => {
    const { headers, hunks } = parseDiffHunks(
      makeDiff([
        { kind: "header", text: "diff --git a/core/x.kt b/core/x.kt" },
        { kind: "hunk", text: "@@ -1,3 +1,4 @@" },
        { kind: "context", text: " contexto" },
        { kind: "removed", text: "-linha antiga" },
        { kind: "added", text: "+linha nova" },
        { kind: "added", text: "+linha nova 2" },
      ])
    );
    expect(headers).toHaveLength(1);
    expect(hunks).toHaveLength(1);
    expect(hunks[0].oldStart).toBe(1);
    expect(hunks[0].newStart).toBe(1);
    expect(hunks[0].lines).toHaveLength(4);
  });

  it("linhas antes do primeiro hunk caem num hunk implícito", () => {
    const { hunks } = parseDiffHunks(makeDiff([{ kind: "added", text: "+orfã" }]));
    expect(hunks).toHaveLength(1);
    expect(hunks[0].lines[0].text).toBe("+orfã");
  });

  it("diff vazio não produz hunks", () => {
    const { headers, hunks } = parseDiffHunks(makeDiff([]));
    expect(headers).toHaveLength(0);
    expect(hunks).toHaveLength(0);
  });
});

describe("renderUnifiedDiffHtml / renderSplitDiffHtml", () => {
  const { hunks } = parseDiffHunks(
    makeDiff([
      { kind: "hunk", text: "@@ -1,2 +1,2 @@" },
      { kind: "context", text: " igual" },
      { kind: "removed", text: "-saiu" },
      { kind: "added", text: "+entrou" },
    ])
  );

  it("unificado tem botão de reverter por hunk", () => {
    const html = renderUnifiedDiffHtml(hunks);
    expect(html).toContain("diff-hunk-revert");
    expect(html).toContain("entrou");
  });

  it("vazio avisa ausência de blocos", () => {
    expect(renderUnifiedDiffHtml([])).toContain("Sem blocos");
    expect(renderSplitDiffHtml([])).toContain("Sem blocos");
  });

  it("split pareia removidas/adicionadas em lotes", () => {
    const html = renderSplitDiffHtml(hunks);
    expect(html).toContain("diff-split-table");
    // 1 linha de contexto + 1 par removed/added
    expect((html.match(/<tr>/g) || []).length).toBe(2);
  });
});

describe("contadores de linha", () => {
  it("countLinesFast conta \\n", () => {
    expect(countLinesFast("a\nb\nc")).toBe(3);
    expect(countLinesFast("")).toBe(1);
    expect(countLinesFast("sem quebra")).toBe(1);
  });

  it("renderLineNumbers gera numeração 1..N", () => {
    expect(renderLineNumbers("a\nb\nc")).toBe("1\n2\n3");
    expect(renderLineNumbers("")).toBe("1");
  });
});
