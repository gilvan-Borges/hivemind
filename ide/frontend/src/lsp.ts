import type { Diagnostic } from "@codemirror/lint";
import type { LSPDiagnosticsEvent, ProblemItem } from "./types";
import { activeEditorView, setEditorDiagnostics, setLSPCallbacks } from "./cm-editor";
import { openFileInEditor } from "./editor";
import { updateStatusBar } from "./shell";
import { api, normalizePathKey, setStatus, state, toastError } from "./state";
import { renderTerminalDrawer } from "./terminal";

const changeTimers = new Map<string, number>();

export function isLSPFile(relPath: string): boolean {
  return /\.go$/i.test((relPath || "").trim());
}

export function initLSPHooks(): void {
  setLSPCallbacks({
    onDefinition: (line, col) => {
      void triggerGoToDefinition(line, col);
    },
    onHover: (line, col) => fetchLSPHover(line, col),
  });
}

export async function refreshLSPStatus(targetRoot?: string): Promise<void> {
  const backend = api();
  const root = targetRoot || state.project?.root;
  if (!backend?.LSPStatus || !root) return;
  const targetKey = normalizePathKey(root);
  try {
    const st = await backend.LSPStatus(root);
    const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
    if (ctx) ctx.lspStatus = st;
    if (state.project && normalizePathKey(state.project.root) === targetKey) {
      state.lspStatus = st;
      updateStatusBar();
    }
  } catch {
    // ignore LSP probe failure
  }
}

export function notifyLSPDidOpen(relPath: string, content: string): void {
  const backend = api();
  const root = state.project?.root;
  if (!backend?.LSPDidOpen || !root || !isLSPFile(relPath)) return;
  void backend.LSPDidOpen(root, relPath, content).then(() => refreshLSPStatus(root)).catch(() => {});
}

export function scheduleLSPDidChange(relPath: string, content: string): void {
  const backend = api();
  const root = state.project?.root;
  if (!backend?.LSPDidChange || !root || !isLSPFile(relPath)) return;
  const key = `${normalizePathKey(root)}::${relPath}`;
  const prev = changeTimers.get(key);
  if (prev) window.clearTimeout(prev);
  const timer = window.setTimeout(() => {
    changeTimers.delete(key);
    void backend.LSPDidChange?.(root, relPath, content).catch(() => {});
  }, 280);
  changeTimers.set(key, timer);
}

export function notifyLSPDidSave(relPath: string, content: string): void {
  const backend = api();
  const root = state.project?.root;
  if (!backend?.LSPDidSave || !root || !isLSPFile(relPath)) return;
  void backend.LSPDidSave(root, relPath, content).catch(() => {});
}

export function notifyLSPDidClose(relPath: string): void {
  const backend = api();
  const root = state.project?.root;
  if (!backend?.LSPDidClose || !root || !isLSPFile(relPath)) return;
  void backend.LSPDidClose(root, relPath).catch(() => {});
}

export function handleLSPDiagnosticsEvent(payload: LSPDiagnosticsEvent): void {
  const wsKey = normalizePathKey(payload?.workspace || "");
  const rel = (payload?.relPath || "").replace(/\\/g, "/");
  if (!wsKey || !rel) return;

  const list = Array.isArray(payload.diagnostics) ? payload.diagnostics : [];
  const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === wsKey);
  if (ctx) {
    if (!ctx.lspDiagnostics) ctx.lspDiagnostics = {};
    if (list.length === 0) {
      delete ctx.lspDiagnostics[rel];
    } else {
      ctx.lspDiagnostics[rel] = list;
    }
  }

  if (state.project && normalizePathKey(state.project.root) === wsKey) {
    if (list.length === 0) {
      delete state.lspDiagnostics[rel];
    } else {
      state.lspDiagnostics[rel] = list;
    }
    if (state.activeEditorPath === rel) {
      applyDiagnosticsToActiveEditor(rel);
    }
    updateStatusBar();
    if (state.terminal.open && state.bottomTab === "problems") {
      renderTerminalDrawer();
    }
  }
}

export function mergeProblems(
  lspMap: Record<string, ProblemItem[]>,
  buildList: ProblemItem[]
): ProblemItem[] {
  const seen = new Set<string>();
  const out: ProblemItem[] = [];
  const add = (p: ProblemItem) => {
    const file = (p.file || "").replace(/\\/g, "/");
    const key = `${file.toLowerCase()}:${p.line}:${p.col}:${p.message}`;
    if (!file || seen.has(key)) return;
    seen.add(key);
    out.push({ ...p, file });
  };
  for (const items of Object.values(lspMap || {})) {
    for (const p of items || []) add(p);
  }
  for (const p of buildList || []) add(p);

  const rank = (sev: string) => (sev === "error" ? 0 : sev === "warning" ? 1 : 2);
  out.sort((a, b) => {
    const r = rank(a.severity) - rank(b.severity);
    if (r !== 0) return r;
    if (a.file !== b.file) return a.file.localeCompare(b.file);
    return a.line - b.line || a.col - b.col;
  });
  return out;
}

export function getAllProblems(): ProblemItem[] {
  return mergeProblems(state.lspDiagnostics, state.buildProblems);
}

export function countProblems(): { errors: number; warnings: number; total: number } {
  const all = getAllProblems();
  let errors = 0;
  let warnings = 0;
  for (const p of all) {
    if (p.severity === "warning") warnings++;
    else if (p.severity === "error") errors++;
  }
  return { errors, warnings, total: all.length };
}

export function applyDiagnosticsToActiveEditor(relPath: string): void {
  const view = activeEditorView();
  if (!view || !relPath) return;
  const norm = relPath.replace(/\\/g, "/").toLowerCase();
  const matching = getAllProblems().filter((p) => p.file.replace(/\\/g, "/").toLowerCase() === norm);
  const doc = view.state.doc;

  const cmDiags: Diagnostic[] = [];
  for (const p of matching) {
    const lineNo = Math.max(1, Math.min(p.line || 1, doc.lines));
    const lineObj = doc.line(lineNo);
    const colOffset = Math.max(0, (p.col || 1) - 1);
    const from = Math.min(lineObj.from + colOffset, lineObj.to);
    let to = from;
    if (p.endLine && p.endLine >= lineNo && p.endLine <= doc.lines) {
      const endLineObj = doc.line(p.endLine);
      const endColOffset = Math.max(0, (p.endCol || 1) - 1);
      to = Math.min(endLineObj.from + endColOffset, endLineObj.to);
    }
    if (to <= from) {
      to = Math.min(lineObj.to, from + 1);
    }
    cmDiags.push({
      from,
      to: Math.max(from, to),
      severity: p.severity === "warning" ? "warning" : p.severity === "info" ? "info" : "error",
      source: p.source || "lsp",
      message: p.code ? `[${p.code}] ${p.message}` : p.message,
    });
  }
  setEditorDiagnostics(cmDiags);
}

export async function triggerGoToDefinition(line: number, col: number): Promise<void> {
  const backend = api();
  const root = state.project?.root;
  const activeTab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
  if (!backend?.LSPDefinition || !root || !activeTab || !isLSPFile(activeTab.relPath)) return;
  try {
    const locs = (await backend.LSPDefinition(root, activeTab.relPath, line, col)) || [];
    if (locs.length === 0) {
      setStatus("Nenhuma definição encontrada sob o cursor.");
      return;
    }
    const target = locs[0];
    const rel = target.relPath || target.absPath;
    if (!rel) return;
    const name = rel.split("/").pop() || rel;
    await openFileInEditor(rel, name, target.line, target.col);
    setStatus(`Definição: ${rel}:${target.line}:${target.col}`);
  } catch (err: any) {
    toastError("ir para definição (LSP)", err);
  }
}

export async function fetchLSPHover(line: number, col: number): Promise<string | null> {
  const backend = api();
  const root = state.project?.root;
  const activeTab = state.editorTabs.find((t) => t.kind === "file" && t.relPath === state.activeEditorPath);
  if (!backend?.LSPHover || !root || !activeTab || !isLSPFile(activeTab.relPath)) return null;
  try {
    const res = await backend.LSPHover(root, activeTab.relPath, line, col);
    return res?.contents?.trim() || null;
  } catch {
    return null;
  }
}

export async function runProblemsCheck(customCmd?: string): Promise<void> {
  const backend = api();
  const root = state.project?.root;
  if (!backend?.CheckWorkspaceProblems || !root || state.problemsChecking) return;
  const targetKey = normalizePathKey(root);
  state.problemsChecking = true;
  state.terminal.open = true;
  state.bottomTab = "problems";
  renderTerminalDrawer();
  setStatus("Verificando problemas de build/compilação…");
  try {
    const res = await backend.CheckWorkspaceProblems(root, customCmd || "");
    const problems = res?.problems || [];
    const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
    if (ctx) ctx.buildProblems = problems;
    if (state.project && normalizePathKey(state.project.root) === targetKey) {
      state.buildProblems = problems;
      state.problemsLastCmd = res?.command || customCmd || "";
      if (state.activeEditorPath) {
        applyDiagnosticsToActiveEditor(state.activeEditorPath);
      }
      const label = problems.length === 0
        ? `Verificação limpa (${res.command} · ${res.durationMs}ms)`
        : `${problems.length} problema(s) encontrado(s) em ${res.command}`;
      setStatus(label);
    }
  } catch (err: any) {
    toastError("verificar problemas do projeto", err);
  } finally {
    state.problemsChecking = false;
    updateStatusBar();
    if (state.terminal.open) renderTerminalDrawer();
  }
}
