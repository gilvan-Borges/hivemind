// Cola CodeMirror 6 para o editor de arquivos da IDE.
// O editor.ts monta/desmonta; este módulo é o "dumb component": um EditorView
// persistente por aba, com estado (doc + cursor + scroll) em cache por relPath.
// Fase 3 do ROADMAP: Ghost Text inline (Tab / Ctrl+→ / Esc), Ctrl+K range apply
// e decorações de revisão hunk-a-hunk diretamente no buffer editável.
import { EditorSelection, EditorState, Prec, StateEffect, StateField, type Extension } from "@codemirror/state";
import { Decoration, type DecorationSet, EditorView, WidgetType, crosshairCursor, drawSelection, dropCursor, highlightActiveLine, highlightActiveLineGutter, highlightSpecialChars, hoverTooltip, keymap, lineNumbers, rectangularSelection, scrollPastEnd, type Tooltip } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { StreamLanguage, HighlightStyle, bracketMatching, defaultHighlightStyle, foldGutter, foldKeymap, indentOnInput, indentUnit, syntaxHighlighting } from "@codemirror/language";
import { tags as highlightTags } from "@lezer/highlight";
import { lintGutter, setDiagnostics, type Diagnostic } from "@codemirror/lint";
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
import { clike } from "@codemirror/legacy-modes/mode/clike";
import { yaml } from "@codemirror/legacy-modes/mode/yaml";
import { shell } from "@codemirror/legacy-modes/mode/shell";

// Tema visual casado com o Obsidian. Palavra-chave usa o dourado de --accent-strong.
const goldTokenStyle = syntaxHighlighting(HighlightStyle.define([
  { tag: highlightTags.keyword, color: "var(--accent-strong)" },
]));
const hivemindTheme = EditorView.theme(
  {
    "&": { height: "100%", backgroundColor: "transparent", fontSize: "13px" },
    ".cm-scroller": { fontFamily: "var(--mono)", lineHeight: "1.55", overflow: "auto" },
    ".cm-content": { caretColor: "#7aa2ff", paddingBottom: "36px" },
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

// ========================
// Fase 3: Ghost Text (FIM)
// ========================

export interface GhostTextState {
  pos: number;
  text: string;
}

export const setGhostTextEffect = StateEffect.define<GhostTextState | null>();

class GhostTextWidget extends WidgetType {
  constructor(readonly text: string) {
    super();
  }
  eq(other: GhostTextWidget): boolean {
    return this.text === other.text;
  }
  toDOM(): HTMLElement {
    const span = document.createElement("span");
    span.className = "cm-ghost-text";
    span.textContent = this.text;
    span.setAttribute("aria-hidden", "true");
    return span;
  }
  ignoreEvent(): boolean {
    return true;
  }
}

export const ghostTextField = StateField.define<GhostTextState | null>({
  create: () => null,
  update(value, tr) {
    for (const eff of tr.effects) {
      if (eff.is(setGhostTextEffect)) {
        const next = eff.value;
        if (!next || !next.text) return null;
        return { pos: Math.max(0, Math.min(next.pos, tr.state.doc.length)), text: next.text };
      }
    }
    if (!value) return null;
    if (tr.docChanged) {
      // Se o usuário digitou exatamente o prefixo do ghost text na mesma posição,
      // aparar o prefixo mantém a sugestão fluida sem piscar.
      let matchedText = "";
      let matched = false;
      let changeCount = 0;
      tr.changes.iterChanges((fromA, toA, _fromB, _toB, inserted) => {
        changeCount++;
        const ins = inserted.toString();
        if (changeCount === 1 && fromA === value.pos && toA === value.pos && ins.length > 0 && value.text.startsWith(ins)) {
          matchedText = value.text.slice(ins.length);
          matched = true;
        }
      });
      if (changeCount === 1 && matched && matchedText.length > 0) {
        return { pos: tr.state.selection.main.head, text: matchedText };
      }
      return null;
    }
    if (tr.selection && tr.state.selection.main.head !== value.pos) {
      return null;
    }
    return value;
  },
  provide: (field) =>
    EditorView.decorations.from(field, (val): DecorationSet => {
      if (!val || !val.text) return Decoration.none;
      return Decoration.set([
        Decoration.widget({
          widget: new GhostTextWidget(val.text),
          side: 1,
        }).range(val.pos),
      ]);
    }),
});

let ghostAcceptCb: ((acceptedText: string) => void) | null = null;

/** Registra callback disparado quando o usuário aceita uma sugestão Ghost Text (Tab / Ctrl+→). */
export function setGhostAcceptCallback(cb: ((acceptedText: string) => void) | null): void {
  ghostAcceptCb = cb;
}

/** Extrai o próximo bloco de 1 palavra (incluindo espaços iniciais) de uma sugestão Ghost Text. */
export function nextGhostWord(text: string): string {
  if (!text) return "";
  const m = text.match(/^(\s*\S+|\s+)/);
  return m ? m[0] : text.slice(0, 1);
}

/** Aceita integralmente o ghost text ativo no EditorView informado. */
export function acceptGhostTextInView(target: EditorView): boolean {
  const ghost = target.state.field(ghostTextField, false);
  if (!ghost || !ghost.text) return false;
  const insert = ghost.text;
  const pos = ghost.pos;
  target.dispatch({
    changes: { from: pos, to: pos, insert },
    selection: { anchor: pos + insert.length },
    effects: setGhostTextEffect.of(null),
    scrollIntoView: true,
  });
  ghostAcceptCb?.(insert);
  return true;
}

/** Aceita apenas a próxima palavra do ghost text ativo (Ctrl+ArrowRight). */
export function acceptGhostTextWordInView(target: EditorView): boolean {
  const ghost = target.state.field(ghostTextField, false);
  if (!ghost || !ghost.text) return false;
  const chunk = nextGhostWord(ghost.text);
  if (!chunk) return false;
  const rest = ghost.text.slice(chunk.length);
  const pos = ghost.pos;
  target.dispatch({
    changes: { from: pos, to: pos, insert: chunk },
    selection: { anchor: pos + chunk.length },
    effects: setGhostTextEffect.of(rest ? { pos: pos + chunk.length, text: rest } : null),
    scrollIntoView: true,
  });
  ghostAcceptCb?.(chunk);
  return true;
}

const ghostTextKeymap = Prec.highest(
  keymap.of([
    {
      key: "Tab",
      run: (target) => acceptGhostTextInView(target),
    },
    {
      key: "Mod-ArrowRight",
      run: (target) => acceptGhostTextWordInView(target),
    },
    {
      key: "Escape",
      run: (target) => {
        const ghost = target.state.field(ghostTextField, false);
        if (!ghost) return false;
        target.dispatch({ effects: setGhostTextEffect.of(null) });
        return true;
      },
    },
  ])
);

// ===========================================
// Fase 3: Revisão Hunk-a-Hunk Inline no CM6
// ===========================================

export interface InlineDiffHunkDecor {
  index: number;
  header: string;
  anchorLine: number; // 1-based line in the current doc where widget sits above
  addedLines: number[]; // 1-based line numbers highlighted as added
  removedLines: string[]; // removed lines rendered above anchorLine (without leading '-')
  addedCount: number;
  removedCount: number;
}

export interface InlineDiffCallbacks {
  onAcceptHunk: (index: number) => void;
  onRejectHunk: (index: number) => void;
}

interface InlineDiffPayload {
  hunks: InlineDiffHunkDecor[];
  callbacks: InlineDiffCallbacks | null;
}

export const setInlineDiffHunksEffect = StateEffect.define<InlineDiffPayload>();

class InlineHunkWidget extends WidgetType {
  constructor(
    readonly hunk: InlineDiffHunkDecor,
    readonly callbacks: InlineDiffCallbacks | null
  ) {
    super();
  }
  eq(other: InlineHunkWidget): boolean {
    return (
      this.hunk.index === other.hunk.index &&
      this.hunk.header === other.hunk.header &&
      this.hunk.addedCount === other.hunk.addedCount &&
      this.hunk.removedCount === other.hunk.removedCount &&
      this.hunk.removedLines.join("\n") === other.hunk.removedLines.join("\n")
    );
  }
  toDOM(): HTMLElement {
    const wrap = document.createElement("div");
    wrap.className = "cm-hunk-widget";

    const bar = document.createElement("div");
    bar.className = "cm-hunk-bar";

    const label = document.createElement("span");
    label.className = "cm-hunk-title";
    label.textContent = `Bloco #${this.hunk.index + 1} · +${this.hunk.addedCount} -${this.hunk.removedCount}`;
    bar.appendChild(label);

    const actions = document.createElement("div");
    actions.className = "cm-hunk-actions";

    const acceptBtn = document.createElement("button");
    acceptBtn.type = "button";
    acceptBtn.className = "cm-hunk-btn accept";
    acceptBtn.textContent = "✓ Aceitar";
    acceptBtn.title = "Aceitar (stage) somente este bloco";
    acceptBtn.addEventListener("mousedown", (e) => e.preventDefault());
    acceptBtn.addEventListener("click", (e) => {
      e.preventDefault();
      e.stopPropagation();
      this.callbacks?.onAcceptHunk(this.hunk.index);
    });

    const rejectBtn = document.createElement("button");
    rejectBtn.type = "button";
    rejectBtn.className = "cm-hunk-btn reject";
    rejectBtn.textContent = "✕ Rejeitar";
    rejectBtn.title = "Reverter somente este bloco";
    rejectBtn.addEventListener("mousedown", (e) => e.preventDefault());
    rejectBtn.addEventListener("click", (e) => {
      e.preventDefault();
      e.stopPropagation();
      this.callbacks?.onRejectHunk(this.hunk.index);
    });

    actions.appendChild(acceptBtn);
    actions.appendChild(rejectBtn);
    bar.appendChild(actions);
    wrap.appendChild(bar);

    if (this.hunk.removedLines.length > 0) {
      const remBox = document.createElement("div");
      remBox.className = "cm-hunk-removed";
      for (const ln of this.hunk.removedLines) {
        const row = document.createElement("div");
        row.className = "cm-hunk-removed-line";
        row.textContent = `- ${ln}`;
        remBox.appendChild(row);
      }
      wrap.appendChild(remBox);
    }

    return wrap;
  }
  ignoreEvent(event: Event): boolean {
    return event.type === "mousedown" || event.type === "click";
  }
}

const addedLineDeco = Decoration.line({ class: "cm-diff-added-line" });

export const inlineDiffField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(decos, tr) {
    for (const eff of tr.effects) {
      if (eff.is(setInlineDiffHunksEffect)) {
        const { hunks, callbacks } = eff.value;
        if (!hunks || hunks.length === 0) return Decoration.none;
        const ranges: Array<ReturnType<Decoration["range"]>> = [];
        const doc = tr.state.doc;
        for (const h of hunks) {
          const clampedAnchor = Math.max(1, Math.min(h.anchorLine, doc.lines));
          const anchorPos = doc.line(clampedAnchor).from;
          ranges.push(
            Decoration.widget({
              widget: new InlineHunkWidget(h, callbacks),
              block: true,
              side: -1,
            }).range(anchorPos)
          );
          for (const ln of h.addedLines) {
            if (ln >= 1 && ln <= doc.lines) {
              ranges.push(addedLineDeco.range(doc.line(ln).from));
            }
          }
        }
        return Decoration.set(ranges, true);
      }
    }
    if (tr.docChanged) {
      return decos.map(tr.changes);
    }
    return decos;
  },
  provide: (field) => EditorView.decorations.from(field),
});

// ========================
// Fase 4: LSP (Diagnostics, Go-to-Definition, Hover)
// ========================

export interface LSPCallbacks {
  onDefinition?: (line: number, col: number) => void;
  onHover?: (line: number, col: number) => Promise<string | null>;
}

let lspCallbacks: LSPCallbacks = {};

export function setLSPCallbacks(cbs: LSPCallbacks): void {
  lspCallbacks = cbs;
}

/** Aplica diagnósticos (gopls / build) diretamente no editor ativo via @codemirror/lint. */
export function setEditorDiagnostics(diags: Diagnostic[]): void {
  if (!view) return;
  view.dispatch(setDiagnostics(view.state, diags));
}

const lspHoverExtension = hoverTooltip(
  async (targetView, pos): Promise<Tooltip | null> => {
    if (!lspCallbacks.onHover) return null;
    const lineObj = targetView.state.doc.lineAt(pos);
    const line = lineObj.number;
    const col = pos - lineObj.from + 1;
    const text = await lspCallbacks.onHover(line, col);
    if (!text) return null;
    return {
      pos,
      above: true,
      create() {
        const dom = document.createElement("div");
        dom.className = "cm-lsp-hover";
        const pre = document.createElement("pre");
        pre.textContent = text;
        dom.appendChild(pre);
        return { dom };
      },
    };
  },
  { hoverTime: 380 }
);

const lspNavigationKeymap = Prec.high(
  keymap.of([
    {
      key: "F12",
      run: (target) => {
        if (!lspCallbacks.onDefinition) return false;
        const pos = target.state.selection.main.head;
        const lineObj = target.state.doc.lineAt(pos);
        lspCallbacks.onDefinition(lineObj.number, pos - lineObj.from + 1);
        return true;
      },
    },
  ])
);

const lspCtrlClickHandler = EditorView.domEventHandlers({
  mousedown(event, target) {
    if (!(event.ctrlKey || event.metaKey) || event.button !== 0 || !lspCallbacks.onDefinition) {
      return false;
    }
    const pos = target.posAtCoords({ x: event.clientX, y: event.clientY });
    if (pos == null) return false;
    const lineObj = target.state.doc.lineAt(pos);
    event.preventDefault();
    lspCallbacks.onDefinition(lineObj.number, pos - lineObj.from + 1);
    return true;
  },
});

// ========================
// Ciclo de Vida do Editor
// ========================

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

/** Cria o conjunto de extensões base do editor CM6 (exportado também para testes). */
export function createBaseExtensions(onDocChange: CmDocChange, fileName: string): Extension[] {
  return [
    lineNumbers(),
    lintGutter(),
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
    bracketMatching(),
    syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
    ghostTextField,
    ghostTextKeymap,
    inlineDiffField,
    lspHoverExtension,
    lspNavigationKeymap,
    lspCtrlClickHandler,
    keymap.of([
      ...searchKeymap,
      ...foldKeymap,
      ...defaultKeymap,
      ...historyKeymap,
      indentWithTab,
    ]),
    languageExtensionFor(fileName),
    oneDark,
    goldTokenStyle,
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
  if (st && st.doc.toString() !== content) {
    st = undefined;
  }
  if (!st) {
    st = EditorState.create({ doc: content, extensions: createBaseExtensions(onDocChange, fileName) });
    stateCache.set(tabKey, st);
  }
  if (view) {
    view.setState(st);
    return view;
  }
  view = new EditorView({ state: st, parent: host });
  return view;
}

/** View ativa (para find/replace, seleção, Ghost Text e Ctrl+K). */
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
 * Sincroniza o doc da view quando o arquivo mudou no disco (file watcher ou revert de hunk).
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

/** Exibe uma sugestão Ghost Text na posição pos da view ativa. */
export function setGhostText(pos: number, text: string): void {
  if (!view) return;
  view.dispatch({ effects: setGhostTextEffect.of(text ? { pos, text } : null) });
}

/** Remove qualquer sugestão Ghost Text ativa. */
export function clearGhostText(): void {
  if (!view) return;
  if (!view.state.field(ghostTextField, false)) return;
  view.dispatch({ effects: setGhostTextEffect.of(null) });
}

/** Retorna o estado atual do Ghost Text na view ativa. */
export function getGhostText(): GhostTextState | null {
  if (!view) return null;
  return view.state.field(ghostTextField, false) ?? null;
}

/** Aplica decorações de revisão hunk-a-hunk diretamente na view ativa. */
export function setInlineDiffHunks(hunks: InlineDiffHunkDecor[], callbacks: InlineDiffCallbacks | null): void {
  if (!view) return;
  view.dispatch({ effects: setInlineDiffHunksEffect.of({ hunks, callbacks }) });
}

/** Remove as decorações de revisão hunk-a-hunk da view ativa. */
export function clearInlineDiffHunks(): void {
  if (!view) return;
  view.dispatch({ effects: setInlineDiffHunksEffect.of({ hunks: [], callbacks: null }) });
}

/** Substitui [from, to] em uma única transação undoable no CodeMirror 6 (usado pelo Ctrl+K). */
export function replaceEditorRange(from: number, to: number, insert: string): boolean {
  if (!view) return false;
  const docLen = view.state.doc.length;
  const safeFrom = Math.max(0, Math.min(from, docLen));
  const safeTo = Math.max(safeFrom, Math.min(to, docLen));
  view.dispatch({
    changes: { from: safeFrom, to: safeTo, insert },
    selection: EditorSelection.single(safeFrom, safeFrom + insert.length),
    effects: setGhostTextEffect.of(null),
    scrollIntoView: true,
  });
  view.focus();
  return true;
}

export interface EditorCursorContext {
  from: number;
  to: number;
  head: number;
  selectedText: string;
  startLine: number;
  endLine: number;
  prefix: string;
  suffix: string;
  beforeContext: string;
  afterContext: string;
  docLength: number;
}

/** Coleta contexto estruturado ao redor do cursor/seleção para Ghost Text e Ctrl+K. */
export function getCursorContext(): EditorCursorContext | null {
  if (!view) return null;
  const doc = view.state.doc;
  const sel = view.state.selection.main;
  const startLineObj = doc.lineAt(sel.from);
  const endLineObj = doc.lineAt(sel.to);
  const beforeStart = doc.line(Math.max(1, startLineObj.number - 60)).from;
  const afterEnd = doc.line(Math.min(doc.lines, endLineObj.number + 40)).to;

  return {
    from: sel.from,
    to: sel.to,
    head: sel.head,
    selectedText: doc.sliceString(sel.from, sel.to),
    startLine: startLineObj.number,
    endLine: endLineObj.number,
    prefix: doc.sliceString(Math.max(0, sel.head - 4000), sel.head),
    suffix: doc.sliceString(sel.head, Math.min(doc.length, sel.head + 1500)),
    beforeContext: doc.sliceString(beforeStart, sel.from),
    afterContext: doc.sliceString(sel.to, afterEnd),
    docLength: doc.length,
  };
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
