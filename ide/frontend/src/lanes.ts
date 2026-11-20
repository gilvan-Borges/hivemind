import { icons } from "./icons";
import type { AgentRuntime, ChatMessage, TeamLane } from "./types";
import { escapeHtml, formatElapsedDuration, formatTokens, matchAgentRuntime, updateAgentRuntimeStatus } from "./utils";
import { rebuildChatMessagesFull, renderTokenBar, syncComposerButtons } from "./chat";
import { renderEditorBody } from "./editor";
import { renderLeftSidebar } from "./explorer";
import { openSidebarView, updateStatusBar } from "./shell";
import { TEAM_CONFIG_TAB_KEY, api, askConfirm, nextLaneSeq, setStatus, state, toast, toastError } from "./state";
import { buildDraftFromProject, updateMissionBoardIfOpen } from "./teamconfig";

export function buildAgentRuntimes(teamName: string): AgentRuntime[] {
  const teams = state.project?.teamConfig?.teams || [];
  const t = teams.find((x) => x.name === teamName) ?? teams[0];
  if (!t) return [];
  return (t.agents || []).map((a) => ({
    name: a.name,
    role: a.role,
    model: a.model,
    status: "idle",
    inTokens: 0,
    outTokens: 0,
    tokens: 0,
    calls: 0,
  }));
}

export function createLane(teamName: string): TeamLane {
  const id = `lane-${nextLaneSeq()}`;
  return {
    id,
    teamName,
    sessionId: "",
    running: false,
    stage: "",
    doneStages: [],
    autoScroll: true,
    turnInTokens: 0,
    turnOutTokens: 0,
    turnTotalTokens: 0,
    sessionInTokens: 0,
    sessionOutTokens: 0,
    sessionTotalTokens: 0,
    agents: buildAgentRuntimes(teamName),
    messages: [],
  };
}

export function laneDisplayName(lane?: TeamLane | null): string {
  if (!lane) return "-";
  const custom = (lane.label || "").trim();
  return custom || lane.teamName;
}

export function activeLane(): TeamLane | undefined {
  return state.lanes.find((l) => l.id === state.activeLaneId) ?? state.lanes[0];
}

let editingLaneId: string | null = null;

export let thinkingTickTimer = 0;

export function formatElapsedThinking(ms: number): string {
  return formatElapsedDuration(ms);
}

export function isCurrentTurnSolo(lane: TeamLane): boolean {
  if (lane.stage === "dev" || lane.stage === "qa") return false;
  for (let i = lane.messages.length - 1; i >= 0; i--) {
    const m = lane.messages[i];
    if (m.kind === "user") break;
    if (m.kind === "handoff") return false;
  }
  return true;
}

export function findActiveThinking(lane: TeamLane): ChatMessage | null {
  for (let i = lane.messages.length - 1; i >= 0; i--) {
    const m = lane.messages[i];
    if (m.kind === "user") break;
    if (m.kind === "thinking" && m.active) return m;
  }
  return null;
}

export function ensureThinkingTicker() {
  if (thinkingTickTimer) return;
  thinkingTickTimer = window.setInterval(() => {
    const now = Date.now();
    const nodes = document.querySelectorAll<HTMLElement>("[data-thinking-started], [data-agent-running-since]");
    let anyActive = false;
    nodes.forEach((node) => {
      const started = Number(node.dataset.thinkingStarted || node.dataset.agentRunningSince || 0);
      if (started > 0) {
        anyActive = true;
        node.textContent = formatElapsedThinking(now - started);
      }
    });
    if (!anyActive) {
      const stillRunning = state.lanes.some((l) => l.messages.some((m) => m.kind === "thinking" && m.active));
      if (!stillRunning) {
        window.clearInterval(thinkingTickTimer);
        thinkingTickTimer = 0;
      }
    }
  }, 1000);
}

export function markThinkingDone(lane: TeamLane) {
  const now = Date.now();
  for (const m of lane.messages) {
    if (m.kind === "thinking" && m.active) {
      m.active = false;
      if (m.startedAt && !m.durationMs) {
        m.durationMs = Math.max(1000, now - m.startedAt);
      }
    }
  }
}

export function markToolsDone(lane: TeamLane, resultDetail?: string, toolName?: string) {
  const now = Date.now();
  if (resultDetail !== undefined) {
    // Match a single tool_result to the earliest active tool message for that toolName
    // (or the earliest active tool message still awaiting output) so parallel tool batches
    // pair 1-to-1 instead of collapsing all outputs onto the last row.
    const target = lane.messages.find(
      (m) => m.kind === "tool" && m.active && !m.toolOutput && (!toolName || m.toolName === toolName),
    ) || lane.messages.find((m) => m.kind === "tool" && m.active);
    if (target) {
      target.active = false;
      if (target.startedAt && !target.durationMs) {
        target.durationMs = Math.max(1, now - target.startedAt);
      }
      if (resultDetail) {
        target.toolOutput = target.toolOutput ? `${target.toolOutput}\n${resultDetail}` : resultDetail;
      }
      return;
    }
  }
  for (const m of lane.messages) {
    if (m.kind === "tool" && m.active) {
      m.active = false;
      if (m.startedAt && !m.durationMs) {
        m.durationMs = Math.max(1, now - m.startedAt);
      }
      if (resultDetail) {
        m.toolOutput = m.toolOutput ? `${m.toolOutput}\n${resultDetail}` : resultDetail;
      }
    }
  }
}

export function syncDynamicDevsInLane(lane: TeamLane, devNames: string[]) {
  if (!devNames || devNames.length === 0) return;
  const existingByName = new Map<string, AgentRuntime>();
  for (const a of lane.agents) {
    existingByName.set(a.name.toLowerCase(), a);
  }
  const baseDev = lane.agents.find((a) => a.role === "developer");
  const baseDevModel = baseDev?.model || "agy";

  const beforeQA: AgentRuntime[] = [];
  const qaAndAfter: AgentRuntime[] = [];
  for (const a of lane.agents) {
    if (a.role === "developer") continue;
    if (a.role === "qa") {
      qaAndAfter.push(a);
    } else {
      beforeQA.push(a);
    }
  }

  const nextDevs: AgentRuntime[] = devNames.map((name) => {
    const prev = existingByName.get(name.toLowerCase());
    if (prev) return prev;
    return {
      name,
      role: "developer",
      model: baseDevModel,
      status: "running",
      runningSince: Date.now(),
      inTokens: 0,
      outTokens: 0,
      tokens: 0,
      calls: 0,
    };
  });

  lane.agents = [...beforeQA, ...nextDevs, ...qaAndAfter];
}

export function updateAgentStatus(lane: TeamLane, author: string, label: string | undefined, status: AgentRuntime["status"], tool?: string) {
  const ag = matchAgentRuntime(lane.agents, author, label);
  if (ag) updateAgentRuntimeStatus(ag, status, tool);
}

export function confirmDeleteSavedTeam(teamName: string) {
  if (!state.project) return;
  const matchingLanes = state.lanes.filter((l) => l.teamName === teamName);
  const runningCount = matchingLanes.filter((l) => l.running).length;
  askConfirm({
    title: `Excluir equipe "${teamName}"`,
    body: runningCount > 0
      ? `Excluir a equipe "${teamName}" de .hivemind/team.yaml vai parar todos os serviços e agentes em execução (${runningCount} aba(s) ativa(s)) dessa equipe. Deseja continuar?`
      : `Excluir a equipe "${teamName}" de .hivemind/team.yaml vai encerrar os serviços vinculados e removê-la do projeto. Deseja continuar?`,
    confirmLabel: "Excluir e Parar Serviços",
    danger: true,
    onConfirm: async () => {
      const backend = api();
      if (!backend || !state.project) return;
      try {
        for (const l of matchingLanes) {
          await backend.CancelRun(l.id);
          l.running = false;
        }
        const cfg = await backend.DeleteTeam(state.project.root, teamName);
        state.project.teamConfig = cfg;
        state.project.hasTeam = (cfg.teams?.length || 0) > 0;
        const fallbackTeam = cfg.active || cfg.teams?.[0]?.name || "principal";
        const remaining = state.lanes.filter((l) => l.teamName !== teamName);
        if (remaining.length > 0) {
          state.lanes = remaining;
        } else {
          state.lanes = [createLane(fallbackTeam)];
        }
        if (!state.lanes.some((l) => l.id === state.activeLaneId)) {
          state.activeLaneId = state.lanes[0].id;
        }
        state.teamConfigDraft = buildDraftFromProject();
        renderLaneBarOnly();
        renderPipelineBar();
        renderTokenBar();
        rebuildChatMessagesFull();
        syncComposerButtons();
        updateStatusBar();
        updateMissionBoardIfOpen();
        if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) renderEditorBody();
        if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
        toast(`Equipe "${teamName}" excluída e serviços encerrados.`, "success");
        setStatus(`Equipe "${teamName}" excluída.`);
      } catch (err: any) {
        toastError(`excluir a equipe "${teamName}"`, err);
      }
    },
  });
}

export function confirmCloseParallelLane(laneId: string) {
  const target = state.lanes.find((l) => l.id === laneId);
  if (!target) return;
  const targetLabel = laneDisplayName(target);
  const isOnlyLane = state.lanes.length <= 1;
  askConfirm({
    title: isOnlyLane ? `Encerrar e limpar equipe "${targetLabel}"` : `Excluir equipe paralela "${targetLabel}"`,
    body: target.running
      ? `Esta equipe está executando tarefas agora. Excluir vai parar imediatamente todos os serviços e agentes desta aba. Deseja continuar?`
      : isOnlyLane
        ? `Isso vai parar qualquer serviço em andamento desta equipe e zerar a conversa atual. Deseja continuar?`
        : `Excluir esta aba de equipe paralela vai encerrar seus serviços e removê-la da barra. Deseja continuar?`,
    confirmLabel: "Excluir e Parar Serviços",
    danger: true,
    onConfirm: async () => {
      try {
        await api()?.CancelRun(target.id);
      } catch {
        // Ignore if already finished
      }
      target.running = false;
      if (state.lanes.length > 1) {
        const idx = state.lanes.findIndex((l) => l.id === laneId);
        state.lanes = state.lanes.filter((l) => l.id !== laneId);
        if (state.activeLaneId === laneId) {
          const nextIdx = Math.min(idx, state.lanes.length - 1);
          state.activeLaneId = state.lanes[nextIdx].id;
        }
      } else {
        const fresh = createLane(target.teamName);
        state.lanes = [fresh];
        state.activeLaneId = fresh.id;
      }
      renderLaneBarOnly();
      renderPipelineBar();
      renderTokenBar();
      rebuildChatMessagesFull();
      syncComposerButtons();
      updateStatusBar();
      updateMissionBoardIfOpen();
      if ((state.sidebarView === "team" || state.sidebarView === "sessions") && state.showLeftSidebar) {
        renderLeftSidebar();
      }
      toast(`Serviços da equipe "${targetLabel}" parados e aba excluída.`, "info");
      setStatus(`Equipe "${targetLabel}" excluída.`);
    },
  });
}

export function startRenameLane(laneId: string) {
  if (!state.lanes.some((l) => l.id === laneId)) return;
  editingLaneId = laneId;
  renderLaneBarOnly();
  const input = document.getElementById("lane-rename-input") as HTMLInputElement | null;
  if (input) {
    input.focus();
    input.select();
  }
}

export function applyLaneLabel(lane: TeamLane, rawValue: string): void {
  const trimmed = rawValue.trim();
  lane.label = trimmed && trimmed !== lane.teamName ? trimmed : undefined;
}

export function commitRenameLane(laneId: string, rawValue: string) {
  const target = state.lanes.find((l) => l.id === laneId);
  editingLaneId = null;
  if (target) {
    applyLaneLabel(target, rawValue);
  }
  renderLaneBarOnly();
  updateStatusBar();
  updateMissionBoardIfOpen();
}

export function renderLaneBarOnly() {
  const bar = document.getElementById("lane-bar");
  const lane = activeLane();
  if (bar) {
    bar.innerHTML = state.lanes.map((l) => {
      const display = laneDisplayName(l);
      const isEditing = editingLaneId === l.id;
      return `
      <div class="lane-pill ${l.id === lane?.id ? "active" : ""} ${isEditing ? "editing" : ""}" data-lane="${l.id}" role="tab" aria-selected="${l.id === lane?.id ? "true" : "false"}">
        ${l.running ? `<span class="lane-dot"></span>` : ""}
        ${isEditing
          ? `<input type="text" class="lane-rename-input" id="lane-rename-input" value="${escapeHtml(display)}" maxlength="40" aria-label="Novo nome da aba" />`
          : `<span class="lane-pill-name" data-dbl-rename="${l.id}" title="Duplo clique para renomear (${escapeHtml(l.teamName)})">${escapeHtml(display)}</span>`
        }
        ${!isEditing && l.sessionTotalTokens > 0 ? `<span class="dim" style="font-size:10px">${formatTokens(l.sessionTotalTokens)}</span>` : ""}
        ${!isEditing ? `<button type="button" class="lane-edit-btn" data-rename-lane="${l.id}" title="Renomear aba desta equipe" aria-label="Renomear ${escapeHtml(display)}">${icons.editPen}</button>` : ""}
        <button type="button" class="lane-close-x" data-close-lane="${l.id}" title="Excluir equipe e parar todos os serviços" aria-label="Excluir equipe ${escapeHtml(display)}">${icons.closeSmall}</button>
      </div>
    `;
    }).join("");

    if (editingLaneId) {
      const activeEditId = editingLaneId;
      const input = document.getElementById("lane-rename-input") as HTMLInputElement | null;
      let finished = false;
      const finish = (save: boolean) => {
        if (finished) return;
        finished = true;
        if (save) {
          commitRenameLane(activeEditId, input?.value ?? "");
        } else {
          editingLaneId = null;
          renderLaneBarOnly();
        }
      };
      input?.addEventListener("click", (ev) => ev.stopPropagation());
      input?.addEventListener("keydown", (ev) => {
        if (ev.key === "Enter") {
          ev.preventDefault();
          ev.stopPropagation();
          finish(true);
        } else if (ev.key === "Escape") {
          ev.preventDefault();
          ev.stopPropagation();
          finish(false);
        }
      });
      input?.addEventListener("blur", () => finish(true));
    }

    bar.querySelectorAll("[data-lane]").forEach((el) => {
      el.addEventListener("click", (ev) => {
        const tgt = ev.target as HTMLElement | null;
        if (tgt?.closest("[data-close-lane]") || tgt?.closest("[data-rename-lane]") || tgt?.closest("#lane-rename-input")) return;
        const laneID = (el as HTMLElement).dataset.lane!;
        if (laneID === "lane-autoreview") {
          state.gitTab = "flow";
          openSidebarView("git");
          return;
        }
        state.activeLaneId = laneID;
        renderLaneBarOnly();
        renderPipelineBar();
        renderTokenBar();
        rebuildChatMessagesFull();
        syncComposerButtons();
        updateStatusBar();
        updateMissionBoardIfOpen();
        if ((state.sidebarView === "team" || state.sidebarView === "sessions") && state.showLeftSidebar) {
          renderLeftSidebar();
        }
      });
    });
    bar.querySelectorAll("[data-dbl-rename]").forEach((el) => {
      el.addEventListener("dblclick", (ev) => {
        ev.stopPropagation();
        const id = (el as HTMLElement).dataset.dblRename!;
        startRenameLane(id);
      });
    });
    bar.querySelectorAll("[data-rename-lane]").forEach((btn) => {
      btn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        const id = (btn as HTMLElement).dataset.renameLane!;
        startRenameLane(id);
      });
    });
    bar.querySelectorAll("[data-close-lane]").forEach((btn) => {
      btn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        const id = (btn as HTMLElement).dataset.closeLane!;
        confirmCloseParallelLane(id);
      });
    });
  }
  const teamLbl = document.getElementById("composer-team-label");
  if (teamLbl && lane) {
    const custom = (lane.label || "").trim();
    teamLbl.textContent = custom ? `${custom} (${lane.teamName})` : lane.teamName;
  }
}

export function renderPipelineBar() {
  const bar = document.getElementById("pipeline-bar");
  const lane = activeLane();
  if (!bar) return;
  const steps: Array<{ id: "chief" | "dev" | "qa" | "closing"; name: string }> = [
    { id: "chief", name: "Chefe" },
    { id: "dev", name: "Dev" },
    { id: "qa", name: "QA" },
    { id: "closing", name: "Fecho" },
  ];
  bar.innerHTML = steps
    .map((s, idx) => {
      const isAct = lane?.running && lane.stage === s.id;
      const isDone = lane?.doneStages?.includes(s.id) && !isAct;
      const cls = isAct ? "active" : isDone ? "done" : "";
      const iconHtml = isAct ? icons.spinner : isDone ? icons.check : "";
      return `${idx > 0 ? `<span class="pipe-sep">${icons.arrowRight}</span>` : ""}<span class="pipe-step ${cls}">${iconHtml}<span>${s.name}</span></span>`;
    })
    .join("");
}
