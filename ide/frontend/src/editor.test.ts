import { describe, expect, it } from "vitest";
import { countLinesFast, parseDiffHunks, renderCommitDetailHtml, renderLineNumbers, renderSplitDiffHtml, renderUnifiedDiffHtml } from "./editor";
import type { CommitDetail, FileDiff } from "./types";

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

describe("readOnly diff & Commit Viewer", () => {
  const { hunks } = parseDiffHunks(
    makeDiff([
      { kind: "hunk", text: "@@ -1,2 +1,2 @@" },
      { kind: "context", text: " igual" },
      { kind: "removed", text: "-saiu" },
      { kind: "added", text: "+entrou" },
    ])
  );

  it("readOnly omite botão Reverter bloco nos diffs unificado e split", () => {
    expect(renderUnifiedDiffHtml(hunks, { readOnly: true })).not.toContain("diff-hunk-revert");
    expect(renderSplitDiffHtml(hunks, { readOnly: true })).not.toContain("diff-hunk-revert");
  });

  it("renderCommitDetailHtml separa arquivo/pasta, omite reverter e recolhe após os 3 primeiros quando há >6 arquivos", () => {
    const files: FileDiff[] = Array.from({ length: 8 }, (_, idx) => ({
      path: `service/src/main/java/com/acme/File${idx + 1}.java`,
      status: "M",
      added: idx + 1,
      removed: 1,
      lines: [
        { kind: "hunk", text: "@@ -1,1 +1,2 @@" },
        { kind: "context", text: " class A {}" },
        { kind: "added", text: "+// added" },
      ],
    }));
    const cd: CommitDetail = {
      hash: "0baf17758deadbeef",
      short: "0baf1775",
      author: "Autor Teste",
      date: "14 hours ago",
      subject: "feat(auth): exemplo",
      files,
    };
    const html = renderCommitDetailHtml(cd, "unified");
    expect(html).toContain("0baf1775");
    expect(html).toContain("8 arquivo(s) alterado(s)");
    expect(html).toContain("+36");
    expect(html).toContain("-8");
    expect(html).not.toContain("diff-hunk-revert");
    expect(html).toContain("File1.java");
    expect(html).toContain("service/src/main/java/com/acme");
    expect((html.match(/<details class="commit-file-card" open>/g) || []).length).toBe(3);
    expect((html.match(/<details class="commit-file-card" >/g) || []).length).toBe(5);
  });

  it("styles.css garante flex-shrink: 0 no commit-file-card para não esmagar arquivos no scroll", async () => {
    const mod = "node:fs/promises";
    const { readFile } = (await import(/* @vite-ignore */ mod)) as {
      readFile: (path: URL, encoding: string) => Promise<string>;
    };
    const css = await readFile(new URL("./styles.css", import.meta.url), "utf8");
    const cardBlock = css.match(/\.commit-file-card\s*\{[^}]*\}/);
    expect(cardBlock?.[0]).toContain("flex-shrink: 0;");
  });
});
