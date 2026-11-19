import { icons } from "./icons";
import type { PermissionMode, SkillTargetRole } from "./types";
import { escapeHtml, exactTokens, roleLabelPt, runModeLabel } from "./utils";
import { refreshSessions } from "./boot";
import { renderTokenBar } from "./chat";
import { openFileInEditor, renderEditorBody, renderEditorTabs } from "./editor";
import { bindAgentModelSelects, refreshExplorerTree, renderLeftSidebar } from "./explorer";
import { activeLane, buildAgentRuntimes, confirmDeleteSavedTeam, renderLaneBarOnly } from "./lanes";
import { openJiraModal, openOpenCodeModal, refreshJiraStatusAndProjects, renderModalLayer } from "./modals";
import { bindModelPickerPairs, refreshSkills, renderModelPickerPairHtml, skillTargetLabel } from "./models";
import { openSidebarView, updateStatusBar } from "./shell";
import { MISSION_TAB_KEY, TEAM_CONFIG_TAB_KEY, api, setStatus, state, toast, toastError } from "./state";
import type { TeamConfigDraft } from "./state";

export function openMissionControlTab() {
  const existing = state.editorTabs.find((t) => t.relPath === MISSION_TAB_KEY);
  if (!existing) {
    state.editorTabs.unshift({
      kind: "mission",
      relPath: MISSION_TAB_KEY,
      name: "Quadro Multiagente",
      content: "",
      dirty: false,
    });
  }
  state.activeEditorPath = MISSION_TAB_KEY;
  renderEditorTabs();
  renderEditorBody();
}

export function buildDraftFromProject(): TeamConfigDraft {
  const settings = state.project?.teamConfig?.settings;
  const activeTeamName = state.project?.teamConfig?.active || "principal";
  const curTeam =
    (state.project?.teamConfig?.teams || []).find((t) => t.name === activeTeamName) ||
    state.project?.teamConfig?.teams?.[0];
  const existingChief = curTeam?.agents?.find(
    (a) => a.name === "chefe" || a.role === "architect" || a.role === "project_manager"
  );
  const existingDev = curTeam?.agents?.find((a) => a.role === "developer");
  const existingQA = curTeam?.agents?.find((a) => a.role === "qa");
  const curChiefRole = existingChief?.role === "project_manager" ? "project_manager" : "architect";
  const defaultModel = existingChief?.model || existingDev?.model || state.models[0]?.slug || "agy";
  return {
    workspace: state.project?.root || "",
    teamName: activeTeamName,
    chiefRole: curChiefRole,
    permissionMode: (settings?.permission_mode || "governed") as PermissionMode,
    defaultModel,
    maxDynamicDevs: settings?.max_dynamic_devs && settings.max_dynamic_devs > 0 ? settings.max_dynamic_devs : 5,
    chiefModel: existingChief?.model || "",
    devModel: existingDev?.model || "",
    qaModel: existingQA?.model || "",
    baseBranch: settings?.base_branch || "main",
    homologBranch: settings?.homolog_branch || "homolog",
    maxTokens: settings?.max_tokens || 0,
    worktrees: settings?.worktrees !== false,
    autoCommit: settings ? settings.auto_commit === true : false,
    autoPR: settings ? settings.auto_pr === true : false,
    jiraEnabled: settings?.jira_enabled === true,
    jiraProject: settings?.jira_project || "",
    skillAssignments: (settings?.skill_assignments || []).map((a) => ({
      name: a.name,
      source: a.source || "",
      roles: [...(a.roles || [])] as SkillTargetRole[],
    })),
    skillFilter: "",
  };
}

export function ensureTeamConfigDraft(): TeamConfigDraft {
  const curRoot = state.project?.root || "";
  if (!state.teamConfigDraft || state.teamConfigDraft.workspace !== curRoot) {
    state.teamConfigDraft = buildDraftFromProject();
  }
  return state.teamConfigDraft;
}

export async function openTeamConfigTab(focusSection?: "skills") {
  state.teamConfigDraft = buildDraftFromProject();
  const existing = state.editorTabs.find((t) => t.relPath === TEAM_CONFIG_TAB_KEY);
  if (!existing) {
    state.editorTabs.unshift({
      kind: "team-config",
      relPath: TEAM_CONFIG_TAB_KEY,
      name: "Configurar Equipe",
      content: "",
      dirty: false,
    });
  }
  state.activeEditorPath = TEAM_CONFIG_TAB_KEY;
  renderEditorTabs();
  renderEditorBody();
  void refreshJiraStatusAndProjects();
  if (state.project) {
    await refreshSkills();
  }
  if (focusSection === "skills") {
    requestAnimationFrame(() => {
      document.getElementById("tc-skills-section")?.scrollIntoView({ behavior: "smooth", block: "start" });
    });
  }
}

export async function openTeamMemoryFile() {
  const backend = api();
  if (!backend || !state.project) return;
  try {
    const rel = await backend.EnsureTeamMemory(state.project.root);
    await refreshExplorerTree();
    await openFileInEditor(rel, "MEMORY.md");
    setStatus("Memória compartilhada (.hivemind/MEMORY.md) aberta no editor.");
  } catch (err: any) {
    toastError("abrir a memória da equipe (.hivemind/MEMORY.md)", err);
  }
}

export function renderMissionBoardHtml(): string {
  const lane = activeLane();
  const agents = lane?.agents || [];
  const sessTotal = Math.max(1, lane?.sessionTotalTokens || 0);
  const projTotal = Math.max(state.sessionsSummary.projectTotalTokens, lane?.sessionTotalTokens || 0);

  return `
    <div class="mission-board" id="mission-board-root">
      <div class="mission-top-card">
        <div>
          <div style="display:flex;align-items:center;gap:8px;font-size:15px;font-weight:700;color:var(--text)">
            ${icons.swarm}
            <span>Quadro de Controle Multiagente — ${escapeHtml(lane?.teamName || "principal")}</span>
          </div>
          <div class="dim" style="margin-top:4px" id="mc-header-summary">
            Modo ativo: <strong>${escapeHtml(runModeLabel(state.runMode))}</strong> · Sessão: <strong>${exactTokens(lane?.sessionTotalTokens)} tok</strong> · Projeto: <strong>${exactTokens(projTotal)} tok</strong>
          </div>
        </div>
        <div style="display:flex;gap:7px;flex-wrap:wrap">
          <button class="mini" id="mc-open-memory">${icons.memory}<span>Memória (.hivemind/MEMORY.md)</span></button>
          <button class="mini" id="mc-open-sessions">${icons.sessions}<span>Histórico de Sessões</span></button>
          <button class="mini primary" id="mc-open-wizard">${icons.settings}<span>Configurar Equipe</span></button>
        </div>
      </div>

      <div class="mission-grid">
        ${agents.map((a) => {
          const pct = lane?.sessionTotalTokens ? Math.min(100, Math.round((a.tokens / sessTotal) * 100)) : 0;
          return `
            <div class="mc-agent-card ${a.status}" data-mc-card="${escapeHtml(a.name)}">
              <div style="display:flex;align-items:center;justify-content:space-between;gap:8px">
                <div>
                  <div style="font-size:13px;font-weight:700;color:var(--text)">${escapeHtml(a.name)}</div>
                  <div class="dim">${escapeHtml(roleLabelPt(a.role))}</div>
                </div>
                <span class="status-pill ${a.status}" data-mc-pill="${escapeHtml(a.name)}">
                  ${a.status === "running" ? `${icons.spinner} rodando` : a.status === "done" ? "concluído" : a.status === "error" ? "erro" : "pronto"}
                </span>
              </div>

              <div>
                <div class="dim" style="font-size:10.5px;margin-bottom:4px">Provedor e Modelo LLM (troca imediata):</div>
                ${renderModelPickerPairHtml({
                  pairKey: `mc-${a.name}`,
                  dataAttr: "data-mc-agent-model",
                  dataAttrVal: a.name,
                  currentModel: a.model,
                  disabled: Boolean(lane?.running),
                  compact: true,
                })}
              </div>

              <div style="display:flex;align-items:center;justify-content:space-between;font-size:11px;font-family:var(--mono)">
                <span class="dim" data-mc-activity="${escapeHtml(a.name)}">${a.tool ? `Ativo: ${escapeHtml(a.tool)}` : `${a.calls} chamada(s)`}</span>
                <span style="color:var(--accent)" data-mc-tok="${escapeHtml(a.name)}">${exactTokens(a.tokens)} tok (${pct}%)</span>
              </div>
              <div class="mc-tok-track">
                <div class="mc-tok-fill" data-mc-fill="${escapeHtml(a.name)}" style="width:${pct}%"></div>
              </div>
              <div class="dim" style="font-size:10.5px;font-family:var(--mono)" data-mc-inout="${escapeHtml(a.name)}">
                Entrada: ${exactTokens(a.inTokens)} · Saída: ${exactTokens(a.outTokens)}
              </div>
            </div>
          `;
        }).join("")}
      </div>
    </div>
  `;
}

export function bindMissionBoardEvents() {
  const root = document.getElementById("mission-board-root");
  if (!root) return;
  document.getElementById("mc-open-memory")?.addEventListener("click", () => openTeamMemoryFile());
  document.getElementById("mc-open-sessions")?.addEventListener("click", () => {
    openSidebarView("sessions");
    refreshSessions();
  });
  document.getElementById("mc-open-wizard")?.addEventListener("click", () => {
    void openTeamConfigTab();
  });
  bindAgentModelSelects(root, "data-mc-agent-model");
}

export function updateMissionBoardIfOpen() {
  if (state.activeEditorPath !== MISSION_TAB_KEY) return;
  const body = document.getElementById("editor-body");
  if (!body) return;
  const root = document.getElementById("mission-board-root");
  const lane = activeLane();
  const agents = lane?.agents || [];
  const cards = root?.querySelectorAll("[data-mc-card]");
  if (!root || !cards || cards.length !== agents.length) {
    body.innerHTML = renderMissionBoardHtml();
    bindMissionBoardEvents();
    return;
  }
  const sessTotal = Math.max(1, lane?.sessionTotalTokens || 0);
  const projTotal = Math.max(state.sessionsSummary.projectTotalTokens, lane?.sessionTotalTokens || 0);
  const summaryEl = document.getElementById("mc-header-summary");
  if (summaryEl) {
    summaryEl.innerHTML = `Modo ativo: <strong>${escapeHtml(runModeLabel(state.runMode))}</strong> · Sessão: <strong>${exactTokens(lane?.sessionTotalTokens)} tok</strong> · Projeto: <strong>${exactTokens(projTotal)} tok</strong>`;
  }
  for (const a of agents) {
    const pct = lane?.sessionTotalTokens ? Math.min(100, Math.round((a.tokens / sessTotal) * 100)) : 0;
    const esc = CSS.escape(a.name);
    const card = root.querySelector<HTMLElement>(`[data-mc-card="${esc}"]`);
    if (card) card.className = `mc-agent-card ${a.status}`;
    const pill = root.querySelector<HTMLElement>(`[data-mc-pill="${esc}"]`);
    if (pill) {
      pill.className = `status-pill ${a.status}`;
      pill.innerHTML = a.status === "running" ? `${icons.spinner} rodando` : a.status === "done" ? "concluído" : a.status === "error" ? "erro" : "pronto";
    }
    const sel = root.querySelector<HTMLSelectElement>(`[data-mc-agent-model="${esc}"]`);
    if (sel) sel.disabled = !!lane?.running;
    const act = root.querySelector<HTMLElement>(`[data-mc-activity="${esc}"]`);
    if (act) act.textContent = a.tool ? `Ativo: ${a.tool}` : `${a.calls} chamada(s)`;
    const tok = root.querySelector<HTMLElement>(`[data-mc-tok="${esc}"]`);
    if (tok) tok.textContent = `${exactTokens(a.tokens)} tok (${pct}%)`;
    const fill = root.querySelector<HTMLElement>(`[data-mc-fill="${esc}"]`);
    if (fill) fill.style.width = `${pct}%`;
    const inout = root.querySelector<HTMLElement>(`[data-mc-inout="${esc}"]`);
    if (inout) inout.textContent = `Entrada: ${exactTokens(a.inTokens)} · Saída: ${exactTokens(a.outTokens)}`;
  }
}

export function draftRolesForSkill(draft: TeamConfigDraft, name: string, source: "" | "native" | "project"): SkillTargetRole[] {
  const norm = name.toLowerCase();
  const hit =
    draft.skillAssignments.find((a) => a.name.toLowerCase() === norm && (!a.source || a.source === source)) ||
    draft.skillAssignments.find((a) => a.name.toLowerCase() === norm);
  return hit?.roles || [];
}

export function setDraftSkillRoles(draft: TeamConfigDraft, name: string, source: "" | "native" | "project", roles: SkillTargetRole[]) {
  const norm = name.toLowerCase();
  draft.skillAssignments = draft.skillAssignments.filter(
    (a) => !(a.name.toLowerCase() === norm && (!a.source || !source || a.source === source))
  );
  if (roles.length > 0) {
    draft.skillAssignments.push({ name, source, roles });
  }
}

export function toggleDraftSkillRole(draft: TeamConfigDraft, name: string, source: "" | "native" | "project", clicked: SkillTargetRole) {
  const cur = draftRolesForSkill(draft, name, source);
  let next: SkillTargetRole[] = [];
  if (clicked === "all") {
    next = cur.includes("all") ? [] : ["all"];
  } else if (cur.includes("all")) {
    next = [clicked];
  } else if (cur.includes(clicked)) {
    next = cur.filter((r) => r !== clicked);
  } else {
    next = [...cur, clicked];
    if (next.includes("chief") && next.includes("dev") && next.includes("qa")) {
      next = ["all"];
    }
  }
  setDraftSkillRoles(draft, name, source, next);
}

export function summarizeSkillScope(roles: SkillTargetRole[]): string {
  if (roles.length === 0) return "Sob demanda (Chefe decide)";
  if (roles.includes("all")) return "Todos os agentes";
  return roles.map((r) => skillTargetLabel(r)).join(" + ");
}

export function renderTeamConfigSkillRowsHtml(draft: TeamConfigDraft): string {
  const q = draft.skillFilter.trim().toLowerCase();
  const list = state.skills.filter(
    (s) => !q || s.name.toLowerCase().includes(q) || (s.description || "").toLowerCase().includes(q)
  );
  if (state.skills.length === 0) {
    return `
      <div class="tc-empty-skills">
        Nenhuma skill cadastrada ainda. Clique em <strong>Nova Skill</strong> para criar diretrizes reutilizáveis para o Chefe, Devs ou QA.
      </div>
    `;
  }
  if (list.length === 0) {
    return `<div class="dim" style="padding:10px 0">Nenhuma skill corresponde a "${escapeHtml(draft.skillFilter)}".</div>`;
  }
  const targets: SkillTargetRole[] = ["all", "chief", "dev", "qa"];
  return list
    .map((s) => {
      const roles = draftRolesForSkill(draft, s.name, s.source);
      const isNative = s.source === "native";
      return `
        <div class="tc-skill-row">
          <div class="tc-skill-info">
            <div class="tc-skill-title-row">
              <span class="skill-name">${escapeHtml(s.name)}</span>
              <span class="skill-tag ${isNative ? "native" : "project"}">${isNative ? "nativa" : "projeto"}</span>
              <span class="tc-skill-scope">${escapeHtml(summarizeSkillScope(roles))}</span>
            </div>
            <div class="tc-skill-desc">${escapeHtml(s.description || "Sem descrição")}</div>
          </div>
          <div class="tc-role-pills" role="group" aria-label="Papéis para ${escapeHtml(s.name)}">
            ${targets
              .map((t) => {
                const active = roles.includes(t);
                return `<button type="button" class="tc-role-pill ${t} ${active ? "active" : ""}" data-tc-skill-name="${escapeHtml(s.name)}" data-tc-skill-src="${escapeHtml(s.source)}" data-tc-role="${t}">${escapeHtml(skillTargetLabel(t))}</button>`;
              })
              .join("")}
          </div>
        </div>
      `;
    })
    .join("");
}

export function renderTeamConfigPageHtml(): string {
  const draft = ensureTeamConfigDraft();
  const assignedCount = draft.skillAssignments.length;

  return `
    <div class="team-config-page" id="team-config-root">
      <div class="tc-header">
        <div>
          <div class="tc-title-row">
            ${icons.settings}
            <h2>Configuração da Equipe & Skills</h2>
            <span class="tok-pill">.hivemind/team.yaml</span>
          </div>
          <div class="dim">Configure modelos por especialista, autonomia, branches, integrações e quais skills cada papel recebe automaticamente.</div>
        </div>
        <div class="tc-header-actions">
          <button type="button" class="mini" id="tc-jira-connect-top" title="Conectar ao Jira Cloud (ou importar de mcp_config.json)">${icons.bolt}<span>${state.jiraStatus?.connected ? "Jira: Conectado" : "Conectar Jira"}</span></button>
          <button type="button" class="mini" id="tc-opencode-btn" title="Configurar token portátil do OpenCode Zen">${icons.bolt}<span>Token OpenCode</span></button>
          ${state.project?.hasTeam && (state.project.teamConfig?.teams?.length || 0) > 0 ? `<button type="button" class="mini danger" id="tc-delete-team-btn" title="Excluir equipe e parar todos os serviços">${icons.trash}<span>Excluir Equipe</span></button>` : ""}
          <button type="button" class="mini" id="tc-close-btn">Fechar</button>
          <button type="button" class="primary" id="tc-save-btn">${icons.save}<span>Salvar Configurações</span></button>
        </div>
      </div>

      <div id="tc-error" class="tc-error" style="display:none"></div>

      <div class="tc-grid">
        <section class="tc-card">
          <div class="tc-card-head">
            ${icons.team}
            <div>
              <h3>Equipe & Autonomia</h3>
              <div class="dim">Time base de 3 especialistas (Chefe, Dev e QA) com devs paralelos sob demanda.</div>
            </div>
          </div>
          <div class="tc-fields">
            <label>Nome da equipe</label>
            <input id="tc-name" value="${escapeHtml(draft.teamName)}" placeholder="principal" />

            <label>Chefe da equipe</label>
            <select id="tc-chief">
              <option value="architect" ${draft.chiefRole === "architect" ? "selected" : ""}>Arquiteto</option>
              <option value="project_manager" ${draft.chiefRole === "project_manager" ? "selected" : ""}>Gerente de Projeto</option>
            </select>

            <label>Permissão / Autonomia</label>
            <select id="tc-permission">
              <option value="governed" ${draft.permissionMode === "governed" ? "selected" : ""}>Governado (Worktree + PR + Pede permissão p/ main)</option>
              <option value="auto_edit" ${draft.permissionMode === "auto_edit" ? "selected" : ""}>Auto-Edição (Edições e comandos locais livres)</option>
              <option value="full_access" ${draft.permissionMode === "full_access" ? "selected" : ""}>Full Acesso & Permissão Total</option>
            </select>

            <label>Máx. Devs dinâmicos</label>
            <input id="tc-max-devs" type="number" min="1" max="5" value="${draft.maxDynamicDevs}" />

            <label>Limite tokens/turno</label>
            <input id="tc-maxtokens" type="number" min="0" step="1000" value="${draft.maxTokens}" placeholder="0 = sem limite" />
          </div>
        </section>

        <section class="tc-card">
          <div class="tc-card-head">
            ${icons.sparkle}
            <div>
              <h3>Modelos LLM por Especialista</h3>
              <div class="dim">Escolha primeiro o provedor (Antigravity, OpenCode, Gemini, Claude ou OpenAI/Grok) e depois o modelo.</div>
            </div>
          </div>
          <div class="tc-fields">
            <label>Modelo padrão</label>
            ${renderModelPickerPairHtml({ pairKey: "tc-default", selectId: "tc-model", currentModel: draft.defaultModel })}

            <label>Modelo do Chefe</label>
            ${renderModelPickerPairHtml({ pairKey: "tc-chief", selectId: "tc-model-chief", currentModel: draft.chiefModel, includeDefaultBlank: true })}

            <label>Modelo do Dev</label>
            ${renderModelPickerPairHtml({ pairKey: "tc-dev", selectId: "tc-model-dev", currentModel: draft.devModel, includeDefaultBlank: true })}

            <label>Modelo do QA</label>
            ${renderModelPickerPairHtml({ pairKey: "tc-qa", selectId: "tc-model-qa", currentModel: draft.qaModel, includeDefaultBlank: true })}
          </div>
        </section>

        <section class="tc-card tc-card-wide">
          <div class="tc-card-head">
            ${icons.gitBranch}
            <div>
              <h3>Git, Worktrees & Integrações</h3>
              <div class="dim">Branches protegidas, comportamento de commit/PR e integração com Jira Cloud.</div>
            </div>
          </div>
          <div class="tc-git-grid">
            <div class="tc-fields">
              <label>Branch Base</label>
              <input id="tc-base-branch" value="${escapeHtml(draft.baseBranch)}" placeholder="main" />

              <label>Branch Homolog</label>
              <input id="tc-homolog-branch" value="${escapeHtml(draft.homologBranch)}" placeholder="homolog" />

              <label>Jira Cloud</label>
              <div class="tc-jira-box">
                <div class="tc-jira-head">
                  <label class="tc-jira-enable">
                    <input id="tc-jira" type="checkbox" ${draft.jiraEnabled ? "checked" : ""} />
                    <span>Ativar neste projeto</span>
                  </label>
                  <span class="tc-jira-pill ${state.jiraStatus?.connected ? "ok" : "off"}" title="${escapeHtml(state.jiraStatus?.detail || "Jira desconectado")}">
                    ${state.jiraStatus?.connected
                      ? `● ${escapeHtml(state.jiraStatus.user || state.jiraStatus.email || "Conectado")}`
                      : "○ Desconectado"
                    }
                  </span>
                  <div class="spacer"></div>
                  <button type="button" class="mini" id="tc-jira-connect-btn">${state.jiraStatus?.connected ? "Configurar" : "Conectar Jira…"}</button>
                </div>
                <div class="tc-jira-picker">
                  ${state.jiraProjects.length > 0
                    ? `
                      <select id="tc-jira-select" style="flex:1">
                        <option value="">(Selecionar projeto Jira…)</option>
                        ${state.jiraProjects.map((p) => `<option value="${escapeHtml(p.key)}" ${p.key.toUpperCase() === (draft.jiraProject || "").toUpperCase() ? "selected" : ""}>${escapeHtml(p.key)} — ${escapeHtml(p.name)}</option>`).join("")}
                        ${draft.jiraProject && !state.jiraProjects.some((p) => p.key.toUpperCase() === draft.jiraProject.toUpperCase())
                          ? `<option value="${escapeHtml(draft.jiraProject)}" selected>${escapeHtml(draft.jiraProject)} (chave manual)</option>`
                          : ""
                        }
                      </select>
                      <button type="button" class="icon-btn" id="tc-jira-refresh" title="Recarregar projetos do Jira">${state.jiraProjectsLoading ? icons.spinner : icons.refresh}</button>
                    `
                    : `
                      <input id="tc-jira-key" value="${escapeHtml(draft.jiraProject)}" placeholder="Chave do projeto Jira (ex: IPG) — ou conecte para listar" style="flex:1" />
                      ${state.jiraStatus?.connected ? `<button type="button" class="icon-btn" id="tc-jira-refresh" title="Carregar lista de projetos do Jira">${state.jiraProjectsLoading ? icons.spinner : icons.refresh}</button>` : ""}
                    `
                  }
                </div>
              </div>
            </div>
            <div class="tc-toggles">
              <label class="tc-toggle-row">
                <input id="tc-worktrees" type="checkbox" ${draft.worktrees ? "checked" : ""} />
                <div>
                  <div class="tc-toggle-title">Worktree separada</div>
                  <div class="dim">Recomendado: cada execução roda em cópia isolada do repositório.</div>
                </div>
              </label>
              <label class="tc-toggle-row">
                <input id="tc-autocommit" type="checkbox" ${draft.autoCommit ? "checked" : ""} />
                <div>
                  <div class="tc-toggle-title">Commit automático</div>
                  <div class="dim">Quando desligado, o Chefe pede confirmação antes de commitar.</div>
                </div>
              </label>
              <label class="tc-toggle-row">
                <input id="tc-autopr" type="checkbox" ${draft.autoPR ? "checked" : ""} />
                <div>
                  <div class="tc-toggle-title">Criar PR automático</div>
                  <div class="dim">Quando desligado, o Pull Request só é aberto quando você pedir.</div>
                </div>
              </label>
            </div>
          </div>
        </section>

        <section class="tc-card tc-card-wide" id="tc-skills-section">
          <div class="tc-skills-header">
            <div class="tc-card-head" style="margin:0">
              ${icons.skills}
              <div>
                <h3>Skills por Agente (${assignedCount} atribuída${assignedCount === 1 ? "" : "s"})</h3>
                <div class="dim">Fixe skills para <strong>Todos</strong>, ou apenas para <strong>Chefe</strong>, <strong>Dev</strong> ou <strong>QA</strong>. Sem seleção = o Chefe escolhe sob demanda.</div>
              </div>
            </div>
            <div class="tc-skills-toolbar">
              <input id="tc-skill-filter" value="${escapeHtml(draft.skillFilter)}" placeholder="Filtrar skills…" />
              <button type="button" class="mini" id="tc-skill-refresh" title="Atualizar lista de skills">${icons.refresh}<span>Atualizar</span></button>
              <button type="button" class="mini primary" id="tc-skill-new">${icons.plus}<span>Nova Skill</span></button>
            </div>
          </div>
          <div class="tc-skills-list" id="tc-skills-list">
            ${renderTeamConfigSkillRowsHtml(draft)}
          </div>
        </section>
      </div>

      <div class="tc-footer">
        <span class="dim">Alterações em papéis de skills são salvas automaticamente se a equipe já existir, ou junto ao clicar em Salvar Configurações.</span>
        <div class="spacer"></div>
        <button type="button" class="primary" id="tc-save-btn-bottom">${icons.save}<span>Salvar Configurações</span></button>
      </div>
    </div>
  `;
}

export function syncTeamConfigDraftFromDom(draft: TeamConfigDraft) {
  const val = (id: string) => (document.getElementById(id) as HTMLInputElement | HTMLSelectElement | null)?.value;
  const chk = (id: string) => (document.getElementById(id) as HTMLInputElement | null)?.checked;
  if (val("tc-name") !== undefined) draft.teamName = val("tc-name") || "principal";
  if (val("tc-chief") !== undefined) draft.chiefRole = val("tc-chief") || "architect";
  if (val("tc-permission") !== undefined) draft.permissionMode = (val("tc-permission") || "governed") as PermissionMode;
  if (val("tc-model") !== undefined) draft.defaultModel = val("tc-model") || "agy";
  if (val("tc-max-devs") !== undefined) draft.maxDynamicDevs = Math.max(1, Math.min(5, parseInt(val("tc-max-devs")!, 10) || 5));
  if (val("tc-maxtokens") !== undefined) draft.maxTokens = Math.max(0, parseInt(val("tc-maxtokens")!, 10) || 0);
  if (val("tc-model-chief") !== undefined) draft.chiefModel = val("tc-model-chief") || "";
  if (val("tc-model-dev") !== undefined) draft.devModel = val("tc-model-dev") || "";
  if (val("tc-model-qa") !== undefined) draft.qaModel = val("tc-model-qa") || "";
  if (val("tc-base-branch") !== undefined) draft.baseBranch = val("tc-base-branch") || "main";
  if (val("tc-homolog-branch") !== undefined) draft.homologBranch = val("tc-homolog-branch") || "homolog";
  if (chk("tc-worktrees") !== undefined) draft.worktrees = Boolean(chk("tc-worktrees"));
  if (chk("tc-autocommit") !== undefined) draft.autoCommit = Boolean(chk("tc-autocommit"));
  if (chk("tc-autopr") !== undefined) draft.autoPR = Boolean(chk("tc-autopr"));
  if (chk("tc-jira") !== undefined) draft.jiraEnabled = Boolean(chk("tc-jira"));
  if (val("tc-jira-select") !== undefined) draft.jiraProject = (val("tc-jira-select") || "").trim().toUpperCase();
  else if (val("tc-jira-key") !== undefined) draft.jiraProject = (val("tc-jira-key") || "").trim().toUpperCase();
}

export function bindTeamConfigSkillPills(draft: TeamConfigDraft) {
  const listEl = document.getElementById("tc-skills-list");
  if (!listEl) return;
  listEl.querySelectorAll("[data-tc-skill-name]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const el = btn as HTMLElement;
      const name = el.dataset.tcSkillName!;
      const rawSrc = el.dataset.tcSkillSrc || "";
      const source: "" | "native" | "project" =
        rawSrc === "native" || rawSrc === "project" ? rawSrc : "";
      const role = el.dataset.tcRole as SkillTargetRole;
      toggleDraftSkillRole(draft, name, source, role);
      listEl.innerHTML = renderTeamConfigSkillRowsHtml(draft);
      bindTeamConfigSkillPills(draft);

      const headCount = document.querySelector("#tc-skills-section h3");
      if (headCount) {
        const n = draft.skillAssignments.length;
        headCount.textContent = `Skills por Agente (${n} atribuída${n === 1 ? "" : "s"})`;
      }

      const backend = api();
      if (backend && state.project?.hasTeam && backend.SaveSkillAssignments) {
        try {
          const cfg = await backend.SaveSkillAssignments(state.project.root, draft.skillAssignments);
          state.project.teamConfig = cfg;
          if (state.sidebarView === "skills" && state.showLeftSidebar) renderLeftSidebar();
          setStatus(`Atribuições de skill "${name}" atualizadas.`);
        } catch (err: any) {
          toastError(`salvar atribuição da skill "${name}"`, err);
        }
      }
    });
  });
}

export function bindTeamConfigEvents() {
  const draft = ensureTeamConfigDraft();
  const root = document.getElementById("team-config-root");
  if (!root) return;

  bindModelPickerPairs(root, () => syncTeamConfigDraftFromDom(draft));

  root.querySelectorAll("input, select").forEach((el) => {
    if ((el as HTMLElement).id === "tc-skill-filter") return;
    el.addEventListener("change", () => syncTeamConfigDraftFromDom(draft));
  });

  const filterIn = document.getElementById("tc-skill-filter") as HTMLInputElement | null;
  filterIn?.addEventListener("input", () => {
    draft.skillFilter = filterIn.value;
    const listEl = document.getElementById("tc-skills-list");
    if (listEl) {
      listEl.innerHTML = renderTeamConfigSkillRowsHtml(draft);
      bindTeamConfigSkillPills(draft);
    }
  });

  bindTeamConfigSkillPills(draft);

  document.getElementById("tc-opencode-btn")?.addEventListener("click", () => openOpenCodeModal());
  document.getElementById("tc-jira-connect-top")?.addEventListener("click", () => void openJiraModal());
  document.getElementById("tc-jira-connect-btn")?.addEventListener("click", () => void openJiraModal());
  document.getElementById("tc-jira-refresh")?.addEventListener("click", () => void refreshJiraStatusAndProjects());
  document.getElementById("tc-delete-team-btn")?.addEventListener("click", () => {
    const target = draft.teamName || state.project?.teamConfig?.active || "";
    if (target) confirmDeleteSavedTeam(target);
  });
  document.getElementById("tc-skill-refresh")?.addEventListener("click", () => void refreshSkills());
  document.getElementById("tc-skill-new")?.addEventListener("click", () => {
    state.skillEditor = {
      mode: "create",
      name: "",
      body: "# Minha Skill\n\nDescreva em uma frase quando esta skill deve ser aplicada.\n\n## Diretrizes\n\n- …",
    };
    renderModalLayer();
  });

  document.getElementById("tc-close-btn")?.addEventListener("click", () => {
    state.editorTabs = state.editorTabs.filter((t) => t.relPath !== TEAM_CONFIG_TAB_KEY);
    if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) {
      state.activeEditorPath = state.editorTabs[0]?.relPath || "";
    }
    renderEditorTabs();
    renderEditorBody();
  });

  const saveAll = async () => {
    const backend = api();
    if (!backend || !state.project) return;
    syncTeamConfigDraftFromDom(draft);
    const agentModels: Record<string, string> = {};
    if (draft.chiefModel) agentModels["chefe"] = draft.chiefModel;
    if (draft.devModel) agentModels["dev"] = draft.devModel;
    if (draft.qaModel) agentModels["qa"] = draft.qaModel;

    const errEl = document.getElementById("tc-error");
    if (errEl) errEl.style.display = "none";

    try {
      const cfg = await backend.SaveTeamWizard(state.project.root, {
        teamName: draft.teamName || "principal",
        chiefRole: draft.chiefRole || "architect",
        devCount: 1,
        maxDynamicDevs: draft.maxDynamicDevs,
        defaultModel: draft.defaultModel || "agy",
        agentModels,
        permissionMode: draft.permissionMode,
        worktrees: draft.worktrees,
        autoCommit: draft.autoCommit,
        autoPR: draft.autoPR,
        jiraEnabled: draft.jiraEnabled,
        jiraProject: draft.jiraProject,
        baseBranch: draft.baseBranch || "main",
        homologBranch: draft.homologBranch || "homolog",
        maxTokens: draft.maxTokens,
        skillAssignments: draft.skillAssignments,
      });
      state.project.hasTeam = true;
      state.project.teamConfig = cfg;
      state.teamConfigDraft = buildDraftFromProject();
      const lane = activeLane();
      if (lane) {
        lane.teamName = cfg.active || "principal";
        lane.agents = buildAgentRuntimes(lane.teamName);
      }
      renderLaneBarOnly();
      renderTokenBar();
      updateStatusBar();
      updateMissionBoardIfOpen();
      if ((state.sidebarView === "team" || state.sidebarView === "skills") && state.showLeftSidebar) {
        renderLeftSidebar();
      }
      toast(`Configuração da equipe "${cfg.active}" salva em .hivemind/team.yaml.`, "success");
      setStatus(`Equipe "${cfg.active}" e skills salvas em .hivemind/team.yaml.`);
    } catch (err: any) {
      const reason = err instanceof Error ? err.message : err?.message || String(err);
      if (errEl) {
        errEl.textContent = `Não foi possível salvar a configuração: ${reason}`;
        errEl.style.display = "block";
      }
    }
  };

  document.getElementById("tc-save-btn")?.addEventListener("click", () => void saveAll());
  document.getElementById("tc-save-btn-bottom")?.addEventListener("click", () => void saveAll());
}
