import { describe, expect, it } from "vitest";
import { languageExtensionFor } from "./cm-editor";

describe("languageExtensionFor", () => {
  const cases: Array<[string, boolean]> = [
    ["main.go", true],
    ["GameScreen.kt", true],
    ["App.java", true],
    ["index.ts", true],
    ["app.jsx", true],
    ["package.json", true],
    ["simulador.py", true],
    ["main.rs", true],
    ["README.md", true],
    ["styles.css", true],
    ["index.html", true],
    ["team.yaml", true],
    ["deploy.sh", true],
  ];

  it.each(cases)("mapeia %s para uma extensão de linguagem", (fileName, expected) => {
    const ext = languageExtensionFor(fileName);
    expect(Array.isArray(ext) ? ext.length > 0 : ext !== null).toBe(expected);
  });

  it("extensão desconhecida fica em texto puro", () => {
    expect(languageExtensionFor("arquivo.xyz123")).toEqual([]);
  });

  it("é case-insensitive", () => {
    const lower = languageExtensionFor("main.go");
    const upper = languageExtensionFor("MAIN.GO");
    expect(Array.isArray(lower) ? lower.length > 0 : true).toBe(true);
    expect(Array.isArray(upper) ? upper.length > 0 : true).toBe(true);
  });
});
