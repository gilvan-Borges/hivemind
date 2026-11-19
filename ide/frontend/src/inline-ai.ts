// Fase 3 — Paridade Agentic no Editor:
// 1. Ghost Text (autocompletar inline com Tab / Ctrl+→ / Esc, via provider HTTP)
// 2. Ctrl+K Inline Edit (seleção + instrução → preview de diff → aplicar em 1 transação)
// 3. Revisão Hunk-a-Hunk no Editor (aceitar/rejeitar blocos de mudança do agente no buffer aberto)
import { icons } from "./icons";
import type { FileDiff } from "./types";
import { escapeHtml } from "./utils";
import {
  activeEditorView,
  clearGhostText,
  clearInlineDiffHunks,
  getCursorContext,
  replaceEditorRange,
  setGhostAcceptCallback,
  setGhostText,
  setInlineDiffHunks,
  syncCodeMirrorDoc,
  type InlineDiffHunkDecor,
} from "./cm-editor";
import { parseDiffHunks, type DiffHunkParsed } from "./editor";
import { refreshDiffs } from "./git";
import { updateStatusBar } from "./shell";
import { api, setStatus, state, toast, toastError } from "./state";

let ghostTimer = 0;
let ghostReqSeq = 0;

/** Inicializa o callback de telemetria quando o usuário aceita Ghost Text. */
export function initInlineAIHooks(): void {
  setGhostAcceptCallback(() => {
    state.ghostTextAccepted += 1;
    updateStatusBar();
  });
}

/** Verifica se há um editor de código CodeMirror ativo no momento. */
export function isCodeEditorActive(): boolean {
  const activeTab = state.editorTabs.find((t) => t.relPath === state.activeEditorPath);
  if (!activeTab || activeTab.kind !== "file") return false;
  if (/\.md$/i.test(activeTab.name) && activeTab.mdView !== "source") return false;
  return activeEditorView() !== null;
}

/** Consulta o backend para saber se Ghost Text (HTTP) e Ctrl+K estão prontos. */
export async function refreshInlineAIStatus(): Promise<void> {
  const backend = api();
  const root = state.project?.root;
  if (!backend?.GetInlineAIStatus || !root) return;
  try {
    state.inlineAIStatus = await backend.GetInlineAIStatus(root);
    updateStatusBar();
  } catch {
    // ignore transient errors
  }
}

/** Cancela o timer de Ghost Text pendente. */
export function clearPendingGhostTimer(): void {
  if (ghostTimer) {
    window.clearTimeout(ghostTimer);
    ghostTimer = 0;
  }
}

/** Liga/desliga Ghost Text inline. */
export function toggleGhostText(): void {
  state.ghostTextEnabled = !state.ghostTextEnabled;
  if (!state.ghostTextEnabled) {
    clearPendingGhostTimer();
    clearGhostText();
    void api()?.CancelInlineAI?.();
    setStatus("Ghost Text inline desativado.");
  } else {
    void refreshInlineAIStatus();
    if (state.inlineAIStatus && !state.inlineAIStatus.ghostAvailable) {
      toast(state.inlineAIStatus.ghostReason || "Configure um provider HTTP (ex.: OpenCode) para ativar o Ghost Text.", "info");
    } else {
      setStatus(`Ghost Text ativo (${state.inlineAIStatus?.ghostModel || "HTTP"}).`);
    }
  }
  updateStatusBar();
  const btn = document.getElementById("btn-toggle-ghost");
  if (btn) {
    btn.classList.toggle("active", state.ghostTextEnabled);
    btn.title = ghostButtonTitle();
  }
}

export function ghostButtonTitle(): string {
  const st = state.inlineAIStatus;
  if (!state.ghostTextEnabled) return "Ghost Text desligado — clique para ativar (Tab aceita sugestão)";
  if (st && !st.ghostAvailable) return st.ghostReason || "Ghost Text indisponível (requer provider HTTP)";
  const mod = st?.ghostModel ? ` · ${st.ghostModel}` : "";
  return `Ghost Text ativo${mod} (Tab aceita, Ctrl+→ aceita palavra, Esc dispensa)`;
}

/** Agenda uma sugestão Ghost Text após pausa de digitação no CodeMirror. */
export function scheduleGhostText(relPath: string): void {
  clearPendingGhostTimer();
  if (!state.ghostTextEnabled || state.inlineEdit.open) return;
  const backend = api();
  const root = state.project?.root;
  if (!backend?.InlineComplete || !root) return;
  if (state.inlineAIStatus && !state.inlineAIStatus.ghostAvailable) return;

  const completeFn = backend.InlineComplete;
  const seq = ++ghostReqSeq;
  ghostTimer = window.setTimeout(async () => {
    ghostTimer = 0;
    if (seq !== ghostReqSeq || !state.ghostTextEnabled || state.inlineEdit.open) return;
    if (state.activeEditorPath !== relPath) return;
    const ctx = getCursorContext();
    if (!ctx) return;
    // Não sugerir com seleção ativa ou documento vazio.
    if (ctx.from !== ctx.to || ctx.docLength === 0) return;
    // Só dispara quando há pelo menos 2 chars de contexto antes do cursor.
    if (ctx.prefix.trim().length < 2) return;

    const capturedHead = ctx.head;
    const capturedDocLen = ctx.docLength;
    try {
      const res = await completeFn(root, relPath, ctx.prefix, ctx.suffix, "");
      if (seq !== ghostReqSeq || !state.ghostTextEnabled || state.inlineEdit.open) return;
      if (state.activeEditorPath !== relPath) return;
      const nowCtx = getCursorContext();
      if (!nowCtx || nowCtx.head !== capturedHead || nowCtx.docLength !== capturedDocLen || nowCtx.from !== nowCtx.to) {
        return;
      }
      if (res?.completion) {
        setGhostText(capturedHead, res.completion);
      }
    } catch {
      // Cancelamentos ou indisponibilidade de rede são silenciosos para não interromper a digitação.
    }
  }, 550);
}

// ====================================
// Ctrl+K Inline Edit (estilo Cursor)
// ====================================

export function openInlineEdit(): void {
  if (!isCodeEditorActive()) return;
  const activeTab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
  const ctx = getCursorContext();
  if (!activeTab || !ctx) return;

  clearPendingGhostTimer();
  clearGhostText();

  // Fecha find bar se estiver aberta para evitar sobreposição visual.
  if (state.editorFind.open) {
    state.editorFind.open = false;
    const findLayer = document.getElementById("editor-find-layer");
    if (findLayer) findLayer.innerHTML = "";
  }

  state.inlineEdit = {
    open: true,
    relPath: activeTab.relPath,
    from: ctx.from,
    to: ctx.to,
    startLine: ctx.startLine,
    endLine: ctx.endLine,
    selectedText: ctx.selectedText,
    instruction: "",
    loading: false,
    error: "",
    result: null,
  };
  renderInlineEditBar();
  const input = document.getElementById("inline-edit-input") as HTMLInputElement | null;
  input?.focus();
}

export function closeInlineEdit(focusEditor = true): void {
  if (state.inlineEdit.loading) {
    void api()?.CancelInlineAI?.();
  }
  state.inlineEdit.open = false;
  state.inlineEdit.loading = false;
  state.inlineEdit.error = "";
  state.inlineEdit.result = null;
  renderInlineEditBar();
  if (focusEditor) {
    activeEditorView()?.focus();
  }
}

export async function runInlineEdit(): Promise<void> {
  const backend = api();
  const root = state.project?.root;
  const ed = state.inlineEdit;
  if (!backend?.InlineEdit || !root || !ed.open || ed.loading) return;
  const instr = ed.instruction.trim();
  if (!instr) return;

  const ctx = getCursorContext();
  const beforeCtx = ctx ? ctx.beforeContext : "";
  const afterCtx = ctx ? ctx.afterContext : "";

  ed.loading = true;
  ed.error = "";
  ed.result = null;
  renderInlineEditBar();

  try {
    const res = await backend.InlineEdit(
      root,
      ed.relPath,
      instr,
      ed.selectedText,
      ed.startLine,
      ed.endLine,
      beforeCtx,
      afterCtx,
      ""
    );
    if (!state.inlineEdit.open) return;
    ed.loading = false;
    ed.result = res;
    renderInlineEditBar();
  } catch (err: any) {
    if (!state.inlineEdit.open) return;
    ed.loading = false;
    const msg = err?.message || String(err);
    if (!/context canceled|canceled/i.test(msg)) {
      ed.error = msg;
    }
    renderInlineEditBar();
  }
}

export function acceptInlineEdit(): void {
  const ed = state.inlineEdit;
  if (!ed.open || !ed.result) return;
  const applied = replaceEditorRange(ed.from, ed.to, ed.result.replacement);
  if (applied) {
    setStatus(`Edição inline aplicada em ${ed.relPath} (+${ed.result.added} / -${ed.result.removed}).`);
  }
  closeInlineEdit(true);
}

export function renderInlineEditBar(): void {
  const layer = document.getElementById("editor-inline-ai-layer");
  if (!layer) return;
  const ed = state.inlineEdit;
  if (!ed.open) {
    layer.innerHTML = "";
    return;
  }

  const rangeBadge = ed.selectedText
    ? (ed.startLine === ed.endLine ? `L${ed.startLine}` : `L${ed.startLine}–L${ed.endLine}`)
    : `Inserir em L${ed.startLine}`;
  const modelTag = ed.result?.model || state.inlineAIStatus?.editModel || "";

  let previewHtml = "";
  if (ed.error) {
    previewHtml = `<div class="inline-edit-err">${escapeHtml(ed.error)}</div>`;
  } else if (ed.result) {
    const lines = ed.result.diffLines || [];
    previewHtml = `
      <div class="inline-edit-preview">
        <div class="inline-edit-preview-bar">
          <span class="dim">Prévia do diff</span>
          <span style="color:var(--green);font-family:var(--mono);font-size:11px">+${ed.result.added}</span>
          <span style="color:var(--red);font-family:var(--mono);font-size:11px">-${ed.result.removed}</span>
          ${modelTag ? `<span class="tok-pill">${escapeHtml(modelTag)}</span>` : ""}
          <div class="spacer"></div>
          <button type="button" class="mini" id="ie-retry">Refinar</button>
          <button type="button" class="mini danger" id="ie-reject">Rejeitar (Esc)</button>
          <button type="button" class="mini primary" id="ie-accept">${icons.check}<span>Aceitar (Ctrl+Enter)</span></button>
        </div>
        <div class="inline-edit-diff">
          ${lines.length === 0
            ? `<div class="dim" style="padding:6px 10px">Sem alterações em relação ao trecho selecionado.</div>`
            : lines.map((ln) => `<div class="diff-line ${escapeHtml(ln.kind)}">${escapeHtml(ln.text)}</div>`).join("")
          }
        </div>
      </div>
    `;
  }

  layer.innerHTML = `
    <div class="inline-edit-bar" role="dialog" aria-label="Edição Inline com IA (Ctrl+K)">
      <div class="inline-edit-row">
        <span class="tok-pill">${icons.sparkle}<span>Ctrl+K · ${escapeHtml(rangeBadge)}</span></span>
        <input
          id="inline-edit-input"
          class="inline-edit-input"
          placeholder="Descreva a alteração desejada… (Enter para gerar, Esc para fechar)"
          value="${escapeHtml(ed.instruction)}"
          ${ed.loading ? "disabled" : ""}
        />
        ${ed.loading
          ? `<button type="button" class="mini danger" id="ie-cancel">${icons.stop}<span>Parar</span></button>`
          : `<button type="button" class="mini primary" id="ie-generate">${icons.bolt}<span>Gerar</span></button>`
        }
        <button type="button" class="icon-btn" id="ie-close" title="Fechar (Esc)">${icons.closeSmall}</button>
      </div>
      ${previewHtml}
    </div>
  `;

  const input = document.getElementById("inline-edit-input") as HTMLInputElement | null;
  input?.addEventListener("input", () => {
    state.inlineEdit.instruction = input.value;
  });
  input?.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      e.preventDefault();
      closeInlineEdit(true);
    } else if (e.key === "Enter" && (e.ctrlKey || e.metaKey) && state.inlineEdit.result) {
      e.preventDefault();
      acceptInlineEdit();
    } else if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void runInlineEdit();
    }
  });
  document.getElementById("ie-generate")?.addEventListener("click", () => void runInlineEdit());
  document.getElementById("ie-cancel")?.addEventListener("click", () => {
    void api()?.CancelInlineAI?.();
    state.inlineEdit.loading = false;
    renderInlineEditBar();
  });
  document.getElementById("ie-close")?.addEventListener("click", () => closeInlineEdit(true));
  document.getElementById("ie-accept")?.addEventListener("click", () => acceptInlineEdit());
  document.getElementById("ie-reject")?.addEventListener("click", () => closeInlineEdit(true));
  document.getElementById("ie-retry")?.addEventListener("click", () => {
    state.inlineEdit.result = null;
    renderInlineEditBar();
    (document.getElementById("inline-edit-input") as HTMLInputElement | null)?.focus();
  });
}

// ==================================================
// Revisão Hunk-a-Hunk no Editor (Diff-Apply Agente)
// ==================================================

/**
 * Converte um FileDiff em decorações CodeMirror 6 (widget acima do hunk com linhas
 * removidas + botões Aceitar/Rejeitar, e destaque verde nas linhas adicionadas).
 * Função pura, testável em Vitest sem DOM.
 */
export function buildInlineDiffDecorations(diff: FileDiff | null, totalDocLines: number): {
  parsedHunks: DiffHunkParsed[];
  decorations: InlineDiffHunkDecor[];
} {
  if (!diff || totalDocLines <= 0) {
    return { parsedHunks: [], decorations: [] };
  }
  const { hunks } = parseDiffHunks(diff);
  const decorations: InlineDiffHunkDecor[] = [];

  for (let idx = 0; idx < hunks.length; idx++) {
    const h = hunks[idx];
    let curNew = Math.max(1, h.newStart);
    const addedLines: number[] = [];
    const removedLines: string[] = [];
    let firstChangeLine = 0;

    for (const ln of h.lines) {
      if (ln.kind === "context") {
        curNew++;
      } else if (ln.kind === "added") {
        if (firstChangeLine === 0) firstChangeLine = curNew;
        if (curNew <= totalDocLines) addedLines.push(curNew);
        curNew++;
      } else if (ln.kind === "removed") {
        if (firstChangeLine === 0) firstChangeLine = curNew;
        removedLines.push(ln.text.startsWith("-") ? ln.text.slice(1) : ln.text);
      }
    }

    if (addedLines.length === 0 && removedLines.length === 0) continue;
    const anchorLine = Math.max(1, Math.min(firstChangeLine || Math.max(1, h.newStart), totalDocLines));
    decorations.push({
      index: idx,
      header: h.header,
      anchorLine,
      addedLines,
      removedLines,
      addedCount: addedLines.length,
      removedCount: removedLines.length,
    });
  }

  return { parsedHunks: hunks, decorations };
}

/**
 * Busca os hunks pendentes (unstaged) do arquivo aberto e renderiza a barra +
 * decorações hunk-a-hunk diretamente no CodeMirror 6.
 */
export async function syncEditorInlineReview(relPath: string): Promise<void> {
  const backend = api();
  const root = state.project?.root;
  const view = activeEditorView();
  if (!backend?.GetFileUnstagedDiff || !root || !view || state.activeEditorPath !== relPath) {
    state.inlineReviewDiff = null;
    clearInlineDiffHunks();
    renderEditorReviewBar();
    return;
  }

  try {
    const diff = await backend.GetFileUnstagedDiff(root, relPath);
    if (state.activeEditorPath !== relPath) return;
    state.inlineReviewDiff = diff && diff.lines && diff.lines.length > 0 ? diff : null;
    applyCurrentInlineReviewDecorations(relPath);
    renderEditorReviewBar();
  } catch {
    state.inlineReviewDiff = null;
    clearInlineDiffHunks();
    renderEditorReviewBar();
  }
}

function applyCurrentInlineReviewDecorations(relPath: string): void {
  const view = activeEditorView();
  if (!view || !state.inlineReviewDiff || !state.inlineReviewEnabled) {
    clearInlineDiffHunks();
    return;
  }
  const { parsedHunks, decorations } = buildInlineDiffDecorations(state.inlineReviewDiff, view.state.doc.lines);
  if (decorations.length === 0) {
    clearInlineDiffHunks();
    return;
  }
  setInlineDiffHunks(decorations, {
    onAcceptHunk: (idx) => {
      const h = parsedHunks[idx];
      if (h) void acceptInlineHunk(relPath, h);
    },
    onRejectHunk: (idx) => {
      const h = parsedHunks[idx];
      if (h) void rejectInlineHunk(relPath, h);
    },
  });
}

async function acceptInlineHunk(relPath: string, hunk: DiffHunkParsed): Promise<void> {
  const backend = api();
  const root = state.project?.root;
  if (!backend?.StageHunk || !root) return;
  try {
    await backend.StageHunk(root, relPath, hunk.header, hunk.lines.map((l) => l.text));
    setStatus(`Bloco aceito (staged) em ${relPath}.`);
    await syncEditorInlineReview(relPath);
    await refreshDiffs();
  } catch (err: any) {
    toastError(`aceitar o bloco em ${relPath}`, err);
  }
}

async function rejectInlineHunk(relPath: string, hunk: DiffHunkParsed): Promise<void> {
  const backend = api();
  const root = state.project?.root;
  if (!backend || !root) return;
  try {
    await backend.RevertHunk(root, relPath, hunk.header, hunk.lines.map((l) => l.text));
    const fresh = await backend.ReadFile(root, relPath).catch(() => null);
    const tab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === relPath);
    if (fresh !== null && tab) {
      tab.content = fresh;
      tab.dirty = false;
      syncCodeMirrorDoc(relPath, fresh);
    }
    setStatus(`Bloco rejeitado e revertido em ${relPath}.`);
    await syncEditorInlineReview(relPath);
    await refreshDiffs();
  } catch (err: any) {
    toastError(`rejeitar o bloco em ${relPath}`, err);
  }
}

export function renderEditorReviewBar(): void {
  const bar = document.getElementById("editor-review-bar");
  if (!bar) return;
  const diff = state.inlineReviewDiff;
  const activeTab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
  if (!diff || !activeTab) {
    bar.innerHTML = "";
    return;
  }
  const { hunks } = parseDiffHunks(diff);
  if (hunks.length === 0) {
    bar.innerHTML = "";
    return;
  }
  const relPath = activeTab.relPath;
  bar.innerHTML = `
    <div class="editor-review-banner">
      <span class="tok-pill">${icons.gitSmall}<span>${hunks.length} bloco(s) pendente(s)</span></span>
      <span style="color:var(--green);font-family:var(--mono);font-size:11px">+${diff.added}</span>
      <span style="color:var(--red);font-family:var(--mono);font-size:11px">-${diff.removed}</span>
      <span class="dim" style="font-size:11.5px">Revise bloco a bloco direto no código</span>
      <div class="spacer"></div>
      <button type="button" class="mini ${state.inlineReviewEnabled ? "primary" : ""}" id="btn-review-toggle">${state.inlineReviewEnabled ? "Ocultar Blocos" : "Mostrar Blocos"}</button>
      <button type="button" class="mini primary" id="btn-review-accept-all">${icons.check}<span>Aceitar Arquivo</span></button>
      <button type="button" class="mini danger" id="btn-review-reject-all">${icons.trash}<span>Descartar Arquivo</span></button>
    </div>
  `;

  document.getElementById("btn-review-toggle")?.addEventListener("click", () => {
    state.inlineReviewEnabled = !state.inlineReviewEnabled;
    applyCurrentInlineReviewDecorations(relPath);
    renderEditorReviewBar();
  });
  document.getElementById("btn-review-accept-all")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      await backend.StageFile(state.project.root, relPath, true);
      setStatus(`Todas as alterações de ${relPath} foram aceitas (staged).`);
      await syncEditorInlineReview(relPath);
      await refreshDiffs();
    } catch (err: any) {
      toastError(`aceitar ${relPath}`, err);
    }
  });
  document.getElementById("btn-review-reject-all")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      await backend.DiscardFile(state.project.root, relPath);
      const fresh = await backend.ReadFile(state.project.root, relPath).catch(() => null);
      if (fresh !== null) {
        activeTab.content = fresh;
        activeTab.dirty = false;
        syncCodeMirrorDoc(relPath, fresh);
      }
      setStatus(`Alterações descartadas em ${relPath}.`);
      await syncEditorInlineReview(relPath);
      await refreshDiffs();
    } catch (err: any) {
      toastError(`descartar ${relPath}`, err);
    }
  });
}
