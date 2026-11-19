import type { ChatMessage, FileNode, GitHubStatus, ProjectInfo, ProjectWorkspaceContext } from "./types";
import { exactTokens, matchAgentRuntime } from "./utils";
import { renderRightPane, scheduleChatFlush } from "./chat";
import { renderEditorBody, renderEditorTabs } from "./editor";
import { queueFileSystemRefresh, renderLeftSidebar, updateTeamStatusPills } from "./explorer";
import { refreshDiffs, refreshDiffsAndSummarize } from "./git";
import { initInlineAIHooks, refreshInlineAIStatus } from "./inline-ai";
import { activeLane, createLane, ensureThinkingTicker, findActiveThinking, isCurrentTurnSolo, markThinkingDone, markToolsDone, renderLaneBarOnly, syncDynamicDevsInLane, updateAgentStatus } from "./lanes";
import { renderModalLayer } from "./modals";
import { mountShell, renderFileMenu, renderProjectTabsBar, switchProjectTab, syncActivityButtons, syncLayoutVisibility, updateHeaderAndStatusBar, updateStatusBar } from "./shell";
import { TEAM_CONFIG_TAB_KEY, api, askConfirm, findLaneWithContext, hasDirtyTabs, loadFromProjectContext, nextProjectSeq, normalizePathKey, saveActiveToProjectContext, setStatus, state, toastError } from "./state";
import { openTeamConfigTab, updateMissionBoardIfOpen } from "./teamconfig";
import { renderTerminalDrawer } from "./terminal";

export async function boot() {
  initInlineAIHooks();
  mountShell();
  const backend = api();
  if (!backend) {
    renderLeftSidebar();
    renderEditorTabs();
    renderEditorBody();
    renderRightPane();
    setStatus("Modo preview (sem runtime Wails)");
    return;
  }

  window.runtime?.EventsOn("hivemind:lane-event", (payload: any) => {
    const hit = findLaneWithContext(payload.laneId);
    if (!hit) return;
    const { lane, ctx, isActiveProject } = hit;
    const targetSummary = isActiveProject
      ? state.sessionsSummary
      : (ctx?.sessionsSummary ?? state.sessionsSummary);
    const ev = payload.event;
    if (ev.stage) {
      if (lane.stage && lane.stage !== ev.stage && !lane.doneStages.includes(lane.stage)) {
        lane.doneStages.push(lane.stage);
      }
      lane.stage = ev.stage;
    }

    if (ev.kind === "stage") {
      // stage updated above
    } else if (ev.kind === "usage") {
      const inTok = Number(ev.inputTokens || 0);
      const outTok = Number(ev.outputTokens || 0);
      const totTok = Number(ev.tokens || inTok + outTok);
      lane.turnInTokens += inTok;
      lane.turnOutTokens += outTok;
      lane.turnTotalTokens += totTok;
      lane.sessionInTokens += inTok;
      lane.sessionOutTokens += outTok;
      lane.sessionTotalTokens += totTok;
      targetSummary.projectInputTokens += inTok;
      targetSummary.projectOutputTokens += outTok;
      targetSummary.projectTotalTokens += totTok;

      const ag = matchAgentRuntime(lane.agents, ev.author, ev.label);
      if (ag) {
        ag.inTokens += inTok;
        ag.outTokens += outTok;
        ag.tokens += totTok;
        ag.calls += 1;
      }
      if (isActiveProject) updateStatusBar();
    } else if (ev.kind === "plan_ready") {
      markToolsDone(lane);
      markThinkingDone(lane);
      lane.messages.push({
        id: `plan-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        kind: "plan",
        label: ev.label || "Plano pronto para aprovação",
        text: ev.text || "",
      });
    } else if (ev.kind === "sdd_gate") {
      markToolsDone(lane);
      markThinkingDone(lane);
      lane.messages.push({
        id: `gate-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        kind: "plan",
        gate: (ev.gate === "jira" ? "jira" : "docs") as ChatMessage["gate"],
        label: ev.label || (ev.gate === "jira" ? "Gate 2 · Backlog Jira pronto" : "Gate 1 · Docs & Arquitetura SDD prontos"),
        text: ev.text || "",
      });
    } else if (ev.kind === "approval_required") {
      markToolsDone(lane);
      lane.messages.push({
        id: `appr-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        kind: "approval",
        label: ev.label || "Permissão Solicitada pelo Chefe",
        text: ev.text || "",
        approvalId: ev.approvalId || "",
      });
    } else if (ev.kind === "devs_scaled") {
      if (Array.isArray(ev.devs) && ev.devs.length > 0) {
        syncDynamicDevsInLane(lane, ev.devs.map(String));
      }
    } else if (ev.kind === "handoff") {
      markToolsDone(lane);
      markThinkingDone(lane);
      lane.messages.push({
        id: `ho-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        kind: "handoff",
        label: ev.label || "Handoff da Equipe",
        text: ev.text || "",
      });
    } else if (ev.kind === "text" && ev.thought && ev.text) {
      let th = findActiveThinking(lane);
      if (!th) {
        th = {
          id: `th-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          kind: "thinking",
          label: isCurrentTurnSolo(lane) ? "Pensando" : (ev.label || "Pensando"),
          text: "",
          active: true,
          solo: isCurrentTurnSolo(lane),
          startedAt: Date.now(),
          stepCount: 0,
        };
        lane.messages.push(th);
        ensureThinkingTicker();
      }
      if (ev.partial) {
        th.text += ev.text;
      } else {
        th.text = th.text ? `${th.text}\n\n${ev.text}` : ev.text;
      }
    } else if (ev.kind === "tool_call") {
      updateAgentStatus(lane, ev.author, ev.label, "running", ev.toolName);
      const th = findActiveThinking(lane);
      if (th) th.stepCount = (th.stepCount || 0) + 1;
      const last = lane.messages[lane.messages.length - 1];
      const toolLabel = ev.label || ev.author || "Agente";
      const baseText = `${ev.toolName}`;
      const incomingDetail = (ev.detail || "").trim();
      const sameTarget = last && last.kind === "tool" && last.label === toolLabel
        && last.toolName === ev.toolName
        && ((last.toolDetail || "").trim() === incomingDetail);
      if (sameTarget) {
        const match = last.text.match(/×(\d+)$/);
        const count = match ? parseInt(match[1], 10) + 1 : 2;
        last.text = `${baseText} ×${count}`;
        last.active = true;
        if (!last.startedAt) last.startedAt = Date.now();
      } else {
        markToolsDone(lane);
        lane.messages.push({
          id: `tool-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          kind: "tool",
          label: toolLabel,
          text: baseText,
          active: true,
          solo: isCurrentTurnSolo(lane),
          toolName: ev.toolName,
          toolDetail: incomingDetail,
          startedAt: Date.now(),
        });
      }
    } else if (ev.kind === "tool_result") {
      markToolsDone(lane, ev.detail);
    } else if (ev.kind === "text" && !ev.thought && ev.text) {
      markToolsDone(lane);
      markThinkingDone(lane);
      updateAgentStatus(lane, ev.author, ev.label, "running", "");
      const last = lane.messages[lane.messages.length - 1];
      const msgLabel = ev.label || ev.author || "Chefe";
      const evTok = !ev.partial && ev.tokens ? Number(ev.tokens) : undefined;
      if (last && last.kind === "chief" && last.label === msgLabel && ev.partial) {
        last.text += ev.text;
      } else if (!ev.partial && last && last.kind === "chief" && last.label === msgLabel) {
        last.text = ev.text;
        if (evTok) last.tokens = evTok;
      } else {
        lane.messages.push({
          id: `chief-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          kind: "chief",
          label: msgLabel,
          text: ev.text,
          tokens: evTok,
        });
      }
    } else if (ev.kind === "error") {
      markToolsDone(lane);
      markThinkingDone(lane);
      const errText = String(ev.text || "Erro desconhecido");
      const isUserCancel = Boolean(lane.canceledByUser) && /context canceled|canceled/i.test(errText);
      const prevMsg = lane.messages[lane.messages.length - 1];
      const isDuplicate = prevMsg && prevMsg.kind === "error" && prevMsg.text === errText;
      if (!isUserCancel && !isDuplicate) {
        lane.messages.push({
          id: `err-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          kind: "error",
          label: "Erro",
          text: errText,
        });
      }
    } else if (ev.kind === "finish") {
      markToolsDone(lane);
      markThinkingDone(lane);
      const wasSolo = isCurrentTurnSolo(lane);
      if (lane.stage && !lane.doneStages.includes(lane.stage)) {
        lane.doneStages.push(lane.stage);
      }
      lane.stage = "";
      lane.agents.forEach((a) => {
        if (a.status === "running") a.status = "done";
        a.tool = "";
      });
      const lastMsg = lane.messages[lane.messages.length - 1];
      if (wasSolo && lastMsg && lastMsg.kind === "chief") {
        if (lane.turnTotalTokens > 0) {
          lastMsg.tokens = lane.turnTotalTokens;
        }
      } else {
        const tokSuffix = lane.turnTotalTokens > 0
          ? ` · ${exactTokens(lane.turnTotalTokens)} tokens no turno (Entrada: ${exactTokens(lane.turnInTokens)} / Saída: ${exactTokens(lane.turnOutTokens)})`
          : "";
        lane.messages.push({
          id: `sys-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          kind: "system",
          label: "Sistema",
          text: `${ev.text || "Execução concluída."}${tokSuffix}`,
        });
      }
      if (isActiveProject) {
        refreshDiffsAndSummarize(lane);
        refreshSessions();
      }
    }

    if (!isActiveProject) {
      renderProjectTabsBar();
      return;
    }

    if (lane.id === activeLane()?.id) {
      scheduleChatFlush();
      updateMissionBoardIfOpen();
    } else {
      renderLaneBarOnly();
    }
    if (state.sidebarView === "team" && state.showLeftSidebar) {
      updateTeamStatusPills();
    }
  });

  window.runtime?.EventsOn("hivemind:terminal-output", (payload: any) => {
    const wsKey = normalizePathKey(payload?.workspace || "");
    const chunk = String(payload?.chunk || "");
    if (!wsKey || !chunk) return;
    saveActiveToProjectContext();
    const targetCtx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === wsKey);
    const term = targetCtx
      ? targetCtx.terminal
      : (state.project && normalizePathKey(state.project.root) === wsKey ? state.terminal : null);
    if (!term) return;
    const runningEntry = [...term.entries].reverse().find((e) => e.running);
    if (!runningEntry) return;
    if (runningEntry.output === "Executando…") {
      runningEntry.output = chunk;
    } else {
      runningEntry.output += chunk;
    }
    if (state.project && normalizePathKey(state.project.root) === wsKey && state.terminal.open) {
      const liveNode = document.querySelector<HTMLElement>(`[data-term-out="${CSS.escape(runningEntry.id)}"]`);
      if (liveNode) {
        liveNode.textContent = runningEntry.output;
        const outBox = document.getElementById("terminal-output");
        if (outBox) outBox.scrollTop = outBox.scrollHeight;
      }
    }
  });

  window.runtime?.EventsOn("hivemind:fs-event", (payload: any) => {
    const wsKey = normalizePathKey(payload?.workspace || "");
    if (!wsKey) return;
    const changed: string[] = Array.isArray(payload?.changed) ? payload.changed.map(String) : [];
    queueFileSystemRefresh(wsKey, changed, Boolean(payload?.overflow));
  });

  try {
    const init = await backend.GetInitialState();
    state.recent = init.recentProjects || [];
    state.models = init.models || [];
    if (init.agyStatus?.Found) state.agyStatus = init.agyStatus;
    if (init.openCodeGoModels && init.openCodeGoModels.length > 0) state.openCodeGoModels = init.openCodeGoModels;
    if (init.openCodeModels && init.openCodeModels.length > 0) state.openCodeModels = init.openCodeModels;
    if (init.openCodeStatus && (init.openCodeStatus.configured || init.openCodeStatus.modelCount > 0)) {
      state.openCodeStatus = init.openCodeStatus;
    }
    state.goVersion = init.goVersion || "";
    state.os = init.os || "windows";
    applyProject(init.initialProject);
    if (!init.initialProject?.hasTeam) {
      void openTeamConfigTab();
    }
    bootstrapAsync(init.initialProject?.root);
  } catch (err: any) {
    toastError("iniciar a conexão com o backend (reinicie o app se o problema persistir)", err);
  }
}

export function bootstrapAsync(rootDir?: string) {
  const backend = api();
  if (!backend) return;

  backend.BootstrapAgy(false).then((res) => {
    state.agyStatus = res.status;
    if (res.models && res.models.length > 0) {
      state.models = res.models;
    }
    updateStatusBar();
    updateMissionBoardIfOpen();
    if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
    if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) renderEditorBody();
    if (state.doctorModal) renderModalLayer();
  }).catch(() => {});

  backend.BootstrapOpenCode?.(false).then((res) => {
    state.openCodeStatus = res.status;
    if (res.goModels && res.goModels.length > 0) {
      state.openCodeGoModels = res.goModels;
    }
    if (res.models && res.models.length > 0) {
      state.openCodeModels = res.models;
    }
    updateStatusBar();
    updateMissionBoardIfOpen();
    void refreshInlineAIStatus();
    if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
    if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) renderEditorBody();
    if (state.showOpenCodeModal) renderModalLayer();
  }).catch(() => {});

  void refreshInlineAIStatus();

  if (rootDir) {
    const targetKey = normalizePathKey(rootDir);
    backend.GitHubStatus(rootDir).then((gh) => {
      const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
      if (ctx) ctx.ghStatus = gh;
      if (state.project && normalizePathKey(state.project.root) === targetKey) {
        state.ghStatus = gh;
        if (state.sidebarView === "git" && state.showLeftSidebar) {
          renderLeftSidebar();
        }
      }
    }).catch(() => {});

    refreshSessions(rootDir);
  }
}

export async function refreshSessions(targetRoot?: string) {
  const backend = api();
  const root = targetRoot || state.project?.root;
  if (!backend || !root) return;
  const targetKey = normalizePathKey(root);
  try {
    const sum = await backend.ListSessions(root);
    const summaryObj = {
      sessions: sum?.sessions || [],
      projectInputTokens: Number(sum?.projectInputTokens || 0),
      projectOutputTokens: Number(sum?.projectOutputTokens || 0),
      projectTotalTokens: Number(sum?.projectTotalTokens || 0),
    };
    const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
    if (ctx) ctx.sessionsSummary = summaryObj;
    if (state.project && normalizePathKey(state.project.root) === targetKey) {
      state.sessionsSummary = summaryObj;
      updateStatusBar();
      updateMissionBoardIfOpen();
      if (state.sidebarView === "sessions" && state.showLeftSidebar) {
        renderLeftSidebar();
      }
    }
  } catch {
    // ignore empty workspace sessions
  }
}

export function applyProject(proj: ProjectInfo, inNewTab = true) {
  saveActiveToProjectContext();
  const targetKey = normalizePathKey(proj.root);
  const alreadyOpen = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
  if (alreadyOpen) {
    alreadyOpen.project = proj;
    loadFromProjectContext(alreadyOpen);
    renderProjectTabsBar();
    updateHeaderAndStatusBar();
    renderLeftSidebar();
    renderEditorTabs();
    renderEditorBody();
    renderTerminalDrawer();
    renderRightPane();
    setStatus(`Projeto em foco: ${proj.root}`);
    refreshDiffs();
    return;
  }

  const activeTeam = proj.teamConfig?.active || proj.teamConfig?.teams?.[0]?.name || "principal";
  const tempPrevProj = state.project;
  state.project = proj;
  const firstLane = createLane(activeTeam);
  state.project = tempPrevProj;

  const dirCache = new Map<string, FileNode[]>();
  dirCache.set("", proj.files || []);

  const newCtx: ProjectWorkspaceContext = {
    id: (inNewTab || !state.activeProjectId) ? `proj-${nextProjectSeq()}` : state.activeProjectId,
    project: proj,
    sidebarView: "explorer",
    expandedDirs: new Set<string>(),
    dirCache,
    explorerFilter: "",
    explorerFilterResults: [],
    editorTabs: [],
    activeEditorPath: "",
    lanes: [firstLane],
    activeLaneId: firstLane.id,
    runMode: "auto",
    diffs: [],
    ghStatus: null,
    textSearch: { query: "", results: [], searching: false },
    sessionsSummary: {
      sessions: [],
      projectInputTokens: 0,
      projectOutputTokens: 0,
      projectTotalTokens: 0,
    },
    issues: { list: [], loading: false, loadedOnce: false },
    prs: { list: [], loading: false, loadedOnce: false },
    gitGraph: { commits: [], loading: false, loadedOnce: false, allBranches: true, collapsed: false },
    gitTab: "changes",
    draftObjective: "",
    draftImages: [],
    jiraKeyInput: "",
    commitMsgInput: "",
    terminal: { open: false, running: false, cmdInput: "", entries: [] },
  };

  if (inNewTab || state.projectTabs.length === 0) {
    state.projectTabs.push(newCtx);
  } else {
    const idx = state.projectTabs.findIndex((p) => p.id === state.activeProjectId);
    if (idx >= 0) state.projectTabs[idx] = newCtx;
    else state.projectTabs.push(newCtx);
  }

  loadFromProjectContext(newCtx);
  syncActivityButtons();
  syncLayoutVisibility();
  renderProjectTabsBar();
  updateHeaderAndStatusBar();
  renderLeftSidebar();
  renderEditorTabs();
  renderEditorBody();
  renderTerminalDrawer();
  renderRightPane();
  setStatus(`Projeto aberto em aba isolada: ${proj.root}`);
  refreshDiffs();
}

export async function openFolderDialog(inNewTab = true) {
  const backend = api();
  if (!backend) return;
  const dir = await backend.ChooseDirectory();
  if (!dir) return;
  await openProjectPath(dir, inNewTab);
}

export async function openProjectPath(dir: string, inNewTab = true) {
  const backend = api();
  if (!backend) return;
  const targetKey = normalizePathKey(dir);
  const existingTab = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
  if (existingTab) {
    state.showFileMenu = false;
    renderFileMenu();
    switchProjectTab(existingTab.id);
    return;
  }
  const doOpen = async () => {
    try {
      const proj = await backend.OpenProject(dir);
      state.recent = await backend.GetRecentProjects();
      state.showFileMenu = false;
      renderFileMenu();
      applyProject(proj, inNewTab);
      bootstrapAsync(proj.root);
      if (!proj.hasTeam) {
        void openTeamConfigTab();
      }
    } catch (err: any) {
      toastError("abrir a pasta do projeto", err);
    }
  };
  if (!inNewTab && hasDirtyTabs()) {
    askConfirm({
      title: "Substituir Projeto na Aba Atual",
      body: "Existem arquivos modificados não salvos nesta aba de projeto. Deseja substituir mesmo assim? (Dica: use 'Abrir Projeto em Nova Aba' para manter ambos abertos)",
      confirmLabel: "Substituir Projeto",
      danger: true,
      onConfirm: doOpen,
    });
    return;
  }
  await doOpen();
}
