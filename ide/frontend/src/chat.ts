import { icons } from "./icons";
import { renderMarkdownLite } from "./markdown";
import type { ChatMessage, PermissionMode, RunMode } from "./types";
import { chipForAgent, cursorToolPhrase, escapeHtml, exactTokens, formatTokens, modelChipHtml, permissionModeLabel, roleHeading, roleLabelPt, runModeLabel, safeImageSrc, splitAssistantBlocks } from "./utils";
import { refreshSessions } from "./boot";
import { openDiffInEditor, openFileInEditor, promptDiscardChanges, reloadCleanOpenTabs } from "./editor";
import { refreshExplorerTree, renderLeftSidebar, setProjectPermissionMode, updateTeamStatusPills } from "./explorer";
import { refreshDiffs, refreshDiffsAndSummarize } from "./git";
import { activeLane, buildAgentRuntimes, createLane, ensureThinkingTicker, formatElapsedThinking, markThinkingDone, markToolsDone, renderLaneBarOnly, renderPipelineBar } from "./lanes";
import { renderModalLayer } from "./modals";
import { openSidebarView, renderProjectTabsBar, syncChatExpandLayout, toggleChatExpanded, updateStatusBar } from "./shell";
import { api, setStatus, state, toastError } from "./state";
import { openTeamMemoryFile, updateMissionBoardIfOpen } from "./teamconfig";

export let chatFlushRaf = 0;

export let mentionTimer = 0;

const laneIdleWaiters = new Map<string, Array<() => void>>();
const laneRedirecting = new Set<string>();

function notifyLaneIdle(laneId: string) {
  const list = laneIdleWaiters.get(laneId);
  if (!list || list.length === 0) return;
  laneIdleWaiters.delete(laneId);
  for (const resolve of list) {
    try {
      resolve();
    } catch {
      // ignore
    }
  }
}

function waitForLaneIdle(laneId: string, timeoutMs = 3000): Promise<void> {
  return new Promise((resolve) => {
    const lane = state.lanes.find((l) => l.id === laneId);
    if (!lane || !lane.running) {
      resolve();
      return;
    }
    let done = false;
    const finish = () => {
      if (done) return;
      done = true;
      window.clearTimeout(timer);
      resolve();
    };
    const timer = window.setTimeout(finish, timeoutMs);
    const list = laneIdleWaiters.get(laneId) || [];
    list.push(finish);
    laneIdleWaiters.set(laneId, list);
  });
}

export function renderRightPane() {
  const right = document.getElementById("right-pane");
  if (!right) return;
  const lane = activeLane();

  right.innerHTML = `
    <div class="sidebar-header">
      <span class="eyebrow">${icons.sparkle}<span>Equipe IA</span></span>
      <div class="spacer"></div>
      <button type="button" class="icon-btn" id="btn-expand-chat" title="Expandir a Equipe IA no centro quando o editor estiver vazio (Ctrl+Shift+E)" aria-pressed="${state.chatExpandedPref ? "true" : "false"}" aria-label="${state.chatExpandedPref ? "Recolher Equipe IA" : "Expandir Equipe IA"}">${icons.expand}</button>
      <button class="mini ghost" id="btn-new-session" title="Começar uma nova conversa limpa">Nova conversa</button>
      <button class="mini" id="btn-add-lane" title="Abrir uma segunda equipe trabalhando em paralelo">${icons.plus}<span>Paralela</span></button>
    </div>
    <div class="lane-bar" id="lane-bar"></div>
    <div class="pipeline-bar" id="pipeline-bar"></div>
    <div class="token-bar" id="token-bar"></div>
    <div class="chat-scroll-wrap">
      <div class="chat-messages" id="chat-messages"></div>
      <button type="button" class="chat-scroll-bottom-btn" id="btn-chat-scroll-bottom" title="Ir para a última mensagem" aria-label="Ir para o final" style="display:none">${icons.arrowDown}</button>
    </div>
    <div class="composer-wrap">
      <div class="mention-popup" id="mention-popup" style="display:none"></div>
      <div class="composer-box">
        <div class="composer-images" id="composer-images" style="display:none"></div>
        <textarea id="chat-input" rows="3" placeholder="Descreva o que construir ou resolver… (@arquivo · Ctrl+V print · Ctrl+L seleção · /ajuda)"></textarea>
        <div class="composer-footer">
          <div class="composer-mode-bar" id="composer-mode-bar"></div>
          <input type="file" id="chat-image-input" accept="image/*" multiple style="display:none" />
          <button type="button" class="composer-icon-btn" id="btn-attach-image" title="Anexar foto/print (ou cole com Ctrl+V)" aria-label="Anexar imagem">${icons.image}</button>
          <input id="jira-key-input" class="composer-jira-input" placeholder="Jira (PROJ-1)" value="${escapeHtml(state.jiraKeyInput)}" title="Vincular chave de issue Jira (opcional)" />
          <span class="dim composer-lane-tag" id="composer-team-label"></span>
          <div class="spacer"></div>
          <button type="button" class="danger mini composer-stop-btn" id="btn-cancel-run" ${lane?.running ? "" : "disabled"} style="${lane?.running ? "" : "display:none"}" title="Interromper execução atual">${icons.stop}<span>Parar</span></button>
          <button type="button" class="primary composer-send" id="btn-send-run" title="${lane?.running ? "Interrompe a execução atual imediatamente e envia sua nova instrução" : "Enviar tarefa para a equipe (Enter)"}">${icons.send}<span>${lane?.running ? "Redirecionar" : "Enviar"}</span></button>
        </div>
      </div>
    </div>
  `;

  const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
  if (chatInput) chatInput.value = state.draftObjective;

  renderComposerModeBar();
  renderLaneBarOnly();
  renderPipelineBar();
  renderTokenBar();
  renderComposerImages();
  rebuildChatMessagesFull();
  bindRightStaticEvents();
  syncChatExpandLayout();
}

export function renderComposerModeBar() {
  const bar = document.getElementById("composer-mode-bar");
  if (!bar) return;
  const perm = (state.project?.teamConfig?.settings?.permission_mode || "governed") as PermissionMode;

  const execModes: Array<{ id: RunMode; label: string; shortLabel: string; tag: string; icon: string; desc: string }> = [
    {
      id: "auto",
      label: "Auto",
      shortLabel: "Auto",
      tag: "Triagem",
      icon: icons.sparkle,
      desc: "Decide sozinho: responde direto ou aciona o Squad em worktree isolada",
    },
    {
      id: "direct",
      label: "Direto",
      shortLabel: "Direto",
      tag: "1 Agente",
      icon: icons.bolt,
      desc: "Resposta direta do Chefe sem delegar — rápido e econômico em tokens",
    },
    {
      id: "squad",
      label: "Squad",
      shortLabel: "Squad",
      tag: "Paralelo",
      icon: icons.swarm,
      desc: "Pipeline completo em worktree: Chefe → Devs paralelos → QA → Fecho",
    },
  ];

  const planModes: Array<{ id: RunMode; label: string; shortLabel: string; tag: string; icon: string; desc: string }> = [
    {
      id: "plan",
      label: "Planejar · Feature / Task",
      shortLabel: "Planejar",
      tag: "Gate",
      icon: icons.plan,
      desc: "Plano arquitetural + fatiamento em projeto existente com aprovação humana",
    },
    {
      id: "plan:sdd",
      label: "Planejar · Projeto do Zero",
      shortLabel: "Planejar · SDD",
      tag: "SDD",
      icon: icons.plan,
      desc: "Entrevista → Docs & Arquitetura → Jira → Squad travado por gates",
    },
  ];

  const allModes = [...execModes, ...planModes];
  const curMode = allModes.find((m) => m.id === state.runMode) || execModes[0];
  const modeOpen = state.composerMenuOpen === "mode";
  const permOpen = state.composerMenuOpen === "perm";

  const permOptions: Array<{ id: PermissionMode; label: string; dotClass: string; desc: string }> = [
    {
      id: "governed",
      label: "Governado",
      dotClass: "perm-dot-governed",
      desc: "Pede aprovação antes de gravar arquivos ou executar comandos",
    },
    {
      id: "auto_edit",
      label: "Auto-Edição",
      dotClass: "perm-dot-auto",
      desc: "Edita arquivos automaticamente; pede confirmação para comandos",
    },
    {
      id: "full_access",
      label: "Full Acesso",
      dotClass: "perm-dot-full",
      desc: "Autonomia total no workspace para fluxos contínuos sem pausas",
    },
  ];
  const curPerm = permOptions.find((p) => p.id === perm) || permOptions[0];

  const renderModeOption = (m: (typeof allModes)[number]) => {
    const active = state.runMode === m.id;
    return `
      <button type="button" class="composer-popover-item ${active ? "active" : ""}" data-run-mode="${m.id}">
        <span class="composer-popover-icon">${m.icon}</span>
        <span class="composer-popover-body">
          <span class="composer-popover-title-row">
            <strong>${escapeHtml(m.label)}</strong>
            <span class="composer-popover-tag">${escapeHtml(m.tag)}</span>
          </span>
          <span class="composer-popover-desc">${escapeHtml(m.desc)}</span>
        </span>
        <span class="composer-popover-check">${active ? icons.check : ""}</span>
      </button>
    `;
  };

  bar.innerHTML = `
    <div class="composer-select-wrap">
      <button type="button" class="composer-select-trigger ${modeOpen ? "open" : ""} ${state.runMode.startsWith("plan") ? "is-plan" : ""}" id="btn-composer-mode-select" title="Escolher modo de execução ou planejamento">
        <span class="composer-trigger-icon">${curMode.icon}</span>
        <span class="composer-trigger-label">${escapeHtml(curMode.shortLabel)}</span>
        <span class="composer-trigger-caret">${icons.chevronDown}</span>
      </button>
      ${modeOpen ? `
        <div class="composer-popover composer-popover-mode" id="composer-mode-popover" role="listbox">
          <div class="composer-popover-section">Modo de execução</div>
          ${execModes.map(renderModeOption).join("")}
          <div class="composer-popover-divider"></div>
          <div class="composer-popover-section">Planejamento com aprovação</div>
          ${planModes.map(renderModeOption).join("")}
        </div>
      ` : ""}
    </div>

    <div class="composer-select-wrap">
      <button type="button" class="composer-select-trigger ${permOpen ? "open" : ""}" id="btn-composer-perm-select" title="Escolher nível de permissão e autonomia">
        <span class="perm-dot ${curPerm.dotClass}"></span>
        <span class="composer-trigger-label">${escapeHtml(curPerm.label)}</span>
        <span class="composer-trigger-caret">${icons.chevronDown}</span>
      </button>
      ${permOpen ? `
        <div class="composer-popover composer-popover-perm" id="composer-perm-popover" role="listbox">
          <div class="composer-popover-section">Autonomia da equipe</div>
          ${permOptions.map((p) => {
            const active = perm === p.id;
            return `
              <button type="button" class="composer-popover-item ${active ? "active" : ""}" data-perm-mode="${p.id}">
                <span class="composer-popover-icon"><span class="perm-dot ${p.dotClass}"></span></span>
                <span class="composer-popover-body">
                  <span class="composer-popover-title-row">
                    <strong>${escapeHtml(p.label)}</strong>
                  </span>
                  <span class="composer-popover-desc">${escapeHtml(p.desc)}</span>
                </span>
                <span class="composer-popover-check">${active ? icons.check : ""}</span>
              </button>
            `;
          }).join("")}
          ${state.cursorCLI?.sandboxWarning ? `<div class="composer-popover-desc" style="padding:8px 12px 10px">${escapeHtml(state.cursorCLI.sandboxWarning)}</div>` : ""}
        </div>
      ` : ""}
    </div>

    <button type="button" class="composer-icon-btn" id="btn-composer-memory" title="Abrir Memória Compartilhada (.hivemind/MEMORY.md)" aria-label="Memória compartilhada">
      ${icons.memory}
    </button>
  `;

  bar.querySelector("#btn-composer-mode-select")?.addEventListener("click", (e) => {
    e.stopPropagation();
    state.composerMenuOpen = state.composerMenuOpen === "mode" ? "" : "mode";
    state.planMenuOpen = false;
    renderComposerModeBar();
  });

  bar.querySelector("#btn-composer-perm-select")?.addEventListener("click", (e) => {
    e.stopPropagation();
    state.composerMenuOpen = state.composerMenuOpen === "perm" ? "" : "perm";
    state.planMenuOpen = false;
    renderComposerModeBar();
  });

  bar.querySelectorAll("[data-run-mode]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      state.runMode = (btn as HTMLElement).dataset.runMode as RunMode;
      state.composerMenuOpen = "";
      state.planMenuOpen = false;
      renderComposerModeBar();
      updateStatusBar();
      updateMissionBoardIfOpen();
    });
  });

  bar.querySelectorAll("[data-perm-mode]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const next = (btn as HTMLElement).dataset.permMode as PermissionMode;
      state.composerMenuOpen = "";
      setProjectPermissionMode(next);
      renderComposerModeBar();
    });
  });

  bar.querySelector("#btn-composer-memory")?.addEventListener("click", () => openTeamMemoryFile());
}

export function renderTokenBar() {
  const bar = document.getElementById("token-bar");
  const lane = activeLane();
  if (!bar || !lane) return;
  const maxTok = state.project?.teamConfig?.settings?.max_tokens || 0;
  const warnBudget = maxTok > 0 && lane.turnTotalTokens >= maxTok * 0.8;

  bar.innerHTML = `
    <span class="tok-pill" title="Turno atual — Entrada: ${exactTokens(lane.turnInTokens)} | Saída: ${exactTokens(lane.turnOutTokens)}">
      ${icons.bolt}
      <span>Turno: ${formatTokens(lane.turnTotalTokens)}</span>
      <span class="dim">(↑${formatTokens(lane.turnInTokens)} ↓${formatTokens(lane.turnOutTokens)})</span>
    </span>
    <span class="tok-pill" title="Total desta sessão (${escapeHtml(lane.sessionId || "nova")}) — Entrada: ${exactTokens(lane.sessionInTokens)} | Saída: ${exactTokens(lane.sessionOutTokens)}">
      <span>Sessão: ${formatTokens(lane.sessionTotalTokens)} tok</span>
    </span>
    ${maxTok > 0 ? `<span class="tok-pill ${warnBudget ? "warn" : ""}" title="Limite configurado em .hivemind/team.yaml">Limite: ${formatTokens(lane.turnTotalTokens)}/${formatTokens(maxTok)}</span>` : ""}
    <div class="spacer"></div>
    <button class="ghost mini" id="btn-open-sessions-bar" style="padding:1px 6px;font-size:11px" title="Ver todas as sessões salvas e total do projeto">Histórico</button>
  `;
  bar.querySelector("#btn-open-sessions-bar")?.addEventListener("click", () => {
    openSidebarView("sessions");
    refreshSessions();
  });
}

export function renderComposerImages() {
  const wrap = document.getElementById("composer-images");
  if (!wrap) return;
  if (state.draftImages.length === 0) {
    wrap.style.display = "none";
    wrap.innerHTML = "";
    return;
  }
  wrap.style.display = "flex";
  wrap.innerHTML = state.draftImages
    .map((img) => {
      const safeSrc = safeImageSrc(img.dataBase64);
      if (!safeSrc) return "";
      return `
        <div class="img-thumb-wrap" title="${escapeHtml(img.name)}">
          <img src="${escapeHtml(safeSrc)}" data-preview-img="${escapeHtml(safeSrc)}" alt="${escapeHtml(img.name)}" />
          <button class="img-thumb-remove" data-remove-img="${escapeHtml(img.id)}" title="Remover imagem" aria-label="Remover imagem">${icons.closeSmall}</button>
        </div>
      `;
    })
    .join("");
  wrap.querySelectorAll("[data-remove-img]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = (btn as HTMLElement).dataset.removeImg!;
      state.draftImages = state.draftImages.filter((x) => x.id !== id);
      renderComposerImages();
    });
  });
  wrap.querySelectorAll("[data-preview-img]").forEach((imgEl) => {
    imgEl.addEventListener("click", () => {
      state.imagePreviewUrl = (imgEl as HTMLElement).dataset.previewImg || "";
      renderModalLayer();
    });
  });
}

export function attachImageFiles(files: FileList | File[]) {
  const list = Array.from(files).filter((f) => f.type.startsWith("image/"));
  if (list.length === 0) return;
  for (const file of list) {
    if (state.draftImages.length >= 8) break;
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === "string") {
        state.draftImages.push({
          id: `img-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
          name: file.name || "print.png",
          mimeType: file.type || "image/png",
          dataBase64: reader.result,
        });
        renderComposerImages();
      }
    };
    reader.readAsDataURL(file);
  }
}

export function messageDigest(m: ChatMessage): string {
  return `${m.kind}|${m.active ? 1 : 0}|${m.solo ? 1 : 0}|${m.approved ? 1 : 0}|${m.resolved || ""}|${m.label}|${m.modelLabel || ""}|${m.modelFullId || ""}|${m.tokens || 0}|${m.durationMs || 0}|${m.stepCount || 0}|${m.toolDetail?.length || 0}|${m.toolOutput?.length || 0}|${m.images?.length || 0}|${m.diffItems?.length || 0}|${m.text.length}|${m.text.slice(-16)}`;
}

function chiefBadgeHtml(m: ChatMessage): string {
  const lane = activeLane();
  const ag = lane?.agents.find((a) => m.label === a.name || m.label.endsWith(`· ${a.name}`));
  const role = ag ? roleLabelPt(ag.role) : roleHeading(m.label);
  const chip = m.modelLabel
    ? modelChipHtml({ label: m.modelLabel, providerLabel: m.modelProvider || "", fullId: m.modelFullId || "" })
    : ag
      ? modelChipHtml(chipForAgent(ag))
      : "";
  return `<span class="msg-badge badge-chief">${escapeHtml(role)}${chip ? ` ${chip}` : ""}</span>`;
}

function renderToolGroup(tools: Array<{ name: string; target: string }>, open: boolean): string {
  const rows = tools.map((t) => {
    const phrase = cursorToolPhrase(t.name, t.target);
    return `<div class="tool-minimal-row stream-tool-row"><span class="tool-step-verb">${escapeHtml(phrase.verb)}</span><span class="tool-step-target">${escapeHtml(phrase.target)}</span></div>`;
  }).join("");
  if (tools.length < 2) return `<div class="stream-tool-group">${rows}</div>`;
  return `<details class="stream-tool-group" ${open ? "open" : ""}><summary class="stream-tool-summary">${tools.length} ações</summary>${rows}</details>`;
}

function renderAssistantBody(text: string, streamOpen: boolean): string {
  const blocks = splitAssistantBlocks(text);
  return blocks.map((b, i) => {
    if (b.kind === "md") return `<div class="md-prose">${renderMarkdownLite(b.text)}</div>`;
    return renderToolGroup(b.tools, streamOpen && i === blocks.length - 1);
  }).join("");
}

export function createMessageElement(m: ChatMessage): HTMLElement {
  const div = document.createElement("div");
  div.className = `msg-card role-${m.kind}`;
  div.dataset.msgId = m.id;
  updateMessageElement(div, m);
  return div;
}

export function formatToolDuration(ms?: number): string {
  if (!ms || ms <= 0) return "";
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

export function toolLangBadge(fileName: string): { label: string; cls: string } {
  const ext = (fileName.split(".").pop() || "").toLowerCase();
  switch (ext) {
    case "go": return { label: "GO", cls: "lang-go" };
    case "ts": case "mts": return { label: "TS", cls: "lang-ts" };
    case "tsx": return { label: "TSX", cls: "lang-ts" };
    case "js": case "mjs": case "cjs": return { label: "JS", cls: "lang-js" };
    case "jsx": return { label: "JSX", cls: "lang-js" };
    case "css": case "scss": case "less": return { label: "CSS", cls: "lang-css" };
    case "html": case "htm": return { label: "HTML", cls: "lang-html" };
    case "json": return { label: "JSON", cls: "lang-json" };
    case "md": case "mdx": return { label: "MD", cls: "lang-md" };
    case "yaml": case "yml": return { label: "YML", cls: "lang-yaml" };
    case "py": return { label: "PY", cls: "lang-py" };
    case "rs": return { label: "RS", cls: "lang-rs" };
    case "sql": return { label: "SQL", cls: "lang-sql" };
    case "sh": case "bash": case "ps1": return { label: "SH", cls: "lang-sh" };
    case "java": case "kt": return { label: "JVM", cls: "lang-html" };
    case "cs": return { label: "C#", cls: "lang-css" };
    default: return { label: ext ? ext.slice(0, 4).toUpperCase() : "TXT", cls: "lang-default" };
  }
}

export interface MinimalToolRowSpec {
  verb: string;
  filePath?: string;
  fileName?: string;
  fileSub?: string;
  targetText?: string;
  expandChevron?: boolean;
}

export function formatMinimalToolStep(m: ChatMessage): MinimalToolRowSpec {
  const name = (m.toolName || m.text.split(/\s+/)[0] || "").toLowerCase();
  const rawArg = (m.toolDetail || "").split("\n")[0].trim();
  const active = Boolean(m.active);
  const countMatch = m.text.match(/(×\d+)$/);
  const countSuffix = countMatch ? ` ${countMatch[1]}` : "";

  const splitFileTarget = (p: string): { filePath: string; fileName: string; fileSub: string } => {
    const clean = p.replace(/\\/g, "/").replace(/^\.\//, "");
    const lineMatch = clean.match(/^(.+?)(#L[\d-]+|:\d+(?:-\d+)?)$/);
    const realPath = lineMatch ? lineMatch[1] : clean;
    const linePart = lineMatch ? (lineMatch[2].startsWith("#") ? lineMatch[2] : `#L${lineMatch[2].slice(1)}`) : "";
    const parts = realPath.split("/");
    const fileName = parts.pop() || realPath;
    const dirPart = parts.join("/");
    const fileSub = linePart || (dirPart ? dirPart : "");
    return { filePath: realPath, fileName, fileSub };
  };

  if (name === "read_file" && rawArg) {
    const f = splitFileTarget(rawArg);
    return { verb: active ? "Analisando" : "Analisou", ...f, expandChevron: false };
  }
  if (name === "edit_file" && rawArg) {
    const f = splitFileTarget(rawArg);
    return { verb: active ? "Editando" : "Editou", ...f, expandChevron: false };
  }
  if (name === "write_file" && rawArg) {
    const f = splitFileTarget(rawArg);
    return { verb: active ? "Gravando" : "Gravou", ...f, expandChevron: false };
  }
  if (name === "list_dir") {
    return {
      verb: active ? "Listando" : "Listou",
      targetText: `${rawArg || "."}/${countSuffix}`.replace("//", "/"),
      expandChevron: true,
    };
  }
  if (name === "search_text") {
    return {
      verb: active ? "Buscando" : "Buscou",
      targetText: `${rawArg || "código"}${countSuffix}`,
      expandChevron: true,
    };
  }
  if (name === "find_files") {
    return {
      verb: active ? "Localizando" : "Localizou",
      targetText: `${rawArg || "arquivos"}${countSuffix}`,
      expandChevron: true,
    };
  }
  if (name === "run_shell") {
    return {
      verb: active ? "Executando" : "Executou",
      targetText: `${rawArg || "comando"}${countSuffix}`,
      expandChevron: true,
    };
  }
  if (name === "git_status") return { verb: active ? "Verificando" : "Verificou", targetText: `git status${countSuffix}`, expandChevron: true };
  if (name === "git_diff") return { verb: active ? "Analisando" : "Analisou", targetText: `git diff${countSuffix}`, expandChevron: true };
  if (name === "git_commit") return { verb: active ? "Commitando" : "Commitou", targetText: rawArg || "alterações", expandChevron: true };
  if (name === "git_push") return { verb: active ? "Enviando" : "Enviou", targetText: `git push${countSuffix}`, expandChevron: true };
  if (name === "git_pull") return { verb: active ? "Atualizando" : "Atualizou", targetText: `git pull${countSuffix}`, expandChevron: true };

  if (name === "github_get_pr") return { verb: active ? "Inspecionando" : "Inspecionou", targetText: `PR ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "github_issue_comments") return { verb: active ? "Lendo" : "Leu", targetText: `comentários ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "github_pr_files") return { verb: active ? "Listando" : "Listou", targetText: `arquivos do PR ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "github_pr_diff") return { verb: active ? "Analisando" : "Analisou", targetText: `diff do PR ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "github_list_prs") return { verb: active ? "Listando" : "Listou", targetText: "Pull Requests", expandChevron: true };
  if (name === "github_get_issue") return { verb: active ? "Lendo" : "Leu", targetText: `Issue ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "github_comment_pr") return { verb: active ? "Comentando" : "Comentou", targetText: `PR ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "github_reply_review_comment") return { verb: active ? "Respondendo" : "Respondeu", targetText: `thread no PR ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "github_review_pr") return { verb: active ? "Revisando" : "Revisou", targetText: `PR ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "github_request_review") return { verb: active ? "Solicitando" : "Solicitou", targetText: `revisão no PR ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "github_merge_pr") return { verb: active ? "Mesclando" : "Mesclou", targetText: `PR ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "github_create_pr") return { verb: active ? "Abrindo" : "Abriu", targetText: `PR ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "github_ci_status") return { verb: active ? "Verificando" : "Verificou", targetText: `CI ${rawArg || ""}`.trim(), expandChevron: true };

  if (name === "jira_get") return { verb: active ? "Consultando" : "Consultou", targetText: `Jira ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "jira_search") return { verb: active ? "Buscando" : "Buscou", targetText: `Jira ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "jira_comment") return { verb: active ? "Comentando" : "Comentou", targetText: `Jira ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "jira_transition") return { verb: active ? "Movendo" : "Moveu", targetText: `Jira ${rawArg || ""}`.trim(), expandChevron: true };
  if (name === "delegate_to_squad") return { verb: active ? "Acionando" : "Acionou", targetText: "equipe paralela", expandChevron: true };
  if (name === "qa_approve") return { verb: "Aprovou", targetText: rawArg || "validação de QA", expandChevron: true };
  if (name === "qa_request_changes") return { verb: "Pediu ajustes", targetText: rawArg || "QA → Devs", expandChevron: true };

  return {
    verb: active ? "Executando" : "Executou",
    targetText: rawArg ? `${m.text} · ${rawArg}` : m.text,
    expandChevron: true,
  };
}

export function updateMessageElement(el: HTMLElement, m: ChatMessage) {
  const digest = messageDigest(m);
  if (el.dataset.digest === digest) return;
  el.dataset.digest = digest;

  if (m.kind === "thinking") {
    el.className = "msg-card role-thinking";
    const wasOpen = el.querySelector("details")?.hasAttribute("open") ?? false;
    const elapsedMs = m.active && m.startedAt
      ? Math.max(0, Date.now() - m.startedAt)
      : (m.durationMs || 0);
    const elapsedText = formatElapsedThinking(elapsedMs);
    const titleWord = m.active ? "Pensando" : "Pensou";
    const thoughtBody = m.text.trim()
      ? renderMarkdownLite(m.text)
      : `<div class="dim" style="font-size:11.5px">${m.active
          ? "Analisando contexto, arquivos e ferramentas disponíveis…"
          : `Concluído em ${escapeHtml(elapsedText)}${m.stepCount ? ` · ${m.stepCount} ação${m.stepCount === 1 ? "" : "es"}` : ""}.`
        }</div>`;
    el.innerHTML = `
      <details class="agent-thought-pill ${m.active ? "is-active" : ""}" ${wasOpen ? "open" : ""}>
        <summary class="agent-thought-summary">
          <span class="agent-thought-title">${escapeHtml(titleWord)}</span>
          <span class="agent-thought-elapsed" ${m.active && m.startedAt ? `data-thinking-started="${m.startedAt}"` : ""}>${escapeHtml(elapsedText)}</span>
          <span class="agent-thought-chev">${icons.chevronDown}</span>
        </summary>
        <div class="agent-thought-body">${thoughtBody}</div>
      </details>
    `;
    return;
  }

  if (m.kind === "tool") {
    el.className = "msg-card role-tool tool-minimal";
    const spec = formatMinimalToolStep(m);
    const durLabel = formatToolDuration(m.durationMs);
    const hasDrawer = Boolean(m.toolDetail || m.toolOutput);
    const wasOpen = el.querySelector("details")?.hasAttribute("open") ?? false;
    const agentChip = m.solo === false && m.label
      ? `<span class="tool-agent-tag">${escapeHtml(m.label.split("·")[0].trim())}</span>`
      : "";

    let contentHtml = "";
    if (spec.fileName && spec.filePath) {
      const lang = toolLangBadge(spec.fileName);
      contentHtml = `
        ${m.active ? `<span class="tool-step-spin">${icons.spinner}</span>` : ""}
        <span class="tool-step-verb">${escapeHtml(spec.verb)}</span>
        <span class="tool-lang-badge ${lang.cls}">${escapeHtml(lang.label)}</span>
        <button type="button" class="tool-file-link" data-open-chat-file="${escapeHtml(spec.filePath)}" title="Abrir ${escapeHtml(spec.filePath)} no editor">${escapeHtml(spec.fileName)}</button>
        ${spec.fileSub ? `<span class="tool-file-sub">${escapeHtml(spec.fileSub)}</span>` : ""}
        ${agentChip}
      `;
    } else {
      contentHtml = `
        ${m.active ? `<span class="tool-step-spin">${icons.spinner}</span>` : ""}
        <span class="tool-step-verb">${escapeHtml(spec.verb)}</span>
        <span class="tool-step-target">${escapeHtml(spec.targetText || m.text)}</span>
        ${agentChip}
      `;
    }

    if (!hasDrawer) {
      el.innerHTML = `<div class="tool-minimal-row">${contentHtml}${durLabel ? `<span class="tool-minimal-dur">${escapeHtml(durLabel)}</span>` : ""}</div>`;
      return;
    }

    const drawerLines: string[] = [];
    if (m.toolDetail) drawerLines.push(`Args: ${m.toolDetail}`);
    if (m.toolOutput) drawerLines.push(`Saída:\n${m.toolOutput}`);
    el.innerHTML = `
      <details class="tool-minimal-details" ${wasOpen ? "open" : ""}>
        <summary class="tool-minimal-row">
          ${contentHtml}
          ${spec.expandChevron ? `<span class="tool-step-chev">${icons.chevronRight}</span>` : `<span class="tool-step-chev hover-only">${icons.chevronRight}</span>`}
          ${durLabel ? `<span class="tool-minimal-dur">${escapeHtml(durLabel)}</span>` : ""}
        </summary>
        <div class="tool-drawer">${escapeHtml(drawerLines.join("\n\n"))}</div>
      </details>
    `;
    return;
  }

  if (m.kind === "plan") {
    el.className = "msg-card role-plan";
    let actionHtml = "";
    let actionHandler: (() => void) | null = null;
    if (m.gate === "docs") {
      actionHtml = `
        <button class="mini primary" data-approve-plan="${escapeHtml(m.id)}" ${m.approved ? "disabled" : ""}>
          ${m.approved ? icons.check : icons.play}
          <span>${m.approved ? "Docs Aprovados · Jira em andamento" : "Aprovar Docs & Criar Tasks no Jira"}</span>
        </button>`;
      actionHandler = () => {
        if (m.approved) return;
        m.approved = true;
        updateMessageElement(el, m);
        void sendToChief(
          "Gate 1 aprovado: Docs & Arquitetura SDD aprovados. Crie agora o backlog no Jira (Epics, Tasks e Subtasks) espelhando specs/05-tarefas.md, grave as chaves de volta na spec e registre as ondas com delegate_to_squad. Pare no Gate 2.",
          "plan:sdd-jira",
        );
      };
    } else if (m.gate === "jira") {
      actionHtml = `
        <button class="mini primary" data-approve-plan="${escapeHtml(m.id)}" ${m.approved ? "disabled" : ""}>
          ${m.approved ? icons.check : icons.play}
          <span>${m.approved ? "Squad Acionado" : "Aprovar Backlog & Disparar Squad"}</span>
        </button>`;
      actionHandler = () => {
        if (m.approved) return;
        m.approved = true;
        updateMessageElement(el, m);
        executeApprovedPlan();
      };
    } else {
      actionHtml = `
        <button class="mini primary" data-approve-plan="${escapeHtml(m.id)}" ${m.approved ? "disabled" : ""}>
          ${m.approved ? icons.check : icons.play}
          <span>${m.approved ? "Squad Acionado" : "Aprovar & Executar Squad"}</span>
        </button>`;
      actionHandler = () => {
        if (m.approved) return;
        m.approved = true;
        updateMessageElement(el, m);
        executeApprovedPlan();
      };
    }
    el.innerHTML = `
      <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap">
        <span class="msg-badge badge-chief" style="margin-bottom:0">${icons.plan}<span>${escapeHtml(m.label)}</span></span>
        ${actionHtml}
      </div>
      <div class="msg-body">${renderMarkdownLite(m.text)}</div>
    `;
    el.querySelector("[data-approve-plan]")?.addEventListener("click", () => actionHandler?.());
    return;
  }

  if (m.kind === "approval") {
    el.className = "msg-card role-plan";
    const done = !!m.resolved;
    el.innerHTML = `
      <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap">
        <span class="msg-badge badge-chief" style="margin-bottom:0">${icons.bolt}<span>${escapeHtml(m.label)}</span></span>
        <div style="display:flex;gap:6px">
          <button class="mini primary" data-approve-gate="1" ${done ? "disabled" : ""}>
            ${icons.check}
            <span>${m.resolved === "approved" ? "Autorizado" : "Autorizar"}</span>
          </button>
          <button class="mini danger" data-deny-gate="1" ${done ? "disabled" : ""}>
            <span>${m.resolved === "denied" ? "Negado" : "Negar"}</span>
          </button>
        </div>
      </div>
      <div class="msg-body">${escapeHtml(m.text)}</div>
    `;
    const lane = activeLane();
    const targetId = m.approvalId || lane?.id || "";
    el.querySelector("[data-approve-gate]")?.addEventListener("click", async () => {
      if (m.resolved || !targetId) return;
      m.resolved = "approved";
      updateMessageElement(el, m);
      await api()?.RespondApproval(targetId, true);
    });
    el.querySelector("[data-deny-gate]")?.addEventListener("click", async () => {
      if (m.resolved || !targetId) return;
      m.resolved = "denied";
      updateMessageElement(el, m);
      await api()?.RespondApproval(targetId, false);
    });
    return;
  }

  if (m.kind === "handoff") {
    el.className = "handoff-card";
    el.innerHTML = `
      <details open>
        <summary>${icons.team}<span>${escapeHtml(m.label)}</span></summary>
        <div class="handoff-body">${renderMarkdownLite(m.text)}</div>
      </details>
    `;
    return;
  }

  if (m.kind === "diff") {
    const items = m.diffItems || [];
    el.innerHTML = `
      <div style="display:flex;align-items:center;justify-content:space-between;gap:8px">
        <span class="msg-badge badge-tool">${icons.gitSmall}<span>${escapeHtml(m.label)}</span></span>
        <div style="display:flex;gap:5px">
          <button class="mini primary" data-chat-commit="1">${icons.commit}<span>Commitar</span></button>
          <button class="mini danger" data-chat-discard="1">${icons.trash}<span>Descartar</span></button>
        </div>
      </div>
      <div class="diff-summary-files">
        ${items.map((it) => `
          <span class="diff-chip-group">
            <button class="diff-chip-btn" data-chat-review-file="${escapeHtml(it.path)}" title="Abrir ${escapeHtml(it.path)} no editor com revisão bloco a bloco (Aceitar/Rejeitar)">
              <span>${escapeHtml(it.path)}</span>
              <span style="color:var(--green)">+${it.added}</span>
              <span style="color:var(--red)">-${it.removed}</span>
            </button>
            <button class="diff-chip-split" data-chat-diff-file="${escapeHtml(it.path)}" title="Abrir diff lado a lado">Diff</button>
          </span>
        `).join("")}
      </div>
    `;
    el.querySelectorAll("[data-chat-review-file]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const p = (btn as HTMLElement).dataset.chatReviewFile!;
        const name = p.split("/").pop() || p;
        state.inlineReviewEnabled = true;
        void openFileInEditor(p, name);
      });
    });
    el.querySelectorAll("[data-chat-diff-file]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const p = (btn as HTMLElement).dataset.chatDiffFile!;
        const found = state.diffs.find((d) => d.path === p);
        if (found) openDiffInEditor(found);
      });
    });
    el.querySelector("[data-chat-commit]")?.addEventListener("click", async () => {
      if (!state.project) return;
      try {
        const msg = await api()?.CommitChanges(state.project.root, state.commitMsgInput || "feat: entrega HiveMind IDE", false);
        if (msg) setStatus(msg);
        refreshDiffs();
      } catch (err: any) {
        toastError("criar o commit das alterações", err);
      }
    });
    el.querySelector("[data-chat-discard]")?.addEventListener("click", () => {
      promptDiscardChanges();
    });
    return;
  }

  const laneNow = activeLane();
  const streamOpen = Boolean(laneNow?.running && laneNow.messages[laneNow.messages.length - 1]?.id === m.id);
  const bodyHtml = m.kind === "chief"
    ? renderAssistantBody(m.text, streamOpen)
    : m.kind === "system"
      ? renderMarkdownLite(m.text)
      : escapeHtml(m.text);
  const badgeHtml = m.kind === "chief"
    ? chiefBadgeHtml(m)
    : `<span class="msg-badge badge-${m.kind}">${escapeHtml(m.label)}</span>`;
  const validImages = (m.images || []).map(safeImageSrc).filter(Boolean);
  const imgsHtml = validImages.length > 0
    ? `<div class="msg-images">${validImages.map((src) => `<img src="${escapeHtml(src)}" data-preview-img="${escapeHtml(src)}" alt="anexo" />`).join("")}</div>`
    : "";
  const tokHtml = m.kind === "chief" && m.tokens && m.tokens > 0
    ? `<div class="msg-tok-footer">${icons.bolt}<span>${exactTokens(m.tokens)} tokens</span></div>`
    : "";
  el.innerHTML = `
    ${badgeHtml}
    <div class="msg-body">${bodyHtml}</div>
    ${imgsHtml}
    ${tokHtml}
  `;
  el.querySelectorAll("[data-preview-img]").forEach((imgEl) => {
    imgEl.addEventListener("click", () => {
      state.imagePreviewUrl = (imgEl as HTMLElement).dataset.previewImg || "";
      renderModalLayer();
    });
  });
}

export function rebuildChatMessagesFull() {
  const box = document.getElementById("chat-messages");
  const lane = activeLane();
  if (!box || !lane) return;
  box.innerHTML = "";
  if (lane.messages.length === 0) {
    const empty = document.createElement("div");
    empty.className = "chat-empty-state";
    const starters = [
      { label: "Explicar a arquitetura do projeto", prompt: "Faça um resumo prático da arquitetura deste projeto e dos arquivos principais." },
      { label: "Revisar alterações e achar bugs", prompt: "Revise as alterações atuais do Git e aponte possíveis bugs ou melhorias." },
      { label: "Planejar uma nova funcionalidade", prompt: "Quero planejar uma nova funcionalidade antes de codar: " },
    ];
    empty.innerHTML = `
      <div class="chat-empty-icon">${icons.brandHero}</div>
      <div class="chat-empty-title">Como a equipe pode ajudar?</div>
      <div class="chat-empty-sub">Descreva o objetivo, cole um print ou escolha um ponto de partida:</div>
      <div class="chat-starters">
        ${starters.map((s) => `<button type="button" class="chat-starter-btn" data-starter-prompt="${escapeHtml(s.prompt)}">${icons.sparkle}<span>${escapeHtml(s.label)}</span></button>`).join("")}
      </div>
    `;
    empty.querySelectorAll<HTMLButtonElement>("[data-starter-prompt]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const prompt = btn.dataset.starterPrompt || "";
        state.draftObjective = prompt;
        const input = document.getElementById("chat-input") as HTMLTextAreaElement | null;
        if (input) {
          input.value = prompt;
          input.focus();
          input.selectionStart = input.selectionEnd = prompt.length;
        }
      });
    });
    box.appendChild(empty);
    return;
  }
  const frag = document.createDocumentFragment();
  for (const m of lane.messages) {
    frag.appendChild(createMessageElement(m));
  }
  box.appendChild(frag);
  scrollChatIfAuto();
}

export function scheduleChatFlush() {
  if (chatFlushRaf) return;
  chatFlushRaf = requestAnimationFrame(() => {
    chatFlushRaf = 0;
    flushChatUpdates();
  });
}

export function flushChatUpdates() {
  const box = document.getElementById("chat-messages");
  const lane = activeLane();
  if (!box || !lane) return;

  if (lane.messages.length > 0) {
    box.querySelector(".chat-empty-state")?.remove();
  }
  for (const m of lane.messages) {
    const existing = box.querySelector<HTMLElement>(`[data-msg-id="${CSS.escape(m.id)}"]`);
    if (existing) {
      updateMessageElement(existing, m);
    } else {
      box.appendChild(createMessageElement(m));
    }
  }
  renderLaneBarOnly();
  renderPipelineBar();
  renderTokenBar();
  syncComposerButtons();
  scrollChatIfAuto();
}

export function syncComposerButtons() {
  const lane = activeLane();
  const running = Boolean(lane?.running);
  const cancelBtn = document.getElementById("btn-cancel-run") as HTMLButtonElement | null;
  const sendBtn = document.getElementById("btn-send-run") as HTMLButtonElement | null;
  const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
  if (cancelBtn) {
    cancelBtn.disabled = !running;
    cancelBtn.style.display = running ? "" : "none";
  }
  if (sendBtn) {
    sendBtn.disabled = false;
    sendBtn.innerHTML = running
      ? `${icons.send}<span>Redirecionar</span>`
      : `${icons.send}<span>Enviar</span>`;
    sendBtn.title = running
      ? "Interrompe a execução atual imediatamente e envia sua nova instrução"
      : "Enviar tarefa para a equipe (Enter)";
  }
  if (chatInput) {
    chatInput.placeholder = running
      ? "Equipe trabalhando… digite para interromper e redirecionar, ou clique em Parar"
      : "Descreva a tarefa para a equipe… (Ctrl+V cola print, Ctrl+L envia seleção, @arquivo, /ajuda)";
  }
}

export function syncScrollBottomBtn() {
  const box = document.getElementById("chat-messages");
  const btn = document.getElementById("btn-chat-scroll-bottom");
  const lane = activeLane();
  if (!box || !btn || !lane) return;
  const distFromBottom = box.scrollHeight - box.scrollTop - box.clientHeight;
  btn.style.display = distFromBottom > 48 ? "flex" : "none";
}

export function scrollChatIfAuto() {
  const box = document.getElementById("chat-messages");
  const lane = activeLane();
  if (box && lane?.autoScroll) {
    box.scrollTop = box.scrollHeight;
  }
  syncScrollBottomBtn();
}

export function renderMentionPopup() {
  const pop = document.getElementById("mention-popup");
  if (!pop) return;
  if (state.mentionResults.length === 0) {
    pop.style.display = "none";
    pop.innerHTML = "";
    return;
  }
  pop.style.display = "block";
  pop.innerHTML = state.mentionResults
    .map((f) => `<button class="dropdown-item" data-mention="${escapeHtml(f)}">@${escapeHtml(f)}</button>`)
    .join("");
  pop.querySelectorAll("[data-mention]").forEach((el) => {
    el.addEventListener("click", () => {
      const f = (el as HTMLElement).dataset.mention!;
      state.draftObjective = state.draftObjective.replace(/@([^\s@]*)$/, `@${f} `);
      state.mentionResults = [];
      const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
      if (chatInput) {
        chatInput.value = state.draftObjective;
        chatInput.focus();
      }
      renderMentionPopup();
    });
  });
}

export function bindRightStaticEvents() {
  document.getElementById("btn-expand-chat")?.addEventListener("click", () => {
    toggleChatExpanded();
  });

  document.getElementById("btn-add-lane")?.addEventListener("click", () => {
    const lane = activeLane();
    const teams = state.project?.teamConfig?.teams || [];
    const used = new Set(state.lanes.map((l) => l.teamName));
    const next = teams.find((t) => !used.has(t.name))?.name || lane?.teamName || "principal";
    const created = createLane(next);
    state.lanes.push(created);
    state.activeLaneId = created.id;
    renderLaneBarOnly();
    renderPipelineBar();
    renderTokenBar();
    rebuildChatMessagesFull();
    syncComposerButtons();
    updateStatusBar();
    updateMissionBoardIfOpen();
    if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
  });

  document.getElementById("btn-new-session")?.addEventListener("click", () => {
    const lane = activeLane();
    if (!lane || lane.running) return;
    lane.sessionId = "";
    lane.turnInTokens = 0;
    lane.turnOutTokens = 0;
    lane.turnTotalTokens = 0;
    lane.sessionInTokens = 0;
    lane.sessionOutTokens = 0;
    lane.sessionTotalTokens = 0;
    lane.agents = buildAgentRuntimes(lane.teamName);
    lane.messages = [];
    rebuildChatMessagesFull();
    renderTokenBar();
    renderLaneBarOnly();
    updateStatusBar();
    updateMissionBoardIfOpen();
    if ((state.sidebarView === "team" || state.sidebarView === "sessions") && state.showLeftSidebar) {
      renderLeftSidebar();
    }
  });

  const chatBox = document.getElementById("chat-messages");
  chatBox?.addEventListener("scroll", () => {
    const lane = activeLane();
    if (!lane || !chatBox) return;
    lane.autoScroll = chatBox.scrollHeight - chatBox.scrollTop - chatBox.clientHeight < 28;
    syncScrollBottomBtn();
  });
  document.getElementById("btn-chat-scroll-bottom")?.addEventListener("click", () => {
    const lane = activeLane();
    const box = document.getElementById("chat-messages");
    if (!lane || !box) return;
    lane.autoScroll = true;
    box.scrollTo({ top: box.scrollHeight, behavior: "smooth" });
    syncScrollBottomBtn();
  });
  chatBox?.addEventListener("click", (e) => {
    const fileBtn = (e.target as HTMLElement)?.closest<HTMLButtonElement>("[data-open-chat-file]");
    if (fileBtn) {
      e.preventDefault();
      e.stopPropagation();
      const rel = fileBtn.dataset.openChatFile || "";
      if (rel) {
        const name = rel.split("/").pop() || rel;
        void openFileInEditor(rel, name);
      }
      return;
    }
    const target = (e.target as HTMLElement)?.closest<HTMLButtonElement>("[data-copy-code]");
    if (!target) return;
    const wrap = target.closest(".md-code-wrap");
    const codeText = wrap?.querySelector("code")?.textContent ?? "";
    if (!codeText) return;
    navigator.clipboard?.writeText(codeText).then(() => {
      const prev = target.textContent;
      target.textContent = "Copiado!";
      window.setTimeout(() => {
        target.textContent = prev || "Copiar";
      }, 1400);
    }).catch(() => {});
  });

  const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;
  chatInput?.addEventListener("input", () => {
    state.draftObjective = chatInput.value;
    const m = state.draftObjective.match(/@([^\s@]*)$/);
    if (mentionTimer) window.clearTimeout(mentionTimer);
    if (m && state.project) {
      const q = m[1];
      const root = state.project.root;
      mentionTimer = window.setTimeout(async () => {
        const res = await api()?.SearchFiles(root, q);
        state.mentionResults = res || [];
        renderMentionPopup();
      }, 80);
    } else if (state.mentionResults.length > 0) {
      state.mentionResults = [];
      renderMentionPopup();
    }
  });
  chatInput?.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      sendToChief();
    }
  });
  chatInput?.addEventListener("paste", (e) => {
    const items = e.clipboardData?.items;
    if (!items) return;
    const imgFiles: File[] = [];
    for (let i = 0; i < items.length; i++) {
      if (items[i].type.startsWith("image/")) {
        const f = items[i].getAsFile();
        if (f) imgFiles.push(f);
      }
    }
    if (imgFiles.length > 0) {
      e.preventDefault();
      attachImageFiles(imgFiles);
    }
  });

  const fileInput = document.getElementById("chat-image-input") as HTMLInputElement | null;
  document.getElementById("btn-attach-image")?.addEventListener("click", () => fileInput?.click());
  fileInput?.addEventListener("change", () => {
    if (fileInput.files && fileInput.files.length > 0) {
      attachImageFiles(fileInput.files);
      fileInput.value = "";
    }
  });

  const rightPane = document.getElementById("right-pane");
  rightPane?.addEventListener("dragover", (e) => {
    e.preventDefault();
    rightPane.classList.add("drag-over");
  });
  rightPane?.addEventListener("dragleave", () => {
    rightPane.classList.remove("drag-over");
  });
  rightPane?.addEventListener("drop", (e) => {
    e.preventDefault();
    rightPane.classList.remove("drag-over");
    if (e.dataTransfer?.files && e.dataTransfer.files.length > 0) {
      attachImageFiles(e.dataTransfer.files);
    }
  });

  const jiraInput = document.getElementById("jira-key-input") as HTMLInputElement | null;
  jiraInput?.addEventListener("input", () => {
    state.jiraKeyInput = jiraInput.value;
  });

  document.getElementById("btn-send-run")?.addEventListener("click", () => sendToChief());
  document.getElementById("btn-cancel-run")?.addEventListener("click", () => void cancelActiveLane(false));
}

export async function executeApprovedPlan() {
  const lane = activeLane();
  if (!lane || lane.running) return;
  await sendToChief("Plano aprovado. Prossigam com a implementação paralela e validação de QA conforme o plano registrado.", "squad");
}

export async function sendToChief(overrideText?: string, overrideMode?: RunMode) {
  const lane = activeLane();
  const backend = api();
  if (!lane || !backend || !state.project) return;
  const isOverride = typeof overrideText === "string";
  const text = (isOverride ? overrideText : state.draftObjective).trim();
  const attached = isOverride ? [] : [...state.draftImages];
  const effectiveMode = overrideMode || state.runMode;
  if (!text && attached.length === 0) return;

  if (lane.id === "lane-autoreview") {
    state.gitTab = "flow";
    openSidebarView("git");
    setStatus("A lane auto-review é exclusiva da automação. Escolha um PR no Fluxo e use Revisar c/ Equipe.");
    return;
  }

  if (lane.running) {
    if (laneRedirecting.has(lane.id)) return;
    laneRedirecting.add(lane.id);
    try {
      await cancelActiveLane(true);
      await waitForLaneIdle(lane.id, 3500);
    } finally {
      laneRedirecting.delete(lane.id);
    }
    if (lane.running) {
      setStatus("Aguardando encerramento da execução anterior…");
      return;
    }
  }

  const chatInput = document.getElementById("chat-input") as HTMLTextAreaElement | null;

  if (!isOverride && text === "/ajuda" && attached.length === 0) {
    lane.messages.push({
      id: `sys-${Date.now()}`,
      kind: "system",
      label: "Sistema",
      text: "Modos & Atalhos:\n- **Auto / Direto / Planejar / Squad** — controle se responde em 1 agente, pede aprovação do plano ou roda todos os Devs em paralelo\n- **Quadro (`Ctrl+Shift+A`)** — painel em tempo real dos agentes + troca rápida de modelo LLM\n- **Memória (`.hivemind/MEMORY.md`)** — contexto persistente compartilhado por todo o time\n- `Ctrl+K` — Paleta de Comandos | `Ctrl+P` — Buscar Arquivo | `Ctrl+Shift+F` — Busca no Código | `Ctrl+L` — Enviar seleção",
    });
    state.draftObjective = "";
    if (chatInput) chatInput.value = "";
    flushChatUpdates();
    return;
  }

  lane.messages.push({
    id: `usr-${Date.now()}`,
    kind: "user",
    label: `Usuário · ${runModeLabel(effectiveMode)}`,
    text: text || "(imagem anexada)",
    images: attached.map((x) => x.dataBase64),
  });

  lane.messages.push({
    id: `th-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
    kind: "thinking",
    label: "Pensando",
    text: "",
    active: true,
    solo: effectiveMode !== "squad",
    startedAt: Date.now(),
    stepCount: 0,
  });
  ensureThinkingTicker();

  if (!isOverride) {
    state.draftObjective = "";
    state.draftImages = [];
    state.mentionResults = [];
    if (chatInput) chatInput.value = "";
    renderComposerImages();
    renderMentionPopup();
  }

  const runProjectId = state.activeProjectId;
  const runProjectRoot = state.project.root;
  lane.running = true;
  lane.canceledByUser = false;
  lane.stage = effectiveMode === "squad" ? "dev" : "chief";
  lane.doneStages = [];
  lane.turnInTokens = 0;
  lane.turnOutTokens = 0;
  lane.turnTotalTokens = 0;
  lane.agents.forEach((a) => {
    a.status = "idle";
    a.tool = "";
  });
  setStatus(`Executando "${lane.teamName}" em modo ${runModeLabel(effectiveMode)}…`);
  renderProjectTabsBar();
  flushChatUpdates();
  updateMissionBoardIfOpen();
  if (state.sidebarView === "team" && state.showLeftSidebar) updateTeamStatusPills();

  try {
    const payloadImgs = attached.map((x) => ({ name: x.name, mimeType: x.mimeType, dataBase64: x.dataBase64 }));
    const res = backend.StartRunWithOptions
      ? await backend.StartRunWithOptions(
          lane.id,
          runProjectRoot,
          lane.teamName,
          text,
          state.jiraKeyInput.trim(),
          lane.sessionId,
          effectiveMode,
          payloadImgs
        )
      : await backend.StartRunWithImages(
          lane.id,
          runProjectRoot,
          lane.teamName,
          text,
          state.jiraKeyInput.trim(),
          lane.sessionId,
          payloadImgs
        );
    const sid = res?.SessionID || res?.sessionID;
    if (sid) lane.sessionId = sid;
    setStatus(`Equipe "${lane.teamName}" concluída.`);
  } catch (err: any) {
    const errText = err?.message || String(err);
    const isUserCancel = Boolean(lane.canceledByUser) && /context canceled|canceled/i.test(errText);
    if (!isUserCancel) {
      const prevMsg = lane.messages[lane.messages.length - 1];
      const alreadyShown = prevMsg && prevMsg.kind === "error" && prevMsg.text === errText;
      if (!alreadyShown) {
        lane.messages.push({
          id: `err-${Date.now()}`,
          kind: "error",
          label: "Erro",
          text: errText,
        });
      }
      toastError("executar a tarefa com a equipe", err);
    }
  } finally {
    lane.running = false;
    lane.canceledByUser = false;
    lane.stage = "";
    markToolsDone(lane);
    markThinkingDone(lane);
    notifyLaneIdle(lane.id);
    renderProjectTabsBar();
    await refreshSessions(runProjectRoot);
    if (state.activeProjectId === runProjectId) {
      await refreshExplorerTree();
      await reloadCleanOpenTabs();
      await refreshDiffsAndSummarize(lane);
      flushChatUpdates();
      updateMissionBoardIfOpen();
      if (state.sidebarView === "team" && state.showLeftSidebar) updateTeamStatusPills();
    }
  }
}

export async function cancelActiveLane(forRedirect = false) {
  const lane = activeLane();
  const backend = api();
  if (!lane || !backend || !lane.running) return;
  lane.canceledByUser = true;
  for (const m of lane.messages) {
    if (m.kind === "approval" && !m.resolved) {
      m.resolved = "denied";
    }
  }
  await backend.CancelRun(lane.id);
  markToolsDone(lane);
  markThinkingDone(lane);
  lane.messages.push({
    id: `sys-${Date.now()}`,
    kind: "system",
    label: "Sistema",
    text: forRedirect
      ? "Execução anterior interrompida — aplicando nova instrução do usuário."
      : "Execução interrompida pelo usuário.",
  });
  setStatus(forRedirect ? "Redirecionando equipe…" : "Execução interrompida.");
  flushChatUpdates();
}
