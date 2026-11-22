import type { AgentRuntime, ChatMessage, FileNode, GitHubStatus, GitHubSyncEvent, ProjectInfo, ProjectWorkspaceContext, TeamLane } from "./types";
import { chipForAgent, describeModelRoute, exactTokens, formatMicroUsd, matchAgentRuntime } from "./utils";
import { renderRightPane, renderTokenBar, scheduleChatFlush } from "./chat";
import { sanitizeAssistantText } from "./chat-timeline";
import { renderEditorBody, renderEditorTabs } from "./editor";
import { queueFileSystemRefresh, renderLeftSidebar, updateTeamStatusPills } from "./explorer";
import { appendGitProgress, loadGitHubPRs, refreshDiffs, refreshDiffsAndSummarize, updateGitActivityBadge } from "./git";
import { initInlineAIHooks, refreshInlineAIStatus } from "./inline-ai";
import { activeLane, createLane, ensureThinkingTicker, findActiveThinking, isCurrentTurnSolo, markThinkingDone, markToolsDone, renderLaneBarOnly, stopLaneRunningAgents, syncDynamicDevsInLane, syncStageAgents, updateAgentStatus } from "./lanes";
import { refreshJiraStatusAndProjects, refreshProviderProfiles, renderModalLayer } from "./modals";
import { handleResidentLaneEvent, openResidentTab, refreshResidentStatus } from "./resident";
import { installRenderGuardFlushHooks, renderGuard } from "./render-guard";
import { mountShell, renderFileMenu, renderProjectTabsBar, switchProjectTab, syncActivityButtons, syncLayoutVisibility, updateHeaderAndStatusBar, updateStatusBar } from "./shell";
import { TEAM_CONFIG_TAB_KEY, api, askConfirm, findLaneWithContext, hasDirtyTabs, loadFromProjectContext, nextProjectSeq, normalizePathKey, saveActiveToProjectContext, setStatus, state, toastError } from "./state";
import { openTeamConfigTab, updateMissionBoardIfOpen } from "./teamconfig";
import { handleLSPDiagnosticsEvent, initLSPHooks, refreshLSPStatus } from "./lsp";
import { handlePTYOutputEvent, renderTerminalDrawer } from "./terminal";
import { countTurnFiles } from "./companion/logic";
import { dismissMessage, mountCompanion, noteActivity, publishGitHubNotice, publishNotice } from "./companion/host";

function noticeProjectId(isActive: boolean, ctx?: ProjectWorkspaceContext): string {
  return isActive ? state.activeProjectId : (ctx?.id || "");
}

function stampChiefModel(m: ChatMessage, ag?: AgentRuntime) {
  if (!ag || m.modelLabel) return;
  const label = m.label || "";
  const named = label === ag.name || label.endsWith(`· ${ag.name}`);
  if (!named) return;
  const chip = chipForAgent(ag);
  if (!chip.label) return;
  m.modelLabel = chip.label;
  m.modelProvider = chip.providerLabel;
  m.modelFullId = chip.fullId;
}

function stampStreamingModel(lane: TeamLane, ag: AgentRuntime) {
  const tail = lane.messages[lane.messages.length - 1];
  if (!tail || tail.kind !== "chief") return;
  if (!(tail.label === ag.name || tail.label.endsWith(`· ${ag.name}`))) return;
  const chip = chipForAgent(ag);
  if (!chip.label) return;
  tail.modelLabel = chip.label;
  tail.modelProvider = chip.providerLabel;
  tail.modelFullId = chip.fullId;
}

export async function boot() {
  initInlineAIHooks();
  initLSPHooks();
  mountShell();
  mountCompanion();
  // Flush hooks do render guard (W4-06): re-renders async diferidos rodando
  // quando o usuário abandona um input da região protegida.
  installRenderGuardFlushHooks();
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
    if (payload?.laneId === "resident") {
      handleResidentLaneEvent(payload);
      return;
    }
    let hit = findLaneWithContext(payload.laneId);
    if (!hit && payload.laneId === "lane-autoreview") {
      // Auto-cria lane dedicada de Revisão Automática no projeto ativo sem roubar foco do chat manual.
      const teamName = payload.team || state.project?.teamConfig?.active || state.project?.teamConfig?.teams?.[0]?.name || activeLane()?.teamName || "principal";
      const autoLane = createLane(teamName);
      autoLane.id = "lane-autoreview";
      autoLane.label = "auto-review";
      autoLane.running = true;
      state.lanes.push(autoLane);
      renderLaneBarOnly();
      hit = { lane: autoLane, isActiveProject: true };
    }
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
      syncStageAgents(lane, ev.stage);
    }

    if (ev.kind === "stage") {
      if (ev.stage) syncStageAgents(lane, ev.stage);
      updateAgentStatus(lane, ev.author, ev.label, "running");
      noteActivity({ laneId: lane.id, kind: "stage", stage: ev.stage, label: ev.label, author: ev.author });
    } else if (ev.kind === "model_route" || ev.kind === "provider_failover") {
      const ag = matchAgentRuntime(lane.agents, ev.author, ev.label);
      const chip = describeModelRoute(String(ev.provider || ""), String(ev.model || ""));
      if (ag && chip.label) {
        ag.routeModel = chip.fullId;
        ag.routeProvider = String(ev.provider || "");
        ag.routeFriendly = chip.label;
        ag.routeTier = ev.tier ? String(ev.tier) : undefined;
        ag.routeReasons = Array.isArray(ev.reasons) ? ev.reasons.map(String) : undefined;
        stampStreamingModel(lane, ag);
      }
      const routeText = String(ev.text || (chip.fullId ? `Jev escolheu ${chip.fullId}` : "")).trim();
      if (routeText) {
        lane.messages.push({
          id: `route-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          kind: "system",
          label: ev.kind === "provider_failover" ? "Failover" : "Rota Jev",
          text: routeText,
        });
      }
    } else if (ev.kind === "usage") {
      const inTok = Number(ev.inputTokens || 0);
      const outTok = Number(ev.outputTokens || 0);
      const totTok = Number(ev.tokens || inTok + outTok);
      const costMicro = Number(ev.costMicroUsd || 0);
      lane.turnInTokens += inTok;
      lane.turnOutTokens += outTok;
      lane.turnTotalTokens += totTok;
      lane.sessionInTokens += inTok;
      lane.sessionOutTokens += outTok;
      lane.sessionTotalTokens += totTok;
      lane.turnCostMicroUsd = (lane.turnCostMicroUsd || 0) + costMicro;
      lane.sessionCostMicroUsd = (lane.sessionCostMicroUsd || 0) + costMicro;
      if (costMicro > 0) {
        const roleKey = String(ev.role || "Outro");
        lane.costByRole = lane.costByRole || {};
        lane.costByRole[roleKey] = (lane.costByRole[roleKey] || 0) + costMicro;
      }
      targetSummary.projectInputTokens += inTok;
      targetSummary.projectOutputTokens += outTok;
      targetSummary.projectTotalTokens += totTok;

      const ag = matchAgentRuntime(lane.agents, ev.author, ev.label);
      if (ag) {
        ag.inTokens += inTok;
        ag.outTokens += outTok;
        ag.tokens += totTok;
        ag.calls += 1;
        ag.costMicroUsd = (ag.costMicroUsd || 0) + costMicro;
      }
      if (isActiveProject) {
        updateStatusBar();
        renderTokenBar();
      }
    } else if (ev.kind === "plan_ready") {
      markToolsDone(lane);
      markThinkingDone(lane);
      stopLaneRunningAgents(lane, "done");
      const planId = `plan-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
      lane.messages.push({
        id: planId,
        kind: "plan",
        label: ev.label || "Plano pronto para aprovação",
        text: ev.text || "",
      });
      publishNotice(
        { laneId: lane.id, kind: "plan_ready", stage: ev.stage, label: ev.label, text: ev.text, gate: ev.gate },
        { surface: "team", projectId: noticeProjectId(isActiveProject, ctx), laneId: lane.id, messageId: planId },
      );
    } else if (ev.kind === "sdd_gate") {
      markToolsDone(lane);
      markThinkingDone(lane);
      stopLaneRunningAgents(lane, "done");
      const gateId = `gate-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
      lane.messages.push({
        id: gateId,
        kind: "plan",
        gate: (ev.gate === "jira" ? "jira" : "docs") as ChatMessage["gate"],
        label: ev.label || (ev.gate === "jira" ? "Gate 2 · Backlog Jira pronto" : "Gate 1 · Docs & Arquitetura SDD prontos"),
        text: ev.text || "",
      });
      publishNotice(
        { laneId: lane.id, kind: "sdd_gate", stage: ev.stage, label: ev.label, text: ev.text, gate: ev.gate },
        { surface: "team", projectId: noticeProjectId(isActiveProject, ctx), laneId: lane.id, messageId: gateId },
      );
    } else if (ev.kind === "approval_required") {
      markToolsDone(lane);
      const apprId = `appr-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
      lane.messages.push({
        id: apprId,
        kind: "approval",
        label: ev.label || "Permissão Solicitada pelo Chefe",
        text: ev.text || "",
        approvalId: ev.approvalId || "",
        toolName: ev.toolName || "",
      });
      publishNotice(
        { laneId: lane.id, kind: "approval_required", label: ev.label, text: ev.text, toolName: ev.toolName, gate: ev.gate },
        { surface: "team", projectId: noticeProjectId(isActiveProject, ctx), laneId: lane.id, messageId: apprId },
      );
    } else if (ev.kind === "devs_scaled") {
      if (Array.isArray(ev.devs) && ev.devs.length > 0) {
        syncDynamicDevsInLane(lane, ev.devs.map(String));
      }
    } else if (ev.kind === "handoff") {
      markToolsDone(lane);
      markThinkingDone(lane);
      if (ev.author || ev.label) {
        updateAgentStatus(lane, ev.author || "", ev.label, "done", "");
      }
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
      noteActivity({ laneId: lane.id, kind: "text", thought: true, author: ev.author, label: ev.label });
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
      if (sameTarget && !last.active) {
        const match = last.text.match(/×(\d+)$/);
        const count = match ? parseInt(match[1], 10) + 1 : 2;
        last.text = `${baseText} ×${count}`;
        last.active = true;
        if (!last.startedAt) last.startedAt = Date.now();
      } else {
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
      noteActivity({ laneId: lane.id, kind: "tool_call", toolName: ev.toolName, stage: lane.stage, author: ev.author });
    } else if (ev.kind === "tool_result") {
      markToolsDone(lane, ev.detail || "", ev.toolName);
    } else if (ev.kind === "text" && !ev.thought && ev.text) {
      const sanitized = sanitizeAssistantText(String(ev.text));
      if (!sanitized) {
        if (!ev.partial) {
          const tail = lane.messages[lane.messages.length - 1];
          if (tail && tail.kind === "chief" && sanitizeAssistantText(tail.text).trim() === "") {
            lane.messages.pop();
          }
        }
      } else {
        markToolsDone(lane);
        markThinkingDone(lane);
        updateAgentStatus(lane, ev.author, ev.label, "running", "");
        const last = lane.messages[lane.messages.length - 1];
        const msgLabel = ev.label || ev.author || "Chefe";
        const evTok = !ev.partial && ev.tokens ? Number(ev.tokens) : undefined;
        const ag = matchAgentRuntime(lane.agents, ev.author, msgLabel);
        const keepStream = (current: string, incoming: string) =>
          current.includes("[[hmtool:") && !incoming.includes("[[hmtool:");
        const findPendingChiefInTurn = (): ChatMessage | undefined => {
          for (let i = lane.messages.length - 1; i >= 0; i--) {
            const m = lane.messages[i];
            if (m.kind === "user" || m.kind === "handoff" || m.kind === "plan" || m.kind === "approval") {
              break;
            }
            if (m.kind === "chief" && m.label === msgLabel) {
              return m.tokens ? undefined : m;
            }
          }
          return undefined;
        };
        const pendingChief = !ev.partial ? findPendingChiefInTurn() : undefined;
        if (last && last.kind === "chief" && last.label === msgLabel && ev.partial && !last.tokens) {
          last.text = sanitizeAssistantText(`${last.text}${sanitized}`);
          stampChiefModel(last, ag);
          if (!last.text) lane.messages.pop();
        } else if (!ev.partial && pendingChief) {
          if (keepStream(pendingChief.text, sanitized)) {
            const summary = sanitized.trim();
            if (summary && !pendingChief.text.includes(summary)) {
              pendingChief.text = pendingChief.text.endsWith("\n")
                ? `${pendingChief.text}\n${summary}`
                : `${pendingChief.text}\n\n${summary}`;
            }
          } else {
            pendingChief.text = sanitized;
          }
          if (evTok) pendingChief.tokens = evTok;
          stampChiefModel(pendingChief, ag);
        } else if (sanitized.trim() !== "" || ev.partial) {
          const created: ChatMessage = {
            id: `chief-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
            kind: "chief",
            label: msgLabel,
            text: sanitized,
            tokens: evTok,
          };
          stampChiefModel(created, ag);
          lane.messages.push(created);
        }
      }
    } else if (ev.kind === "error") {
      markToolsDone(lane);
      markThinkingDone(lane);
      stopLaneRunningAgents(lane, "error", ev.author, ev.label);
      const errText = String(ev.text || "Erro desconhecido");
      const isUserCancel = Boolean(lane.canceledByUser) && /context canceled|canceled/i.test(errText);
      const prevMsg = lane.messages[lane.messages.length - 1];
      const isDuplicate = prevMsg && prevMsg.kind === "error" && prevMsg.text === errText;
      if (!isUserCancel && !isDuplicate) {
        const errId = `err-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
        lane.messages.push({
          id: errId,
          kind: "error",
          label: "Erro",
          text: errText,
        });
        publishNotice(
          { laneId: lane.id, kind: "error", stage: lane.stage, label: ev.label, author: ev.author, text: errText },
          { surface: "team", projectId: noticeProjectId(isActiveProject, ctx), laneId: lane.id, messageId: errId },
        );
      }
    } else if (ev.kind === "finish") {
      markToolsDone(lane);
      markThinkingDone(lane);
      const stageNow = lane.stage || "";
      const wasSolo = isCurrentTurnSolo(lane);
      if (lane.stage && !lane.doneStages.includes(lane.stage)) {
        lane.doneStages.push(lane.stage);
      }
      lane.stage = "";
      stopLaneRunningAgents(lane, "done");
      const lastMsg = lane.messages[lane.messages.length - 1];
      let cardId = "";
      if (wasSolo && lastMsg && lastMsg.kind === "chief") {
        cardId = lastMsg.id;
        if (lane.turnTotalTokens > 0) {
          lastMsg.tokens = lane.turnTotalTokens;
        }
      } else {
        const tokSuffix = lane.turnTotalTokens > 0
          ? ` · ${exactTokens(lane.turnTotalTokens)} tokens no turno (Entrada: ${exactTokens(lane.turnInTokens)} / Saída: ${exactTokens(lane.turnOutTokens)})`
          : "";
        cardId = `sys-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
        lane.messages.push({
          id: cardId,
          kind: "system",
          label: "Sistema",
          text: `${ev.text || "Execução concluída."}${tokSuffix}`,
        });
      }
      publishNotice(
        { laneId: lane.id, kind: "finish", stage: stageNow, text: String(ev.text || ""), origin: ev.origin, fileCount: countTurnFiles(lane.messages) },
        { surface: "team", projectId: noticeProjectId(isActiveProject, ctx), laneId: lane.id, messageId: cardId },
      );
      if (isActiveProject) {
        refreshDiffsAndSummarize(lane);
        refreshSessions();
      }
    } else if (ev.kind === "approval_resolved") {
      // Uma aprovação resolvida no celular (ou em outra aba) desaparece daqui também.
      const target = lane.messages.find((m) => m.kind === "approval" && m.approvalId === ev.approvalId && !m.resolved);
      if (target) {
        target.resolved = /recusada/.test(String(ev.text || "")) ? "denied" : "approved";
        dismissMessage(target.id);
        if (isActiveProject) {
          renderRightPane();
          updateStatusBar();
        }
      }
    }

    if (!isActiveProject) {
      renderProjectTabsBar();
      return;
    }

    if (lane.id === activeLane()?.id || state.splitLaneIds.includes(lane.id)) {
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

  let gitChangedTimer: ReturnType<typeof setTimeout> | null = null;
  window.runtime?.EventsOn("hivemind:git-changed", (payload: { workspace?: string; branch?: string }) => {
    const wsKey = normalizePathKey(payload?.workspace || "");
    if (!wsKey) return;
    const branch = String(payload?.branch || "").trim();
    const tabCtx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === wsKey);
    if (tabCtx && branch) {
      tabCtx.project.branch = branch;
    }
    if (state.project && normalizePathKey(state.project.root) === wsKey) {
      if (branch) {
        state.project.branch = branch;
        updateHeaderAndStatusBar();
      }
      if (gitChangedTimer) clearTimeout(gitChangedTimer);
      gitChangedTimer = setTimeout(() => {
        gitChangedTimer = null;
        void refreshDiffs();
      }, 250);
    }
  });

  window.runtime?.EventsOn("hivemind:fs-event", (payload: any) => {
    const wsKey = normalizePathKey(payload?.workspace || "");
    if (!wsKey) return;
    const changed: string[] = Array.isArray(payload?.changed) ? payload.changed.map(String) : [];
    queueFileSystemRefresh(wsKey, changed, Boolean(payload?.overflow));
  });

  window.runtime?.EventsOn("hivemind:git-progress", (payload: any) => {
    const wsKey = normalizePathKey(payload?.workspace || "");
    if (!state.project || normalizePathKey(state.project.root) !== wsKey) return;
    appendGitProgress(String(payload?.label || ""), String(payload?.line || ""));
  });

  window.runtime?.EventsOn("hivemind:worktree-progress", (payload: any) => {
    const wsKey = normalizePathKey(payload?.workspace || "");
    if (!state.project || normalizePathKey(state.project.root) !== wsKey) return;
    state.worktrees.progress = {
      step: String(payload?.step || "processando"),
      pct: Number(payload?.pct || 0),
    };
    if (state.sidebarView === "git" && state.gitTab === "worktrees") {
      renderLeftSidebar();
    }
  });

  window.runtime?.EventsOn("hivemind:pty-output", (payload: any) => {
    handlePTYOutputEvent(payload);
  });

  window.runtime?.EventsOn("hivemind:lsp-diagnostics", (payload: any) => {
    handleLSPDiagnosticsEvent(payload);
  });
  void refreshResidentStatus();

  // Botão "Abrir IDE" do Agent Hive flutuante: traz a IDE para frente e abre
  // a aba Agent Hive (hive:resident) já sincronizada (Spec 007 H008).
  window.runtime?.EventsOn("hivemind:open-resident", () => {
    openResidentTab();
  });

  window.runtime?.EventsOn("hivemind:autoreview-status", (payload: any) => {
    const wsKey = normalizePathKey(payload?.workspace || "");
    if (!wsKey) return;
    const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === wsKey);
    const prevWasReviewing = (ctx ? ctx.autoReview?.state : state.autoReview?.state) === "reviewing";
    if (ctx) ctx.autoReview = payload;

    const isActive = Boolean(state.project && normalizePathKey(state.project.root) === wsKey);
    if (isActive) {
      state.autoReview = payload;
      const autoLane = state.lanes.find((l) => l.id === "lane-autoreview");
      if (autoLane) {
        autoLane.running = payload.state === "reviewing";
        if (!autoLane.running) {
          stopLaneRunningAgents(autoLane, "done");
        }
        renderLaneBarOnly();
      }
      updateStatusBar();
      if (state.sidebarView === "git" && state.showLeftSidebar) {
        renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
      }
      if (prevWasReviewing && payload.state !== "reviewing") {
        void loadGitHubPRs();
        void refreshSessions(state.project!.root);
      }
    }
  });

  window.runtime?.EventsOn("hivemind:github-sync", (payload: GitHubSyncEvent) => {
    const wsKey = normalizePathKey(payload?.workspace || "");
    if (!wsKey) return;
    const prs = Array.isArray(payload?.prs) ? payload.prs : [];
    const events = Array.isArray(payload?.events) ? payload.events : [];
    const syncedAt = String(payload?.syncedAt || "");

    const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === wsKey);
    const isActive = Boolean(state.project && normalizePathKey(state.project.root) === wsKey);
    const viewingFlow = isActive && state.sidebarView === "git" && state.showLeftSidebar && state.gitTab === "flow" && document.hasFocus();

    if (ctx) {
      ctx.prs.list = prs;
      ctx.prs.loadedOnce = true;
      ctx.prs.loading = false;
      ctx.githubLastSyncAt = syncedAt;
      if (events.length > 0 && !viewingFlow) {
        ctx.githubUnreadCount = (ctx.githubUnreadCount || 0) + events.length;
      }
    }
    if (isActive) {
      state.prs.list = prs;
      state.prs.loadedOnce = true;
      state.prs.loading = false;
      state.githubLastSyncAt = syncedAt;
      if (events.length > 0 && !viewingFlow) {
        state.githubUnreadCount += events.length;
      }
      if (state.sidebarView === "git" && state.showLeftSidebar) {
        renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
      }
    }

    updateGitActivityBadge();

    if (events.length > 0) {
      const projectName = ctx?.project.name || (isActive ? state.project?.name : undefined);
      publishGitHubNotice(events, noticeProjectId(isActive, ctx), projectName);
    }
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
    if (state.sidebarView === "team" && state.showLeftSidebar) renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
    if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) renderEditorBody();
    if (state.showAPIKeysModal) renderGuard.guardedRender("modal-layer", renderModalLayer);
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
    if (state.sidebarView === "team" && state.showLeftSidebar) renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
    if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) renderEditorBody();
    if (state.showAPIKeysModal) renderGuard.guardedRender("modal-layer", renderModalLayer);
  }).catch(() => {});

  backend.ScanAllProviders?.(rootDir || "").then((report) => {
    state.providersScan = report;
    updateStatusBar();
    if (state.showAPIKeysModal) renderGuard.guardedRender("modal-layer", renderModalLayer);
  }).catch(() => {});

  void refreshInlineAIStatus();
  void refreshJiraStatusAndProjects();
  void refreshProviderProfiles();

  backend.MobileBridgeStatus?.().then((st) => {
    state.mobileStatus = st;
    updateStatusBar();
  }).catch(() => {});
  backend.RelayStatus?.().then((st) => {
    state.relayStatus = st;
    updateStatusBar();
  }).catch(() => {});

  backend.CursorCLIStatus?.().then((st) => {
    state.cursorCLI = st;
    if (state.sidebarView === "team" && state.showLeftSidebar) renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
    if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) renderEditorBody();
  }).catch(() => {});

  if (rootDir) {
    const targetKey = normalizePathKey(rootDir);
    backend.GitHubStatus(rootDir).then((gh) => {
      const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
      if (ctx) ctx.ghStatus = gh;
      if (state.project && normalizePathKey(state.project.root) === targetKey) {
        state.ghStatus = gh;
        if (state.sidebarView === "git" && state.showLeftSidebar) {
          renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
        }
      }
    }).catch(() => {});

    refreshSessions(rootDir);
    void refreshLSPStatus(rootDir);

    backend.GetAutoReviewStatus?.(rootDir).then((ar) => {
      const ctx = state.projectTabs.find((p) => normalizePathKey(p.project.root) === targetKey);
      if (ctx) ctx.autoReview = ar;
      if (state.project && normalizePathKey(state.project.root) === targetKey) {
        state.autoReview = ar;
        updateStatusBar();
        if (state.sidebarView === "git" && state.showLeftSidebar) {
          renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
        }
      }
    }).catch(() => {});
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
        renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
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
    void refreshJiraStatusAndProjects();
    void refreshProviderProfiles();
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
    gitRepos: [],
    activeGitRepoRoot: "",
    collapsedGitRepos: {},
    commitMsgByRepo: {},
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
    gitGraph: { commits: [], loading: false, loadedOnce: false, allBranches: true, collapsed: false, repoRoot: "" },
    gitTab: "changes",
    githubUnreadCount: 0,
    githubLastSyncAt: "",
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
