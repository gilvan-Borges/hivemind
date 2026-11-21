import { icons } from "./icons";
import { api, state, toast, toastError } from "./state";
import type { BlameLine, ConflictedFile, FileCommitEntry } from "./types";
import { escapeHtml } from "./utils";

export interface FileGitInspectState {
  mode: "none" | "blame" | "history";
  relPath: string;
  loading: boolean;
  error: string;
  blame: BlameLine[];
  history: FileCommitEntry[];
}

export interface ConflictListState {
  loading: boolean;
  error: string;
  files: ConflictedFile[];
  busyKey: string;
}

export function createEmptyInspectState(): FileGitInspectState {
  return {
    mode: "none",
    relPath: "",
    loading: false,
    error: "",
    blame: [],
    history: [],
  };
}

export function createEmptyConflictState(): ConflictListState {
  return {
    loading: false,
    error: "",
    files: [],
    busyKey: "",
  };
}

function formatShortDate(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso.slice(0, 10);
  return d.toLocaleDateString("pt-BR", { day: "2-digit", month: "2-digit", year: "2-digit" });
}

export function renderFileGitInspectPanelHtml(st: FileGitInspectState): string {
  if (st.mode === "none") return "";
  const title = st.mode === "blame" ? "Git Blame" : "Histórico do Arquivo";
  let body = "";
  if (st.loading) {
    body = `<div class="dim git-inspect-state">${icons.spinner}<span>Carregando ${st.mode === "blame" ? "blame" : "commits"}…</span></div>`;
  } else if (st.error) {
    body = `
      <div class="git-inspect-state git-inspect-error">
        <span>${escapeHtml(st.error)}</span>
        <button type="button" class="mini" id="btn-git-inspect-retry">${icons.refresh}<span>Tentar novamente</span></button>
      </div>
    `;
  } else if (st.mode === "blame") {
    if (st.blame.length === 0) {
      body = `<div class="dim git-inspect-state">Nenhuma linha rastreada pelo Git para este arquivo.</div>`;
    } else {
      body = `
        <div class="git-blame-rows">
          ${st.blame.slice(0, 300).map((ln) => `
            <div class="git-blame-row" title="${escapeHtml(`${ln.author} · ${ln.summary || "(sem mensagem)"}`)}">
              <span class="git-tabular git-blame-line">${ln.line}</span>
              <code class="git-tabular git-blame-hash">${escapeHtml(ln.short)}</code>
              <span class="git-blame-author">${escapeHtml(ln.author || "—")}</span>
              <span class="git-tabular git-blame-date">${escapeHtml(formatShortDate(ln.timestamp))}</span>
              <code class="git-blame-code">${escapeHtml(ln.text)}</code>
            </div>
          `).join("")}
        </div>
      `;
    }
  } else {
    if (st.history.length === 0) {
      body = `<div class="dim git-inspect-state">Nenhum commit encontrado no histórico deste arquivo.</div>`;
    } else {
      body = `
        <div class="git-history-rows">
          ${st.history.map((c) => `
            <div class="git-history-row">
              <code class="git-tabular git-blame-hash">${escapeHtml(c.short)}</code>
              <span class="git-history-subject">${escapeHtml(c.subject)}</span>
              <span class="git-blame-author">${escapeHtml(c.author)}</span>
              <span class="git-tabular git-blame-date">${escapeHtml(formatShortDate(c.timestamp))}</span>
            </div>
          `).join("")}
        </div>
      `;
    }
  }

  return `
    <div class="git-inspect-drawer" id="git-inspect-drawer">
      <div class="git-inspect-head">
        <strong>${title}</strong>
        <span class="dim">${escapeHtml(st.relPath)}</span>
        <div class="spacer"></div>
        <button type="button" class="mini" id="btn-git-inspect-close" title="Fechar painel">${icons.closeSmall}<span>Fechar</span></button>
      </div>
      <div class="git-inspect-body">${body}</div>
    </div>
  `;
}

export function renderConflictsSectionHtml(st: ConflictListState): string {
  if (!st.loading && !st.error && st.files.length === 0) {
    return "";
  }
  if (st.loading && st.files.length === 0) {
    return `
      <div class="scm-section git-conflicts-section">
        <div class="scm-section-head"><span>Conflitos de Merge</span></div>
        <div class="dim git-inspect-state">${icons.spinner}<span>Verificando conflitos…</span></div>
      </div>
    `;
  }
  if (st.error) {
    return `
      <div class="scm-section git-conflicts-section">
        <div class="scm-section-head"><span>Conflitos de Merge</span></div>
        <div class="git-inspect-error">
          <span>${escapeHtml(st.error)}</span>
          <button type="button" class="mini" id="btn-conflicts-retry">${icons.refresh}<span>Tentar novamente</span></button>
        </div>
      </div>
    `;
  }

  return `
    <div class="scm-section git-conflicts-section" id="git-conflicts-section">
      <div class="scm-section-head">
        <span>Conflitos de Merge</span>
        <div class="spacer"></div>
        <span class="scm-count-pill git-tabular">${st.files.length}</span>
      </div>
      <div class="git-conflicts-list">
        ${st.files.map((file) => {
          const allCleared = file.blocks.length === 0;
          const busyFile = st.busyKey === `mark:${file.path}`;
          return `
            <div class="git-conflict-card">
              <div class="git-conflict-file-head">
                <span class="git-conflict-path" title="${escapeHtml(file.path)}">${escapeHtml(file.path)}</span>
                <span class="dim git-tabular">${file.blocks.length} bloco(s)</span>
                ${allCleared
                  ? `<button type="button" class="mini primary" data-mark-resolved="${escapeHtml(file.path)}" ${busyFile ? "disabled" : ""}>${icons.check}<span>Marcar resolvido</span></button>`
                  : ""}
              </div>
              ${file.blocks.map((blk) => {
                const busy = st.busyKey === `${file.path}:${blk.index}`;
                return `
                  <div class="git-conflict-block">
                    <div class="git-conflict-block-head">
                      <span class="dim git-tabular">Bloco #${blk.index + 1} (L${blk.startLine}–L${blk.endLine})</span>
                    </div>
                    <div class="git-conflict-panes">
                      <div class="git-conflict-pane">
                        <div class="dim">Atual (${escapeHtml(blk.oursLabel || "HEAD")})</div>
                        <pre>${escapeHtml(blk.ours || "(vazio)")}</pre>
                      </div>
                      <div class="git-conflict-pane">
                        <div class="dim">Recebida (${escapeHtml(blk.theirsLabel || "incoming")})</div>
                        <pre>${escapeHtml(blk.theirs || "(vazio)")}</pre>
                      </div>
                    </div>
                    <div class="git-conflict-actions">
                      <button type="button" class="mini" data-resolve-file="${escapeHtml(file.path)}" data-resolve-block="${blk.index}" data-resolve-choice="ours" ${busy ? "disabled" : ""}>Manter atual</button>
                      <button type="button" class="mini" data-resolve-file="${escapeHtml(file.path)}" data-resolve-block="${blk.index}" data-resolve-choice="theirs" ${busy ? "disabled" : ""}>Aceitar recebida</button>
                      <button type="button" class="mini" data-resolve-file="${escapeHtml(file.path)}" data-resolve-block="${blk.index}" data-resolve-choice="both" ${busy ? "disabled" : ""}>Manter ambas</button>
                    </div>
                  </div>
                `;
              }).join("")}
            </div>
          `;
        }).join("")}
      </div>
    </div>
  `;
}

export async function openFileGitInspect(mode: "blame" | "history", relPath: string, onUpdate: () => void) {
  const backend = api();
  const root = state.activeGitRepoRoot || state.project?.root || "";
  if (!backend || !root || !relPath) return;

  if (state.fileGitInspect.mode === mode && state.fileGitInspect.relPath === relPath && !state.fileGitInspect.error) {
    state.fileGitInspect = createEmptyInspectState();
    onUpdate();
    return;
  }

  state.fileGitInspect = {
    mode,
    relPath,
    loading: true,
    error: "",
    blame: [],
    history: [],
  };
  onUpdate();

  try {
    if (mode === "blame" && backend.GitBlameFile) {
      state.fileGitInspect.blame = (await backend.GitBlameFile(root, relPath)) || [];
    } else if (mode === "history" && backend.GitFileHistory) {
      state.fileGitInspect.history = (await backend.GitFileHistory(root, relPath, 50)) || [];
    }
  } catch (err: any) {
    state.fileGitInspect.error = err?.message || String(err);
  } finally {
    state.fileGitInspect.loading = false;
    onUpdate();
  }
}

export function bindFileGitInspectEvents(onUpdate: () => void) {
  document.getElementById("btn-git-inspect-close")?.addEventListener("click", () => {
    state.fileGitInspect = createEmptyInspectState();
    onUpdate();
  });
  document.getElementById("btn-git-inspect-retry")?.addEventListener("click", () => {
    const { mode, relPath } = state.fileGitInspect;
    if (mode !== "none" && relPath) {
      state.fileGitInspect.mode = "none";
      void openFileGitInspect(mode, relPath, onUpdate);
    }
  });
}

export async function refreshConflictedFiles(repoRoot?: string) {
  const backend = api();
  const root = repoRoot || state.activeGitRepoRoot || state.project?.root || "";
  if (!backend?.ListConflictedFiles || !root) return;
  state.gitConflicts.loading = true;
  state.gitConflicts.error = "";
  try {
    state.gitConflicts.files = (await backend.ListConflictedFiles(root)) || [];
  } catch (err: any) {
    state.gitConflicts.error = err?.message || String(err);
  } finally {
    state.gitConflicts.loading = false;
  }
}

export function bindConflictResolutionEvents(onAfterChange: () => Promise<void>) {
  const root = state.activeGitRepoRoot || state.project?.root || "";
  document.getElementById("btn-conflicts-retry")?.addEventListener("click", async () => {
    await refreshConflictedFiles(root);
    await onAfterChange();
  });
  document.querySelectorAll("[data-resolve-file]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const b = api();
      if (!b?.ResolveConflictBlock || !root) return;
      const el = btn as HTMLElement;
      const file = el.dataset.resolveFile || "";
      const block = parseInt(el.dataset.resolveBlock || "0", 10) || 0;
      const choice = (el.dataset.resolveChoice || "ours") as "ours" | "theirs" | "both";
      state.gitConflicts.busyKey = `${file}:${block}`;
      try {
        const updated = await b.ResolveConflictBlock(root, file, block, choice);
        state.gitConflicts.files = state.gitConflicts.files.map((f) => (f.path === file ? updated : f));
        toast(`Conflito atualizado em ${file} (${choice}).`, "info");
        await onAfterChange();
      } catch (err: any) {
        toastError(`resolver conflito em ${file}`, err);
      } finally {
        state.gitConflicts.busyKey = "";
      }
    });
  });
  document.querySelectorAll("[data-mark-resolved]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const b = api();
      if (!b?.MarkConflictResolved || !root) return;
      const file = (btn as HTMLElement).dataset.markResolved || "";
      state.gitConflicts.busyKey = `mark:${file}`;
      try {
        await b.MarkConflictResolved(root, file);
        await refreshConflictedFiles(root);
        toast(`${file} marcado como resolvido (staged).`, "success");
        await onAfterChange();
      } catch (err: any) {
        toastError(`marcar ${file} como resolvido`, err);
      } finally {
        state.gitConflicts.busyKey = "";
      }
    });
  });
}
