import { fileBadge, icons } from "./icons";
import type { AutoReviewSettings, AutoReviewStatus, FileDiff, GitGraphCommit, GitHubStatus, GitSubTab, ProjectInfo, ProjectIssue, ProjectPullRequest, TeamLane, WorkspaceGitRepo } from "./types";
import { escapeHtml, formatTokens } from "./utils";
import { rebuildChatMessagesFull, renderTokenBar, scheduleChatFlush, syncComposerButtons } from "./chat";
import { openDiffInEditor, openFileInEditor, promptDiscardChanges, reloadCleanOpenTabs, renderEditorBody, renderEditorTabs } from "./editor";
import { bindConflictResolutionEvents, refreshConflictedFiles, renderConflictsSectionHtml } from "./git-history";
import { bindTreeRowClicks, renderExplorerTreeHtml, renderLeftSidebar } from "./explorer";
import { activeLane, renderLaneBarOnly, renderPipelineBar } from "./lanes";
import { openGitHubModal, openJiraModal } from "./modals";
import { renderGuard } from "./render-guard";
import { syncLayoutVisibility, toggleBranchPopover, updateHeaderAndStatusBar, updateStatusBar } from "./shell";
import { api, askConfirm, normalizePathKey, setStatus, state, toast, toastError } from "./state";
import { bindWorktreesTabEvents, refreshWorktreesPanel, renderWorktreesTabHtml } from "./worktrees";

function withTimeout<T>(promise: Promise<T>, ms: number, label: string): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = globalThis.setTimeout(() => {
      reject(new Error(`Tempo limite (${Math.round(ms / 1000)}s) ao ${label}.`));
    }, ms);
    promise.then(
      (val) => {
        globalThis.clearTimeout(timer);
        resolve(val);
      },
      (err) => {
        globalThis.clearTimeout(timer);
        reject(err);
      }
    );
  });
}

export function getEffectiveGitRepos(): WorkspaceGitRepo[] {
  if (state.gitRepos.length > 0) return state.gitRepos;
  if (state.project?.branch) {
    return [
      {
        root: state.project.root,
        name: state.project.name,
        relPath: "",
        branch: state.project.branch,
        isRoot: true,
        diffs: state.diffs,
      },
    ];
  }
  return [];
}

export function getActiveGitRepo(): WorkspaceGitRepo | null {
  const repos = getEffectiveGitRepos();
  if (repos.length === 0) return null;
  const activeKey = normalizePathKey(state.activeGitRepoRoot);
  if (activeKey) {
    const match = repos.find((r) => normalizePathKey(r.root) === activeKey);
    if (match) return match;
  }
  return repos[0];
}

export function isMultiRepoMode(repos: WorkspaceGitRepo[]): boolean {
  return repos.length > 1 || (repos.length === 1 && !repos[0].isRoot);
}

function getRepoCommitDraft(repoRoot: string): string {
  const key = normalizePathKey(repoRoot);
  if (Object.prototype.hasOwnProperty.call(state.commitMsgByRepo, key)) {
    return state.commitMsgByRepo[key] || "";
  }
  const activeKey = normalizePathKey(getActiveGitRepo()?.root || state.project?.root || "");
  return key === activeKey ? state.commitMsgInput : "";
}

function setRepoCommitDraft(repoRoot: string, value: string) {
  const key = normalizePathKey(repoRoot);
  state.commitMsgByRepo[key] = value;
  const activeKey = normalizePathKey(getActiveGitRepo()?.root || state.project?.root || "");
  if (key === activeKey) {
    state.commitMsgInput = value;
  }
}

export function selectActiveGitRepo(repoRoot: string) {
  const repos = getEffectiveGitRepos();
  const target = repos.find((r) => normalizePathKey(r.root) === normalizePathKey(repoRoot));
  if (!target) return;
  const prevKey = normalizePathKey(state.activeGitRepoRoot);
  const nextKey = normalizePathKey(target.root);
  state.activeGitRepoRoot = target.root;
  state.diffs = target.diffs || [];
  state.commitMsgInput = getRepoCommitDraft(target.root);
  state.commitMenuOpen = false;
  state.commitMenuRepoRoot = "";

  const ctx = state.projectTabs.find((p) => p.id === state.activeProjectId);
  if (ctx) {
    ctx.activeGitRepoRoot = state.activeGitRepoRoot;
    ctx.diffs = state.diffs;
    ctx.commitMsgInput = state.commitMsgInput;
  }

  const graphOutdated = normalizePathKey(state.gitGraph.repoRoot || "") !== nextKey || !state.gitGraph.loadedOnce;
  if (prevKey !== nextKey || graphOutdated) {
    state.gitGraph.repoRoot = target.root;
    state.gitGraph.commits = [];
    state.gitGraph.loadedOnce = false;
    state.gitGraph.loading = false;
    void api()?.GitHubStatus(target.root).then((gh) => {
      if (normalizePathKey(state.activeGitRepoRoot) === nextKey) {
        state.ghStatus = gh;
        if (state.sidebarView === "git") renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
      }
    }).catch(() => { /* ignore */ });
    if (state.sidebarView === "git") renderLeftSidebar();
    void loadGitGraph();
  } else if (state.sidebarView === "git") {
    renderLeftSidebar();
  }
}

export async function refreshDiffsAndSummarize(lane: TeamLane) {
  await refreshDiffs();
  const repos = getEffectiveGitRepos();
  const allDiffs = repos.flatMap((r) =>
    (r.diffs || []).map((d) => ({
      ...d,
      path: r.relPath ? `${r.relPath}/${d.path}` : d.path,
    }))
  );
  if (allDiffs.length === 0) return;
  const last = lane.messages[lane.messages.length - 1];
  if (last && last.kind === "diff") return;
  const totalAdd = allDiffs.reduce((s, d) => s + d.added, 0);
  const totalRem = allDiffs.reduce((s, d) => s + d.removed, 0);
  lane.messages.push({
    id: `diff-${Date.now()}`,
    kind: "diff",
    label: `${allDiffs.length} arquivo(s) alterado(s) (+${totalAdd} / -${totalRem})`,
    text: "",
    diffItems: allDiffs.map((d) => ({ path: d.path, added: d.added, removed: d.removed })),
  });
  if (lane.id === activeLane()?.id) {
    scheduleChatFlush();
  }
}

export function getGitStatusMap(): { files: Map<string, "M" | "U">; dirs: Set<string> } {
  const files = new Map<string, "M" | "U">();
  const dirs = new Set<string>();
  const repos = getEffectiveGitRepos();
  for (const repo of repos) {
    const prefix = repo.relPath ? repo.relPath.replace(/\\/g, "/").replace(/\/+$/, "") : "";
    for (const d of (repo.diffs || [])) {
      const raw = (d.path || "").replace(/\\/g, "/");
      if (!raw) continue;
      const p = prefix ? `${prefix}/${raw}` : raw;
      const isNew = d.status === "U" || d.lines.some((ln) => ln.kind === "header" && ln.text.startsWith("new file mode"));
      files.set(p, isNew ? "U" : "M");
      const parts = p.split("/");
      for (let i = 1; i < parts.length; i++) {
        dirs.add(parts.slice(0, i).join("/"));
      }
    }
  }
  return { files, dirs };
}

export function updateGitActivityBadge() {
  const diffBadge = document.getElementById("git-badge");
  if (diffBadge) {
    const repos = getEffectiveGitRepos();
    const total = repos.length > 0
      ? repos.reduce((sum, r) => sum + (r.diffs?.length || 0), 0)
      : state.diffs.length;
    diffBadge.textContent = String(total);
    diffBadge.style.display = total > 0 ? "inline-block" : "none";
  }
  const ghBadge = document.getElementById("git-gh-badge");
  if (ghBadge) {
    const totalUnread = state.projectTabs.reduce(
      (sum, ctx) => sum + (ctx.id === state.activeProjectId ? state.githubUnreadCount : (ctx.githubUnreadCount || 0)),
      state.projectTabs.length === 0 ? state.githubUnreadCount : 0
    );
    ghBadge.textContent = totalUnread > 9 ? "9+" : String(totalUnread);
    ghBadge.style.display = totalUnread > 0 ? "inline-block" : "none";
  }
}

export function clearGitHubUnread() {
  if (state.githubUnreadCount === 0) return;
  state.githubUnreadCount = 0;
  const activeCtx = state.projectTabs.find((p) => p.id === state.activeProjectId);
  if (activeCtx) activeCtx.githubUnreadCount = 0;
  updateGitActivityBadge();
}

function formatSyncTime(iso?: string): string {
  if (!iso) return "";
  const dt = new Date(iso);
  if (Number.isNaN(dt.getTime())) return "";
  return dt.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export async function refreshDiffs() {
  const backend = api();
  if (!backend || !state.project) return;
  const targetRoot = state.project.root;
  const targetKey = normalizePathKey(targetRoot);
  try {
    let discoveredRepos: WorkspaceGitRepo[] | null = null;
    if (backend.GetWorkspaceGitRepos) {
      discoveredRepos = (await backend.GetWorkspaceGitRepos(targetRoot)) || [];
    }
    const diffs = discoveredRepos
      ? (discoveredRepos[0]?.diffs || [])
      : ((await backend.GetDiff(targetRoot)) || []);

    const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
    const activeCtxRepoRoot = ctx?.activeGitRepoRoot || state.activeGitRepoRoot;
    let resolvedRepos: WorkspaceGitRepo[] = discoveredRepos || [];
    if (!discoveredRepos && state.project.branch) {
      resolvedRepos = [{
        root: targetRoot,
        name: state.project.name,
        relPath: "",
        branch: state.project.branch,
        isRoot: true,
        diffs,
      }];
    }

    const activeMatch =
      resolvedRepos.find((r) => normalizePathKey(r.root) === normalizePathKey(activeCtxRepoRoot))
      || resolvedRepos.find((r) => (r.diffs?.length || 0) > 0)
      || resolvedRepos[0];
    const nextActiveRoot = activeMatch?.root || "";
    const activeDiffs = activeMatch?.diffs || diffs;

    if (ctx) {
      ctx.gitRepos = resolvedRepos;
      ctx.activeGitRepoRoot = nextActiveRoot;
      ctx.diffs = activeDiffs;
    }
    if (!state.project || normalizePathKey(state.project.root) !== targetKey) {
      return;
    }
    state.gitRepos = resolvedRepos;
    state.activeGitRepoRoot = nextActiveRoot;
    state.diffs = activeDiffs;
    updateGitActivityBadge();
    await refreshConflictedFiles(nextActiveRoot || targetRoot);

    // Sync any open diff tabs with updated diff objects across all discovered repos
    let activeDiffChanged = false;
    for (const tab of [...state.editorTabs]) {
      if (tab.kind !== "diff" || !tab.diff) continue;
      const tabRepoKey = normalizePathKey(tab.repoRoot || state.project.root);
      const repoMatch = resolvedRepos.find((r) => normalizePathKey(r.root) === tabRepoKey);
      const pool = repoMatch ? (repoMatch.diffs || []) : state.diffs;
      const updated = pool.find((d) => d.path === tab.diff?.path);
      if (updated) {
        tab.diff = updated;
        if (state.activeEditorPath === tab.relPath) activeDiffChanged = true;
      } else {
        state.editorTabs = state.editorTabs.filter((t) => t.relPath !== tab.relPath);
        if (state.activeEditorPath === tab.relPath) {
          state.activeEditorPath = state.editorTabs[0]?.relPath || "";
          activeDiffChanged = true;
        }
      }
    }
    if (activeDiffChanged) {
      renderEditorTabs();
      renderEditorBody();
    }
    if (state.sidebarView === "git" && state.showLeftSidebar) {
      // Polling de fundo (watcher/equipe): nunca destruir a mensagem de commit
      // que o usuário está digitando (W4-06).
      renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
    } else if (state.sidebarView === "explorer" && state.showLeftSidebar) {
      const treeBody = document.getElementById("explorer-tree-body");
      if (treeBody) {
        treeBody.innerHTML = renderExplorerTreeHtml();
        bindTreeRowClicks();
      }
    }
  } catch {
    // ignore non-git
  }
}

export const GRAPH_PALETTE = ["#4c8dff", "#fbbf24", "#ec4899", "#34d399", "var(--accent)", "#f97316"];

export function buildGraphRowSvgs(commits: GitGraphCommit[]): string[] {
  const lanes: Array<string | null> = [];
  const laneColors: number[] = [];
  let nextColor = 0;

  const allocColor = () => {
    const c = nextColor % GRAPH_PALETTE.length;
    nextColor++;
    return c;
  };

  interface EdgeSeg {
    fromCol: number;
    toCol: number;
    color: string;
    half: "top" | "bottom";
  }

  const rowPlans: Array<{
    col: number;
    color: string;
    isMerge: boolean;
    maxCols: number;
    edges: EdgeSeg[];
  }> = [];

  for (const c of commits) {
    const edges: EdgeSeg[] = [];
    const matching: number[] = [];
    for (let i = 0; i < lanes.length; i++) {
      if (lanes[i] === c.hash) matching.push(i);
    }

    let col = -1;
    let colColorIdx = 0;
    if (matching.length > 0) {
      col = matching[0];
      colColorIdx = laneColors[col] ?? 0;
      for (const idx of matching) {
        const clr = GRAPH_PALETTE[(laneColors[idx] ?? idx) % GRAPH_PALETTE.length];
        edges.push({ fromCol: idx, toCol: col, color: clr, half: "top" });
        if (idx !== col) {
          lanes[idx] = null;
        }
      }
    } else {
      col = lanes.indexOf(null);
      if (col < 0) {
        col = lanes.length;
        lanes.push(null);
        laneColors.push(allocColor());
      } else {
        laneColors[col] = allocColor();
      }
      colColorIdx = laneColors[col];
    }

    for (let i = 0; i < lanes.length; i++) {
      if (lanes[i] !== null && i !== col) {
        const clr = GRAPH_PALETTE[(laneColors[i] ?? i) % GRAPH_PALETTE.length];
        edges.push({ fromCol: i, toCol: i, color: clr, half: "top" });
      }
    }

    const colColor = GRAPH_PALETTE[colColorIdx % GRAPH_PALETTE.length];
    const parents = c.parents || [];

    if (parents.length === 0) {
      lanes[col] = null;
    } else {
      lanes[col] = parents[0];
      edges.push({ fromCol: col, toCol: col, color: colColor, half: "bottom" });

      for (let pIdx = 1; pIdx < parents.length; pIdx++) {
        const pHash = parents[pIdx];
        let target = lanes.indexOf(pHash);
        let pColorIdx: number;
        if (target >= 0) {
          pColorIdx = laneColors[target] ?? target;
        } else {
          target = lanes.indexOf(null);
          if (target < 0) {
            target = lanes.length;
            lanes.push(pHash);
            pColorIdx = allocColor();
            laneColors.push(pColorIdx);
          } else {
            lanes[target] = pHash;
            pColorIdx = allocColor();
            laneColors[target] = pColorIdx;
          }
        }
        const pColor = GRAPH_PALETTE[pColorIdx % GRAPH_PALETTE.length];
        edges.push({ fromCol: col, toCol: target, color: pColor, half: "bottom" });
      }
    }

    for (let i = 0; i < lanes.length; i++) {
      if (lanes[i] !== null && i !== col) {
        const clr = GRAPH_PALETTE[(laneColors[i] ?? i) % GRAPH_PALETTE.length];
        edges.push({ fromCol: i, toCol: i, color: clr, half: "bottom" });
      }
    }

    while (lanes.length > 0 && lanes[lanes.length - 1] === null) {
      lanes.pop();
      laneColors.pop();
    }

    const maxCols = Math.max(col + 1, lanes.length, ...edges.map((e) => Math.max(e.fromCol, e.toCol) + 1));
    rowPlans.push({
      col,
      color: colColor,
      isMerge: parents.length > 1,
      maxCols: Math.min(8, Math.max(1, maxCols)),
      edges,
    });
  }

  const step = 12;
  const h = 24;
  const cy = 12;
  return rowPlans.map((plan) => {
    const w = plan.maxCols * step + 6;
    const xOf = (cIdx: number) => 8 + cIdx * step;
    const paths = plan.edges
      .map((e) => {
        const x1 = xOf(e.fromCol);
        const x2 = xOf(e.toCol);
        if (e.half === "top") {
          if (x1 === x2) {
            return `<line x1="${x1}" y1="0" x2="${x2}" y2="${cy}" stroke="${e.color}" stroke-width="1.6"/>`;
          }
          return `<path d="M ${x1} 0 C ${x1} ${cy * 0.55}, ${x2} ${cy * 0.45}, ${x2} ${cy}" fill="none" stroke="${e.color}" stroke-width="1.6"/>`;
        }
        if (x1 === x2) {
          return `<line x1="${x1}" y1="${cy}" x2="${x2}" y2="${h}" stroke="${e.color}" stroke-width="1.6"/>`;
        }
        return `<path d="M ${x1} ${cy} C ${x1} ${cy + 6}, ${x2} ${cy + 6}, ${x2} ${h}" fill="none" stroke="${e.color}" stroke-width="1.6"/>`;
      })
      .join("");
    const cx = xOf(plan.col);
    const node = plan.isMerge
      ? `<circle cx="${cx}" cy="${cy}" r="3.7" fill="var(--bg-sidebar)" stroke="${plan.color}" stroke-width="1.8"/>`
      : `<circle cx="${cx}" cy="${cy}" r="3.4" fill="${plan.color}"/>`;
    return `<svg class="scm-graph-svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" aria-hidden="true">${paths}${node}</svg>`;
  });
}

export function renderGraphRefPillsHtml(refs?: string[]): string {
  if (!refs || refs.length === 0) return "";
  return refs
    .slice(0, 3)
    .map((r) => {
      const clean = r.replace(/^HEAD\s*->\s*/, "").trim();
      const isHead = r.startsWith("HEAD ->") || r === "HEAD";
      const isRemote = clean.includes("origin/");
      const isTag = clean.startsWith("tag:");
      const label = clean.replace(/^tag:\s*/, "").replace(/^origin\//, "");
      const cls = isHead ? "head" : isRemote ? "remote" : isTag ? "tag" : "local";
      const ic = isHead ? icons.target : isRemote ? icons.cloud : icons.gitSmall;
      return `<span class="scm-ref-pill ${cls}" title="${escapeHtml(r)}">${ic}<span>${escapeHtml(label)}</span></span>`;
    })
    .join("");
}

export function renderScmFileRowHtml(d: FileDiff, repoRoot = "", repoRelPath = ""): string {
  const parts = d.path.split("/");
  const fileName = parts.pop() || d.path;
  const dirPath = parts.join("\\");
  const st = (d.status || "M").toUpperCase();
  const stCls = st === "U" ? "untracked" : st === "A" ? "added" : st === "D" ? "deleted" : st === "R" ? "renamed" : "modified";
  const workspaceRelPath = repoRelPath ? `${repoRelPath}/${d.path}` : d.path;
  return `
    <div class="scm-file-row ${state.activeEditorPath === "diff:" + workspaceRelPath ? "active" : ""}" data-open-diff="${escapeHtml(d.path)}" data-repo-root="${escapeHtml(repoRoot)}" data-repo-rel="${escapeHtml(repoRelPath)}" title="${escapeHtml(workspaceRelPath)} (+${d.added} -${d.removed})">
      <span class="scm-file-icon">${fileBadge(fileName)}</span>
      <span class="scm-file-name">${escapeHtml(fileName)}</span>
      ${dirPath ? `<span class="scm-file-dir">${escapeHtml(dirPath)}</span>` : `<span class="scm-file-dir"></span>`}
      <span class="scm-file-actions">
        <button type="button" class="scm-row-btn" data-scm-open-file="${escapeHtml(workspaceRelPath)}" title="Abrir arquivo">${icons.openInNew}</button>
        <button type="button" class="scm-row-btn" data-scm-discard-file="${escapeHtml(d.path)}" data-repo-root="${escapeHtml(repoRoot)}" title="Descartar alterações">${icons.undo}</button>
        ${d.staged
          ? `<button type="button" class="scm-row-btn" data-scm-unstage="${escapeHtml(d.path)}" data-repo-root="${escapeHtml(repoRoot)}" title="Remover do Stage (Unstage)">${icons.minus}</button>`
          : `<button type="button" class="scm-row-btn" data-scm-stage="${escapeHtml(d.path)}" data-repo-root="${escapeHtml(repoRoot)}" title="Preparar Alteração (Stage)">${icons.plus}</button>`
        }
      </span>
      <span class="scm-status-letter ${stCls}">${escapeHtml(st)}</span>
    </div>
  `;
}

export function renderGitGraphSectionHtml(): string {
  const g = state.gitGraph;
  const repos = getEffectiveGitRepos();
  const activeRepo = getActiveGitRepo();
  const showRepoBadge = isMultiRepoMode(repos) && Boolean(activeRepo);
  const targetRepoRoot = g.repoRoot || activeRepo?.root || state.project?.root || "";
  const rowSvgs = g.commits.length > 0 ? buildGraphRowSvgs(g.commits) : [];
  return `
    <div class="scm-section">
      <div class="scm-section-head">
        <button type="button" class="scm-collapse-btn" id="btn-toggle-graph">
          ${g.collapsed ? icons.chevronRight : icons.chevronDown}
          <span>Graph</span>
        </button>
        ${showRepoBadge ? `<span class="scm-graph-repo-pill" title="${escapeHtml(activeRepo!.relPath || activeRepo!.name)}">${escapeHtml(activeRepo!.name)}</span>` : ""}
        <div class="spacer"></div>
        <button type="button" class="scm-mini-pill ${g.allBranches ? "active" : ""}" id="btn-graph-scope" title="${g.allBranches ? "Mostrando todas as branches (Auto)" : "Mostrando apenas a branch atual"}">
          ${icons.gitSmall}
          <span>${g.allBranches ? "Auto" : "Branch"}</span>
        </button>
        <button type="button" class="icon-btn" data-git-action="fetch" title="Fetch (buscar atualizações)" ${state.gitOp.running ? "disabled" : ""}>${state.gitOp.running && state.gitOp.action === "fetch" ? icons.spinner : icons.arrowDown}</button>
        <button type="button" class="icon-btn" data-git-action="pull" title="Pull (baixar commits)" ${state.gitOp.running ? "disabled" : ""}>${state.gitOp.running && state.gitOp.action === "pull" ? icons.spinner : icons.download}</button>
        <button type="button" class="icon-btn" data-git-action="push" title="Push (enviar commits)" ${state.gitOp.running ? "disabled" : ""}>${state.gitOp.running && state.gitOp.action === "push" ? icons.spinner : icons.arrowUp}</button>
        <button type="button" class="icon-btn" id="btn-refresh-graph" title="Atualizar Graph">${icons.refresh}</button>
      </div>
      ${g.collapsed ? "" : `
        <div class="scm-graph-list" id="scm-graph-list">
          ${g.loading && !g.loadedOnce
            ? `<div class="dim" style="padding:8px 10px">${icons.spinner} Carregando histórico…</div>`
            : g.commits.length === 0
              ? `<div class="dim" style="padding:8px 10px">Nenhum commit neste repositório ainda.</div>`
              : g.commits.map((c, i) => `
                <div class="scm-graph-row ${state.activeEditorPath === "commit:" + c.hash ? "active" : ""}" data-open-commit="${escapeHtml(c.hash)}" data-repo-root="${escapeHtml(targetRepoRoot)}" title="${escapeHtml(c.short)} · ${escapeHtml(c.author)} · ${escapeHtml(c.date)}&#10;${escapeHtml(c.subject)}">
                  ${rowSvgs[i] || ""}
                  <span class="scm-graph-subject ${c.refs?.some((r) => r.includes("HEAD")) ? "is-head" : ""}">${escapeHtml(c.subject)}</span>
                  <span class="scm-graph-refs">${renderGraphRefPillsHtml(c.refs)}</span>
                </div>
              `).join("")
          }
        </div>
      `}
    </div>
  `;
}

export function renderPrCardHtml(pr: ProjectPullRequest, idx: number, opts?: { showResponseBadge?: boolean; showMineBadge?: boolean; showWaitingBadge?: boolean }): string {
  const showResp = Boolean((opts?.showResponseBadge || (!pr.isMine && pr.needsAttention)) && pr.myResponse);
  const respPill = showResp
    ? pr.myResponse === "approved"
      ? `<span class="flow-pill approved">Aprovado</span>`
      : pr.myResponse === "changes_requested"
        ? `<span class="flow-pill changes">Reprovado</span>`
        : `<span class="flow-pill commented">Comentado</span>`
    : "";
  const attentionCls = pr.attentionReason === "Pronto p/ merge"
    ? "approved"
    : pr.attentionReason === "Ajustes"
      ? "changes"
      : (pr.mentionedMe || pr.attentionReason === "Você foi marcado")
        ? "mention"
        : "pending";
  const attentionPill = pr.needsAttention
    ? `<span class="flow-pill ${attentionCls}">${escapeHtml(pr.attentionReason || "Sua revisão")}</span>`
    : pr.reviewRequested && !pr.isMine && !pr.myResponse
      ? `<span class="flow-pill pending">Sua revisão</span>`
      : pr.isMine && opts?.showWaitingBadge && !pr.needsAttention
        ? `<span class="flow-pill commented">Aguardando revisor</span>`
        : "";
  const decisionPill = pr.isMine && pr.reviewDecision && !pr.needsAttention && !opts?.showWaitingBadge
    ? pr.reviewDecision === "APPROVED"
      ? `<span class="flow-pill approved">Aprovado</span>`
      : pr.reviewDecision === "CHANGES_REQUESTED"
        ? `<span class="flow-pill changes">Ajustes</span>`
        : pr.reviewDecision === "REVIEW_REQUIRED"
          ? `<span class="flow-pill pending">Aguardando revisão</span>`
          : ""
    : "";
  return `
    <div class="issue-card pr-card">
      <div class="pr-card-top">
        <span class="tok-pill">#${pr.number}</span>
        ${pr.isMine && opts?.showMineBadge !== false ? `<span class="flow-pill mine">Meu PR</span>` : ""}
        ${attentionPill}
        ${pr.isDraft ? `<span class="pr-draft-pill">DRAFT</span>` : ""}
        ${respPill}
        ${decisionPill}
        ${pr.author ? `<span class="dim" style="font-size:10.5px">@${escapeHtml(pr.author)}</span>` : ""}
      </div>
      <div class="pr-card-title">${escapeHtml(pr.title)}</div>
      ${pr.headRef || pr.baseRef ? `<div class="pr-branch-flow">${escapeHtml(pr.headRef || "?")} → ${escapeHtml(pr.baseRef || "?")}</div>` : ""}
      ${pr.needsAttention && pr.lastActivitySnippet ? `<div class="pr-activity-snippet" title="${escapeHtml(pr.lastActivitySnippet)}">${pr.lastActivityAuthor ? `<strong>@${escapeHtml(pr.lastActivityAuthor)}:</strong> ` : ""}${escapeHtml(pr.lastActivitySnippet)}</div>` : ""}
      <div class="pr-card-actions">
        <button class="mini primary" style="flex:1" data-review-pr="${idx}">${icons.send}<span>${pr.isMine ? "Trabalhar c/ Equipe" : "Revisar c/ Equipe"}</span></button>
        <button class="mini" data-open-pr-url="${escapeHtml(pr.url)}" title="Abrir no navegador">${icons.openInNew}</button>
        ${pr.isMine ? `<button class="mini danger" data-close-pr="${pr.number}" title="Fechar meu PR no GitHub">${icons.trash}</button>` : ""}
      </div>
    </div>
  `;
}

export function renderIssueCardHtml(iss: ProjectIssue, idx: number): string {
  const isGH = iss.source === "github";
  const num = isGH ? parseInt(iss.key.replace(/^#/, ""), 10) || 0 : 0;
  const subTotal = iss.subtaskTotal || (iss.subtasks ? iss.subtasks.length : 0);
  const subDone = iss.subtaskDone ?? (iss.subtasks ? iss.subtasks.filter((s) => s.done).length : 0);
  const subPending = iss.subtaskPending ?? Math.max(0, subTotal - subDone);
  const hasSubtasks = subTotal > 0;
  const isExpanded = hasSubtasks && state.expandedIssueSubtasks.has(iss.key);
  const pctDone = hasSubtasks ? Math.round((subDone / subTotal) * 100) : 0;
  const hasParent = Boolean(iss.parentKey);
  const isSubtaskBadge = Boolean(iss.isSubtask || (hasParent && !hasSubtasks));

  return `
    <div class="issue-card ${hasSubtasks ? "has-subtasks" : ""} ${hasParent ? "is-subtask-card" : ""}">
      <div class="pr-card-top" style="justify-content:space-between">
        <div style="display:flex;align-items:center;gap:5px;flex-wrap:wrap">
          <span class="tok-pill">${escapeHtml(iss.source.toUpperCase())} · ${escapeHtml(iss.key)}</span>
          ${isSubtaskBadge ? `<span class="flow-pill subtask-type-pill" title="Subtarefa vinculada a ${escapeHtml(iss.parentKey || "task pai")}">Subtask</span>` : ""}
          ${hasSubtasks ? `<span class="flow-pill parent-count-pill" title="${subDone} concluída(s) · ${subPending} sem fazer · total de ${subTotal} subtasks">${subTotal} subtask${subTotal === 1 ? "" : "s"}</span>` : ""}
          ${isGH && iss.isMine ? `<span class="flow-pill mine">Minha</span>` : ""}
        </div>
        ${iss.status ? `<span class="dim" style="font-size:10.5px">${escapeHtml(iss.status)}</span>` : ""}
      </div>
      ${hasParent ? `
        <div class="issue-parent-banner" title="Task Pai: ${escapeHtml(iss.parentKey!)}${iss.parentTitle ? " — " + escapeHtml(iss.parentTitle) : ""}">
          <span class="issue-parent-icon">↳</span>
          <span class="issue-parent-label">Pai:</span>
          <button type="button" class="issue-parent-key-btn" data-filter-parent-key="${escapeHtml(iss.parentKey!)}" title="Filtrar pela task pai ${escapeHtml(iss.parentKey!)}">${escapeHtml(iss.parentKey!)}</button>
          ${iss.parentTitle ? `<span class="issue-parent-title">${escapeHtml(iss.parentTitle)}</span>` : ""}
          ${iss.parentUrl ? `<button type="button" class="issue-parent-open-btn" data-open-pr-url="${escapeHtml(iss.parentUrl)}" title="Abrir task pai ${escapeHtml(iss.parentKey!)} no Jira">${icons.openInNew}</button>` : ""}
        </div>
      ` : ""}
      <div class="pr-card-title">${escapeHtml(iss.title)}</div>
      ${hasSubtasks ? `
        <div class="issue-subtasks-box">
          <button type="button" class="issue-subtasks-summary" data-toggle-issue-subtasks="${escapeHtml(iss.key)}" title="Clique para ${isExpanded ? "ocultar" : "listar"} as ${subTotal} subtasks">
            <span class="subtask-stat ok">${icons.check} <strong>${subDone}</strong> OK</span>
            <span class="subtask-sep">·</span>
            <span class="subtask-stat pending"><strong>${subPending}</strong> sem fazer</span>
            <span class="subtask-sep">=</span>
            <span class="subtask-stat total"><strong>${subTotal}</strong> subtask${subTotal === 1 ? "" : "s"}</span>
            <span class="subtask-expand-icon">${isExpanded ? icons.chevronDown : icons.chevronRight}</span>
          </button>
          <div class="issue-subtasks-bar" title="${pctDone}% concluído (${subDone} de ${subTotal})">
            <div class="issue-subtasks-bar-fill" style="width:${pctDone}%"></div>
          </div>
          ${isExpanded && iss.subtasks && iss.subtasks.length > 0 ? `
            <div class="issue-subtasks-list">
              ${iss.subtasks.map((st) => `
                <div class="issue-subtask-row ${st.done ? "is-done" : "is-pending"}" title="${escapeHtml(st.key)} — ${escapeHtml(st.summary)} [${escapeHtml(st.status || (st.done ? "Concluído" : "Pendente"))}]">
                  <span class="subtask-row-mark">${st.done ? "✓" : "○"}</span>
                  <span class="subtask-row-key">${escapeHtml(st.key)}</span>
                  <span class="subtask-row-summary">${escapeHtml(st.summary)}</span>
                  ${st.status ? `<span class="subtask-row-status">${escapeHtml(st.status)}</span>` : ""}
                  ${!st.done ? `<button type="button" class="subtask-row-solve" data-solve-subtask-key="${escapeHtml(st.key)}" data-solve-subtask-title="${escapeHtml(st.summary)}" data-solve-subtask-parent="${escapeHtml(iss.key)} — ${escapeHtml(iss.title)}" title="Resolver ${escapeHtml(st.key)} c/ Equipe">${icons.send}</button>` : ""}
                  ${st.url ? `<button type="button" class="subtask-row-open" data-open-pr-url="${escapeHtml(st.url)}" title="Abrir ${escapeHtml(st.key)} no Jira">${icons.openInNew}</button>` : ""}
                </div>
              `).join("")}
            </div>
          ` : ""}
        </div>
      ` : ""}
      <div class="pr-card-actions">
        <button class="mini primary" style="flex:1" data-solve-issue="${idx}">${icons.send}<span>Resolver c/ Equipe</span></button>
        ${iss.url ? `<button class="mini" data-open-pr-url="${escapeHtml(iss.url)}" title="Abrir no navegador">${icons.openInNew}</button>` : ""}
        ${iss.isMine && isGH && num > 0 ? `<button class="mini danger" data-close-issue="${num}" title="Fechar minha issue no GitHub">${icons.trash}</button>` : ""}
      </div>
    </div>
  `;
}

export function renderAutoReviewCardHtml(st: AutoReviewStatus | null): string {
  const enabled = Boolean(st?.enabled);
  const cfg = st?.config || {
    enabled: false,
    poll_interval_seconds: 120,
    run_tests: true,
    review_action: "full_review" as const,
    max_prs_per_hour: 6,
  };
  const runTests = cfg.run_tests !== false;
  const action = cfg.review_action === "comment_only" ? "comment_only" : "full_review";
  const interval = cfg.poll_interval_seconds || 120;
  const maxPerHour = cfg.max_prs_per_hour || 6;
  const isReviewing = st?.state === "reviewing" && Boolean(st?.activePr);

  let statusLine = "Desligada · revisões apenas sob demanda";
  if (enabled) {
    if (isReviewing) {
      statusLine = `Revisando PR #${st!.activePr}${runTests ? " (testes isolados ativos)" : ""}…`;
    } else if (st?.state === "polling") {
      statusLine = "Verificando PRs pendentes no Fluxo…";
    } else if (st?.state === "paused_limit") {
      statusLine = st.lastError || "Pausada por limite de volume/quota";
    } else if (st?.state === "error") {
      statusLine = st.lastError || "Erro na última verificação";
    } else {
      const qTxt = st && st.queueCount > 0 ? ` · ${st.queueCount} na fila` : "";
      statusLine = `Vigiando fluxo · ${st?.reviewedLastHour || 0}/${maxPerHour} PRs/h${qTxt}`;
    }
  }

  const history = (st?.history || []).slice(0, 5);
  const hasAutoLane = state.lanes.some((l) => l.id === "lane-autoreview");

  return `
    <div class="autoreview-card ${enabled ? "is-on" : ""}">
      <div class="autoreview-head">
        <label class="autoreview-toggle-label" title="Quando ligada, monitora a aba Fluxo e revisa PRs de terceiros automaticamente sem nunca fazer merge">
          <input type="checkbox" id="ar-enabled-toggle" ${enabled ? "checked" : ""} />
          <span><strong>Revisão Automática</strong></span>
        </label>
        <span class="autoreview-badge-nomerge" title="Bloqueio técnico em código: github_merge_pr é removida no modo automático">Sem Merge</span>
        <div class="spacer"></div>
        <button type="button" class="icon-btn" id="ar-toggle-cfg" title="Configurações da Revisão Automática">
          ${state.autoReviewConfigOpen ? icons.chevronDown : icons.chevronRight}
        </button>
      </div>
      <div class="autoreview-status ${st?.state === "error" || st?.state === "paused_limit" ? "warn" : isReviewing ? "active" : ""}">
        ${isReviewing || st?.state === "polling" ? icons.spinner : ""}
        <span>${escapeHtml(statusLine)}</span>
      </div>
      ${enabled ? `
        <div class="autoreview-actions">
          <button type="button" class="mini" id="ar-poll-now" ${isReviewing ? "disabled" : ""}>${icons.refresh}<span>Verificar agora</span></button>
          ${isReviewing ? `<button type="button" class="mini danger" id="ar-stop-pr">${icons.stop}<span>Parar #${st!.activePr}</span></button>` : ""}
          ${hasAutoLane ? `<button type="button" class="mini" id="ar-open-lane" title="Acompanhar ao vivo na lane auto-review">${icons.sparkle}<span>Acompanhar</span></button>` : ""}
        </div>
      ` : ""}
      ${state.autoReviewConfigOpen ? `
        <div class="autoreview-cfg">
          <label class="autoreview-cfg-check">
            <input type="checkbox" id="ar-run-tests" ${runTests ? "checked" : ""} />
            <span>Rodar testes do PR em worktree isolada</span>
          </label>
          <div class="autoreview-cfg-row">
            <span class="dim">Ação:</span>
            <select id="ar-review-action">
              <option value="full_review" ${action === "full_review" ? "selected" : ""}>Parecer completo (Approve/Ajustes/Comentário)</option>
              <option value="comment_only" ${action === "comment_only" ? "selected" : ""}>Somente comentar (COMMENT)</option>
            </select>
          </div>
          <div class="autoreview-cfg-row">
            <span class="dim">Intervalo:</span>
            <select id="ar-poll-interval">
              <option value="60" ${interval === 60 ? "selected" : ""}>1 min</option>
              <option value="120" ${interval === 120 ? "selected" : ""}>2 min</option>
              <option value="300" ${interval === 300 ? "selected" : ""}>5 min</option>
              <option value="600" ${interval === 600 ? "selected" : ""}>10 min</option>
            </select>
            <span class="dim">Máx/h:</span>
            <input type="number" id="ar-max-hour" min="1" max="30" value="${maxPerHour}" style="width:52px;padding:2px 6px" />
          </div>
        </div>
      ` : ""}
      ${history.length > 0 ? `
        <div class="autoreview-history">
          <div class="dim" style="font-size:10px;margin-bottom:3px">Últimas revisões automáticas</div>
          ${history.map((h) => `
            <div class="autoreview-hist-row" title="${escapeHtml(h.error || h.summary || h.title)}">
              <span class="tok-pill">#${h.prNumber}</span>
              <span class="autoreview-hist-status ${escapeHtml(h.status)}">${h.status === "reviewed" ? "ok" : h.status === "canceled" ? "parado" : "erro"}</span>
              <span class="autoreview-hist-title">${escapeHtml(h.title)}</span>
              ${h.tokens ? `<span class="dim" style="font-family:var(--mono);font-size:10px">${formatTokens(h.tokens)}</span>` : ""}
            </div>
          `).join("")}
        </div>
      ` : ""}
    </div>
  `;
}

function renderRepoCommitBoxHtml(repo: WorkspaceGitRepo, compact = false): string {
  const hasDiffs = (repo.diffs?.length || 0) > 0;
  const draft = getRepoCommitDraft(repo.root);
  const menuOpenHere = state.commitMenuOpen && normalizePathKey(state.commitMenuRepoRoot || getActiveGitRepo()?.root || "") === normalizePathKey(repo.root);
  return `
    <div class="scm-commit-box ${compact ? "is-compact" : ""}">
      <input data-commit-input-repo="${escapeHtml(repo.root)}" placeholder='Mensagem (Ctrl+Enter em "${escapeHtml(repo.branch)}")' value="${escapeHtml(draft)}" />
      <div class="scm-split-btn">
        <button type="button" class="primary scm-commit-main" data-commit-repo="${escapeHtml(repo.root)}" ${hasDiffs ? "" : "disabled"}>${icons.check}<span>Commit</span></button>
        <button type="button" class="primary scm-commit-arrow" data-commit-menu-repo="${escapeHtml(repo.root)}" ${hasDiffs ? "" : "disabled"} title="Mais opções de Commit">${icons.chevronDown}</button>
        ${menuOpenHere ? `
          <div class="scm-dropdown" id="scm-commit-dropdown">
            <button type="button" class="dropdown-item" data-commit-mode="commit" data-repo-root="${escapeHtml(repo.root)}">Commit</button>
            <button type="button" class="dropdown-item" data-commit-mode="push" data-repo-root="${escapeHtml(repo.root)}">Commit & Push</button>
            <button type="button" class="dropdown-item" data-commit-mode="sync" data-repo-root="${escapeHtml(repo.root)}">Commit & Sync (Pull + Push)</button>
          </div>
        ` : ""}
      </div>
    </div>
  `;
}

function renderRepoFileGroupsHtml(repo: WorkspaceGitRepo): string {
  const diffs = repo.diffs || [];
  if (diffs.length === 0) {
    return `<div class="dim" style="padding:6px 10px">Nenhuma alteração pendente.</div>`;
  }
  const stagedDiffs = diffs.filter((d) => d.staged);
  const unstagedDiffs = diffs.filter((d) => !d.staged);
  return `
    ${stagedDiffs.length > 0 ? `
      <div class="scm-group-label">Preparadas (Staged) · ${stagedDiffs.length}</div>
      ${stagedDiffs.map((d) => renderScmFileRowHtml(d, repo.root, repo.relPath)).join("")}
    ` : ""}
    ${unstagedDiffs.length > 0 ? `
      ${stagedDiffs.length > 0 ? `<div class="scm-group-label">Alterações · ${unstagedDiffs.length}</div>` : ""}
      ${unstagedDiffs.map((d) => renderScmFileRowHtml(d, repo.root, repo.relPath)).join("")}
    ` : ""}
  `;
}

function renderMultiRepoChangesHtml(repos: WorkspaceGitRepo[], activeRepo: WorkspaceGitRepo | null): string {
  const totalDiffs = repos.reduce((sum, r) => sum + (r.diffs?.length || 0), 0);
  const activeKey = normalizePathKey(activeRepo?.root || "");
  return `
    <div class="scm-section">
      <div class="scm-section-head">
        <button type="button" class="scm-collapse-btn" id="btn-toggle-changes">
          ${state.changesCollapsed ? icons.chevronRight : icons.chevronDown}
          <span>Alterações (${repos.length} repos)</span>
        </button>
        <div class="spacer"></div>
        <span class="scm-count-pill">${totalDiffs}</span>
      </div>
      ${state.changesCollapsed ? "" : `
        <div class="scm-repos-list">
          ${repos.map((repo) => {
            const key = normalizePathKey(repo.root);
            const isSelected = key === activeKey;
            const isCollapsed = Boolean(state.collapsedGitRepos[key]);
            const diffCount = repo.diffs?.length || 0;
            const relHint = repo.relPath && repo.relPath !== repo.name ? repo.relPath : "";
            return `
              <div class="scm-repo-card ${isSelected ? "is-active" : ""}" data-repo-card="${escapeHtml(repo.root)}">
                <div class="scm-repo-head" data-select-repo="${escapeHtml(repo.root)}" title="Selecionar ${escapeHtml(repo.name)} (${escapeHtml(repo.branch)}) para visualizar o Graph">
                  <button type="button" class="scm-repo-toggle" data-toggle-repo="${escapeHtml(repo.root)}" title="${isCollapsed ? "Expandir" : "Recolher"}">
                    ${isCollapsed ? icons.chevronRight : icons.chevronDown}
                  </button>
                  ${icons.gitSmall}
                  <span class="scm-repo-title">${escapeHtml(repo.name)}</span>
                  ${relHint ? `<span class="scm-repo-rel">${escapeHtml(relHint)}</span>` : `<span class="scm-repo-rel"></span>`}
                  <span class="scm-repo-branch-pill">${escapeHtml(repo.branch || "HEAD")}${diffCount > 0 ? "*" : ""}</span>
                  <span class="scm-repo-actions">
                    ${diffCount > 0 ? `<button type="button" class="scm-row-btn" data-discard-repo="${escapeHtml(repo.root)}" data-repo-name="${escapeHtml(repo.name)}" title="Descartar alterações de ${escapeHtml(repo.name)}">${icons.undo}</button>` : ""}
                    <span class="scm-count-pill">${diffCount}</span>
                  </span>
                </div>
                ${isCollapsed ? "" : `
                  <div class="scm-repo-body">
                    ${renderRepoCommitBoxHtml(repo, true)}
                    ${renderRepoFileGroupsHtml(repo)}
                  </div>
                `}
              </div>
            `;
          }).join("")}
        </div>
      `}
    </div>
  `;
}

export function renderGitSidebarHtml(proj: ProjectInfo | null): string {
  const gh = state.ghStatus;
  const repos = getEffectiveGitRepos();
  const activeRepo = getActiveGitRepo();
  const hasGit = repos.length > 0 || Boolean(proj?.branch);
  const multiRepo = isMultiRepoMode(repos);
  const totalDiffs = repos.length > 0
    ? repos.reduce((sum, r) => sum + (r.diffs?.length || 0), 0)
    : state.diffs.length;
  const activeDiffs = activeRepo?.diffs || state.diffs;
  const activeHasDiffs = activeDiffs.length > 0;
  const jiraOn = Boolean(proj?.teamConfig?.settings?.jira_enabled);
  const activeGitTab: GitSubTab = !hasGit && state.gitTab === "changes" ? "prs_issues" : state.gitTab;

  const indexedPrs = state.prs.list.map((pr, idx) => ({ pr, idx }));
  const pendingReviews = indexedPrs.filter(({ pr }) => !pr.isMine && (pr.needsAttention || (pr.reviewRequested && !pr.myResponse)));
  const myPrs = indexedPrs.filter(({ pr }) => pr.isMine && Boolean(pr.needsAttention));
  const respondedPrs = indexedPrs.filter(({ pr }) =>
    (pr.isMine && !pr.needsAttention) || (!pr.isMine && Boolean(pr.myResponse) && !pr.needsAttention)
  );

  const prsIssuesTotal = state.prs.loadedOnce || state.issues.loadedOnce
    ? ` (${state.prs.list.length + state.issues.list.length})`
    : "";
  const flowPendingCount = pendingReviews.length + myPrs.length;
  const flowBadge = state.prs.loadedOnce ? ` (${flowPendingCount})` : "";

  let tabContentHtml = "";
  if (activeGitTab === "changes" && hasGit) {
    const conflictsHtml = renderConflictsSectionHtml(state.gitConflicts);
    if (multiRepo) {
      tabContentHtml = `
        ${conflictsHtml}
        ${renderMultiRepoChangesHtml(repos, activeRepo)}
        ${renderGitGraphSectionHtml()}
      `;
    } else if (activeRepo) {
      tabContentHtml = `
        ${conflictsHtml}
        ${renderRepoCommitBoxHtml(activeRepo, false)}
        <div class="scm-section">
          <div class="scm-section-head">
            <button type="button" class="scm-collapse-btn" id="btn-toggle-changes">
              ${state.changesCollapsed ? icons.chevronRight : icons.chevronDown}
              <span>Alterações</span>
            </button>
            <div class="spacer"></div>
            ${activeHasDiffs ? `<button type="button" class="icon-btn" id="btn-discard" data-discard-repo="${escapeHtml(activeRepo.root)}" data-repo-name="${escapeHtml(activeRepo.name)}" title="Descartar todas as alterações">${icons.undo}</button>` : ""}
            <span class="scm-count-pill">${activeDiffs.length}</span>
          </div>
          ${state.changesCollapsed ? "" : `
            <div id="git-diff-files">
              ${renderRepoFileGroupsHtml(activeRepo)}
            </div>
          `}
        </div>

        ${renderGitGraphSectionHtml()}
      `;
    }
  } else if (activeGitTab === "prs_issues") {
    const loadingAny = state.prs.loading || state.issues.loading;
    tabContentHtml = `
      <div class="git-section" style="padding:10px">
        <div class="git-section-head">
          <span>Pull Requests (${state.prs.list.length})</span>
          <div style="display:flex;gap:5px">
            ${hasGit ? `<button class="mini" id="btn-open-pr" title="Criar PR da branch atual">${icons.pr}<span>Novo PR</span></button>` : ""}
            <button class="mini" id="btn-load-prs-issues" ${loadingAny ? "disabled" : ""}>
              ${loadingAny ? icons.spinner : icons.refresh}
              <span>${state.prs.loadedOnce || state.issues.loadedOnce ? "Atualizar" : "Carregar"}</span>
            </button>
          </div>
        </div>
        <div id="git-prs-list">
          ${state.prs.loading && !state.prs.loadedOnce
            ? `<div class="dim">${icons.spinner} Carregando PRs…</div>`
            : !state.prs.loadedOnce
              ? `<div class="dim">Clique em "Carregar" para buscar PRs e Issues.</div>`
              : state.prs.list.length === 0
                ? `<div class="dim">Nenhum Pull Request aberto.</div>`
                : state.prs.list.map((pr, idx) => renderPrCardHtml(pr, idx, { showResponseBadge: true })).join("")
          }
        </div>
      </div>

      <div class="git-section" style="padding:10px">
        ${(() => {
          const allIssues = state.issues.list.map((iss, idx) => ({ iss, idx }));
          const jiraCount = allIssues.filter((x) => x.iss.source === "jira").length;
          const ghCount = allIssues.filter((x) => x.iss.source === "github").length;
          const hasBothSources = jiraCount > 0 && ghCount > 0;
          const activeSource = hasBothSources ? state.issuesSourceFilter : "";
          const sourceFiltered = allIssues.filter(({ iss }) => !activeSource || iss.source === activeSource);
          const statuses = Array.from(new Set(sourceFiltered.map((x) => (x.iss.status || "").trim()).filter(Boolean)));
          const activeStatus = statuses.includes(state.issuesStatusFilter) ? state.issuesStatusFilter : "";
          const q = (state.issuesFilterQuery || "").trim().toLowerCase();
          const filtered = sourceFiltered.filter(({ iss }) => {
            if (activeStatus && (iss.status || "").trim() !== activeStatus) return false;
            if (q && !(`${iss.key} ${iss.title} ${iss.status || ""} ${iss.parentKey || ""} ${iss.parentTitle || ""}`.toLowerCase().includes(q))) return false;
            return true;
          });
          const countLabel = filtered.length !== allIssues.length
            ? `${filtered.length}/${allIssues.length}`
            : `${allIssues.length}`;
          const detectedJiraKeys = Array.from(new Set(
            allIssues
              .filter((x) => x.iss.source === "jira" && x.iss.key.includes("-"))
              .map((x) => x.iss.key.split("-")[0].toUpperCase())
          ));
          const savedJiraProj = (proj?.teamConfig?.settings?.jira_project || "").trim().toUpperCase();
          const activeJiraProj = savedJiraProj === "NONE"
            ? "NONE"
            : (savedJiraProj || detectedJiraKeys[0] || "");
          return `
            <div class="git-section-head" style="gap:6px;flex-wrap:wrap">
              <span>Minhas tasks & issues (${countLabel})</span>
              ${state.jiraStatus?.connected && state.jiraProjects.length > 0 ? `
                <select id="git-quick-jira-project" title="Projeto Jira vinculado a este repositório" style="font-size:10.5px;padding:2px 6px;max-width:148px;height:22px">
                  <option value="" ${!activeJiraProj ? "selected" : ""}>Auto (pelo repo)</option>
                  <option value="NONE" ${activeJiraProj === "NONE" ? "selected" : ""}>Sem Jira neste repo</option>
                  ${state.jiraProjects.map((p) => `<option value="${escapeHtml(p.key)}" ${p.key.toUpperCase() === activeJiraProj ? "selected" : ""}>Jira: ${escapeHtml(p.key)} — ${escapeHtml(p.name)}</option>`).join("")}
                  <option value="*" ${activeJiraProj === "*" ? "selected" : ""}>Jira: Todos projetos</option>
                </select>
              ` : activeJiraProj && activeJiraProj !== "NONE" ? `
                <span class="tok-pill" title="Projeto Jira vinculado automaticamente a este repositório">Jira: ${escapeHtml(activeJiraProj)}</span>
              ` : ""}
            </div>
            ${allIssues.length > 0 ? `
              <div style="display:flex;flex-direction:column;gap:6px;margin-bottom:8px">
                <input id="issues-filter-input" value="${escapeHtml(state.issuesFilterQuery)}" placeholder="Filtrar por chave ou título…" style="width:100%;font-size:11.5px;padding:4px 8px" />
                ${hasBothSources ? `
                  <div style="display:flex;gap:4px;flex-wrap:wrap">
                    <button type="button" class="mini ${activeSource === "" ? "primary" : "ghost"}" data-issue-source="">Todas (${allIssues.length})</button>
                    <button type="button" class="mini ${activeSource === "jira" ? "primary" : "ghost"}" data-issue-source="jira">Jira${activeJiraProj ? ` · ${escapeHtml(activeJiraProj)}` : ""} (${jiraCount})</button>
                    <button type="button" class="mini ${activeSource === "github" ? "primary" : "ghost"}" data-issue-source="github">GitHub (${ghCount})</button>
                  </div>
                ` : ""}
                ${statuses.length > 1 ? `
                  <div style="display:flex;gap:4px;flex-wrap:wrap">
                    <button type="button" class="mini ${activeStatus === "" ? "primary" : "ghost"}" data-issue-status="">Todos status</button>
                    ${statuses.map((st) => `
                      <button type="button" class="mini ${activeStatus === st ? "primary" : "ghost"}" data-issue-status="${escapeHtml(st)}">${escapeHtml(st)}</button>
                    `).join("")}
                  </div>
                ` : ""}
              </div>
            ` : ""}
            <div id="git-issues-list">
              ${state.issues.loading && !state.issues.loadedOnce
                ? `<div class="dim">${icons.spinner} Carregando suas tasks…</div>`
                : !state.issues.loadedOnce
                  ? `<div class="dim">Aguardando carregamento…</div>`
                  : allIssues.length === 0
                    ? `<div class="dim">Nenhuma task atribuída a você encontrada.</div>`
                    : filtered.length === 0
                      ? `<div class="dim">Nenhuma task corresponde ao filtro atual.</div>`
                      : filtered.map(({ iss, idx }) => renderIssueCardHtml(iss, idx)).join("")
              }
            </div>
          `;
        })()}
      </div>
    `;
  } else if (activeGitTab === "worktrees" && hasGit) {
    tabContentHtml = renderWorktreesTabHtml();
  } else {
    tabContentHtml = `
      <div class="git-section" style="padding:10px">
        ${renderAutoReviewCardHtml(state.autoReview)}
      </div>

      <div class="git-section" style="padding:10px">
        <div class="git-section-head">
          <span>Para revisar (${pendingReviews.length})</span>
          <div style="display:flex;align-items:center;gap:6px">
            ${state.githubLastSyncAt ? `<span class="gh-autosync-meta" title="Atualização automática do GitHub ativa (a cada 60s)">Auto · ${escapeHtml(formatSyncTime(state.githubLastSyncAt))}</span>` : ""}
            <button class="mini" id="btn-load-flow" ${state.prs.loading ? "disabled" : ""}>
              ${state.prs.loading ? icons.spinner : icons.refresh}
              <span>${state.prs.loadedOnce ? "Atualizar" : "Carregar"}</span>
            </button>
          </div>
        </div>
        <div>
          ${state.prs.loading && !state.prs.loadedOnce
            ? `<div class="dim">${icons.spinner} Analisando seus PRs e revisões…</div>`
            : !state.prs.loadedOnce
              ? `<div class="dim">Clique em "Carregar" para ver seu fluxo de revisões.</div>`
              : pendingReviews.length === 0
                ? `<div class="dim">Nenhum PR aguardando sua revisão.</div>`
                : pendingReviews.map(({ pr, idx }) => renderPrCardHtml(pr, idx, { showMineBadge: false })).join("")
          }
        </div>
      </div>

      <div class="git-section" style="padding:10px">
        <div class="git-section-head">
          <span>Meus PRs (${myPrs.length})</span>
          ${hasGit ? `<button class="mini" id="btn-open-pr" title="Criar PR da branch atual">${icons.pr}<span>Novo PR</span></button>` : ""}
        </div>
        <div>
          ${!state.prs.loadedOnce
            ? `<div class="dim">Carregando…</div>`
            : myPrs.length === 0
              ? `<div class="dim">Nenhum PR seu aguardando ação sua.</div>`
              : myPrs.map(({ pr, idx }) => renderPrCardHtml(pr, idx, { showMineBadge: false })).join("")
          }
        </div>
      </div>

      <div class="git-section" style="padding:10px">
        <div class="git-section-head">
          <button type="button" class="scm-collapse-btn" id="btn-toggle-flow-responded" style="padding:0;text-transform:none;letter-spacing:0;font-size:11px">
            ${state.showFlowResponded ? icons.chevronDown : icons.chevronRight}
            <span>Aguardando / Respondidos (${respondedPrs.length})</span>
          </button>
          <span class="dim" style="font-size:10px">aguardando revisor · respondidos</span>
        </div>
        ${state.showFlowResponded ? `
          <div>
            ${respondedPrs.length === 0
              ? `<div class="dim">Nenhum PR aguardando ou respondido ainda.</div>`
              : respondedPrs.map(({ pr, idx }) => renderPrCardHtml(pr, idx, { showResponseBadge: true, showMineBadge: pr.isMine, showWaitingBadge: pr.isMine })).join("")
            }
          </div>
        ` : ""}
      </div>
    `;
  }

  return `
    <div class="sidebar-header scm-header">
      <span class="eyebrow">Controle de Código</span>
      <div class="spacer"></div>
      ${hasGit ? `
        <button type="button" class="icon-btn" id="btn-header-commit" title="Commitar alterações (${escapeHtml(activeRepo?.name || proj?.name || "")})" ${activeHasDiffs ? "" : "disabled"}>${icons.check}</button>
        <button type="button" class="icon-btn" id="btn-refresh-diff" title="Atualizar Source Control">${icons.refresh}</button>
        <div class="scm-more-wrap">
          <button type="button" class="icon-btn" id="btn-scm-more" title="Mais ações Git…" ${state.gitOp.running ? "disabled" : ""}>${icons.moreHoriz}</button>
          ${state.gitMoreMenuOpen && !state.gitOp.running ? `
            <div class="scm-dropdown scm-more-menu" id="scm-more-dropdown">
              <button type="button" class="dropdown-item" data-git-action="pull">Pull</button>
              <button type="button" class="dropdown-item" data-git-action="push">Push</button>
              <button type="button" class="dropdown-item" data-git-action="sync">Sync (Pull + Push)</button>
              <button type="button" class="dropdown-item" data-git-action="fetch">Fetch</button>
              <div class="scm-menu-sep"></div>
              <button type="button" class="dropdown-item" data-git-action="stash">Stash alterações</button>
              <button type="button" class="dropdown-item" data-git-action="stash-pop">Pop Stash</button>
              <div class="scm-menu-sep"></div>
              <button type="button" class="dropdown-item" id="menu-open-pr">Abrir Pull Request…</button>
              <button type="button" class="dropdown-item" id="menu-github-connector">Conector GitHub Nativo…</button>
              <button type="button" class="dropdown-item" id="menu-jira-connector">Conector Jira Nativo…</button>
              ${jiraOn ? `<button type="button" class="dropdown-item" id="menu-comment-jira">Comentar no Jira…</button>` : ""}
              <button type="button" class="dropdown-item" id="menu-switch-branch">Trocar / Criar Branch…</button>
              ${activeHasDiffs ? `<div class="scm-menu-sep"></div><button type="button" class="dropdown-item danger" id="menu-discard-all">Descartar Todas as Alterações</button>` : ""}
            </div>
          ` : ""}
        </div>
      ` : `<button type="button" class="icon-btn" id="btn-refresh-diff" title="Atualizar Source Control">${icons.refresh}</button>`}
    </div>
    <div class="sidebar-body scm-body">
      <div class="scm-repo-strip">
        <span class="gh-dot ${hasGit ? "ok" : "off"}"></span>
        ${hasGit ? `
          <button type="button" class="scm-branch-switch" id="btn-scm-branch-switch" title="Trocar ou criar branch em ${escapeHtml(activeRepo?.name || proj?.name || "")}">
            <span class="scm-branch-label">${state.gitOp.running ? icons.spinner : ""}<span id="scm-branch-git-label">${escapeHtml(state.gitOp.running ? (state.gitOp.label || activeRepo?.branch || proj?.branch || "") : (multiRepo && activeRepo ? `${activeRepo.name} · ${activeRepo.branch || "HEAD"}` : (activeRepo?.branch || proj?.branch || "")))}</span></span>
          </button>
        ` : `<span class="scm-branch-label"><span id="scm-branch-git-label">Sem repositório Git</span></span>`}
        <button type="button" class="scm-gh-pill ${gh?.loggedIn ? "ok" : "off"}" id="btn-github-connector" title="${escapeHtml([gh?.detail || "Configurar Conector GitHub Nativo", gh?.remote].filter(Boolean).join(" — "))}">
          ${gh?.loggedIn ? `@${escapeHtml(gh.user)}` : "Conectar GitHub"}
        </button>
      </div>
      ${gitOpPanelHtml()}
      ${!hasGit ? `<div style="padding:0 10px 8px"><button class="primary mini" id="btn-init-git" style="width:100%">Inicializar Git (git init)</button></div>` : ""}

      <div class="git-subtabs scm-tabs-pad">
        ${hasGit ? `<button type="button" class="git-subtab ${activeGitTab === "changes" ? "active" : ""}" data-git-tab="changes">Alterações (${totalDiffs})</button>` : ""}
        <button type="button" class="git-subtab ${activeGitTab === "prs_issues" ? "active" : ""}" data-git-tab="prs_issues">PRs & Issues${prsIssuesTotal}</button>
        ${hasGit ? `<button type="button" class="git-subtab ${activeGitTab === "flow" ? "active" : ""}" data-git-tab="flow">Fluxo${flowBadge}</button>` : ""}
        ${hasGit ? `<button type="button" class="git-subtab ${activeGitTab === "worktrees" ? "active" : ""}" data-git-tab="worktrees">Worktrees</button>` : ""}
      </div>

      ${tabContentHtml}
    </div>
  `;
}

export async function loadGitGraph() {
  const backend = api();
  if (!backend || !state.project || state.gitGraph.loading || !backend.ListGitGraph) return;
  const activeRepo = getActiveGitRepo();
  const targetRoot = activeRepo?.root || state.project.root;
  const targetKey = normalizePathKey(targetRoot);
  const projectKey = normalizePathKey(state.project.root);
  const targetGraph = state.gitGraph;
  const allBranches = targetGraph.allBranches;
  targetGraph.repoRoot = targetRoot;
  targetGraph.loading = true;
  if (state.sidebarView === "git") renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
  try {
    const commits = (await withTimeout(backend.ListGitGraph(targetRoot, 60, allBranches), 20000, "carregar o Graph de commits")) || [];
    const currentRepoKey = normalizePathKey(state.activeGitRepoRoot || state.project?.root || "");
    if (!state.project || normalizePathKey(state.project.root) !== projectKey || currentRepoKey !== targetKey) {
      return;
    }
    targetGraph.commits = commits;
    targetGraph.loadedOnce = true;
  } catch (err: any) {
    const currentRepoKey = normalizePathKey(state.activeGitRepoRoot || state.project?.root || "");
    if (state.project && normalizePathKey(state.project.root) === projectKey && currentRepoKey === targetKey) {
      toastError("carregar o Graph de commits", err);
    }
  } finally {
    targetGraph.loading = false;
    if (state.sidebarView === "git" && state.project && normalizePathKey(state.project.root) === projectKey) {
      renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
    }
  }
}

export async function openCommitInEditor(hash: string, repoRoot?: string) {
  const backend = api();
  if (!backend || !state.project || !backend.GetCommitDetail) return;
  const targetRoot = repoRoot || getActiveGitRepo()?.root || state.project.root;
  try {
    const detail = await backend.GetCommitDetail(targetRoot, hash);
    const key = `commit:${detail.hash}`;
    const existing = state.editorTabs.find((t) => t.relPath === key);
    if (existing) {
      existing.commitDetail = detail;
    } else {
      state.editorTabs.push({
        kind: "commit",
        relPath: key,
        name: `Commit ${detail.short || hash.slice(0, 7)}`,
        content: "",
        dirty: false,
        commitDetail: detail,
      });
    }
    state.activeEditorPath = key;
    renderEditorTabs();
    renderEditorBody();
    if (state.sidebarView === "git") renderLeftSidebar();
  } catch (err: any) {
    toastError("abrir detalhes do commit", err);
  }
}

const GIT_OP_LABEL: Record<string, string> = {
  pull: "Puxando",
  push: "Enviando",
  fetch: "Buscando",
  sync: "Sincronizando",
  stash: "Guardando alterações",
  "stash-pop": "Restaurando stash",
};

export function gitTimeoutSec(): number {
  try {
    const n = Number(localStorage.getItem("hivemind.gitTimeoutSec") || "");
    if (Number.isFinite(n) && n >= 15 && n <= 3600) return Math.floor(n);
  } catch {
    // preview sem storage
  }
  return 120;
}

function gitTargetLabel(action: string): string {
  const activeRepo = getActiveGitRepo();
  const branchName = activeRepo?.branch || state.project?.branch || "";
  const branch = branchName ? `origin/${branchName}` : "origin";
  const verb = GIT_OP_LABEL[action] || `Executando git ${action}`;
  const repoPrefix = isMultiRepoMode(getEffectiveGitRepos()) && activeRepo ? `[${activeRepo.name}] ` : "";
  if (action === "stash" || action === "stash-pop") return `${repoPrefix}${verb}…`;
  return `${repoPrefix}${verb} de ${branch}…`.replace("Enviando de ", "Enviando para ").replace("Sincronizando de ", "Sincronizando com ");
}

function gitOpPanelHtml(): string {
  const op = state.gitOp;
  if (!op.running && !op.result && !op.error) return "";
  const headline = op.running ? op.label : op.error || op.result;
  return `
    <div class="scm-git-op ${op.error ? "is-error" : ""}" role="status" aria-live="polite">
      <div class="scm-git-op-head">
        ${op.running ? icons.spinner : ""}
        <span id="scm-git-label">${escapeHtml(headline)}</span>
        ${op.running ? `<button type="button" class="mini" id="btn-cancel-git" aria-label="Cancelar operação Git">Cancelar</button>` : ""}
      </div>
      <details class="scm-git-log-wrap" ${op.running ? "open" : ""}>
        <summary>Saída do git</summary>
        <pre id="scm-git-log" class="scm-git-log" title="Limite ${gitTimeoutSec()}s. Ajuste hivemind.gitTimeoutSec ou HIVEMIND_GIT_TIMEOUT.">${escapeHtml(op.log)}</pre>
      </details>
    </div>`;
}

export function appendGitProgress(label: string, line: string) {
  if (label) state.gitOp.label = label;
  if (line) {
    state.gitOp.log = state.gitOp.log ? `${state.gitOp.log}\n${line}` : line;
    if (state.gitOp.log.length > 12000) state.gitOp.log = state.gitOp.log.slice(-12000);
  }
  const lab = document.getElementById("scm-git-label");
  const branch = document.getElementById("scm-branch-git-label");
  if (lab && label) lab.textContent = label;
  if (branch && label && state.gitOp.running) branch.textContent = label;
  const pre = document.getElementById("scm-git-log");
  if (pre) {
    pre.textContent = state.gitOp.log;
    pre.scrollTop = pre.scrollHeight;
    return;
  }
  if (state.sidebarView === "git") renderLeftSidebar();
}

export async function cancelGitAction() {
  const backend = api();
  if (!backend?.CancelGitAction || !state.project) return;
  const targetRoot = getActiveGitRepo()?.root || state.project.root;
  try {
    await backend.CancelGitAction(targetRoot);
  } catch (err: any) {
    toastError("cancelar o git", err);
  }
}

export async function runGitScmAction(action: string, repoRoot?: string) {
  const backend = api();
  if (!backend || !state.project || state.gitOp.running) return;
  const targetRoot = repoRoot || getActiveGitRepo()?.root || state.project.root;
  const label = gitTargetLabel(action);
  state.gitMoreMenuOpen = false;
  state.gitOp = { running: true, action, label, log: "", result: "", error: "" };
  setStatus(label);
  if (state.sidebarView === "git") renderLeftSidebar();
  try {
    const out = backend.RunGitActionTimeout
      ? await backend.RunGitActionTimeout(targetRoot, action, gitTimeoutSec())
      : await backend.RunGitAction(targetRoot, action);
    const summary = out || `Git ${action} concluído.`;
    state.gitOp.running = false;
    state.gitOp.result = summary;
    state.gitOp.error = "";
    state.gitOp.label = summary;
    setStatus(summary);
    toast(summary, "success");
    await refreshDiffs();
    await loadGitGraph();
  } catch (err: any) {
    const reason = err instanceof Error ? err.message : typeof err === "string" ? err : err?.message || "";
    const clean = String(reason || "").trim();
    state.gitOp.running = false;
    if (/cancelad/i.test(clean)) {
      state.gitOp.result = "Operação cancelada.";
      state.gitOp.error = "";
      state.gitOp.label = "Operação cancelada.";
      setStatus("Operação de git cancelada.");
      toast("Operação de git cancelada.", "info");
    } else {
      state.gitOp.error = clean || `Não foi possível executar git ${action}.`;
      state.gitOp.result = "";
      state.gitOp.label = state.gitOp.error;
      toastError(`executar git ${action}`, err);
    }
  } finally {
    if (state.sidebarView === "git") renderLeftSidebar();
  }
}

export async function loadGitHubPRs() {
  const backend = api();
  if (!backend || !state.project || state.prs.loading) return;
  const targetRoot = getActiveGitRepo()?.root || state.project.root;
  const projectKey = normalizePathKey(state.project.root);
  const targetPrs = state.prs;
  targetPrs.loading = true;
  if (state.sidebarView === "git") renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
  try {
    targetPrs.list = (await withTimeout(backend.ListPullRequests(targetRoot), 20000, "buscar os Pull Requests")) || [];
    targetPrs.loadedOnce = true;
  } catch (err: any) {
    if (state.project && normalizePathKey(state.project.root) === projectKey) {
      toastError("buscar os Pull Requests do repositório", err);
    }
  } finally {
    targetPrs.loading = false;
    if (state.sidebarView === "git" && state.project && normalizePathKey(state.project.root) === projectKey) {
      renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
    }
  }
}

export async function loadProjectIssues() {
  const backend = api();
  if (!backend || !state.project || state.issues.loading) return;
  const targetRoot = state.project.root;
  const targetKey = normalizePathKey(targetRoot);
  const targetIssues = state.issues;
  targetIssues.loading = true;
  if (state.sidebarView === "git") renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
  try {
    targetIssues.list = (await withTimeout(backend.ListIssues(targetRoot), 20000, "buscar as issues do GitHub")) || [];
    targetIssues.loadedOnce = true;
  } catch (err: any) {
    if (state.project && normalizePathKey(state.project.root) === targetKey) {
      toastError("buscar as issues do GitHub", err);
    }
  } finally {
    targetIssues.loading = false;
    if (state.sidebarView === "git" && state.project && normalizePathKey(state.project.root) === targetKey) {
      renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
    }
  }
}

export function bindGitSidebarEvents() {
  const resolveRepoForRoot = (repoRoot?: string): WorkspaceGitRepo | null => {
    const repos = getEffectiveGitRepos();
    if (repoRoot) {
      const m = repos.find((r) => normalizePathKey(r.root) === normalizePathKey(repoRoot));
      if (m) return m;
    }
    return getActiveGitRepo();
  };

  const doCommit = async (mode: "commit" | "push" | "sync", repoRoot?: string) => {
    const backend = api();
    if (!backend || !state.project) return;
    const targetRepo = resolveRepoForRoot(repoRoot);
    const targetRoot = targetRepo?.root || state.project.root;
    const message = getRepoCommitDraft(targetRoot);
    state.commitMenuOpen = false;
    state.commitMenuRepoRoot = "";
    try {
      const msg = await backend.CommitChanges(targetRoot, message, mode === "push");
      setRepoCommitDraft(targetRoot, "");
      if (mode === "sync") {
        setStatus(msg);
        toast(msg, "success");
        await runGitScmAction("sync", targetRoot);
      } else {
        setStatus(msg);
        toast(msg, "success");
      }
      await refreshDiffs();
      await loadGitGraph();
    } catch (err: any) {
      toastError(mode === "commit" ? "commitar alterações" : `executar ${mode}`, err);
    }
  };

  document.getElementById("btn-refresh-diff")?.addEventListener("click", () => {
    void refreshDiffs();
    if (state.gitTab === "changes") void loadGitGraph();
    if (state.gitTab === "prs_issues") {
      void loadGitHubPRs();
      void loadProjectIssues();
    }
    if (state.gitTab === "flow") void loadGitHubPRs();
    if (state.gitTab === "worktrees") void refreshWorktreesPanel(renderLeftSidebar);
  });

  bindConflictResolutionEvents(async () => {
    await reloadCleanOpenTabs();
    await refreshDiffs();
    renderLeftSidebar();
  });

  document.getElementById("btn-scm-more")?.addEventListener("click", (ev) => {
    ev.stopPropagation();
    state.gitMoreMenuOpen = !state.gitMoreMenuOpen;
    state.commitMenuOpen = false;
    renderLeftSidebar();
  });

  document.getElementById("btn-scm-branch-switch")?.addEventListener("click", () => {
    const activeRoot = getActiveGitRepo()?.root || state.project?.root;
    void toggleBranchPopover(true, activeRoot);
  });

  document.querySelectorAll("[data-select-repo]").forEach((head) => {
    head.addEventListener("click", () => {
      const repoRoot = (head as HTMLElement).dataset.selectRepo!;
      selectActiveGitRepo(repoRoot);
    });
  });

  document.querySelectorAll("[data-toggle-repo]").forEach((btn) => {
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      const repoRoot = (btn as HTMLElement).dataset.toggleRepo!;
      const key = normalizePathKey(repoRoot);
      state.collapsedGitRepos[key] = !state.collapsedGitRepos[key];
      const ctx = state.projectTabs.find((p) => p.id === state.activeProjectId);
      if (ctx) ctx.collapsedGitRepos = state.collapsedGitRepos;
      renderLeftSidebar();
    });
  });

  document.querySelectorAll("[data-discard-repo]").forEach((btn) => {
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      const el = btn as HTMLElement;
      promptDiscardChanges(el.dataset.discardRepo, el.dataset.repoName);
    });
  });

  document.querySelectorAll("[data-commit-menu-repo]").forEach((btn) => {
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      const repoRoot = (btn as HTMLElement).dataset.commitMenuRepo || "";
      const sameRepo = normalizePathKey(state.commitMenuRepoRoot) === normalizePathKey(repoRoot);
      state.commitMenuOpen = state.commitMenuOpen && sameRepo ? false : true;
      state.commitMenuRepoRoot = state.commitMenuOpen ? repoRoot : "";
      state.gitMoreMenuOpen = false;
      renderLeftSidebar();
    });
  });

  document.querySelectorAll("[data-git-tab]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const tab = (btn as HTMLElement).dataset.gitTab as GitSubTab;
      state.gitTab = tab;
      state.gitMoreMenuOpen = false;
      state.commitMenuOpen = false;
      renderLeftSidebar();
      if (tab === "changes" && (getActiveGitRepo()?.branch || state.project?.branch) && !state.gitGraph.loadedOnce && !state.gitGraph.loading) {
        void loadGitGraph();
      }
      if (tab === "prs_issues") {
        if (!state.prs.loadedOnce && !state.prs.loading && (getActiveGitRepo()?.branch || state.project?.branch)) void loadGitHubPRs();
        if (!state.issues.loadedOnce && !state.issues.loading) void loadProjectIssues();
      }
      if (tab === "flow") {
        clearGitHubUnread();
        if (!state.prs.loadedOnce && !state.prs.loading) {
          void loadGitHubPRs();
        }
      }
      if (tab === "worktrees") {
        void refreshWorktreesPanel(renderLeftSidebar);
      }
    });
  });

  document.getElementById("btn-toggle-flow-responded")?.addEventListener("click", () => {
    state.showFlowResponded = !state.showFlowResponded;
    renderLeftSidebar();
  });

  document.getElementById("btn-toggle-changes")?.addEventListener("click", () => {
    state.changesCollapsed = !state.changesCollapsed;
    renderLeftSidebar();
  });
  document.getElementById("btn-toggle-graph")?.addEventListener("click", () => {
    state.gitGraph.collapsed = !state.gitGraph.collapsed;
    renderLeftSidebar();
  });
  document.getElementById("btn-graph-scope")?.addEventListener("click", () => {
    state.gitGraph.allBranches = !state.gitGraph.allBranches;
    state.gitGraph.loadedOnce = false;
    void loadGitGraph();
  });
  document.getElementById("btn-refresh-graph")?.addEventListener("click", () => void loadGitGraph());

  document.querySelectorAll("[data-git-action]").forEach((btn) => {
    btn.addEventListener("click", () => {
      if (state.gitOp.running) return;
      state.gitMoreMenuOpen = false;
      const action = (btn as HTMLElement).dataset.gitAction!;
      void runGitScmAction(action);
    });
  });
  document.getElementById("btn-cancel-git")?.addEventListener("click", () => {
    void cancelGitAction();
  });

  document.getElementById("menu-github-connector")?.addEventListener("click", () => {
    state.gitMoreMenuOpen = false;
    renderLeftSidebar();
    void openGitHubModal();
  });
  document.getElementById("btn-github-connector")?.addEventListener("click", () => {
    void openGitHubModal();
  });
  document.getElementById("menu-jira-connector")?.addEventListener("click", () => {
    state.gitMoreMenuOpen = false;
    renderLeftSidebar();
    void openJiraModal();
  });
  document.getElementById("menu-switch-branch")?.addEventListener("click", () => {
    state.gitMoreMenuOpen = false;
    renderLeftSidebar();
    void toggleBranchPopover(true, getActiveGitRepo()?.root);
  });
  document.getElementById("menu-discard-all")?.addEventListener("click", () => {
    state.gitMoreMenuOpen = false;
    const activeRepo = getActiveGitRepo();
    promptDiscardChanges(activeRepo?.root, activeRepo?.name);
  });

  document.getElementById("btn-init-git")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      await backend.InitGit(state.project.root);
      const updated = await backend.OpenProject(state.project.root);
      state.project.branch = updated.branch;
      state.ghStatus = await backend.GitHubStatus(state.project.root);
      await refreshDiffs();
      updateHeaderAndStatusBar();
      renderLeftSidebar();
      setStatus("Repositório Git inicializado (branch main).");
    } catch (err: any) {
      toastError("inicializar o repositório Git", err);
    }
  });

  document.querySelectorAll("[data-open-diff]").forEach((el) => {
    el.addEventListener("click", () => {
      const ds = (el as HTMLElement).dataset;
      const p = ds.openDiff!;
      const repoRoot = ds.repoRoot || "";
      const repoRel = ds.repoRel || "";
      const targetRepo = resolveRepoForRoot(repoRoot);
      const pool = targetRepo ? (targetRepo.diffs || []) : state.diffs;
      const d = pool.find((x) => x.path === p);
      if (d) openDiffInEditor(d, targetRepo?.root || repoRoot, repoRel);
    });
  });

  document.querySelectorAll("[data-scm-open-file]").forEach((btn) => {
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      const rel = (btn as HTMLElement).dataset.scmOpenFile!;
      const name = rel.split("/").pop() || rel;
      void openFileInEditor(rel, name);
    });
  });

  document.querySelectorAll("[data-scm-discard-file]").forEach((btn) => {
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      const ds = (btn as HTMLElement).dataset;
      const rel = ds.scmDiscardFile!;
      const repoRoot = ds.repoRoot || getActiveGitRepo()?.root || state.project?.root || "";
      if (!state.project || !repoRoot) return;
      askConfirm({
        title: `Descartar "${rel}"`,
        body: `As alterações locais em "${rel}" serão descartadas permanentemente.`,
        confirmLabel: "Descartar",
        danger: true,
        onConfirm: async () => {
          if (!state.project) return;
          try {
            await api()?.DiscardFile(repoRoot, rel);
            await refreshDiffs();
            setStatus(`Alterações de ${rel} descartadas.`);
          } catch (err: any) {
            toastError(`descartar "${rel}"`, err);
          }
        },
      });
    });
  });

  document.querySelectorAll("[data-scm-stage], [data-scm-unstage]").forEach((btn) => {
    btn.addEventListener("click", async (ev) => {
      ev.stopPropagation();
      const el = btn as HTMLElement;
      const stage = Boolean(el.dataset.scmStage);
      const rel = el.dataset.scmStage || el.dataset.scmUnstage!;
      const repoRoot = el.dataset.repoRoot || getActiveGitRepo()?.root || state.project?.root || "";
      if (!state.project || !api()?.StageFile || !repoRoot) return;
      try {
        await api()!.StageFile(repoRoot, rel, stage);
        await refreshDiffs();
      } catch (err: any) {
        toastError(stage ? `preparar "${rel}"` : `remover "${rel}" do stage`, err);
      }
    });
  });

  document.querySelectorAll("[data-open-commit]").forEach((row) => {
    row.addEventListener("click", () => {
      const ds = (row as HTMLElement).dataset;
      void openCommitInEditor(ds.openCommit!, ds.repoRoot);
    });
  });

  document.querySelectorAll<HTMLInputElement>("[data-commit-input-repo]").forEach((inputEl) => {
    const repoRoot = inputEl.dataset.commitInputRepo || "";
    inputEl.addEventListener("input", () => {
      setRepoCommitDraft(repoRoot, inputEl.value);
    });
    inputEl.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        const repo = resolveRepoForRoot(repoRoot);
        if ((repo?.diffs?.length || 0) > 0) void doCommit("commit", repoRoot);
      }
    });
  });

  document.querySelectorAll("[data-commit-repo]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const repoRoot = (btn as HTMLElement).dataset.commitRepo || "";
      void doCommit("commit", repoRoot);
    });
  });

  document.querySelectorAll("[data-commit-mode]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const ds = (btn as HTMLElement).dataset;
      const mode = (ds.commitMode || "commit") as "commit" | "push" | "sync";
      void doCommit(mode, ds.repoRoot);
    });
  });

  document.getElementById("btn-header-commit")?.addEventListener("click", () => {
    void doCommit("commit", getActiveGitRepo()?.root);
  });

  const openPrAction = async () => {
    const backend = api();
    if (!backend || !state.project) return;
    const activeRepo = getActiveGitRepo();
    const targetRoot = activeRepo?.root || state.project.root;
    state.gitMoreMenuOpen = false;
    try {
      const url = await backend.OpenPR(targetRoot, getRepoCommitDraft(targetRoot), "", state.project.teamConfig?.settings?.base_branch || "main");
      setStatus(`PR aberto: ${url}`);
      toast(`PR aberto: ${url}`, "success");
    } catch (err: any) {
      toastError("abrir o Pull Request", err);
    }
  };
  document.getElementById("btn-open-pr")?.addEventListener("click", () => void openPrAction());
  document.getElementById("menu-open-pr")?.addEventListener("click", () => void openPrAction());

  const commentJiraAction = async () => {
    const backend = api();
    if (!backend || !state.project) return;
    state.gitMoreMenuOpen = false;
    try {
      await backend.CommentJira(state.project.root, state.jiraKeyInput, state.commitMsgInput || "Atualização pela HiveMind IDE.");
      setStatus(`Comentário enviado ao Jira ${state.jiraKeyInput}.`);
    } catch (err: any) {
      toastError("comentar na issue Jira", err);
    }
  };
  document.getElementById("btn-comment-jira")?.addEventListener("click", () => void commentJiraAction());
  document.getElementById("menu-comment-jira")?.addEventListener("click", () => void commentJiraAction());

  document.getElementById("btn-load-prs-issues")?.addEventListener("click", () => {
    if (state.project?.branch) void loadGitHubPRs();
    void loadProjectIssues();
  });
  document.getElementById("btn-load-flow")?.addEventListener("click", () => void loadGitHubPRs());

  const issuesFilterEl = document.getElementById("issues-filter-input") as HTMLInputElement | null;
  issuesFilterEl?.addEventListener("input", () => {
    const pos = issuesFilterEl.selectionStart ?? issuesFilterEl.value.length;
    state.issuesFilterQuery = issuesFilterEl.value;
    renderLeftSidebar();
    const nextEl = document.getElementById("issues-filter-input") as HTMLInputElement | null;
    if (nextEl) {
      nextEl.focus();
      nextEl.setSelectionRange(pos, pos);
    }
  });
  document.getElementById("git-quick-jira-project")?.addEventListener("change", async (e) => {
    const val = (e.target as HTMLSelectElement).value;
    const backend = api();
    if (!state.project || !backend?.UpdateProjectJira) return;
    try {
      const cfg = await backend.UpdateProjectJira(state.project.root, val);
      state.project.teamConfig = cfg;
      if (state.teamConfigDraft) {
        state.teamConfigDraft.jiraProject = cfg.settings.jira_project || "";
        state.teamConfigDraft.jiraEnabled = cfg.settings.jira_enabled;
      }
      state.issues.loadedOnce = false;
      await loadProjectIssues();
    } catch (err: any) {
      toastError("vincular projeto Jira ao repositório", err);
    }
  });
  document.querySelectorAll("[data-issue-source]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const src = ((btn as HTMLElement).dataset.issueSource || "") as "" | "jira" | "github";
      state.issuesSourceFilter = src;
      state.issuesStatusFilter = "";
      renderLeftSidebar();
    });
  });
  document.querySelectorAll("[data-issue-status]").forEach((btn) => {
    btn.addEventListener("click", () => {
      state.issuesStatusFilter = (btn as HTMLElement).dataset.issueStatus || "";
      renderLeftSidebar();
    });
  });

  const saveAutoReviewFromUI = async (overrideEnabled?: boolean) => {
    const backend = api();
    if (!backend?.SetAutoReviewConfig || !state.project) return;
    const cur = state.autoReview?.config || {
      enabled: false,
      poll_interval_seconds: 120,
      run_tests: true,
      review_action: "full_review" as const,
      max_prs_per_hour: 6,
    };
    const enabledEl = document.getElementById("ar-enabled-toggle") as HTMLInputElement | null;
    const testsEl = document.getElementById("ar-run-tests") as HTMLInputElement | null;
    const actionEl = document.getElementById("ar-review-action") as HTMLSelectElement | null;
    const intervalEl = document.getElementById("ar-poll-interval") as HTMLSelectElement | null;
    const maxEl = document.getElementById("ar-max-hour") as HTMLInputElement | null;

    const nextCfg: AutoReviewSettings = {
      ...cur,
      enabled: overrideEnabled !== undefined ? overrideEnabled : Boolean(enabledEl?.checked ?? cur.enabled),
      run_tests: testsEl ? testsEl.checked : cur.run_tests !== false,
      review_action: (actionEl?.value === "comment_only" ? "comment_only" : (cur.review_action || "full_review")) as "full_review" | "comment_only",
      poll_interval_seconds: intervalEl ? (parseInt(intervalEl.value, 10) || 120) : (cur.poll_interval_seconds || 120),
      max_prs_per_hour: maxEl ? Math.max(1, Math.min(30, parseInt(maxEl.value, 10) || 6)) : (cur.max_prs_per_hour || 6),
    };
    try {
      const updated = await backend.SetAutoReviewConfig(state.project.root, nextCfg);
      state.autoReview = updated;
      updateStatusBar();
      renderLeftSidebar();
      setStatus(nextCfg.enabled ? "Revisão Automática do Fluxo ativada (Sem Merge)." : "Revisão Automática do Fluxo desativada.");
    } catch (err: any) {
      toastError("atualizar a Revisão Automática", err);
    }
  };

  document.getElementById("ar-enabled-toggle")?.addEventListener("change", (e) => {
    void saveAutoReviewFromUI((e.target as HTMLInputElement).checked);
  });
  document.getElementById("ar-toggle-cfg")?.addEventListener("click", () => {
    state.autoReviewConfigOpen = !state.autoReviewConfigOpen;
    renderLeftSidebar();
  });
  document.getElementById("ar-run-tests")?.addEventListener("change", () => void saveAutoReviewFromUI());
  document.getElementById("ar-review-action")?.addEventListener("change", () => void saveAutoReviewFromUI());
  document.getElementById("ar-poll-interval")?.addEventListener("change", () => void saveAutoReviewFromUI());
  document.getElementById("ar-max-hour")?.addEventListener("change", () => void saveAutoReviewFromUI());
  document.getElementById("ar-poll-now")?.addEventListener("click", async () => {
    if (!state.project || !api()?.TriggerAutoReviewPoll) return;
    state.autoReview = await api()!.TriggerAutoReviewPoll!(state.project.root);
    updateStatusBar();
    renderLeftSidebar();
  });
  document.getElementById("ar-stop-pr")?.addEventListener("click", async () => {
    if (!state.project || !api()?.StopActiveAutoReview) return;
    await api()!.StopActiveAutoReview!(state.project.root);
    setStatus("Revisão automática do PR atual interrompida.");
  });
  document.getElementById("ar-open-lane")?.addEventListener("click", () => {
    const autoLane = state.lanes.find((l) => l.id === "lane-autoreview");
    if (!autoLane) return;
    state.activeLaneId = autoLane.id;
    state.showRightAI = true;
    syncLayoutVisibility();
    renderLaneBarOnly();
    renderPipelineBar();
    renderTokenBar();
    rebuildChatMessagesFull();
    syncComposerButtons();
  });

  document.querySelectorAll("[data-close-pr]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const num = parseInt((btn as HTMLElement).dataset.closePr || "0", 10);
      if (!num || !state.project) return;
      askConfirm({
        title: `Fechar meu PR #${num}`,
        body: `O Pull Request #${num} (aberto por você) será fechado no GitHub e removido da lista de abertos. Deseja continuar?`,
        confirmLabel: "Fechar PR",
        danger: true,
        onConfirm: async () => {
          if (!state.project || !api()?.CloseOwnGitHubItem) return;
          try {
            await api()!.CloseOwnGitHubItem(state.project.root, "pr", num);
            toast(`PR #${num} fechado no GitHub.`, "success");
            setStatus(`PR #${num} fechado.`);
            await loadGitHubPRs();
          } catch (err: any) {
            toastError(`fechar o PR #${num}`, err);
          }
        },
      });
    });
  });

  document.querySelectorAll("[data-close-issue]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const num = parseInt((btn as HTMLElement).dataset.closeIssue || "0", 10);
      if (!num || !state.project) return;
      askConfirm({
        title: `Fechar minha issue #${num}`,
        body: `A issue #${num} (aberta por você) será fechada no GitHub e removida da lista de abertas. Deseja continuar?`,
        confirmLabel: "Fechar Issue",
        danger: true,
        onConfirm: async () => {
          if (!state.project || !api()?.CloseOwnGitHubItem) return;
          try {
            await api()!.CloseOwnGitHubItem(state.project.root, "issue", num);
            toast(`Issue #${num} fechada no GitHub.`, "success");
            setStatus(`Issue #${num} fechada.`);
            await loadProjectIssues();
          } catch (err: any) {
            toastError(`fechar a issue #${num}`, err);
          }
        },
      });
    });
  });

  document.querySelectorAll("[data-open-pr-url]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const url = (btn as HTMLElement).dataset.openPrUrl || "";
      if (!url) return;
      try {
        await api()?.OpenExternalURL(url);
      } catch (err: any) {
        toastError("abrir URL do Pull Request", err);
      }
    });
  });

  document.querySelectorAll("[data-review-pr]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const idx = parseInt((btn as HTMLElement).dataset.reviewPr || "0", 10);
      const pr = state.prs.list[idx];
      if (!pr) return;
      const branchInfo = pr.headRef && pr.baseRef ? ` (${pr.headRef} → ${pr.baseRef})` : "";
      const bodyPart = pr.body ? `\n\nDescrição do PR:\n${pr.body.slice(0, 1200)}` : "";
      if (pr.isMine) {
        state.draftObjective = `Atender feedback do nosso PR #${pr.number}${branchInfo} — ${pr.title}. Leia o PR completo e todos os comentários/revisões dos revisores com github_get_pr antes de agir.${bodyPart}`;
      } else if (pr.mentionedMe) {
        state.draftObjective = `Responder menção no PR #${pr.number} de @${pr.author || "terceiro"}${branchInfo} — ${pr.title}. Fui marcado por ${pr.lastActivityAuthor ? "@" + pr.lastActivityAuthor : "um colega"}; leia o PR e os comentários mais recentes com github_get_pr antes de responder.${bodyPart}`;
      } else if (pr.needsAttention && pr.myResponse) {
        state.draftObjective = `Reavaliar PR #${pr.number} de @${pr.author || "terceiro"}${branchInfo} — ${pr.title}. Houve ${pr.attentionReason ? pr.attentionReason.toLowerCase() : "nova atualização"} após minha última revisão; leia os comentários mais recentes com github_get_pr antes de responder.${bodyPart}`;
      } else {
        state.draftObjective = `Revisar PR #${pr.number} de @${pr.author || "terceiro"}${branchInfo} — ${pr.title}${bodyPart}`;
      }
      if (activeLane()?.id === "lane-autoreview") {
        const manualLane = state.lanes.find((lane) => lane.id !== "lane-autoreview");
        if (manualLane) {
          state.activeLaneId = manualLane.id;
        }
      }
      const targetLane = activeLane();
      if (targetLane && !targetLane.running && targetLane.sessionId) {
        // Start a clean session when loading a new PR for review so old session history
        // and token counters from previous tasks/PRs never pollute this review.
        targetLane.sessionId = "";
        targetLane.turnInTokens = 0;
        targetLane.turnOutTokens = 0;
        targetLane.turnTotalTokens = 0;
        targetLane.sessionInTokens = 0;
        targetLane.sessionOutTokens = 0;
        targetLane.sessionTotalTokens = 0;
        targetLane.turnCostMicroUsd = 0;
        targetLane.sessionCostMicroUsd = 0;
        targetLane.costByRole = {};
        targetLane.messages = [];
      }
      renderLaneBarOnly();
      renderPipelineBar();
      renderTokenBar();
      rebuildChatMessagesFull();
      syncComposerButtons();
      state.showRightAI = true;
      syncLayoutVisibility();
      const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
      if (chatInput) {
        chatInput.value = state.draftObjective;
        chatInput.focus();
      }
      setStatus(`PR #${pr.number} carregado no chat do Chefe.`);
    });
  });

  document.querySelectorAll("[data-toggle-issue-subtasks]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const key = (btn as HTMLElement).dataset.toggleIssueSubtasks || "";
      if (!key) return;
      if (state.expandedIssueSubtasks.has(key)) {
        state.expandedIssueSubtasks.delete(key);
      } else {
        state.expandedIssueSubtasks.add(key);
      }
      renderLeftSidebar();
    });
  });

  document.querySelectorAll("[data-filter-parent-key]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const parentKey = ((btn as HTMLElement).dataset.filterParentKey || "").trim();
      if (!parentKey) return;
      state.issuesFilterQuery = state.issuesFilterQuery.trim().toUpperCase() === parentKey.toUpperCase() ? "" : parentKey;
      renderLeftSidebar();
    });
  });

  const loadIssueObjectiveIntoChat = (issueKey: string, objectiveText: string, source: string) => {
    if (source === "jira" && issueKey) {
      state.jiraKeyInput = issueKey;
      const jk = document.getElementById("jira-key-input") as HTMLInputElement | null;
      if (jk) jk.value = issueKey;
    }
    state.draftObjective = objectiveText;
    const targetLane = activeLane();
    if (targetLane && !targetLane.running && targetLane.sessionId) {
      // Start a clean session when loading a new Jira/GitHub issue from the sidebar.
      targetLane.sessionId = "";
      targetLane.turnInTokens = 0;
      targetLane.turnOutTokens = 0;
      targetLane.turnTotalTokens = 0;
      targetLane.sessionInTokens = 0;
      targetLane.sessionOutTokens = 0;
      targetLane.sessionTotalTokens = 0;
      targetLane.turnCostMicroUsd = 0;
      targetLane.sessionCostMicroUsd = 0;
      targetLane.costByRole = {};
      targetLane.messages = [];
      renderLaneBarOnly();
      renderPipelineBar();
      renderTokenBar();
      rebuildChatMessagesFull();
      syncComposerButtons();
    }
    state.showRightAI = true;
    syncLayoutVisibility();
    const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
    if (chatInput) {
      chatInput.value = state.draftObjective;
      chatInput.focus();
    }
    setStatus(`Issue ${issueKey} carregada no chat do Chefe.`);
  };

  document.querySelectorAll("[data-solve-subtask-key]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const el = btn as HTMLElement;
      const subKey = (el.dataset.solveSubtaskKey || "").trim();
      const subTitle = (el.dataset.solveSubtaskTitle || "").trim();
      const parentInfo = (el.dataset.solveSubtaskParent || "").trim();
      if (!subKey) return;
      const parentLine = parentInfo ? `\nTask pai: ${parentInfo}` : "";
      loadIssueObjectiveIntoChat(subKey, `Resolver issue ${subKey} — ${subTitle}${parentLine}`, "jira");
    });
  });

  document.querySelectorAll("[data-solve-issue]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const idx = parseInt((btn as HTMLElement).dataset.solveIssue || "0", 10);
      const iss = state.issues.list[idx];
      if (!iss) return;
      const parentLine = iss.parentKey
        ? `\nTask pai: ${iss.parentKey}${iss.parentTitle ? ` — ${iss.parentTitle}` : ""}`
        : "";
      const subLine = iss.subtaskTotal && iss.subtaskTotal > 0
        ? `\nSubtasks no Jira: ${iss.subtaskDone || 0} OK · ${iss.subtaskPending ?? Math.max(0, iss.subtaskTotal - (iss.subtaskDone || 0))} sem fazer = ${iss.subtaskTotal} subtasks`
        : "";
      const bodyPart = iss.body ? `\n\nDescrição:\n${iss.body.slice(0, 1200)}` : "";
      loadIssueObjectiveIntoChat(
        iss.key,
        `Resolver issue ${iss.key} — ${iss.title}${parentLine}${subLine}${bodyPart}`,
        iss.source
      );
    });
  });

  bindWorktreesTabEvents(renderLeftSidebar);
}
