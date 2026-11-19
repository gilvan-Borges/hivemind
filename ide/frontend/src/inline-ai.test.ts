import { describe, expect, it } from "vitest";
import { EditorState } from "@codemirror/state";
import { ghostTextField, nextGhostWord, setGhostTextEffect } from "./cm-editor";
import { buildInlineDiffDecorations } from "./inline-ai";
import type { FileDiff } from "./types";

describe("Fase 3 — Ghost Text StateField (CodeMirror 6)", () => {
  it("armazena sugestão, faz prefix-trim ao digitar e limpa ao mover cursor", () => {
    let st = EditorState.create({
      doc: "const x = ",
      selection: { anchor: 10 },
      extensions: [ghostTextField],
    });

    // Ativa ghost text na posição do cursor.
    st = st.update({ effects: setGhostTextEffect.of({ pos: 10, text: "computeValue(a, b)" }) }).state;
    expect(st.field(ghostTextField)).toEqual({ pos: 10, text: "computeValue(a, b)" });

    // Usuário digita o início exato "compute" -> faz prefix-trim sem perder o resto da sugestão.
    st = st.update({
      changes: { from: 10, to: 10, insert: "compute" },
      selection: { anchor: 17 },
    }).state;
    expect(st.field(ghostTextField)).toEqual({ pos: 17, text: "Value(a, b)" });

    // Usuário digita texto divergente -> limpa o ghost text.
    st = st.update({
      changes: { from: 17, to: 17, insert: "ZZ" },
      selection: { anchor: 19 },
    }).state;
    expect(st.field(ghostTextField)).toBeNull();
  });

  it("nextGhostWord fatia uma palavra por vez para Ctrl+ArrowRight", () => {
    expect(nextGhostWord("return a + b")).toBe("return");
    expect(nextGhostWord(" a + b")).toBe(" a");
    expect(nextGhostWord("  fooBar()")).toBe("  fooBar()");
    expect(nextGhostWord("")).toBe("");
  });
});

describe("Fase 3 — buildInlineDiffDecorations (Revisão Hunk-a-Hunk)", () => {
  it("mapeia linhas adicionadas e removidas de múltiplos hunks para o buffer", () => {
    const diff: FileDiff = {
      path: "src/app.ts",
      added: 2,
      removed: 1,
      lines: [
        { kind: "header", text: "diff --git a/src/app.ts b/src/app.ts" },
        { kind: "hunk", text: "@@ -2,3 +2,4 @@" },
        { kind: "context", text: " const a = 1;" },
        { kind: "removed", text: "-const b = 2;" },
        { kind: "added", text: "+const b = 20;" },
        { kind: "added", text: "+const c = 30;" },
        { kind: "context", text: " export { a, b };" },
      ],
    };

    const { parsedHunks, decorations } = buildInlineDiffDecorations(diff, 20);
    expect(parsedHunks).toHaveLength(1);
    expect(decorations).toHaveLength(1);
    expect(decorations[0].anchorLine).toBe(3);
    expect(decorations[0].addedLines).toEqual([3, 4]);
    expect(decorations[0].removedLines).toEqual(["const b = 2;"]);
    expect(decorations[0].addedCount).toBe(2);
    expect(decorations[0].removedCount).toBe(1);
  });

  it("retorna vazio quando não há diff", () => {
    const res = buildInlineDiffDecorations(null, 10);
    expect(res.decorations).toEqual([]);
  });
});
