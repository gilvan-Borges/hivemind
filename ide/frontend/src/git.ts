import { fileBadge, icons } from "./icons";
import type { AutoReviewSettings, AutoReviewStatus, FileDiff, GitGraphCommit, GitHubStatus, GitSubTab, ProjectInfo, ProjectIssue, ProjectPullRequest, TeamLane } from "./types";
import { escapeHtml, formatTokens } from "./utils";
import { rebuildChatMessagesFull, renderTokenBar, scheduleChatFlush, syncComposerButtons } from "./chat";
import { openDiffInEditor, openFileInEditor, promptDiscardChanges, renderEditorBody, renderEditorTabs } from "./editor";
import { bindTreeRowClicks, renderExplorerTreeHtml, renderLeftSidebar } from "./explorer";
import { activeLane, renderLaneBarOnly, renderPipelineBar } from "./lanes";
import { openGitHubModal, openJiraModal } from "./modals";
import { syncLayoutVisibility, toggleBranchPopover, updateHeaderAndStatusBar, updateStatusBar } from "./shell";
import { api, askConfirm, normalizePathKey, setStatus, state, toast, toastError } from "./state";

function withTimeout<T>(promise: Promise<T>, ms: number, label: string): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = window.setTimeout(() => {
      reject(new Error(`Tempo limite (${Math.round(ms / 1000)}s) ao ${label}.`));
    }, ms);
    promise.then(
      (val) => {
        window.clearTimeout(timer);
        resolve(val);
      },
      (err) => {
        window.clearTimeout(timer);
        reject(err);
      }
    );
  });
}

export async function refreshDiffsAndSummarize(lane: TeamLane) {
  await refreshDiffs();
  if (state.diffs.length === 0) return;
  const last = lane.messages[lane.messages.length - 1];
  if (last && last.kind === "diff") return;
  const totalAdd = state.diffs.reduce((s, d) => s + d.added, 0);
  const totalRem = state.diffs.reduce((s, d) => s + d.removed, 0);
  lane.messages.push({
    id: `diff-${Date.now()}`,
    kind: "diff",
    label: `${state.diffs.length} arquivo(s) alterado(s) (+${totalAdd} / -${totalRem})`,
    text: "",
    diffItems: state.diffs.map((d) => ({ path: d.path, added: d.added, removed: d.removed })),
  });
  if (lane.id === activeLane()?.id) {
    scheduleChatFlush();
  }
}

export function getGitStatusMap(): { files: Map<string, "M" | "U">; dirs: Set<string> } {
  const files = new Map<string, "M" | "U">();
  const dirs = new Set<string>();
  for (const d of state.diffs) {
    const p = (d.path || "").replace(/\\/g, "/");
    if (!p) continue;
    const isNew = d.lines.some((ln) => ln.kind === "header" && ln.text.startsWith("new file mode"));
    files.set(p, isNew ? "U" : "M");
    const parts = p.split("/");
    for (let i = 1; i < parts.length; i++) {
      dirs.add(parts.slice(0, i).join("/"));
    }
  }
  return { files, dirs };
}

export async function refreshDiffs() {
  const backend = api();
  if (!backend || !state.project) return;
  const targetRoot = state.project.root;
  const targetKey = normalizePathKey(targetRoot);
  try {
    const diffs = (await backend.GetDiff(targetRoot)) || [];
    const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
    if (ctx) ctx.diffs = diffs;
    if (!state.project || normalizePathKey(state.project.root) !== targetKey) {
      return;
    }
    state.diffs = diffs;
    const badge = document.getElementById("git-badge");
    if (badge) {
      badge.textContent = String(state.diffs.length);
      badge.style.display = state.diffs.length > 0 ? "inline-block" : "none";
    }
    // Sync any open diff tabs with updated diff objects
    let activeDiffChanged = false;
    for (const tab of [...state.editorTabs]) {
      if (tab.kind !== "diff" || !tab.diff) continue;
      const updated = state.diffs.find((d) => d.path === tab.diff?.path);
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
      renderLeftSidebar();
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

export const GRAPH_PALETTE = ["#4c8dff", "#fbbf24", "#ec4899", "#34d399", "#a78bfa", "#f97316"];

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

export function renderScmFileRowHtml(d: FileDiff): string {
  const parts = d.path.split("/");
  const fileName = parts.pop() || d.path;
  const dirPath = parts.join("\\");
  const st = (d.status || "M").toUpperCase();
  const stCls = st === "U" ? "untracked" : st === "A" ? "added" : st === "D" ? "deleted" : st === "R" ? "renamed" : "modified";
  return `
    <div class="scm-file-row ${state.activeEditorPath === "diff:" + d.path ? "active" : ""}" data-open-diff="${escapeHtml(d.path)}" title="${escapeHtml(d.path)} (+${d.added} -${d.removed})">
      <span class="scm-file-icon">${fileBadge(fileName)}</span>
      <span class="scm-file-name">${escapeHtml(fileName)}</span>
      ${dirPath ? `<span class="scm-file-dir">${escapeHtml(dirPath)}</span>` : `<span class="scm-file-dir"></span>`}
      <span class="scm-file-actions">
        <button type="button" class="scm-row-btn" data-scm-open-file="${escapeHtml(d.path)}" title="Abrir arquivo">${icons.openInNew}</button>
        <button type="button" class="scm-row-btn" data-scm-discard-file="${escapeHtml(d.path)}" title="Descartar alterações">${icons.undo}</button>
        ${d.staged
          ? `<button type="button" class="scm-row-btn" data-scm-unstage="${escapeHtml(d.path)}" title="Remover do Stage (Unstage)">${icons.minus}</button>`
          : `<button type="button" class="scm-row-btn" data-scm-stage="${escapeHtml(d.path)}" title="Preparar Alteração (Stage)">${icons.plus}</button>`
        }
      </span>
      <span class="scm-status-letter ${stCls}">${escapeHtml(st)}</span>
    </div>
  `;
}

export function renderGitGraphSectionHtml(): string {
  const g = state.gitGraph;
  const rowSvgs = g.commits.length > 0 ? buildGraphRowSvgs(g.commits) : [];
  return `
    <div class="scm-section">
      <div class="scm-section-head">
        <button type="button" class="scm-collapse-btn" id="btn-toggle-graph">
          ${g.collapsed ? icons.chevronRight : icons.chevronDown}
          <span>Graph</span>
        </button>
        <div class="spacer"></div>
        <button type="button" class="scm-mini-pill ${g.allBranches ? "active" : ""}" id="btn-graph-scope" title="${g.allBranches ? "Mostrando todas as branches (Auto)" : "Mostrando apenas a branch atual"}">
          ${icons.gitSmall}
          <span>${g.allBranches ? "Auto" : "Branch"}</span>
        </button>
        <button type="button" class="icon-btn" data-git-action="fetch" title="Fetch (buscar atualizações)">${icons.arrowDown}</button>
        <button type="button" class="icon-btn" data-git-action="pull" title="Pull (baixar commits)">${icons.download}</button>
        <button type="button" class="icon-btn" data-git-action="push" title="Push (enviar commits)">${icons.arrowUp}</button>
        <button type="button" class="icon-btn" id="btn-refresh-graph" title="Atualizar Graph">${icons.refresh}</button>
      </div>
      ${g.collapsed ? "" : `
        <div class="scm-graph-list" id="scm-graph-list">
          ${g.loading && !g.loadedOnce
            ? `<div class="dim" style="padding:8px 10px">${icons.spinner} Carregando histórico…</div>`
            : g.commits.length === 0
              ? `<div class="dim" style="padding:8px 10px">Nenhum commit neste repositório ainda.</div>`
              : g.commits.map((c, i) => `
                <div class="scm-graph-row ${state.activeEditorPath === "commit:" + c.hash ? "active" : ""}" data-open-commit="${escapeHtml(c.hash)}" title="${escapeHtml(c.short)} · ${escapeHtml(c.author)} · ${escapeHtml(c.date)}&#10;${escapeHtml(c.subject)}">
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
  return `
    <div class="issue-card">
      <div class="pr-card-top" style="justify-content:space-between">
        <div style="display:flex;align-items:center;gap:5px;flex-wrap:wrap">
          <span class="tok-pill">${escapeHtml(iss.source.toUpperCase())} · ${escapeHtml(iss.key)}</span>
          ${iss.isMine ? `<span class="flow-pill mine">Minha</span>` : ""}
        </div>
        ${iss.status ? `<span class="dim" style="font-size:10.5px">${escapeHtml(iss.status)}</span>` : ""}
      </div>
      <div class="pr-card-title">${escapeHtml(iss.title)}</div>
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

export function renderGitSidebarHtml(proj: ProjectInfo | null): string {
  const gh = state.ghStatus;
  const hasGit = Boolean(proj?.branch);
  const diffs = state.diffs;
  const stagedDiffs = diffs.filter((d) => d.staged);
  const unstagedDiffs = diffs.filter((d) => !d.staged);
  const hasDiffs = diffs.length > 0;
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
    tabContentHtml = `
      <div class="scm-commit-box">
        <input id="commit-msg-input" placeholder='Mensagem (Ctrl+Enter em "${escapeHtml(proj!.branch)}")' value="${escapeHtml(state.commitMsgInput)}" />
        <div class="scm-split-btn">
          <button type="button" class="primary scm-commit-main" id="btn-commit" ${hasDiffs ? "" : "disabled"}>${icons.check}<span>Commit</span></button>
          <button type="button" class="primary scm-commit-arrow" id="btn-commit-menu" ${hasDiffs ? "" : "disabled"} title="Mais opções de Commit">${icons.chevronDown}</button>
          ${state.commitMenuOpen ? `
            <div class="scm-dropdown" id="scm-commit-dropdown">
              <button type="button" class="dropdown-item" id="btn-commit-only">Commit</button>
              <button type="button" class="dropdown-item" id="btn-commit-push">Commit & Push</button>
              <button type="button" class="dropdown-item" id="btn-commit-sync">Commit & Sync (Pull + Push)</button>
            </div>
          ` : ""}
        </div>
      </div>

      <div class="scm-section">
        <div class="scm-section-head">
          <button type="button" class="scm-collapse-btn" id="btn-toggle-changes">
            ${state.changesCollapsed ? icons.chevronRight : icons.chevronDown}
            <span>Alterações</span>
          </button>
          <div class="spacer"></div>
          ${hasDiffs ? `<button type="button" class="icon-btn" id="btn-discard" title="Descartar todas as alterações">${icons.undo}</button>` : ""}
          <span class="scm-count-pill">${diffs.length}</span>
        </div>
        ${state.changesCollapsed ? "" : `
          <div id="git-diff-files">
            ${!hasDiffs
              ? `<div class="dim" style="padding:6px 10px">Nenhuma alteração pendente.</div>`
              : `${stagedDiffs.length > 0 ? `
                  <div class="scm-group-label">Preparadas (Staged) · ${stagedDiffs.length}</div>
                  ${stagedDiffs.map(renderScmFileRowHtml).join("")}
                ` : ""}
                ${unstagedDiffs.length > 0 ? `
                  ${stagedDiffs.length > 0 ? `<div class="scm-group-label">Alterações · ${unstagedDiffs.length}</div>` : ""}
                  ${unstagedDiffs.map(renderScmFileRowHtml).join("")}
                ` : ""}`
            }
          </div>
        `}
      </div>

      ${renderGitGraphSectionHtml()}
    `;
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
        <div class="git-section-head">
          <span>Issues abertas (${state.issues.list.length})</span>
        </div>
        <div id="git-issues-list">
          ${state.issues.loading && !state.issues.loadedOnce
            ? `<div class="dim">${icons.spinner} Carregando issues…</div>`
            : !state.issues.loadedOnce
              ? `<div class="dim">Aguardando carregamento…</div>`
              : state.issues.list.length === 0
                ? `<div class="dim">Nenhuma issue aberta encontrada.</div>`
                : state.issues.list.map((iss, idx) => renderIssueCardHtml(iss, idx)).join("")
          }
        </div>
      </div>
    `;
  } else {
    tabContentHtml = `
      <div class="git-section" style="padding:10px">
        ${renderAutoReviewCardHtml(state.autoReview)}
      </div>

      <div class="git-section" style="padding:10px">
        <div class="git-section-head">
          <span>Para revisar (${pendingReviews.length})</span>
          <button class="mini" id="btn-load-flow" ${state.prs.loading ? "disabled" : ""}>
            ${state.prs.loading ? icons.spinner : icons.refresh}
            <span>${state.prs.loadedOnce ? "Atualizar" : "Carregar"}</span>
          </button>
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
        <button type="button" class="icon-btn" id="btn-header-commit" title="Commitar alterações" ${hasDiffs ? "" : "disabled"}>${icons.check}</button>
        <button type="button" class="icon-btn" id="btn-refresh-diff" title="Atualizar Source Control">${icons.refresh}</button>
        <div class="scm-more-wrap">
          <button type="button" class="icon-btn" id="btn-scm-more" title="Mais ações Git…">${icons.moreHoriz}</button>
          ${state.gitMoreMenuOpen ? `
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
              ${hasDiffs ? `<div class="scm-menu-sep"></div><button type="button" class="dropdown-item danger" id="menu-discard-all">Descartar Todas as Alterações</button>` : ""}
            </div>
          ` : ""}
        </div>
      ` : ""}
    </div>
    <div class="sidebar-body scm-body">
      <div class="scm-repo-strip">
        <span class="gh-dot ${hasGit ? "ok" : "off"}"></span>
        <span class="scm-branch-label">${hasGit ? escapeHtml(proj!.branch) : "Sem repositório Git"}</span>
        <button type="button" class="scm-gh-pill ${gh?.loggedIn ? "ok" : "off"}" id="btn-github-connector" title="${escapeHtml([gh?.detail || "Configurar Conector GitHub Nativo", gh?.remote].filter(Boolean).join(" — "))}">
          ${gh?.loggedIn ? `@${escapeHtml(gh.user)}` : "Conectar GitHub"}
        </button>
      </div>
      ${!hasGit ? `<div style="padding:0 10px 8px"><button class="primary mini" id="btn-init-git" style="width:100%">Inicializar Git (git init)</button></div>` : ""}

      <div class="git-subtabs scm-tabs-pad">
        ${hasGit ? `<button type="button" class="git-subtab ${activeGitTab === "changes" ? "active" : ""}" data-git-tab="changes">Alterações (${diffs.length})</button>` : ""}
        <button type="button" class="git-subtab ${activeGitTab === "prs_issues" ? "active" : ""}" data-git-tab="prs_issues">PRs & Issues${prsIssuesTotal}</button>
        ${hasGit ? `<button type="button" class="git-subtab ${activeGitTab === "flow" ? "active" : ""}" data-git-tab="flow">Fluxo${flowBadge}</button>` : ""}
      </div>

      ${tabContentHtml}
    </div>
  `;
}

export async function loadGitGraph() {
  const backend = api();
  if (!backend || !state.project || state.gitGraph.loading || !backend.ListGitGraph) return;
  const targetRoot = state.project.root;
  const targetKey = normalizePathKey(targetRoot);
  const targetGraph = state.gitGraph;
  const allBranches = targetGraph.allBranches;
  targetGraph.loading = true;
  if (state.sidebarView === "git") renderLeftSidebar();
  try {
    targetGraph.commits = (await withTimeout(backend.ListGitGraph(targetRoot, 60, allBranches), 20000, "carregar o Graph de commits")) || [];
    targetGraph.loadedOnce = true;
  } catch (err: any) {
    if (state.project && normalizePathKey(state.project.root) === targetKey) {
      toastError("carregar o Graph de commits", err);
    }
  } finally {
    targetGraph.loading = false;
    if (state.sidebarView === "git" && state.project && normalizePathKey(state.project.root) === targetKey) {
      renderLeftSidebar();
    }
  }
}

export async function openCommitInEditor(hash: string) {
  const backend = api();
  if (!backend || !state.project || !backend.GetCommitDetail) return;
  try {
    const detail = await backend.GetCommitDetail(state.project.root, hash);
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

export async function runGitScmAction(action: string) {
  const backend = api();
  if (!backend || !state.project || !backend.RunGitAction) return;
  setStatus(`Executando git ${action}…`);
  try {
    const out = await backend.RunGitAction(state.project.root, action);
    setStatus(out || `Git ${action} concluído.`);
    toast(out || `Git ${action} concluído.`, "success");
    await refreshDiffs();
    await loadGitGraph();
  } catch (err: any) {
    toastError(`executar git ${action}`, err);
  }
}

export async function loadGitHubPRs() {
  const backend = api();
  if (!backend || !state.project || state.prs.loading) return;
  const targetRoot = state.project.root;
  const targetKey = normalizePathKey(targetRoot);
  const targetPrs = state.prs;
  targetPrs.loading = true;
  if (state.sidebarView === "git") renderLeftSidebar();
  try {
    targetPrs.list = (await withTimeout(backend.ListPullRequests(targetRoot), 20000, "buscar os Pull Requests")) || [];
    targetPrs.loadedOnce = true;
  } catch (err: any) {
    if (state.project && normalizePathKey(state.project.root) === targetKey) {
      toastError("buscar os Pull Requests do repositório", err);
    }
  } finally {
    targetPrs.loading = false;
    if (state.sidebarView === "git" && state.project && normalizePathKey(state.project.root) === targetKey) {
      renderLeftSidebar();
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
  if (state.sidebarView === "git") renderLeftSidebar();
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
      renderLeftSidebar();
    }
  }
}

export function bindGitSidebarEvents() {
  const doCommit = async (mode: "commit" | "push" | "sync") => {
    const backend = api();
    if (!backend || !state.project) return;
    state.commitMenuOpen = false;
    try {
      const msg = await backend.CommitChanges(state.project.root, state.commitMsgInput, mode === "push");
      state.commitMsgInput = "";
      if (mode === "sync" && backend.RunGitAction) {
        const syncMsg = await backend.RunGitAction(state.project.root, "sync");
        setStatus(`${msg} ${syncMsg}`);
        toast(`${msg} ${syncMsg}`, "success");
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
  });

  document.getElementById("btn-scm-more")?.addEventListener("click", (ev) => {
    ev.stopPropagation();
    state.gitMoreMenuOpen = !state.gitMoreMenuOpen;
    state.commitMenuOpen = false;
    renderLeftSidebar();
  });

  document.getElementById("btn-commit-menu")?.addEventListener("click", (ev) => {
    ev.stopPropagation();
    state.commitMenuOpen = !state.commitMenuOpen;
    state.gitMoreMenuOpen = false;
    renderLeftSidebar();
  });

  document.querySelectorAll("[data-git-tab]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const tab = (btn as HTMLElement).dataset.gitTab as GitSubTab;
      state.gitTab = tab;
      state.gitMoreMenuOpen = false;
      state.commitMenuOpen = false;
      renderLeftSidebar();
      if (tab === "changes" && state.project?.branch && !state.gitGraph.loadedOnce && !state.gitGraph.loading) {
        void loadGitGraph();
      }
      if (tab === "prs_issues") {
        if (!state.prs.loadedOnce && !state.prs.loading && state.project?.branch) void loadGitHubPRs();
        if (!state.issues.loadedOnce && !state.issues.loading) void loadProjectIssues();
      }
      if (tab === "flow" && !state.prs.loadedOnce && !state.prs.loading) {
        void loadGitHubPRs();
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
      state.gitMoreMenuOpen = false;
      const action = (btn as HTMLElement).dataset.gitAction!;
      void runGitScmAction(action);
    });
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
    void toggleBranchPopover(true);
  });
  document.getElementById("menu-discard-all")?.addEventListener("click", () => {
    state.gitMoreMenuOpen = false;
    promptDiscardChanges();
  });

  document.getElementById("btn-init-git")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend || !state.project) return;
    try {
      await backend.InitGit(state.project.root);
      const updated = await backend.OpenProject(state.project.root);
      state.project.branch = updated.branch;
      state.ghStatus = await backend.GitHubStatus(state.project.root);
      updateHeaderAndStatusBar();
      renderLeftSidebar();
      setStatus("Repositório Git inicializado (branch main).");
    } catch (err: any) {
      toastError("inicializar o repositório Git", err);
    }
  });

  document.querySelectorAll("[data-open-diff]").forEach((el) => {
    el.addEventListener("click", () => {
      const p = (el as HTMLElement).dataset.openDiff!;
      const d = state.diffs.find((x) => x.path === p);
      if (d) openDiffInEditor(d);
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
      const rel = (btn as HTMLElement).dataset.scmDiscardFile!;
      if (!state.project) return;
      askConfirm({
        title: `Descartar "${rel}"`,
        body: `As alterações locais em "${rel}" serão descartadas permanentemente.`,
        confirmLabel: "Descartar",
        danger: true,
        onConfirm: async () => {
          if (!state.project) return;
          try {
            await api()?.DiscardFile(state.project.root, rel);
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
      if (!state.project || !api()?.StageFile) return;
      try {
        await api()!.StageFile(state.project.root, rel, stage);
        await refreshDiffs();
      } catch (err: any) {
        toastError(stage ? `preparar "${rel}"` : `remover "${rel}" do stage`, err);
      }
    });
  });

  document.querySelectorAll("[data-open-commit]").forEach((row) => {
    row.addEventListener("click", () => {
      const hash = (row as HTMLElement).dataset.openCommit!;
      void openCommitInEditor(hash);
    });
  });

  const commitIn = document.getElementById("commit-msg-input") as HTMLInputElement | null;
  commitIn?.addEventListener("input", () => {
    state.commitMsgInput = commitIn.value;
  });
  commitIn?.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      if (state.diffs.length > 0) void doCommit("commit");
    }
  });

  document.getElementById("btn-commit")?.addEventListener("click", () => void doCommit("commit"));
  document.getElementById("btn-header-commit")?.addEventListener("click", () => void doCommit("commit"));
  document.getElementById("btn-commit-only")?.addEventListener("click", () => void doCommit("commit"));
  document.getElementById("btn-commit-push")?.addEventListener("click", () => void doCommit("push"));
  document.getElementById("btn-commit-sync")?.addEventListener("click", () => void doCommit("sync"));

  const openPrAction = async () => {
    const backend = api();
    if (!backend || !state.project) return;
    state.gitMoreMenuOpen = false;
    try {
      const url = await backend.OpenPR(state.project.root, state.commitMsgInput, "", state.project.teamConfig?.settings?.base_branch || "main");
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

  document.getElementById("btn-discard")?.addEventListener("click", () => promptDiscardChanges());
  document.getElementById("btn-load-prs-issues")?.addEventListener("click", () => {
    if (state.project?.branch) void loadGitHubPRs();
    void loadProjectIssues();
  });
  document.getElementById("btn-load-flow")?.addEventListener("click", () => void loadGitHubPRs());

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
      } else if (pr.needsAttention && pr.myResponse) {
        state.draftObjective = `Reavaliar PR #${pr.number} de @${pr.author || "terceiro"}${branchInfo} — ${pr.title}. Houve ${pr.attentionReason ? pr.attentionReason.toLowerCase() : "nova atualização"} após minha última revisão; leia os comentários mais recentes com github_get_pr antes de responder.${bodyPart}`;
      } else {
        state.draftObjective = `Revisar PR #${pr.number} de @${pr.author || "terceiro"}${branchInfo} — ${pr.title}${bodyPart}`;
      }
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

  document.querySelectorAll("[data-solve-issue]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const idx = parseInt((btn as HTMLElement).dataset.solveIssue || "0", 10);
      const iss = state.issues.list[idx];
      if (!iss) return;
      if (iss.source === "jira") {
        state.jiraKeyInput = iss.key;
        const jk = document.getElementById("jira-key-input") as HTMLInputElement | null;
        if (jk) jk.value = iss.key;
      }
      const bodyPart = iss.body ? `\n\nDescrição:\n${iss.body.slice(0, 1200)}` : "";
      state.draftObjective = `Resolver issue ${iss.key} — ${iss.title}${bodyPart}`;
      state.showRightAI = true;
      syncLayoutVisibility();
      const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
      if (chatInput) {
        chatInput.value = state.draftObjective;
        chatInput.focus();
      }
      setStatus(`Issue ${iss.key} carregada no chat do Chefe.`);
    });
  });
}
