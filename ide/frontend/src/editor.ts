import { fileBadge, icons } from "./icons";
import { renderMarkdownLite } from "./markdown";
import type { CommitDetail, EditorTab, FileDiff } from "./types";
import { escapeHtml } from "./utils";
import { highlightActiveTreeRow, refreshExplorerTree } from "./explorer";
import { refreshDiffs } from "./git";
import { openCommandPalette, openQuickOpen } from "./quickopen";
import { bindResidentViewEvents, renderResidentViewHtml } from "./resident";
import { bindIdeSettingsEvents, renderIdeSettingsPageHtml } from "./ide-settings";
import { renderProjectTabsBar, syncActivityButtons, syncChatExpandLayout, syncLayoutVisibility } from "./shell";
import { api, askConfirm, setStatus, state, toastError } from "./state";
import { bindAIUsagePageEvents, renderAIUsagePageHtml } from "./modals";
import { bindMissionBoardEvents, bindTeamConfigEvents, openMissionControlTab, openTeamConfigTab, openTeamMemoryFile, renderMissionBoardHtml, renderTeamConfigPageHtml } from "./teamconfig";
import { activeEditorView, currentSelection, forgetTabState, mountCodeMirror, selectRange, syncCodeMirrorDoc, unmountCodeMirror } from "./cm-editor";
import { autoResizeTextarea } from "./chat-timeline";
import { closeInlineEdit, ghostButtonTitle, openInlineEdit, renderInlineEditBar, scheduleGhostText, syncEditorInlineReview, toggleGhostText } from "./inline-ai";
import { bindFileGitInspectEvents, openFileGitInspect, renderFileGitInspectPanelHtml } from "./git-history";
import { applyDiagnosticsToActiveEditor, notifyLSPDidClose, notifyLSPDidOpen, notifyLSPDidSave, scheduleLSPDidChange } from "./lsp";

export async function reloadCleanOpenTabs() {
  const backend = api();
  if (!backend || !state.project) return;
  let activeChanged = false;
  for (const tab of state.editorTabs) {
    if (tab.kind !== "file" || tab.dirty) continue;
    try {
      const fresh = await backend.ReadFile(state.project.root, tab.relPath);
      if (fresh !== tab.content) {
        tab.content = fresh;
        if (tab.relPath === state.activeEditorPath) {
          // Com CodeMirror, substituir só o doc preserva a view montada (sem re-render integral).
          activeChanged = !syncCodeMirrorDoc(tab.relPath, fresh);
        }
      }
    } catch {
      // File may have been removed or moved
    }
  }
  if (activeChanged) {
    renderEditorBody();
  } else if (state.activeEditorPath) {
    const activeTab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
    if (activeTab) void syncEditorInlineReview(activeTab.relPath);
  }
}

export function promptDiscardChanges(targetRepoRoot?: string, targetRepoName?: string) {
  if (!state.project) return;
  const repoRoot = targetRepoRoot || state.activeGitRepoRoot || state.project.root;
  const suffix = targetRepoName ? ` em "${targetRepoName}"` : "";
  askConfirm({
    title: targetRepoName ? `Descartar Alterações (${targetRepoName})` : "Descartar Todas as Alterações",
    body: `Isso reverterá arquivos rastreados${suffix} para HEAD e removerá arquivos novos não rastreados (mantendo .hivemind/). Deseja continuar?`,
    confirmLabel: "Descartar Alterações",
    danger: true,
    onConfirm: async () => {
      if (!state.project) return;
      try {
        await api()?.DiscardChanges(repoRoot);
        setStatus(targetRepoName ? `Alterações descartadas em ${targetRepoName}.` : "Alterações descartadas.");
        await refreshExplorerTree();
        await reloadCleanOpenTabs();
        await refreshDiffs();
      } catch (err: any) {
        toastError("descartar as alterações", err);
      }
    },
  });
}

export function jumpEditorToLine(targetLine?: number, targetCol?: number) {
  if (!targetLine || targetLine <= 0) return;
  const view = activeEditorView();
  if (!view) return;
  const doc = view.state.doc;
  const clamped = Math.min(targetLine, doc.lines);
  const line = doc.line(clamped);
  if (targetCol && targetCol > 1) {
    const pos = Math.min(line.from + targetCol - 1, line.to);
    selectRange(pos, pos);
    return;
  }
  selectRange(line.from, line.to);
}

export async function openFileInEditor(relPath: string, name: string, targetLine?: number, targetCol?: number) {
  const existing = state.editorTabs.find((t) => t.kind === "file" && t.relPath === relPath);
  if (existing) {
    if (targetLine && /\.md$/i.test(existing.name)) existing.mdView = "source";
    state.activeEditorPath = existing.relPath;
    renderEditorTabs();
    renderEditorBody();
    highlightActiveTreeRow();
    jumpEditorToLine(targetLine, targetCol);
    return;
  }
  const backend = api();
  if (!backend || !state.project) return;
  try {
    const content = await backend.ReadFile(state.project.root, relPath);
    state.editorTabs.push({
      kind: "file",
      relPath,
      name,
      content,
      dirty: false,
      mdView: /\.md$/i.test(name) ? (targetLine ? "source" : "preview") : "source",
    });
    state.activeEditorPath = relPath;
    renderEditorTabs();
    renderEditorBody();
    highlightActiveTreeRow();
    jumpEditorToLine(targetLine, targetCol);
  } catch (err: any) {
    toastError(`abrir o arquivo ${relPath}`, err);
  }
}

export function sendEditorSelectionToChief() {
  const activeTab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
  if (!activeTab) return;
  let snippet = `@${activeTab.relPath} `;
  const sel = currentSelection();
  if (sel) {
    const view = activeEditorView()!;
    const doc = view.state.doc;
    const startLine = doc.lineAt(sel.start).number;
    if (sel.end > sel.start) {
      const endLine = doc.lineAt(sel.end).number;
      const ext = activeTab.name.includes(".") ? activeTab.name.split(".").pop() || "" : "";
      snippet = `@${activeTab.relPath}:L${startLine}-L${endLine}\n\`\`\`${ext}\n${sel.text}\n\`\`\`\n`;
    } else {
      snippet = `@${activeTab.relPath}:L${startLine} `;
    }
  }
  const prefix = state.draftObjective && !state.draftObjective.endsWith("\n") && !state.draftObjective.endsWith(" ")
    ? state.draftObjective + "\n"
    : state.draftObjective;
  state.draftObjective = prefix + snippet;
  state.showRightAI = true;
  syncLayoutVisibility();
  const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
  if (chatInput) {
    chatInput.value = state.draftObjective;
    autoResizeTextarea(chatInput);
    chatInput.focus();
    chatInput.selectionStart = chatInput.selectionEnd = chatInput.value.length;
  }
  setStatus(`Trecho de ${activeTab.name} anexado ao chat.`);
}

export function openDiffInEditor(diff: FileDiff, repoRoot?: string, repoRelPath?: string) {
  const effectiveRepoRoot = repoRoot || state.project?.root || "";
  const workspaceRelPath = repoRelPath ? `${repoRelPath}/${diff.path}` : diff.path;
  const key = `diff:${workspaceRelPath}`;
  const existing = state.editorTabs.find((t) => t.relPath === key);
  if (existing) {
    existing.diff = diff;
    existing.repoRoot = effectiveRepoRoot;
    existing.workspaceRelPath = workspaceRelPath;
    state.activeEditorPath = key;
    renderEditorTabs();
    renderEditorBody();
    return;
  }
  state.editorTabs.push({
    kind: "diff",
    relPath: key,
    name: `Diff: ${diff.path.split("/").pop()}`,
    content: "",
    dirty: false,
    diff,
    repoRoot: effectiveRepoRoot,
    workspaceRelPath,
  });
  state.activeEditorPath = key;
  renderEditorTabs();
  renderEditorBody();
}

export async function saveActiveEditor() {
  const tab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
  const backend = api();
  if (!tab || !backend || !state.project) return;
  try {
    await backend.SaveFile(state.project.root, tab.relPath, tab.content);
    tab.dirty = false;
    syncActiveTabDirtyUI(tab);
    setStatus(`Salvo: ${tab.relPath}`);
    notifyLSPDidSave(tab.relPath, tab.content);
    refreshDiffs();
    void syncEditorInlineReview(tab.relPath);
  } catch (err: any) {
    toastError("salvar o arquivo", err, () => { void saveActiveEditor(); });
  }
}

export function renderEditorTabs() {
  const tabsEl = document.getElementById("editor-tabs");
  if (!tabsEl) return;
  tabsEl.innerHTML = state.editorTabs.map((t) => {
    const tabIcon =
      t.kind === "resident"
        ? icons.sparkle
        : t.kind === "mission"
          ? icons.swarm
          : t.kind === "team-config"
            ? icons.team
            : t.kind === "ide-settings"
              ? icons.settings
              : t.kind === "ai-usage"
                ? icons.aiUsage
                : t.kind === "diff" || t.kind === "commit"
                  ? icons.gitSmall
                  : fileBadge(t.name);
    return `
      <div class="editor-tab ${t.relPath === state.activeEditorPath ? "active" : ""}" data-tab="${escapeHtml(t.relPath)}">
        ${tabIcon}
        <span>${escapeHtml(t.name)}</span>
        <span class="dirty-dot" data-dirty-for="${escapeHtml(t.relPath)}" style="display:${t.dirty ? "inline-flex" : "none"}">${icons.dot}</span>
        <span class="close-x" data-close-tab="${escapeHtml(t.relPath)}">${icons.closeSmall}</span>
      </div>
    `;
  }).join("");

  tabsEl.querySelectorAll("[data-tab]").forEach((el) => {
    el.addEventListener("click", () => {
      state.activeEditorPath = (el as HTMLElement).dataset.tab!;
      renderEditorTabs();
      renderEditorBody();
      highlightActiveTreeRow();
    });
  });
  tabsEl.querySelectorAll("[data-close-tab]").forEach((el) => {
    el.addEventListener("click", (ev) => {
      ev.stopPropagation();
      const path = (el as HTMLElement).dataset.closeTab!;
      const closing = state.editorTabs.find((t) => t.relPath === path);
      const doClose = () => {
        state.editorTabs = state.editorTabs.filter((t) => t.relPath !== path);
        if (closing?.kind === "file") {
          forgetTabState(path);
          notifyLSPDidClose(path);
        }
        if (state.activeEditorPath === path) {
          state.activeEditorPath = state.editorTabs[0]?.relPath || "";
        }
        renderEditorTabs();
        renderEditorBody();
        highlightActiveTreeRow();
      };
      if (closing?.kind === "file" && closing.dirty) {
        askConfirm({
          title: "Fechar Aba sem Salvar",
          body: `O arquivo "${closing.name}" possui alterações não salvas. Deseja fechá-lo sem salvar?`,
          confirmLabel: "Fechar sem salvar",
          danger: true,
          onConfirm: doClose,
        });
        return;
      }
      doClose();
    });
  });
  syncChatExpandLayout();
  syncActivityButtons();
}

export function syncActiveTabDirtyUI(tab: EditorTab) {
  const saveBtn = document.getElementById("btn-save-file") as HTMLButtonElement | null;
  if (saveBtn) saveBtn.disabled = !tab.dirty;
  document.querySelectorAll<HTMLElement>("[data-dirty-for]").forEach((dot) => {
    if (dot.dataset.dirtyFor === tab.relPath) {
      dot.style.display = tab.dirty ? "inline-flex" : "none";
    }
  });
  renderProjectTabsBar();
}

export function renderLineNumbers(text: string): string {
  const count = Math.max(1, text.split("\n").length);
  const lines: string[] = [];
  for (let i = 1; i <= count; i++) lines.push(String(i));
  return lines.join("\n");
}

export interface DiffHunkParsed {
  header: string;
  oldStart: number;
  newStart: number;
  lines: Array<{ kind: string; text: string }>;
}

export function parseDiffHunks(diff: FileDiff): { headers: string[]; hunks: DiffHunkParsed[] } {
  const headers: string[] = [];
  const hunks: DiffHunkParsed[] = [];
  let cur: DiffHunkParsed | null = null;
  for (const ln of diff.lines) {
    if (ln.kind === "header") {
      if (!cur) headers.push(ln.text);
      continue;
    }
    if (ln.kind === "hunk") {
      const m = ln.text.match(/@@\s*-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s*@@/);
      cur = {
        header: ln.text,
        oldStart: m ? parseInt(m[1], 10) : 1,
        newStart: m ? parseInt(m[2], 10) : 1,
        lines: [],
      };
      hunks.push(cur);
      continue;
    }
    if (!cur) {
      cur = { header: "@@ -1 +1 @@", oldStart: 1, newStart: 1, lines: [] };
      hunks.push(cur);
    }
    cur.lines.push(ln);
  }
  return { headers, hunks };
}

export function renderUnifiedDiffHtml(hunks: DiffHunkParsed[], opts?: { readOnly?: boolean }): string {
  if (hunks.length === 0) {
    return `<div class="dim" style="padding:12px">Sem blocos de diff para exibir.</div>`;
  }
  const readOnly = Boolean(opts?.readOnly);
  return hunks
    .map((h, idx) => {
      let oldLine = h.oldStart;
      let newLine = h.newStart;
      const linesHtml = h.lines
        .map((ln) => {
          let oldNum = "";
          let newNum = "";
          if (ln.kind === "removed") {
            oldNum = String(oldLine++);
          } else if (ln.kind === "added") {
            newNum = String(newLine++);
          } else {
            oldNum = String(oldLine++);
            newNum = String(newLine++);
          }
          return `<div class="diff-line ${ln.kind}"><span class="diff-ln-num">${oldNum}</span><span class="diff-ln-num">${newNum}</span><span class="diff-ln-code">${escapeHtml(ln.text)}</span></div>`;
        })
        .join("");
      return `
      <div class="diff-hunk-bar">
        <span>${escapeHtml(h.header)}</span>
        ${readOnly ? "" : `<button class="diff-hunk-revert" data-revert-hunk="${idx}" title="Reverter somente este bloco (git apply --reverse)">Reverter bloco</button>`}
      </div>
      ${linesHtml}
    `;
    })
    .join("");
}

export function renderSplitDiffHtml(hunks: DiffHunkParsed[], opts?: { readOnly?: boolean }): string {
  if (hunks.length === 0) {
    return `<div class="dim" style="padding:12px">Sem blocos de diff para exibir.</div>`;
  }
  const readOnly = Boolean(opts?.readOnly);
  const sections: string[] = [];
  for (let hIdx = 0; hIdx < hunks.length; hIdx++) {
    const h = hunks[hIdx];
    let oldLine = h.oldStart;
    let newLine = h.newStart;
    const rows: string[] = [];
    let i = 0;
    while (i < h.lines.length) {
      const ln = h.lines[i];
      if (ln.kind === "context") {
        const body = ln.text.startsWith(" ") ? ln.text.slice(1) : ln.text;
        rows.push(`
          <tr>
            <td class="diff-split-num">${oldLine++}</td>
            <td class="diff-split-code">${escapeHtml(body)}</td>
            <td class="diff-split-num">${newLine++}</td>
            <td class="diff-split-code">${escapeHtml(body)}</td>
          </tr>
        `);
        i++;
        continue;
      }
      const removedBatch: string[] = [];
      const addedBatch: string[] = [];
      while (i < h.lines.length && h.lines[i].kind === "removed") {
        removedBatch.push(h.lines[i].text.slice(1));
        i++;
      }
      while (i < h.lines.length && h.lines[i].kind === "added") {
        addedBatch.push(h.lines[i].text.slice(1));
        i++;
      }
      const maxLen = Math.max(removedBatch.length, addedBatch.length);
      if (maxLen === 0) {
        i++;
        continue;
      }
      for (let k = 0; k < maxLen; k++) {
        const hasOld = k < removedBatch.length;
        const hasNew = k < addedBatch.length;
        rows.push(`
          <tr>
            <td class="diff-split-num">${hasOld ? oldLine++ : ""}</td>
            <td class="diff-split-code ${hasOld ? "removed" : "empty"}">${hasOld ? escapeHtml(removedBatch[k]) : ""}</td>
            <td class="diff-split-num">${hasNew ? newLine++ : ""}</td>
            <td class="diff-split-code ${hasNew ? "added" : "empty"}">${hasNew ? escapeHtml(addedBatch[k]) : ""}</td>
          </tr>
        `);
      }
    }
    sections.push(`
      <div class="diff-hunk-bar">
        <span>${escapeHtml(h.header)}</span>
        ${readOnly ? "" : `<button class="diff-hunk-revert" data-revert-hunk="${hIdx}" title="Reverter somente este bloco (git apply --reverse)">Reverter bloco</button>`}
      </div>
      <table class="diff-split-table"><tbody>${rows.join("")}</tbody></table>
    `);
  }
  return `<div class="diff-split-wrap">${sections.join("")}</div>`;
}

function splitCommitFilePath(rawPath: string): { fileName: string; dirPart: string } {
  const norm = rawPath.replace(/\\/g, "/");
  const idx = norm.lastIndexOf("/");
  if (idx < 0) return { fileName: norm, dirPart: "" };
  return { fileName: norm.slice(idx + 1), dirPart: norm.slice(0, idx) };
}

export function renderCommitDetailHtml(cd: CommitDetail, diffMode: "unified" | "split" = "unified"): string {
  const totalAdded = cd.files.reduce((sum, f) => sum + (f.added || 0), 0);
  const totalRemoved = cd.files.reduce((sum, f) => sum + (f.removed || 0), 0);
  const isSplit = diffMode === "split";
  const expandLimit = cd.files.length <= 6 ? cd.files.length : 3;

  return `
    <div class="commit-view-page">
      <div class="commit-header-card">
        <div class="tc-title-row">
          ${icons.commit}
          <h2>${escapeHtml(cd.subject)}</h2>
          <span class="tok-pill">${escapeHtml(cd.short)}</span>
        </div>
        <div class="dim">${escapeHtml(cd.author)} · ${escapeHtml(cd.date)} · ${cd.files.length} arquivo(s) alterado(s) · <span class="commit-stat-add">+${totalAdded}</span> <span class="commit-stat-rem">-${totalRemoved}</span></div>
        ${cd.body ? `<pre class="commit-body-pre">${escapeHtml(cd.body)}</pre>` : ""}
        <div class="commit-toolbar">
          <button type="button" class="mini ${!isSplit ? "primary" : ""}" id="btn-commit-diff-unified">Unificado</button>
          <button type="button" class="mini ${isSplit ? "primary" : ""}" id="btn-commit-diff-split">Lado a Lado</button>
          <span class="spacer"></span>
          <button type="button" class="mini ghost" id="btn-commit-expand-all">Expandir todos</button>
          <button type="button" class="mini ghost" id="btn-commit-collapse-all">Recolher todos</button>
        </div>
      </div>
      ${cd.files
        .map((f, idx) => {
          const { hunks } = parseDiffHunks(f);
          const { fileName, dirPart } = splitCommitFilePath(f.path);
          const autoOpen = idx < expandLimit;
          const bodyHtml = isSplit
            ? renderSplitDiffHtml(hunks, { readOnly: true })
            : `<div class="diff-lines commit-diff-lines">${renderUnifiedDiffHtml(hunks, { readOnly: true })}</div>`;
          return `
            <details class="commit-file-card" ${autoOpen ? "open" : ""}>
              <summary class="commit-file-head" title="${escapeHtml(f.path)}">
                <span class="commit-file-chev">${icons.chevronRight}</span>
                <span class="commit-file-path">
                  <span class="commit-file-name">${escapeHtml(fileName)}</span>
                  ${dirPart ? `<span class="commit-file-dir">${escapeHtml(dirPart)}</span>` : ""}
                </span>
                <span class="commit-file-stats">
                  <span class="is-add">+${f.added}</span>
                  <span class="is-rem">-${f.removed}</span>
                </span>
                <button type="button" class="mini ghost commit-open-file-btn" data-commit-open-file="${escapeHtml(f.path)}" title="Abrir arquivo atual no editor">Abrir</button>
              </summary>
              <div class="commit-file-body">${bodyHtml}</div>
            </details>
          `;
        })
        .join("")}
    </div>
  `;
}

export function countLinesFast(text: string): number {
  let count = 1;
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10) count++;
  }
  return count;
}

export function openEditorFind(showReplace: boolean) {
  const sel = currentSelection();
  if (sel && sel.text && !sel.text.includes("\n") && sel.text.length <= 120) {
    state.editorFind.query = sel.text;
  }
  state.editorFind.open = true;
  state.editorFind.showReplace = showReplace;
  renderEditorFindBar();
  recomputeEditorFindMatches(false);
  const input = document.getElementById("editor-find-query") as HTMLInputElement | null;
  if (input) {
    input.focus();
    input.select();
  }
}

export function recomputeEditorFindMatches(selectMatch = true) {
  const view = activeEditorView();
  const q = state.editorFind.query;
  state.editorFind.matches = [];
  if (!view || !q) {
    state.editorFind.matchIndex = 0;
    updateEditorFindCounter();
    return;
  }
  const hay = view.state.doc.toString().toLowerCase();
  const needle = q.toLowerCase();
  let pos = 0;
  while (pos <= hay.length - needle.length) {
    const idx = hay.indexOf(needle, pos);
    if (idx === -1) break;
    state.editorFind.matches.push({ start: idx, end: idx + needle.length });
    pos = idx + Math.max(1, needle.length);
    if (state.editorFind.matches.length >= 2000) break;
  }
  if (state.editorFind.matches.length === 0) {
    state.editorFind.matchIndex = 0;
  } else if (state.editorFind.matchIndex >= state.editorFind.matches.length) {
    state.editorFind.matchIndex = 0;
  }
  updateEditorFindCounter();
  if (selectMatch && state.editorFind.matches.length > 0) {
    focusCurrentEditorMatch();
  }
}

export function updateEditorFindCounter() {
  const counterEl = document.getElementById("editor-find-count");
  if (!counterEl) return;
  const total = state.editorFind.matches.length;
  if (!state.editorFind.query) {
    counterEl.textContent = "0 de 0";
  } else if (total === 0) {
    counterEl.textContent = "Sem ocorr.";
  } else {
    counterEl.textContent = `${state.editorFind.matchIndex + 1} de ${total}`;
  }
}

export function focusCurrentEditorMatch() {
  const m = state.editorFind.matches[state.editorFind.matchIndex];
  if (!m) return;
  selectRange(m.start, m.end);
}

export function stepEditorFind(delta: number) {
  const total = state.editorFind.matches.length;
  if (total === 0) return;
  state.editorFind.matchIndex = (state.editorFind.matchIndex + delta + total) % total;
  updateEditorFindCounter();
  focusCurrentEditorMatch();
}

export function replaceEditorCurrentMatch() {
  const view = activeEditorView();
  const m = state.editorFind.matches[state.editorFind.matchIndex];
  if (!view || !m) return;
  const rep = state.editorFind.replaceText;
  view.dispatch({
    changes: { from: m.start, to: m.end, insert: rep },
    selection: { anchor: m.start + rep.length },
    scrollIntoView: true,
  });
  recomputeEditorFindMatches(true);
}

export function replaceEditorAllMatches() {
  const view = activeEditorView();
  const q = state.editorFind.query;
  if (!view || !q || state.editorFind.matches.length === 0) return;
  const count = state.editorFind.matches.length;
  const rep = state.editorFind.replaceText;
  const changes = state.editorFind.matches.map((m) => ({ from: m.start, to: m.end, insert: rep }));
  view.dispatch({ changes, scrollIntoView: true });
  recomputeEditorFindMatches(false);
  setStatus(`${count} ocorrência(s) substituída(s).`);
}

export function renderEditorFindBar() {
  const layer = document.getElementById("editor-find-layer");
  if (!layer) return;
  if (!state.editorFind.open) {
    layer.innerHTML = "";
    return;
  }
  const total = state.editorFind.matches.length;
  const countLabel = !state.editorFind.query
    ? "0 de 0"
    : total === 0
      ? "Sem ocorr."
      : `${state.editorFind.matchIndex + 1} de ${total}`;

  layer.innerHTML = `
    <div class="editor-find-bar">
      <div class="editor-find-row">
        <button class="mini ghost" id="ef-toggle-replace" title="Mostrar/ocultar Substituir (Ctrl+H)" style="padding:2px 5px">
          ${state.editorFind.showReplace ? icons.chevronDown : icons.chevronRight}
        </button>
        <input class="editor-find-input" id="editor-find-query" placeholder="Localizar no arquivo (Ctrl+F)…" value="${escapeHtml(state.editorFind.query)}" />
        <span class="editor-find-count" id="editor-find-count">${countLabel}</span>
        <button class="mini" id="ef-prev" title="Anterior (Shift+Enter)">↑</button>
        <button class="mini" id="ef-next" title="Próximo (Enter)">↓</button>
        <button class="icon-btn" id="ef-close" title="Fechar (Esc)">${icons.closeSmall}</button>
      </div>
      ${state.editorFind.showReplace ? `
        <div class="editor-find-row" style="padding-left:24px">
          <input class="editor-find-input" id="editor-find-replace" placeholder="Substituir por…" value="${escapeHtml(state.editorFind.replaceText)}" />
          <button class="mini" id="ef-replace-one" title="Substituir atual">Substituir</button>
          <button class="mini primary" id="ef-replace-all" title="Substituir todas">Tudo</button>
        </div>
      ` : ""}
    </div>
  `;

  const qIn = document.getElementById("editor-find-query") as HTMLInputElement | null;
  const rIn = document.getElementById("editor-find-replace") as HTMLInputElement | null;
  qIn?.addEventListener("input", () => {
    state.editorFind.query = qIn.value;
    state.editorFind.matchIndex = 0;
    recomputeEditorFindMatches(true);
  });
  qIn?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      stepEditorFind(e.shiftKey ? -1 : 1);
    } else if (e.key === "Escape") {
      e.preventDefault();
      state.editorFind.open = false;
      renderEditorFindBar();
      activeEditorView()?.focus();
    }
  });
  rIn?.addEventListener("input", () => {
    state.editorFind.replaceText = rIn.value;
  });
  rIn?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      replaceEditorCurrentMatch();
    }
  });
  document.getElementById("ef-toggle-replace")?.addEventListener("click", () => {
    state.editorFind.showReplace = !state.editorFind.showReplace;
    renderEditorFindBar();
  });
  document.getElementById("ef-prev")?.addEventListener("click", () => stepEditorFind(-1));
  document.getElementById("ef-next")?.addEventListener("click", () => stepEditorFind(1));
  document.getElementById("ef-replace-one")?.addEventListener("click", () => replaceEditorCurrentMatch());
  document.getElementById("ef-replace-all")?.addEventListener("click", () => replaceEditorAllMatches());
  document.getElementById("ef-close")?.addEventListener("click", () => {
    state.editorFind.open = false;
    renderEditorFindBar();
  });
}

export function renderEditorBody() {
  const body = document.getElementById("editor-body");
  if (!body) return;
  const activeTab = state.editorTabs.find((t) => t.relPath === state.activeEditorPath);
  const isFileSource =
    !!activeTab && activeTab.kind === "file" && !(/\.md$/i.test(activeTab.name) && activeTab.mdView !== "source");
  const isMdFile = !!activeTab && activeTab.kind === "file" && /\.md$/i.test(activeTab.name);
  // Views CodeMirror só existem na aba de arquivo em modo código; todo o resto desmonta.
  if (!isFileSource) {
    unmountCodeMirror();
  }

  if (!activeTab) {
    const proj = state.project;
    const branchTag = proj?.branch ? `<span class="welcome-meta-pill">${icons.gitSmall}<span>${escapeHtml(proj.branch)}</span></span>` : "";
    const stackTag = proj?.stack?.summary ? `<span class="welcome-meta-pill">${escapeHtml(proj.stack.summary)}</span>` : "";
    body.innerHTML = `
      <div class="empty-welcome">
        <div class="welcome-hero">
          <div class="welcome-hero-logo">${icons.brandHero}</div>
          <h1 class="welcome-title">${proj ? escapeHtml(proj.name) : "HiveMind IDE"}</h1>
          <p class="welcome-subtitle">${proj ? "Abra um arquivo, acompanhe sua equipe em tempo real ou descreva o próximo passo no painel ao lado." : "Abra uma pasta do seu computador para começar a programar com sua equipe de IA."}</p>
          ${(branchTag || stackTag) ? `<div class="welcome-meta-row">${branchTag}${stackTag}</div>` : ""}
        </div>

        <div class="welcome-cards">
          <button type="button" class="welcome-card is-primary" id="welcome-quick-open">
            <span class="welcome-card-icon">${icons.search}</span>
            <span class="welcome-card-body">
              <span class="welcome-card-title">Abrir arquivo rápido <span class="kbd">Ctrl+P</span></span>
              <span class="welcome-card-desc">Vá direto para qualquer arquivo do projeto sem navegar pastas.</span>
            </span>
          </button>
          <button type="button" class="welcome-card" id="welcome-mission">
            <span class="welcome-card-icon">${icons.swarm}</span>
            <span class="welcome-card-body">
              <span class="welcome-card-title">Quadro da Equipe <span class="kbd">Ctrl+Shift+A</span></span>
              <span class="welcome-card-desc">Veja agentes trabalhando ao vivo, modelos e uso de tokens.</span>
            </span>
          </button>
          <button type="button" class="welcome-card" id="welcome-team-config">
            <span class="welcome-card-icon">${icons.settings}</span>
            <span class="welcome-card-body">
              <span class="welcome-card-title">Configurar Equipe & Skills</span>
              <span class="welcome-card-desc">Escolha modelos, nível de autonomia, Jira e skills do projeto.</span>
            </span>
          </button>
          <button type="button" class="welcome-card" id="welcome-memory">
            <span class="welcome-card-icon">${icons.memory}</span>
            <span class="welcome-card-body">
              <span class="welcome-card-title">Memória do Projeto</span>
              <span class="welcome-card-desc">Regras, padrões e contexto que toda a equipe lembra sempre.</span>
            </span>
          </button>
        </div>

        <div class="welcome-shortcuts-strip">
          <button type="button" class="welcome-shortcut-pill" id="welcome-cmd-palette"><span>Comandos</span><span class="kbd">Ctrl+K</span></button>
          <span class="welcome-shortcut-pill"><span>Busca no código</span><span class="kbd">Ctrl+Shift+F</span></span>
          <span class="welcome-shortcut-pill"><span>Enviar seleção à IA</span><span class="kbd">Ctrl+L</span></span>
          <span class="welcome-shortcut-pill"><span>Terminal</span><span class="kbd">Ctrl+J</span></span>
        </div>
      </div>
    `;
    document.getElementById("welcome-mission")?.addEventListener("click", () => openMissionControlTab());
    document.getElementById("welcome-team-config")?.addEventListener("click", () => void openTeamConfigTab());
    document.getElementById("welcome-quick-open")?.addEventListener("click", () => openQuickOpen());
    document.getElementById("welcome-cmd-palette")?.addEventListener("click", () => openCommandPalette());
    document.getElementById("welcome-memory")?.addEventListener("click", () => openTeamMemoryFile());
    return;
  }

  if (activeTab.kind === "resident") {
    body.innerHTML = renderResidentViewHtml();
    bindResidentViewEvents();
    return;
  }

  if (activeTab.kind === "mission") {
    body.innerHTML = renderMissionBoardHtml();
    bindMissionBoardEvents();
    return;
  }

  if (activeTab.kind === "team-config") {
    body.innerHTML = renderTeamConfigPageHtml();
    bindTeamConfigEvents();
    return;
  }

  if (activeTab.kind === "ai-usage") {
    body.innerHTML = renderAIUsagePageHtml();
    bindAIUsagePageEvents();
    return;
  }

  if (activeTab.kind === "ide-settings") {
    body.innerHTML = renderIdeSettingsPageHtml();
    bindIdeSettingsEvents();
    return;
  }

  if (activeTab.kind === "commit" && activeTab.commitDetail) {
    const cd = activeTab.commitDetail;
    body.innerHTML = renderCommitDetailHtml(cd, state.diffViewMode);
    document.getElementById("btn-commit-diff-unified")?.addEventListener("click", () => {
      state.diffViewMode = "unified";
      renderEditorBody();
    });
    document.getElementById("btn-commit-diff-split")?.addEventListener("click", () => {
      state.diffViewMode = "split";
      renderEditorBody();
    });
    document.getElementById("btn-commit-expand-all")?.addEventListener("click", () => {
      body.querySelectorAll<HTMLDetailsElement>("details.commit-file-card").forEach((d) => {
        d.open = true;
      });
    });
    document.getElementById("btn-commit-collapse-all")?.addEventListener("click", () => {
      body.querySelectorAll<HTMLDetailsElement>("details.commit-file-card").forEach((d) => {
        d.open = false;
      });
    });
    body.querySelectorAll<HTMLButtonElement>("[data-commit-open-file]").forEach((btn) => {
      btn.addEventListener("click", (ev) => {
        ev.preventDefault();
        ev.stopPropagation();
        const filePath = btn.dataset.commitOpenFile || "";
        if (!filePath) return;
        const name = filePath.split("/").pop() || filePath;
        void openFileInEditor(filePath, name);
      });
    });
    return;
  }

  if (activeTab.kind === "diff" && activeTab.diff) {
    const diff = activeTab.diff;
    const diffRepoRoot = activeTab.repoRoot || state.project?.root || "";
    const diffWorkspacePath = activeTab.workspaceRelPath || diff.path;
    const { hunks } = parseDiffHunks(diff);
    const isSplit = state.diffViewMode === "split";
    body.innerHTML = `
      <div class="editor-breadcrumb">
        <span class="dim">${escapeHtml(diffWorkspacePath)}</span>
        <span style="color:var(--green)">+${diff.added}</span>
        <span style="color:var(--red)">-${diff.removed}</span>
        <div class="spacer"></div>
        <button class="mini ${!isSplit ? "primary" : ""}" id="btn-diff-unified">Unificado</button>
        <button class="mini ${isSplit ? "primary" : ""}" id="btn-diff-split">Lado a Lado</button>
        <button class="mini" id="btn-diff-open-file" title="Abrir arquivo no editor">Abrir Arquivo</button>
        <button class="mini danger" id="btn-diff-revert-file" title="Reverter todas as alterações deste arquivo">Reverter Arquivo</button>
      </div>
      ${isSplit ? renderSplitDiffHtml(hunks) : `<div class="diff-lines">${renderUnifiedDiffHtml(hunks)}</div>`}
    `;
    document.getElementById("btn-diff-unified")?.addEventListener("click", () => {
      state.diffViewMode = "unified";
      renderEditorBody();
    });
    document.getElementById("btn-diff-split")?.addEventListener("click", () => {
      state.diffViewMode = "split";
      renderEditorBody();
    });
    document.getElementById("btn-diff-open-file")?.addEventListener("click", () => {
      const name = diff.path.split("/").pop() || diff.path;
      openFileInEditor(diffWorkspacePath, name);
    });
    document.getElementById("btn-diff-revert-file")?.addEventListener("click", () => {
      if (!state.project) return;
      askConfirm({
        title: `Reverter "${diff.path}"`,
        body: `Deseja descartar todas as alterações de "${diff.path}" (restaurando para HEAD ou removendo se for arquivo novo)?`,
        confirmLabel: "Reverter Arquivo",
        danger: true,
        onConfirm: async () => {
          if (!state.project) return;
          try {
            await api()?.DiscardFile(diffRepoRoot, diff.path);
            setStatus(`Arquivo revertido: ${diffWorkspacePath}`);
            await refreshExplorerTree();
            await reloadCleanOpenTabs();
            await refreshDiffs();
          } catch (err: any) {
            toastError(`reverter o arquivo ${diff.path}`, err);
          }
        },
      });
    });
    body.querySelectorAll("[data-revert-hunk]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        if (!state.project) return;
        const idx = parseInt((btn as HTMLElement).dataset.revertHunk || "0", 10);
        const h = hunks[idx];
        if (!h) return;
        try {
          await api()?.RevertHunk(
            diffRepoRoot,
            diff.path,
            h.header,
            h.lines.map((l) => l.text)
          );
          setStatus(`Bloco revertido em ${diff.path}.`);
          await reloadCleanOpenTabs();
          await refreshDiffs();
        } catch (err: any) {
          toastError(`reverter o bloco em ${diff.path}`, err);
        }
      });
    });
    return;
  }

  const inspectHtml = state.fileGitInspect.relPath === activeTab.relPath
    ? renderFileGitInspectPanelHtml(state.fileGitInspect)
    : "";
  const isBlameActive = state.fileGitInspect.mode === "blame" && state.fileGitInspect.relPath === activeTab.relPath;
  const isHistoryActive = state.fileGitInspect.mode === "history" && state.fileGitInspect.relPath === activeTab.relPath;

  if (isMdFile && activeTab.mdView !== "source") {
    body.innerHTML = `
      <div class="editor-breadcrumb">
        <span class="dim">${escapeHtml(activeTab.relPath)}</span>
        <div class="spacer"></div>
        <button class="mini ${isBlameActive ? "active" : ""}" id="btn-editor-blame" title="Git Blame por linha">${icons.gitSmall}<span>Blame</span></button>
        <button class="mini ${isHistoryActive ? "active" : ""}" id="btn-editor-history" title="Histórico de commits do arquivo">${icons.sessions}<span>Histórico</span></button>
        <button class="mini primary" id="btn-md-preview" title="Visualização renderizada do Markdown (padrão)">Visualização</button>
        <button class="mini" id="btn-md-source" title="Ver e editar o código-fonte do Markdown">Código</button>
        <button class="mini primary" id="btn-save-file" ${activeTab.dirty ? "" : "disabled"}>${icons.save}<span>Salvar (Ctrl+S)</span></button>
      </div>
      <div class="md-preview" id="md-preview-scroll"><div class="md-preview-inner">${renderMarkdownLite(activeTab.content)}</div></div>
      ${inspectHtml}
    `;
    document.getElementById("btn-editor-blame")?.addEventListener("click", () => void openFileGitInspect("blame", activeTab.relPath, renderEditorBody));
    document.getElementById("btn-editor-history")?.addEventListener("click", () => void openFileGitInspect("history", activeTab.relPath, renderEditorBody));
    bindFileGitInspectEvents(renderEditorBody);
    document.getElementById("btn-md-preview")?.addEventListener("click", () => {
      activeTab.mdView = "preview";
      renderEditorBody();
    });
    document.getElementById("btn-md-source")?.addEventListener("click", () => {
      activeTab.mdView = "source";
      renderEditorBody();
    });
    document.getElementById("btn-save-file")?.addEventListener("click", saveActiveEditor);
    body.querySelectorAll("[data-copy-code]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const wrap = (btn as HTMLElement).closest(".md-code-wrap");
        const codeText = wrap?.querySelector("code")?.textContent ?? "";
        navigator.clipboard?.writeText(codeText);
        setStatus("Código copiado.");
      });
    });
    return;
  }

  body.innerHTML = `
    <div class="editor-breadcrumb">
      <span class="dim">${escapeHtml(activeTab.relPath)}</span>
      <div class="spacer"></div>
      ${isMdFile
        ? `<button class="mini" id="btn-md-preview" title="Voltar para a visualização renderizada">Visualização</button><button class="mini primary" id="btn-md-source" title="Editar o código-fonte do Markdown">Código</button>`
        : `<button class="mini" id="btn-inline-edit" title="Editar seleção ou inserir código com IA (Ctrl+K)">${icons.sparkle}<span>Editar IA (Ctrl+K)</span></button><button class="mini ${state.ghostTextEnabled ? "active" : ""}" id="btn-toggle-ghost" title="${escapeHtml(ghostButtonTitle())}">${icons.bolt}<span>Ghost</span></button><button class="mini" id="btn-editor-find" title="Localizar e Substituir no arquivo (Ctrl+F / Ctrl+H)">${icons.search}<span>Localizar</span></button><button class="mini" id="btn-send-selection" title="Envia as linhas selecionadas para o chat do Chefe (Ctrl+L)">${icons.send}<span>Chefe (Ctrl+L)</span></button>`
      }
      <button class="mini ${isBlameActive ? "active" : ""}" id="btn-editor-blame" title="Git Blame por linha">${icons.gitSmall}<span>Blame</span></button>
      <button class="mini ${isHistoryActive ? "active" : ""}" id="btn-editor-history" title="Histórico de commits do arquivo">${icons.sessions}<span>Histórico</span></button>
      <button class="mini primary" id="btn-save-file" ${activeTab.dirty ? "" : "disabled"}>${icons.save}<span>Salvar (Ctrl+S)</span></button>
    </div>
    <div class="editor-live-wrap">
      <div id="editor-review-bar"></div>
      <div id="editor-inline-ai-layer"></div>
      <div id="editor-find-layer"></div>
      <div id="cm-host" style="flex:1;min-width:0;height:100%;overflow:hidden"></div>
    </div>
    ${inspectHtml}
  `;

  if (state.editorFind.open) {
    renderEditorFindBar();
  }
  if (state.inlineEdit.open && state.inlineEdit.relPath !== activeTab.relPath) {
    closeInlineEdit(false);
  }

  const tab = activeTab;
  mountCodeMirror(document.getElementById("cm-host")!, tab.relPath, tab.content, tab.name, (text) => {
    tab.content = text;
    tab.dirty = true;
    syncActiveTabDirtyUI(tab);
    scheduleGhostText(tab.relPath);
    scheduleLSPDidChange(tab.relPath, text);
  });
  notifyLSPDidOpen(tab.relPath, tab.content);
  applyDiagnosticsToActiveEditor(tab.relPath);

  if (state.inlineEdit.open && state.inlineEdit.relPath === tab.relPath) {
    renderInlineEditBar();
  }
  void syncEditorInlineReview(tab.relPath);

  document.getElementById("btn-editor-blame")?.addEventListener("click", () => void openFileGitInspect("blame", tab.relPath, renderEditorBody));
  document.getElementById("btn-editor-history")?.addEventListener("click", () => void openFileGitInspect("history", tab.relPath, renderEditorBody));
  bindFileGitInspectEvents(renderEditorBody);

  document.getElementById("btn-inline-edit")?.addEventListener("click", () => openInlineEdit());
  document.getElementById("btn-toggle-ghost")?.addEventListener("click", () => toggleGhostText());
  document.getElementById("btn-editor-find")?.addEventListener("click", () => openEditorFind(false));
  document.getElementById("btn-send-selection")?.addEventListener("click", sendEditorSelectionToChief);
  document.getElementById("btn-save-file")?.addEventListener("click", saveActiveEditor);
  document.getElementById("btn-md-preview")?.addEventListener("click", () => {
    activeTab.mdView = "preview";
    renderEditorBody();
  });
  document.getElementById("btn-md-source")?.addEventListener("click", () => {
    activeTab.mdView = "source";
    renderEditorBody();
  });
}

/** Re-renderiza o corpo do editor em respostas assíncronas sem descartar edição ativa em inputs. */
export function renderEditorBodyAsync(): void {
  if (typeof document !== "undefined") {
    const active = document.activeElement as HTMLElement | null;
    const body = document.getElementById("editor-body");
    if (active && body && body.contains(active)) {
      const tag = active.tagName.toLowerCase();
      if (tag === "input" || tag === "textarea" || tag === "select") {
        const el = active as HTMLInputElement | HTMLTextAreaElement;
        if (tag !== "select" && el.value !== (el.defaultValue ?? "")) {
          return;
        }
        const activeId = active.id;
        renderEditorBody();
        if (activeId) document.getElementById(activeId)?.focus();
        return;
      }
    }
  }
  renderEditorBody();
}
