import { fileBadge, icons } from "./icons";
import type { AgentRuntime, ChatMessage, FileNode, PermissionMode, ProjectWorkspaceContext } from "./types";
import { chipForAgent, escapeHtml, exactTokens, formatTokens, matchAgentRuntime, modelChipHtml, permissionModeLabel, reasonPhrase, renderSkeletonRowsHtml, roleLabelPt } from "./utils";
import { openFolderDialog, refreshSessions } from "./boot";
import { rebuildChatMessagesFull, renderComposerModeBar, renderTokenBar } from "./chat";
import { openFileInEditor, renderEditorBody, renderEditorTabs } from "./editor";
import { bindGitSidebarEvents, clearCenterStage, getGitStatusMap, placeGitTabContent, renderGitSidebarHtml } from "./git";
import { activeLane, buildAgentRuntimes, confirmDeleteSavedTeam, renderLaneBarOnly } from "./lanes";
import { syncHiveBinding } from "./resident";
import { renderModalLayer } from "./modals";
import { notifyRegionRendered } from "./render-guard";
import { bindMCPSidebarEvents, refreshMCP, renderMCPSidebar } from "./mcp";
import { bindModelPickerPairs, handleInlineModelChange, refreshSkills, renderModelPickerPairHtml, renderSkillRowHtml } from "./models";
import { syncLayoutVisibility, updateStatusBar } from "./shell";
import { api, askConfirm, beginBusyOp, endBusyOp, normalizePathKey, setStatus, state, toast, toastError, triggerEnterMotion } from "./state";
import { syncCodeMirrorDoc } from "./cm-editor";
import { openMissionControlTab, openTeamConfigTab, openTeamMemoryFile, updateMissionBoardIfOpen } from "./teamconfig";

export let globalSearchTimer = 0;

export let explorerFilterTimer = 0;

// Coalescência de eventos de filesystem vindos do backend (hivemind:fs-event).
// O backend já agrupa em lotes com debounce; aqui agrupamos de novo por 250ms
// para não re-renderizar a árvore várias vezes por segundo.
export const fsPendingBatches = new Map<string, { changed: Set<string>; overflow: boolean }>();

export let fsFlushTimer: ReturnType<typeof setTimeout> | null = null;

export function queueFileSystemRefresh(wsKey: string, changed: string[], overflow: boolean) {
  let entry = fsPendingBatches.get(wsKey);
  if (!entry) {
    entry = { changed: new Set<string>(), overflow: false };
    fsPendingBatches.set(wsKey, entry);
  }
  for (const c of changed) entry.changed.add(c);
  entry.overflow = entry.overflow || overflow;
  if (fsFlushTimer) clearTimeout(fsFlushTimer);
  fsFlushTimer = setTimeout(flushFileSystemRefreshes, 250);
}

export async function flushFileSystemRefreshes() {
  fsFlushTimer = null;
  const batches = [...fsPendingBatches.entries()];
  fsPendingBatches.clear();
  for (const [wsKey, entry] of batches) {
    const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === wsKey);
    if (!ctx) continue;
    try {
      await applyExternalFileChanges(ctx, entry.changed, entry.overflow);
    } catch {
      // erro transitório de leitura; o próximo lote recupera
    }
  }
}

// Recarrega do disco as abas de editor abertas e NÃO sujas que mudaram
// (ex.: o agente editou o arquivo que está na sua tela) e atualiza a árvore
// do explorador. Abas com edição não salva nunca são sobrescritas — nelas,
// o conflito é sinalizado com um aviso para o usuário decidir (Fase 2, item 3).

export async function applyExternalFileChanges(ctx: ProjectWorkspaceContext, changed: Set<string>, overflow: boolean) {
  const backend = api();
  if (!backend) return;
  const isActive = ctx.id === state.activeProjectId;
  const tabs = ctx.editorTabs;
  const allTargets = tabs.filter((t) => t.kind === "file" && (overflow || changed.has(t.relPath)));
  const dirtyTargets = allTargets.filter((t) => t.dirty);
  if (dirtyTargets.length > 0) {
    const names = dirtyTargets.map((t) => t.name).join(", ");
    toast(`O arquivo aberto mudou no disco e a aba tem edições não salvas: ${names}. Salve (Ctrl+S) ou recarregue antes de continuar.`, "error", { timeoutMs: 9000 });
  }
  const targets = allTargets.filter((t) => !t.dirty);
  let reloaded = 0;
  let activeReloaded = false;
  for (const tab of targets) {
    try {
      const fresh = await backend.ReadFile(ctx.project.root, tab.relPath);
      if (fresh !== tab.content) {
        tab.content = fresh;
        reloaded++;
        if (isActive && tab.relPath === state.activeEditorPath) {
          // Substitui só o doc da view ativa: sem re-render, sem perder a janela.
          activeReloaded = !syncCodeMirrorDoc(tab.relPath, fresh);
        }
      }
    } catch {
      // arquivo removido ou grande demais: mantém o conteúdo atual em tela
    }
  }
  if (isActive) {
    if (reloaded > 0) {
      renderEditorTabs();
      if (activeReloaded) renderEditorBody();
    }
    await refreshExplorerTree();
  }
}

export async function refreshExplorerTree() {
  const backend = api();
  if (!backend || !state.project) return;
  const refreshBtn = document.getElementById("btn-refresh-tree") as HTMLButtonElement | null;
  refreshBtn?.classList.add("is-spinning");
  if (refreshBtn) refreshBtn.disabled = true;
  beginBusyOp("explorer-refresh", "Atualizando arquivos…");
  try {
    const rootNodes = await backend.ListDirectory(state.project.root, "");
    state.dirCache.clear();
    state.dirCache.set("", rootNodes || []);
    for (const dir of [...state.expandedDirs]) {
      try {
        const ch = await backend.ListDirectory(state.project.root, dir);
        state.dirCache.set(dir, ch || []);
      } catch {
        state.expandedDirs.delete(dir);
      }
    }
    const body = document.getElementById("explorer-tree-body");
    if (body) {
      body.innerHTML = renderExplorerTreeHtml();
      bindTreeRowClicks();
    }
  } finally {
    endBusyOp("explorer-refresh");
    refreshBtn?.classList.remove("is-spinning");
    if (refreshBtn) refreshBtn.disabled = false;
  }
}

export const TREE_DIMMED_DIRS = new Set([
  "node_modules", "dist", "build", "out", "target", "coverage", "vendor",
  ".bin", ".cache", ".next", ".nuxt", ".angular", ".turbo", "__pycache__",
]);

export function treeFolderColor(name: string): string {
  const low = name.toLowerCase();
  if (low === "node_modules" || low === "vendor") return "#6ee7b7";
  if (low === "dist" || low === "build" || low === "out" || low === "target" || low === "bin" || low === ".bin") return "#f48fb1";
  if (low === "docs" || low === "doc" || low === "specs") return "#60a5fa";
  if (low === "cmd" || low === "src" || low === "internal" || low === "pkg" || low === "frontend" || low === "app" || low === "web") return "#38bdf8";
  if (low === "ide" || low === "core" || low === "lib") return "#94a3b8";
  if (low === "examples" || low === "assets" || low === "public" || low === "images" || low === "icons" || low === "design-tokens") return "#2dd4bf";
  if (low === "tests" || low === "test" || low === "__tests__" || low === "e2e") return "#34d399";
  if (low === "scripts" || low === "hooks" || low === "tools") return "#f59e0b";
  if (low === ".github" || low === ".hivemind" || low === ".agents" || low === ".claude" || low === ".specify" || low === "deploy" || low === "postgres") return "var(--accent)";
  return "#8fa3bf";
}

export function renderTreeGuidesHtml(depth: number, basePad: number, indentStep: number): string {
  if (depth <= 0) return "";
  let out = "";
  for (let i = 0; i < depth; i++) {
    const left = basePad + i * indentStep + 7;
    out += `<span class="tree-guide" style="left:${left}px"></span>`;
  }
  return out;
}

export function renderTreeNodes(
  nodes: FileNode[],
  depth = 0,
  filter = "",
  gitMap = getGitStatusMap(),
  inheritedDim = false
): string {
  const q = filter.trim().toLowerCase();
  const basePad = 8;
  const indentStep = 12;
  const pad = basePad + depth * indentStep;
  const guidesHtml = renderTreeGuidesHtml(depth, basePad, indentStep);

  return nodes
    .map((n) => {
      if (n.isDir) {
        const open = state.expandedDirs.has(n.relPath);
        const isDim = inheritedDim || TREE_DIMMED_DIRS.has(n.name.toLowerCase());
        const children = state.dirCache.get(n.relPath) ?? n.children ?? [];
        const childrenHtml = open ? renderTreeNodes(children, depth + 1, filter, gitMap, isDim) : "";
        if (q && !n.name.toLowerCase().includes(q) && !childrenHtml) {
          return "";
        }
        const hasModChild = gitMap.dirs.has(n.relPath);
        const fColor = treeFolderColor(n.name);
        return `
          <div class="tree-row ${hasModChild ? "git-mod" : ""} ${isDim ? "is-dimmed" : ""}" data-dir="${escapeHtml(n.relPath)}" style="padding-left:${pad}px" title="${escapeHtml(n.relPath)}">
            ${guidesHtml}
            <span class="tree-chevron">${open ? icons.chevronDown : icons.chevronRight}</span>
            <span class="tree-folder-icon" style="color:${fColor}">${open ? icons.folderOpen : icons.folderClosed}</span>
            <span class="tree-name">${escapeHtml(n.name)}</span>
            ${hasModChild ? `<span class="tree-dir-dot" title="Contém arquivos modificados"></span>` : ""}
            <span class="tree-actions">
              <button class="tree-act-btn" data-tree-newfile="${escapeHtml(n.relPath)}" title="Novo arquivo nesta pasta">${icons.plus}</button>
              <button class="tree-act-btn" data-tree-rename="${escapeHtml(n.relPath)}" title="Renomear">${icons.editPen}</button>
              <button class="tree-act-btn" data-tree-delete="${escapeHtml(n.relPath)}" title="Excluir">${icons.trash}</button>
            </span>
          </div>
          ${childrenHtml}
        `;
      }
      if (q && !n.name.toLowerCase().includes(q) && !n.relPath.toLowerCase().includes(q)) {
        return "";
      }
      const isAct = state.activeEditorPath === n.relPath ? "active" : "";
      const gitSt = gitMap.files.get(n.relPath);
      const gitCls = gitSt === "U" ? "git-add" : gitSt === "M" ? "git-mod" : "";
      const gitBadge = gitSt === "U"
        ? `<span class="tree-git-badge add" title="Arquivo novo / não rastreado">U</span>`
        : gitSt === "M"
          ? `<span class="tree-git-badge mod" title="Arquivo modificado">M</span>`
          : "";
      return `
        <div class="tree-row ${isAct} ${gitCls} ${inheritedDim ? "is-dimmed" : ""}" data-file="${escapeHtml(n.relPath)}" data-name="${escapeHtml(n.name)}" style="padding-left:${pad + 19}px" title="${escapeHtml(n.relPath)}">
          ${guidesHtml}
          ${fileBadge(n.name)}
          <span class="tree-name">${escapeHtml(n.name)}</span>
          ${gitBadge}
          <span class="tree-actions">
            <button class="tree-act-btn" data-tree-rename="${escapeHtml(n.relPath)}" title="Renomear">${icons.editPen}</button>
            <button class="tree-act-btn" data-tree-delete="${escapeHtml(n.relPath)}" title="Excluir">${icons.trash}</button>
          </span>
        </div>
      `;
    })
    .join("");
}

export function highlightActiveTreeRow() {
  document.querySelectorAll("#left-sidebar [data-file]").forEach((el) => {
    el.classList.toggle("active", (el as HTMLElement).dataset.file === state.activeEditorPath);
  });
}

export function bindSkillsSidebarEvents(): void {
  document.getElementById("btn-skill-refresh")?.addEventListener("click", () => refreshSkills());
  document.querySelectorAll("[data-skill-assign]").forEach((btn) => {
    btn.addEventListener("click", () => {
      void openTeamConfigTab("skills");
    });
  });
  document.getElementById("btn-skill-new")?.addEventListener("click", () => {
    state.skillEditor = {
      mode: "create",
      name: "",
      body: "# Minha Skill\n\nDescreva em uma frase quando o Chefe deve usar estas diretrizes.\n\n## Diretrizes\n\n- …",
    };
    renderModalLayer();
  });
  document.querySelectorAll("[data-skill-edit]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const name = (btn as HTMLElement).dataset.skillEdit!;
      const backend = api();
      if (!backend) return;
      try {
        const body = await backend.ReadNativeSkill(name);
        state.skillEditor = { mode: "edit", name, body };
        renderModalLayer();
      } catch (err: any) {
        toastError(`abrir a skill nativa "${name}"`, err);
      }
    });
  });
  document.querySelectorAll("[data-skill-enable], [data-skill-disable]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const el = btn as HTMLElement;
      const name = el.dataset.skillEnable || el.dataset.skillDisable || "";
      const enabled = Boolean(el.dataset.skillEnable);
      const backend = api();
      if (!backend?.SetNativeSkillEnabled || !name) return;
      try {
        await backend.SetNativeSkillEnabled(name, enabled);
        setStatus(`Skill "${name}" ${enabled ? "ativada" : "desligada"}.`);
        await refreshSkills();
      } catch (err: any) {
        toastError(`alterar a skill "${name}"`, err);
      }
    });
  });
  document.querySelectorAll("[data-skill-install]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const name = (btn as HTMLElement).dataset.skillInstall!;
      const backend = api();
      if (!backend || !state.project) return;
      try {
        await backend.InstallSkillToProject(state.project.root, name);
        setStatus(`Skill "${name}" instalada em .agents/skills do projeto.`);
        await refreshSkills();
        await refreshExplorerTree();
      } catch (err: any) {
        toastError(`instalar a skill "${name}" no projeto`, err);
      }
    });
  });
  document.querySelectorAll("[data-skill-delete-native]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const name = (btn as HTMLElement).dataset.skillDeleteNative!;
      askConfirm({
        title: `Excluir skill nativa "${name}"`,
        body: `A skill nativa "${name}" será removida da IDE (as skills dos projetos não são afetadas). Excluir?`,
        confirmLabel: "Excluir Skill",
        danger: true,
        onConfirm: async () => {
          try {
            await api()?.DeleteNativeSkill(name);
            setStatus(`Skill nativa "${name}" excluída.`);
            await refreshSkills();
          } catch (err: any) {
            toastError(`excluir a skill nativa "${name}"`, err);
          }
        },
      });
    });
  });
  document.querySelectorAll("[data-skill-remove-project]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const name = (btn as HTMLElement).dataset.skillRemoveProject!;
      askConfirm({
        title: `Remover skill "${name}" do projeto`,
        body: `A skill "${name}" será removida de .agents/skills deste projeto (a nativa da IDE, se houver, permanece). Remover?`,
        confirmLabel: "Remover do Projeto",
        danger: true,
        onConfirm: async () => {
          if (!state.project) return;
          try {
            await api()?.RemoveProjectSkill(state.project.root, name);
            setStatus(`Skill "${name}" removida do projeto.`);
            await refreshSkills();
            await refreshExplorerTree();
          } catch (err: any) {
            toastError(`remover a skill "${name}" do projeto`, err);
          }
        },
      });
    });
  });
}

export function renderLeftSidebar() {
  const sidebar = document.getElementById("left-sidebar");
  if (!sidebar) return;
  // A region is being re-rendered directly (user interaction or explicit
  // refresh): any async render deferred by the guard is now satisfied.
  notifyRegionRendered("left-sidebar");

  const sameView = sidebar.dataset.renderedView === state.sidebarView;
  const sameGitTab = state.sidebarView !== "git" || sidebar.dataset.renderedGitTab === state.gitTab;
  const preserveScroll = sameView && sameGitTab;
  const prevScrollTop = preserveScroll
    ? (sidebar.querySelector<HTMLElement>(".sidebar-body")?.scrollTop ?? 0)
    : 0;
  const stageEl = document.getElementById("center-stage");
  const prevStageScrollTop = preserveScroll
    ? ((stageEl?.querySelector<HTMLElement>("#git-tab-content")?.scrollTop ?? stageEl?.scrollTop) || 0)
    : 0;
  sidebar.dataset.renderedView = state.sidebarView;
  sidebar.dataset.renderedGitTab = state.sidebarView === "git" ? state.gitTab : "";

  // Evita IDs duplicados entre #left-sidebar e #center-stage durante bindGitSidebarEvents.
  clearCenterStage();
  try {
    renderLeftSidebarInner(sidebar);
  } finally {
    placeGitTabContent(sidebar);
    if (preserveScroll && prevScrollTop > 0) {
      const nextBody = sidebar.querySelector<HTMLElement>(".sidebar-body");
      if (nextBody) nextBody.scrollTop = prevScrollTop;
    }
    if (preserveScroll && prevStageScrollTop > 0 && stageEl && !stageEl.hidden) {
      const stageScroll = stageEl.querySelector<HTMLElement>("#git-tab-content") || stageEl;
      stageScroll.scrollTop = prevStageScrollTop;
    }
    if (!sameView) {
      triggerEnterMotion(sidebar.querySelector<HTMLElement>(".sidebar-body"), "hm-view-enter");
    }
    if (state.sidebarView === "git" && (!sameView || !sameGitTab)) {
      triggerEnterMotion(document.getElementById("git-tab-content"), "hm-view-enter");
    }
  }
}

function renderLeftSidebarInner(sidebar: HTMLElement) {

  const proj = state.project;
  const lane = activeLane();
  const teams = proj?.teamConfig?.teams || [];

  if (state.sidebarView === "search") {
    sidebar.innerHTML = `
      <div class="sidebar-header">
        <span class="eyebrow">Busca no Código</span>
        <div class="spacer"></div>
        <span class="dim" style="font-size:10.5px">Ctrl+Shift+F</span>
      </div>
      <div class="sidebar-filter" style="display:flex;gap:5px">
        <input id="global-search-input" placeholder="Buscar texto nos arquivos…" value="${escapeHtml(state.textSearch.query)}" style="flex:1" />
        <button class="mini primary" id="btn-run-global-search">${icons.search}</button>
      </div>
      <div class="sidebar-body" id="global-search-results">
        ${renderGlobalSearchResultsHtml()}
      </div>
    `;
    bindGlobalSearchEvents();
    return;
  }

  if (state.sidebarView === "sessions") {
    const sum = state.sessionsSummary;
    const projTotal = Math.max(sum.projectTotalTokens, lane?.sessionTotalTokens || 0);
    sidebar.innerHTML = `
      <div class="sidebar-header">
        <span class="eyebrow">Sessões & Tokens (${sum.sessions.length})</span>
        <div class="spacer"></div>
        <button class="icon-btn" id="btn-refresh-sessions" title="Atualizar sessões">${icons.refresh}</button>
      </div>
      <div class="sidebar-body">
        <div class="gh-card" style="margin-bottom:10px">
          <div class="dim" style="color:var(--accent);font-weight:600">Consumo acumulado do projeto</div>
          <div style="display:flex;align-items:center;gap:6px;font-size:18px;font-weight:700;font-family:var(--mono);color:var(--text)">
            ${icons.bolt}
            <span>${exactTokens(projTotal)}</span>
            <span class="dim" style="font-size:11px;font-weight:400">tokens</span>
          </div>
          <div class="dim" style="font-family:var(--mono);font-size:11px">
            ${(() => {
              const pCache = sum.projectCachedReadTokens || 0;
              const pReal = Math.max(0, sum.projectInputTokens - pCache);
              return pCache > 0
                ? `Real: ${exactTokens(pReal)} · Cache: ${exactTokens(pCache)} · Saída: ${exactTokens(sum.projectOutputTokens)}`
                : `Entrada: ${exactTokens(sum.projectInputTokens)} · Saída: ${exactTokens(sum.projectOutputTokens)}`;
            })()}
          </div>
        </div>
        <div class="dim" style="font-weight:600;margin-bottom:6px">Histórico salvo (.hivemind/sessions)</div>
        ${sum.sessions.length === 0
          ? `<div class="dim">Nenhuma sessão salva neste projeto ainda. Envie uma mensagem ao Chefe para iniciar.</div>`
          : sum.sessions.map((s) => {
            const sCache = s.cachedReadTokens || 0;
            const sReal = Math.max(0, s.inputTokens - sCache);
            const tip = sCache > 0
              ? `Real: ${exactTokens(sReal)} | Cache: ${exactTokens(sCache)} | Saída: ${exactTokens(s.outputTokens)}`
              : `Entrada: ${exactTokens(s.inputTokens)} | Saída: ${exactTokens(s.outputTokens)}`;
            const line = sCache > 0
              ? `Real: ${exactTokens(sReal)} · Cache: ${exactTokens(sCache)} · Saída: ${exactTokens(s.outputTokens)}`
              : `Entrada: ${exactTokens(s.inputTokens)} · Saída: ${exactTokens(s.outputTokens)}`;
            return `
            <div class="session-card ${lane?.sessionId === s.id ? "active" : ""}">
              <div class="session-title" title="${escapeHtml(s.title)}">${escapeHtml(s.title)}${s.kind === "hive" ? ` <span class="dim">· Hive</span>` : ""}</div>
              <div class="session-meta">
                <span>${escapeHtml(s.updatedAt)}${s.jiraKey ? ` · <strong>${escapeHtml(s.jiraKey)}</strong>` : ""}</span>
                <span class="tok-pill" title="${tip}">${icons.bolt}<span>${exactTokens(s.totalTokens)} tok</span></span>
              </div>
              <div class="dim" style="font-family:var(--mono);font-size:10.5px">
                ${line}
              </div>
              <div style="display:flex;gap:6px;margin-top:2px">
                <button class="mini primary" style="flex:1" data-resume-session="${escapeHtml(s.id)}">Retomar Sessão</button>
                <button class="mini danger" data-delete-session="${escapeHtml(s.id)}" title="Excluir sessão">${icons.trash}</button>
              </div>
            </div>
          `; }).join("")
        }
      </div>
    `;
    bindSessionsSidebarEvents();
    return;
  }

  if (state.sidebarView === "mcp") {
    sidebar.innerHTML = renderMCPSidebar();
    bindMCPSidebarEvents(() => renderLeftSidebar());
    return;
  }

  if (state.sidebarView === "skills") {
    const native = state.skills.filter((s) => s.source === "native");
    const project = state.skills.filter((s) => s.source === "project");
    sidebar.innerHTML = `
      <div class="sidebar-header">
        <span class="eyebrow">Skills</span>
        <div class="spacer"></div>
        <button class="icon-btn" id="btn-skill-refresh" title="Atualizar catálogo">${icons.refresh}</button>
        <button class="mini primary" id="btn-skill-new" title="Criar uma skill nativa da IDE">${icons.plus}<span>Nova</span></button>
      </div>
      <div class="sidebar-body">
        <div class="dim" style="font-weight:600;margin-bottom:6px">Nativas da IDE (${native.length}) · valem para todos os projetos</div>
        ${native.length === 0
          ? `<div class="dim" style="margin-bottom:12px">Nenhuma skill nativa ainda. Clique em <strong>Nova</strong> para criar diretrizes reutilizáveis — o Chefe as avalia ao delegar tarefas.</div>`
          : native.map((s) => renderSkillRowHtml(s, true)).join("")
        }
        <div class="dim" style="font-weight:600;margin:12px 0 6px">Do projeto (${project.length}) · .agents/skills</div>
        ${project.length === 0
          ? `<div class="dim">Este projeto não tem skills em .agents/skills. Instale uma nativa aqui ou crie .agents/skills/&lt;nome&gt;/SKILL.md.</div>`
          : project.map((s) => renderSkillRowHtml(s, false)).join("")
        }
      </div>
    `;
    bindSkillsSidebarEvents();
    return;
  }

  if (state.sidebarView === "team") {
    const maxTok = proj?.teamConfig?.settings?.max_tokens || 0;
    const permMode = (proj?.teamConfig?.settings?.permission_mode || "governed") as PermissionMode;
    const agents = lane?.agents || [];
    const activeCount = agents.filter((a) => a.status === "running").length;
    const maxDevs = proj?.teamConfig?.settings?.max_dynamic_devs || 5;
    sidebar.innerHTML = `
      <div class="sidebar-header">
        <span class="eyebrow">Equipe (${agents.length})</span>
        <div class="spacer"></div>
        <button class="mini" id="btn-open-mission-sb" title="Abrir Quadro Multiagente">${icons.swarm}<span>Quadro</span></button>
        <button class="mini" id="btn-config-team" title="Configurar Equipe e Skills">${icons.settings}<span>Config</span></button>
      </div>
      <div class="sidebar-body team-sb-body">
        <section class="team-sb-island" aria-label="Configuração da equipe">
          <div class="team-sb-team-row">
            <select id="team-select" class="team-sb-select team-sb-select-main" ${lane?.running ? "disabled" : ""} aria-label="Equipe ativa">
              ${teams.map((t) => `<option value="${escapeHtml(t.name)}" ${t.name === lane?.teamName ? "selected" : ""}>${escapeHtml(t.name)}</option>`).join("")}
            </select>
            <button class="mini team-sb-new-btn" id="btn-new-team-sb" title="Criar nova equipe">${icons.plus}<span>Novo</span></button>
            ${teams.length > 0 ? `<button class="mini danger team-sb-del-btn" id="btn-delete-team-sb" title="Excluir equipe selecionada e parar todos os serviços">${icons.trash}</button>` : ""}
          </div>

          <div class="team-sb-field">
            <div class="team-sb-field-head">
              <label class="team-sb-label" for="sb-permission-select">Permissão & Autonomia</label>
            </div>
            <select id="sb-permission-select" class="team-sb-select">
              <option value="governed" ${permMode === "governed" ? "selected" : ""}>Governado (Worktree + PR + Aprovação)</option>
              <option value="auto_edit" ${permMode === "auto_edit" ? "selected" : ""}>Auto-Edição (Edições e comandos livres)</option>
              <option value="full_access" ${permMode === "full_access" ? "selected" : ""}>Full Acesso & Permissão Total</option>
            </select>
            ${state.cursorCLI?.sandboxWarning ? `
              <details class="team-sb-sandbox-note">
                <summary title="Clique para ver detalhes sobre flags de sandbox do Cursor CLI no Windows">
                  <span class="team-sb-warn-dot" aria-hidden="true"></span>
                  <span>Cursor CLI sem sandbox no Windows</span>
                  <span class="team-sb-warn-chev">${icons.chevronRight}</span>
                </summary>
                <div class="team-sb-sandbox-body">${escapeHtml(state.cursorCLI.sandboxWarning)}</div>
              </details>
            ` : ""}
          </div>

          <div class="team-sb-utils">
            <button type="button" class="team-sb-memory-btn" id="btn-team-memory" title="Abrir memória compartilhada (.hivemind/MEMORY.md)">
              <span class="team-sb-memory-left">${icons.memory}<span>Memória do Time</span></span>
              <span class="team-sb-memory-badge">MEMORY.md</span>
            </button>
            <div class="team-sb-flow-row" title="Fluxo de execução da equipe">
              <span class="team-sb-flow-steps">
                <span>Chefe</span>
                <span class="team-sb-flow-sep">→</span>
                <span>1..${maxDevs} Devs</span>
                <span class="team-sb-flow-sep">→</span>
                <span>QA</span>
                <span class="team-sb-flow-sep">→</span>
                <span>Fecho</span>
              </span>
              ${maxTok > 0 ? `<span class="team-sb-limit-pill">${exactTokens(maxTok)} tok</span>` : ""}
            </div>
          </div>
        </section>

        <div class="team-sb-section-head">
          <span>Agentes</span>
          ${activeCount > 0 ? `<span class="team-sb-active-badge">${activeCount} ativo${activeCount > 1 ? "s" : ""}</span>` : `<span class="team-sb-count-badge">${agents.length}</span>`}
        </div>

        <div id="team-agents-list" class="team-agents-list">
          ${renderAgentCardsHtml(agents)}
        </div>
      </div>
    `;
    bindTeamSidebarEvents();
    return;
  }

  if (state.sidebarView === "git") {
    sidebar.innerHTML = renderGitSidebarHtml(proj);
    bindGitSidebarEvents();
    return;
  }

  sidebar.innerHTML = `
    <div class="sidebar-header">
      <span class="eyebrow">Explorador</span>
      <div class="spacer"></div>
      <button class="icon-btn" id="btn-new-file" title="Novo arquivo">${icons.newFile}</button>
      <button class="icon-btn" id="btn-new-folder" title="Nova pasta">${icons.newFolder}</button>
      <button class="icon-btn" id="btn-refresh-tree" title="Atualizar arquivos">${icons.refresh}</button>
    </div>
    <div class="sidebar-filter">
      <input id="explorer-filter" placeholder="Filtrar arquivos…" value="${escapeHtml(state.explorerFilter)}" />
    </div>
    <div class="sidebar-body" id="explorer-tree-body">
      ${renderExplorerTreeHtml()}
    </div>
  `;
  bindExplorerEvents();
}

export function renderGlobalSearchResultsHtml(): string {
  if (state.textSearch.searching) {
    return `<div class="dim" style="padding:8px">${icons.spinner} Buscando…</div>`;
  }
  if (!state.textSearch.query.trim()) {
    return `<div class="dim" style="padding:8px">Digite um termo para buscar em todo o código do projeto.</div>`;
  }
  if (state.textSearch.results.length === 0) {
    return `<div class="dim" style="padding:8px">Nenhum resultado encontrado.</div>`;
  }
  return state.textSearch.results
    .map((m, i) => `
      <div class="search-hit" data-search-hit="${i}">
        <div class="search-hit-file">${escapeHtml(m.relPath)}:${m.line}</div>
        <div class="search-hit-line">${escapeHtml(m.preview)}</div>
      </div>
    `)
    .join("");
}

export function bindGlobalSearchEvents() {
  const input = document.getElementById("global-search-input") as HTMLInputElement | null;
  const runSearch = async () => {
    const q = (input?.value || "").trim();
    state.textSearch.query = input?.value || "";
    if (!q || !state.project) {
      state.textSearch.results = [];
      state.textSearch.searching = false;
      const box = document.getElementById("global-search-results");
      if (box) box.innerHTML = renderGlobalSearchResultsHtml();
      return;
    }
    state.textSearch.searching = true;
    const box = document.getElementById("global-search-results");
    if (box) box.innerHTML = renderGlobalSearchResultsHtml();
    try {
      const res = (await api()?.SearchText(state.project.root, q)) || [];
      state.textSearch.results = res;
    } catch {
      state.textSearch.results = [];
    } finally {
      state.textSearch.searching = false;
      const updatedBox = document.getElementById("global-search-results");
      if (updatedBox) {
        updatedBox.innerHTML = renderGlobalSearchResultsHtml();
        bindGlobalSearchHits();
      }
    }
  };

  input?.addEventListener("input", () => {
    state.textSearch.query = input.value;
    if (globalSearchTimer) window.clearTimeout(globalSearchTimer);
    globalSearchTimer = window.setTimeout(runSearch, 220);
  });
  input?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      if (globalSearchTimer) window.clearTimeout(globalSearchTimer);
      runSearch();
    }
  });
  document.getElementById("btn-run-global-search")?.addEventListener("click", runSearch);
  bindGlobalSearchHits();
}

export function bindGlobalSearchHits() {
  const box = document.getElementById("global-search-results");
  if (!box) return;
  box.querySelectorAll("[data-search-hit]").forEach((el) => {
    el.addEventListener("click", () => {
      const idx = parseInt((el as HTMLElement).dataset.searchHit || "0", 10);
      const hit = state.textSearch.results[idx];
      if (!hit) return;
      const name = hit.relPath.split("/").pop() || hit.relPath;
      openFileInEditor(hit.relPath, name, hit.line);
    });
  });
}

export function bindSessionsSidebarEvents() {
  document.getElementById("btn-refresh-sessions")?.addEventListener("click", () => refreshSessions());
  document.querySelectorAll("[data-resume-session]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const sid = (btn as HTMLElement).dataset.resumeSession!;
      const backend = api();
      const lane = activeLane();
      if (!backend || !state.project || !lane || lane.running) return;
      try {
        const detail = await backend.LoadSession(state.project.root, sid);
        lane.sessionId = detail.id;
        lane.conversationKind = detail.kind === "hive" ? "hive" : "team";
        void syncHiveBinding();
        lane.turnInTokens = 0;
        lane.turnOutTokens = 0;
        lane.turnTotalTokens = 0;
        lane.turnCostMicroUsd = 0;
        lane.costByRole = {};
        lane.sessionInTokens = Number(detail.inputTokens || 0);
        lane.sessionOutTokens = Number(detail.outputTokens || 0);
        lane.sessionTotalTokens = Number(detail.totalTokens || 0);
        if (detail.jiraKey) {
          state.jiraKeyInput = detail.jiraKey;
          const jk = document.getElementById("jira-key-input") as HTMLInputElement | null;
          if (jk) jk.value = detail.jiraKey;
        }
        lane.agents = buildAgentRuntimes(lane.teamName);
        if (detail.agentUsage) {
          for (const [author, st] of Object.entries(detail.agentUsage)) {
            const ag = matchAgentRuntime(lane.agents, author);
            if (ag) {
              ag.inTokens += Number(st.inputTokens || 0);
              ag.outTokens += Number(st.outputTokens || 0);
              ag.tokens += Number(st.totalTokens || 0);
              ag.calls += Number(st.calls || 0);
            }
          }
        }
        lane.messages = [
          {
            id: `sys-res-${Date.now()}`,
            kind: "system",
            label: "Sessão Retomada",
            text: `Sessão \`${detail.id}\` restaurada — **${exactTokens(detail.totalTokens)} tokens** acumulados (Entrada: ${exactTokens(detail.inputTokens)} / Saída: ${exactTokens(detail.outputTokens)}).`,
          },
          ...(detail.messages || []).map((m): ChatMessage => ({
            id: m.id || `msg-${Math.random().toString(36).slice(2, 8)}`,
            kind: (m.kind === "user" || m.kind === "tool" ? m.kind : "chief"),
            label: m.label || "Chefe",
            text: m.text || "",
            tokens: m.tokens ? Number(m.tokens) : undefined,
          })),
        ];
        state.showRightAI = true;
        syncLayoutVisibility();
        rebuildChatMessagesFull();
        renderTokenBar();
        updateStatusBar();
        updateMissionBoardIfOpen();
        renderLeftSidebar();
        setStatus(`Sessão ${detail.id} retomada.`);
      } catch (err: any) {
        toastError("carregar a sessão salva", err);
      }
    });
  });

  document.querySelectorAll("[data-delete-session]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const sid = (btn as HTMLElement).dataset.deleteSession!;
      const backend = api();
      const lane = activeLane();
      if (!backend || !state.project) return;
      try {
        await backend.DeleteSession(state.project.root, sid);
        if (lane && lane.sessionId === sid) {
          lane.sessionId = "";
        }
        await refreshSessions();
        setStatus(`Sessão ${sid} excluída.`);
      } catch (err: any) {
        toastError("excluir a sessão", err);
      }
    });
  });
}

export function renderExplorerTreeHtml(): string {
  const rootNodes = state.dirCache.get("") ?? state.project?.files ?? [];
  if (!state.project) {
    if (state.projectLoading.active) {
      return `
        <div class="explorer-loading-wrap">
          <div class="dim" style="display:flex;align-items:center;gap:6px;padding:6px 10px">${icons.spinner}<span>${escapeHtml(state.projectLoading.label || "Abrindo projeto…")}</span></div>
          ${renderSkeletonRowsHtml(7, state.projectLoading.label || "Abrindo projeto…")}
        </div>
      `;
    }
    return `
      <div class="empty-panel" style="padding:14px 10px">
        <strong>Nenhum projeto aberto</strong>
        <span>Abra a pasta de um repositório ou projeto para começar.</span>
        <button type="button" class="mini primary" id="btn-explorer-open-folder">${icons.folderOpen}<span>Abrir Pasta do Projeto</span></button>
      </div>
    `;
  }
  const gitMap = getGitStatusMap();
  const q = state.explorerFilter.trim();
  if (q && state.explorerFilterResults.length > 0) {
    return state.explorerFilterResults
      .map((rel) => {
        const name = rel.split("/").pop() || rel;
        const dirPart = rel.includes("/") ? rel.slice(0, rel.lastIndexOf("/")) : "";
        const isAct = state.activeEditorPath === rel ? "active" : "";
        const gitSt = gitMap.files.get(rel);
        const gitCls = gitSt === "U" ? "git-add" : gitSt === "M" ? "git-mod" : "";
        const gitBadge = gitSt === "U"
          ? `<span class="tree-git-badge add">U</span>`
          : gitSt === "M"
            ? `<span class="tree-git-badge mod">M</span>`
            : "";
        return `
          <div class="tree-row ${isAct} ${gitCls}" data-file="${escapeHtml(rel)}" data-name="${escapeHtml(name)}" style="padding-left:10px" title="${escapeHtml(rel)}">
            ${fileBadge(name)}
            <span class="tree-name">${escapeHtml(name)}${dirPart ? ` <span class="dim" style="font-size:10.5px">· ${escapeHtml(dirPart)}</span>` : ""}</span>
            ${gitBadge}
          </div>
        `;
      })
      .join("");
  }
  const html = renderTreeNodes(rootNodes, 0, q, gitMap);
  return html || `<div class="dim" style="padding:8px">Nenhum arquivo correspondente</div>`;
}

export function renderAgentCardsHtml(agents: AgentRuntime[]): string {
  const lane = activeLane();
  const laneRunning = Boolean(lane?.running);
  if (!agents || agents.length === 0) {
    return `<div class="team-sb-empty">Nenhum agente configurado nesta equipe. Clique em <strong>Config</strong> para adicionar.</div>`;
  }
  return agents.map((a) => {
    const effStatus = a.status === "running" && !laneRunning ? "done" : a.status;
    const statusLabel = effStatus === "idle" ? "pronto" : effStatus === "running" ? "rodando" : effStatus === "done" ? "ok" : "erro";
    const chip = modelChipHtml(chipForAgent(a));
    const hasFooter = Boolean(a.routeModel || (laneRunning && a.tool) || a.tokens > 0);
    return `
      <div class="agent-card is-${escapeHtml(effStatus)}" data-agent-card="${escapeHtml(a.name)}">
        <div class="agent-card-top">
          <div class="agent-name">${escapeHtml(roleLabelPt(a.role))}</div>
          <span class="status-pill ${escapeHtml(effStatus)}">
            ${effStatus === "running" ? `<span class="status-dot" aria-hidden="true"></span>` : ""}
            <span>${statusLabel}</span>
          </span>
        </div>
        ${chip ? `
          <div class="agent-card-chip-row">
            ${chip}
            ${a.routeTier ? `<span class="agent-route-tier">${escapeHtml(a.routeTier)}</span>` : ""}
          </div>
        ` : ""}
        <div class="agent-card-pickers">
          ${renderModelPickerPairHtml({
            pairKey: `sb-${a.name}`,
            dataAttr: "data-sb-agent-model",
            dataAttrVal: a.name,
            currentModel: a.model,
            disabled: Boolean(lane?.running),
            compact: true,
          })}
        </div>
        ${hasFooter ? `
          <div class="agent-card-foot">
            ${a.routeModel ? `<div class="agent-route-line">Jev: ${escapeHtml(a.routeModel)}${a.routeReasons?.length ? ` · ${escapeHtml(reasonPhrase(a.routeReasons))}` : ""}</div>` : ""}
            ${a.tool ? `<div class="agent-tool-line">${icons.bolt}<span>${escapeHtml(a.tool)}</span></div>` : ""}
            ${a.tokens > 0
              ? `<div class="agent-token-row" title="Entrada: ${exactTokens(a.inTokens)} | Saída: ${exactTokens(a.outTokens)} | Chamadas: ${a.calls}">
                  <span class="agent-token-total">${icons.tokens}<span>${exactTokens(a.tokens)} tok</span></span>
                  <span class="agent-token-split">↑${formatTokens(a.inTokens)} ↓${formatTokens(a.outTokens)} · ${a.calls}x</span>
                </div>`
              : ""
            }
          </div>
        ` : ""}
      </div>
    `;
  }).join("");
}

export function bindAgentModelSelects(container: ParentNode | null, attrName: string) {
  if (!container) return;
  bindModelPickerPairs(container, (sel, newVal) => {
    const agName = sel.getAttribute(attrName) || "";
    if (agName && newVal) handleInlineModelChange(agName, newVal);
  });
}

export function updateTeamStatusPills() {
  const list = document.getElementById("team-agents-list");
  const lane = activeLane();
  if (!list || !lane) return;
  list.innerHTML = renderAgentCardsHtml(lane.agents);
  bindAgentModelSelects(list, "data-sb-agent-model");
}

export function bindExplorerEvents() {
  document.getElementById("btn-explorer-open-folder")?.addEventListener("click", () => {
    void openFolderDialog(true);
  });
  const filterIn = document.getElementById("explorer-filter") as HTMLInputElement | null;
  filterIn?.addEventListener("input", () => {
    state.explorerFilter = filterIn.value;
    const q = state.explorerFilter.trim();
    if (explorerFilterTimer) window.clearTimeout(explorerFilterTimer);
    if (!q) {
      state.explorerFilterResults = [];
      const body = document.getElementById("explorer-tree-body");
      if (body) {
        body.innerHTML = renderExplorerTreeHtml();
        bindTreeRowClicks();
      }
      return;
    }
    explorerFilterTimer = window.setTimeout(async () => {
      if (state.project) {
        state.explorerFilterResults = (await api()?.SearchFiles(state.project.root, q)) || [];
      }
      const body = document.getElementById("explorer-tree-body");
      if (body) {
        body.innerHTML = renderExplorerTreeHtml();
        bindTreeRowClicks();
      }
    }, 60);
  });

  document.getElementById("btn-new-file")?.addEventListener("click", () => {
    if (!state.project) return;
    state.pathModal = { mode: "newFile", targetRel: "", value: "" };
    renderModalLayer();
  });
  document.getElementById("btn-new-folder")?.addEventListener("click", () => {
    if (!state.project) return;
    state.pathModal = { mode: "newDir", targetRel: "", value: "" };
    renderModalLayer();
  });
  document.getElementById("btn-refresh-tree")?.addEventListener("click", refreshExplorerTree);

  bindTreeRowClicks();
}

export function bindTreeRowClicks() {
  const body = document.getElementById("explorer-tree-body");
  if (!body) return;

  body.querySelectorAll("[data-tree-newfile]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const folder = (btn as HTMLElement).dataset.treeNewfile!;
      state.pathModal = { mode: "newFile", targetRel: folder, value: `${folder}/` };
      renderModalLayer();
    });
  });

  body.querySelectorAll("[data-tree-rename]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const rel = (btn as HTMLElement).dataset.treeRename!;
      state.pathModal = { mode: "rename", targetRel: rel, value: rel };
      renderModalLayer();
    });
  });

  body.querySelectorAll("[data-tree-delete]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const rel = (btn as HTMLElement).dataset.treeDelete!;
      state.pathModal = { mode: "delete", targetRel: rel, value: rel };
      renderModalLayer();
    });
  });

  body.querySelectorAll("[data-dir]").forEach((el) => {
    el.addEventListener("click", async () => {
      const rel = (el as HTMLElement).dataset.dir!;
      if (state.expandedDirs.has(rel)) {
        state.expandedDirs.delete(rel);
      } else {
        state.expandedDirs.add(rel);
        if (!state.dirCache.has(rel) && state.project) {
          const backend = api();
          if (backend) {
            const children = await backend.ListDirectory(state.project.root, rel);
            state.dirCache.set(rel, children || []);
          }
        }
      }
      body.innerHTML = renderExplorerTreeHtml();
      bindTreeRowClicks();
    });
  });

  body.querySelectorAll("[data-file]").forEach((el) => {
    el.addEventListener("click", () => {
      const ds = (el as HTMLElement).dataset;
      openFileInEditor(ds.file!, ds.name!);
    });
  });
}

export function bindTeamSidebarEvents() {
  const lane = activeLane();
  document.getElementById("btn-open-mission-sb")?.addEventListener("click", () => openMissionControlTab());
  document.getElementById("btn-team-memory")?.addEventListener("click", () => openTeamMemoryFile());
  document.getElementById("btn-config-team")?.addEventListener("click", () => {
    void openTeamConfigTab();
  });
  document.getElementById("btn-new-team-sb")?.addEventListener("click", () => {
    void openTeamConfigTab();
  });
  document.getElementById("btn-delete-team-sb")?.addEventListener("click", () => {
    const sel = document.getElementById("team-select") as HTMLSelectElement | null;
    const name = sel?.value || activeLane()?.teamName || state.project?.teamConfig?.active || "";
    if (name) confirmDeleteSavedTeam(name);
  });
  const teamSel = document.getElementById("team-select") as HTMLSelectElement | null;
  teamSel?.addEventListener("change", async () => {
    if (!lane || !state.project) return;
    lane.teamName = teamSel.value;
    lane.agents = buildAgentRuntimes(lane.teamName);
    await api()?.SetActiveTeam(state.project.root, lane.teamName);
    updateTeamStatusPills();
    updateMissionBoardIfOpen();
    renderLaneBarOnly();
    updateStatusBar();
  });
  const permSel = document.getElementById("sb-permission-select") as HTMLSelectElement | null;
  permSel?.addEventListener("change", () => {
    setProjectPermissionMode(permSel.value as PermissionMode);
  });
  bindAgentModelSelects(document.getElementById("team-agents-list"), "data-sb-agent-model");
}

export async function setProjectPermissionMode(mode: PermissionMode) {
  const backend = api();
  if (!backend || !state.project) return;
  try {
    if (backend.UpdatePermissionMode) {
      const cfg = await backend.UpdatePermissionMode(state.project.root, mode);
      state.project.teamConfig = cfg;
    } else if (state.project.teamConfig?.settings) {
      state.project.teamConfig.settings.permission_mode = mode;
    }
    renderComposerModeBar();
    updateStatusBar();
    updateMissionBoardIfOpen();
    if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
    setStatus(`Modo de permissão atualizado para: ${permissionModeLabel(mode)}.`);
  } catch (err: any) {
    toastError("atualizar o nível de permissão", err);
  }
}
