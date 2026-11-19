// Cola CodeMirror 6 para o editor de arquivos da IDE.
// O editor.ts monta/desmonta; este módulo é o "dumb component": um EditorView
// persistente por aba, com estado (doc + cursor + scroll) em cache por relPath.
// Fase 2 do ROADMAP: base para ghost text e diff-apply inline (Fase 3).
import { EditorSelection, EditorState, type Extension } from "@codemirror/state";
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection, dropCursor, rectangularSelection, crosshairCursor, highlightSpecialChars, scrollPastEnd } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { bracketMatching, foldGutter, foldKeymap, indentUnit, indentOnInput, syntaxHighlighting, defaultHighlightStyle } from "@codemirror/language";
import { highlightSelectionMatches, searchKeymap } from "@codemirror/search";
import { oneDark } from "@codemirror/theme-one-dark";
import { go } from "@codemirror/lang-go";
import { java } from "@codemirror/lang-java";
import { javascript } from "@codemirror/lang-javascript";
import { json } from "@codemirror/lang-json";
import { python } from "@codemirror/lang-python";
import { rust } from "@codemirror/lang-rust";
import { markdown } from "@codemirror/lang-markdown";
import { css } from "@codemirror/lang-css";
import { html as htmlLang } from "@codemirror/lang-html";
import { StreamLanguage } from "@codemirror/language";
import { clike } from "@codemirror/legacy-modes/mode/clike";
import { yaml } from "@codemirror/legacy-modes/mode/yaml";
import { shell } from "@codemirror/legacy-modes/mode/shell";

// Tema visual casado com o Obsidian Graphite (#12121a / #e2e0ec) sem tocar em styles.css.
const hivemindTheme = EditorView.theme(
  {
    "&": { height: "100%", backgroundColor: "transparent", fontSize: "13px" },
    ".cm-scroller": { fontFamily: "var(--mono)", lineHeight: "1.55", overflow: "auto" },
    ".cm-content": { caretColor: "#7aa2ff", paddingBottom: "24px" },
    ".cm-gutters": { backgroundColor: "transparent", color: "#767680", border: "none", paddingRight: "4px" },
    ".cm-activeLine": { backgroundColor: "rgba(255,255,255,0.035)" },
    ".cm-activeLineGutter": { backgroundColor: "rgba(255,255,255,0.05)", color: "#e2e0ec" },
    ".cm-foldGutter span": { color: "#767680" },
    "&.cm-focused": { outline: "none" },
    "&.cm-focused .cm-selectionBackground, .cm-content ::selection": { backgroundColor: "rgba(76,141,255,0.30)" },
    ".cm-cursor, .cm-dropCursor": { borderLeftColor: "#7aa2ff", borderLeftWidth: "2px" },
    ".cm-selectionMatch": { backgroundColor: "rgba(76,141,255,0.16)" },
    ".cm-panels": { backgroundColor: "#17171f", color: "#e2e0ec" },
  },
  { dark: true }
);

let view: EditorView | null = null;

// Estado por aba: preserva doc/cursor/scroll quando a aba é retomada.
const stateCache = new Map<string, EditorState>();

export interface CmDocChange {
  (text: string): void;
}

/** Resolve a extensão de linguagem pelo nome do arquivo. */
export function languageExtensionFor(fileName: string): Extension {
  const lower = fileName.toLowerCase();
  if (lower.endsWith(".go")) return go();
  if (lower.endsWith(".kt") || lower.endsWith(".kts")) return StreamLanguage.define(clike({ name: "kotlin" }));
  if (lower.endsWith(".java")) return java();
  if (/\.(jsx?|mjs|tsx?)$/.test(lower)) return javascript({ typescript: /\.(ts|tsx)$/.test(lower), jsx: /\.(jsx|tsx)$/.test(lower) });
  if (lower.endsWith(".json")) return json();
  if (lower.endsWith(".py")) return python();
  if (lower.endsWith(".rs")) return rust();
  if (lower.endsWith(".md") || lower.endsWith(".markdown")) return markdown();
  if (lower.endsWith(".css")) return css();
  if (lower.endsWith(".html") || lower.endsWith(".htm")) return htmlLang();
  if (lower.endsWith(".yaml") || lower.endsWith(".yml")) return StreamLanguage.define(yaml);
  if (lower.endsWith(".sh") || lower.endsWith(".bash")) return StreamLanguage.define(shell);
  return [];
}

function baseExtensions(onDocChange: CmDocChange, fileName: string): Extension[] {
  return [
    lineNumbers(),
    highlightActiveLineGutter(),
    highlightSpecialChars(),
    history(),
    foldGutter(),
    drawSelection(),
    dropCursor(),
    EditorState.allowMultipleSelections.of(true),
    indentOnInput(),
    indentUnit.of("  "),
    rectangularSelection(),
    crosshairCursor(),
    highlightActiveLine(),
    scrollPastEnd(),
    highlightSelectionMatches(),
    syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
    keymap.of([
      ...searchKeymap,
      ...foldKeymap,
      ...defaultKeymap,
      ...historyKeymap,
      indentWithTab,
    ]),
    languageExtensionFor(fileName),
    oneDark,
    hivemindTheme,
    EditorView.updateListener.of((v) => {
      if (v.docChanged) onDocChange(v.state.doc.toString());
    }),
  ];
}

/** Monta (ou reusa) o editor CodeMirror para a aba atual dentro de host. */
export function mountCodeMirror(host: HTMLElement, tabKey: string, content: string, fileName: string, onDocChange: CmDocChange): EditorView | null {
  if (!host) return null;
  let st = stateCache.get(tabKey);
  // Estado em cache só vale se o doc coincide com o conteúdo da aba (o
  // conteúdo pode ter sido recarregado do disco pelo file watcher).
  if (st && st.doc.toString() !== content) {
    st = undefined;
  }
  if (!st) {
    st = EditorState.create({ doc: content, extensions: baseExtensions(onDocChange, fileName) });
    stateCache.set(tabKey, st);
  }
  if (view) {
    view.setState(st);
    return view;
  }
  view = new EditorView({ state: st, parent: host });
  return view;
}

/** View ativa (para find/replace, seleção e saltos de linha). */
export function activeEditorView(): EditorView | null {
  return view;
}

/** Desmonta o editor (aba não-arquivo ou nenhuma aba ativa). */
export function unmountCodeMirror(): void {
  if (view) {
    view.destroy();
    view = null;
  }
}

/** Esquece o estado em cache de uma aba fechada. */
export function forgetTabState(tabKey: string): void {
  stateCache.delete(tabKey);
}

/**
 * Sincroniza o doc da view quando o arquivo mudou no disco (file watcher).
 * Se a aba ativa for essa e a view estiver montada, substitui o documento
 * in-place (sem re-render). Para abas inativas, descarta o cache: a próxima
 * montagem recria a partir do tab.content já atualizado.
 * Retorna true quando a substituição foi feita na view ativa.
 */
export function syncCodeMirrorDoc(tabKey: string, content: string): boolean {
  const st = stateCache.get(tabKey);
  if (!st) return false;
  if (st.doc.toString() === content) return false;
  if (view && view.state === st) {
    view.dispatch({ changes: { from: 0, to: st.doc.length, insert: content } });
    stateCache.set(tabKey, view.state);
    return true;
  }
  stateCache.delete(tabKey);
  return false;
}

/** Seleção atual como {start, end} de offsets absolutos do doc (para Ctrl+L e find). */
export function currentSelection(): { start: number; end: number; text: string } | null {
  if (!view) return null;
  const sel = view.state.selection.main;
  return { start: sel.from, end: sel.to, text: view.state.doc.sliceString(sel.from, sel.to) };
}

/** Seleciona um trecho e rola até ele (find e jump-to-line). */
export function selectRange(start: number, end: number): void {
  if (!view) return;
  view.dispatch({
    selection: EditorSelection.single(start, end),
    scrollIntoView: true,
  });
  view.focus();
}
