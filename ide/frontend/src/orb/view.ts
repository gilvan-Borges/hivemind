import type { Mood } from "../companion/logic";
import { autoResizeTextarea, groupConversationItems, sanitizeAssistantText, summarizeActivityGroup } from "../chat-timeline";
import { icons } from "../icons";
import { renderMarkdownStreamed } from "../markdown";
import { escapeHtml } from "../utils";
import { attachBeeLife, hiveBeeSVG, mapMoodToBeeState, type HiveBeeTheme } from "./hivebee";
import {
  attachStrikerLife,
  hiveStrikerSVG,
  mapMoodToStrikerState,
  type HiveStrikerPalette,
} from "./hivestriker";

export interface OrbNoticeItem {
  id: string;
  title: string;
  body?: string;
  mood: Mood | string;
  surface?: string;
  projectId?: string;
  laneId?: string;
  messageId?: string;
  createdAt?: string;
}

/** Espelho de ide/internal/orb.OrbResidentMessage (Spec 007 H001). */
export interface OrbResidentMessage {
  id: string;
  kind: "user" | "chief" | "tool" | "approval" | "error" | "system" | string;
  label?: string;
  text: string;
  toolName?: string;
  toolDetail?: string;
  riskTier?: string;
  approvalId?: string;
  resolved?: "" | "approved" | "denied" | string;
  active?: boolean;
}

/** Espelho de ide/internal/orb.OrbResidentState. */
export interface OrbResidentState {
  state?: string; // ocioso | pensando | aguardando você
  workstream?: string;
  workspace?: string;
  provider?: string;
  model?: string;
  sandboxAvailable?: boolean;
  sandboxMechanism?: string;
  running?: boolean;
  messages?: OrbResidentMessage[] | null;
}

export interface OrbSnapshot {
  mood: Mood | string;
  ideVisible: boolean;
  projectName?: string;
  pendingCount: number;
  items: OrbNoticeItem[];
  resident?: OrbResidentState;
  updatedAt?: string;
}

export interface WailsOrbApp {
  IsOrbMode?(): Promise<boolean>;
  GetOrbState?(): Promise<OrbSnapshot>;
  SetOrbExpanded?(expanded: boolean): Promise<void>;
  StartOrbDrag?(): Promise<boolean>;
  StartOrbResize?(edge: string): Promise<boolean>;
  ShowIDE?(): Promise<void>;
  OpenNotice?(id: string): Promise<void>;
  DismissNotice?(id: string): Promise<void>;
  ClearNotices?(): Promise<void>;
  QuitAll?(): Promise<void>;
  CloseOrbOnly?(): Promise<void>;
  SendHiveMessage?(text: string, workstream: string): Promise<void>;
  CancelHiveTurn?(): Promise<void>;
  RespondHiveApproval?(approvalId: string, approved: boolean): Promise<void>;
  ClearHiveChat?(workstream: string): Promise<void>;
  OpenHiveInIDE?(): Promise<void>;
}

export type OrbMascotId = "hivebee" | "hivestriker";

export interface OrbAgentConfig {
  mascot: OrbMascotId;
  beeTheme: HiveBeeTheme;
  strikerPalette: HiveStrikerPalette;
  strikerVisor: boolean;
}

export type OrbTab = "chat" | "avisos" | "agente";

interface MascotLifeController {
  tapNow(): void;
  celebrateNow(): void;
  destroy(): void;
}

const LEGACY_BEE_THEME_KEY = "hivemind:orb-bee-theme";
const AGENT_CONFIG_KEY = "hivemind:orb-agent-config";

const RESIDENT_SUGGESTIONS = [
  "Resumo do workspace",
  "Git status e branch",
  "Revisar alterações",
];

export function orbRiskBadge(tierRaw?: string): { tier: string; label: string; css: string } {
  const t = (tierRaw || "").trim().toLowerCase();
  switch (t) {
    case "read":
      return { tier: "read", label: "Risco Baixo · Leitura", css: "risk-read" };
    case "write":
      return { tier: "write", label: "Risco Médio · Escrita", css: "risk-write" };
    case "exec":
      return { tier: "exec", label: "Risco Alto · Execução em Sandbox", css: "risk-exec" };
    case "network":
      return { tier: "network", label: "Risco Alto · Rede (Allowlist)", css: "risk-network" };
    case "credentials":
      return { tier: "credentials", label: "Risco Crítico · Credencial do Cofre", css: "risk-credentials" };
    case "destructive":
      return { tier: "destructive", label: "Risco Crítico · Ação Destrutiva", css: "risk-destructive" };
    default:
      return { tier: t || "write", label: t ? `Risco · ${t}` : "Aprovação Necessária", css: "risk-write" };
  }
}

/** Sublinha compacta do cabeçalho: estado · rota · workstream (Spec 007 §2.2). */
export function orbStatusLine(resident?: OrbResidentState): string {
  const state = (resident?.state || "ocioso").trim() || "ocioso";
  const route = resident?.provider
    ? `${resident.provider}/${resident.model || "auto"}`
    : resident?.model || "auto";
  const ws = resident?.workstream || "geral";
  return `${escapeHtml(state)} · ${escapeHtml(route)} · ${escapeHtml(ws)}`;
}

/** Mensagens do residente normalizadas (o Go envia null quando vazia). */
export function orbResidentMessages(resident?: OrbResidentState): OrbResidentMessage[] {
  return Array.isArray(resident?.messages) ? (resident?.messages as OrbResidentMessage[]) : [];
}

/** Aprovações pendentes ancoradas acima do composer (Spec 007 D/H006). */
export function orbPendingApprovals(resident?: OrbResidentState): OrbResidentMessage[] {
  return orbResidentMessages(resident).filter((m) => m.kind === "approval" && !m.resolved);
}

export function orbChatDomStrategy(
  draft: string,
  composerFocused: boolean,
  tab: OrbTab,
): "full" | "patch" {
  if (tab !== "chat") return "full";
  if (composerFocused || draft.trim().length > 0) return "patch";
  return "full";
}

function orbApi(): WailsOrbApp | undefined {
  return (window as Window & { go?: { main?: { OrbApp?: WailsOrbApp } } }).go?.main?.OrbApp;
}

export function isOrbRuntime(): boolean {
  if (typeof window === "undefined") return false;
  if (orbApi()) return true;
  try {
    return new URLSearchParams(window.location.search).get("orb") === "1";
  } catch {
    return false;
  }
}

function loadAgentConfig(): OrbAgentConfig {
  const fallback: OrbAgentConfig = {
    mascot: "hivebee",
    beeTheme: "honey",
    strikerPalette: "obsidian",
    strikerVisor: false,
  };
  try {
    const raw = localStorage.getItem(AGENT_CONFIG_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<OrbAgentConfig>;
      return {
        mascot: parsed.mascot === "hivestriker" ? "hivestriker" : "hivebee",
        beeTheme: parsed.beeTheme === "gold" ? "gold" : "honey",
        strikerPalette:
          parsed.strikerPalette === "stealth" || parsed.strikerPalette === "amber"
            ? parsed.strikerPalette
            : "obsidian",
        strikerVisor: Boolean(parsed.strikerVisor),
      };
    }
    const legacy = localStorage.getItem(LEGACY_BEE_THEME_KEY);
    if (legacy === "gold") {
      fallback.beeTheme = "gold";
    }
  } catch {
    /* ignore */
  }
  return fallback;
}

function saveAgentConfig(cfg: OrbAgentConfig): void {
  try {
    localStorage.setItem(AGENT_CONFIG_KEY, JSON.stringify(cfg));
  } catch {
    /* ignore */
  }
}

function surfaceLabel(surface?: string): string {
  if (surface === "github") return "GitHub";
  if (surface === "resident") return "Agent Hive";
  return "Equipe IA";
}

function formatTime(iso?: string): string {
  if (!iso) return "";
  const dt = new Date(iso);
  if (Number.isNaN(dt.getTime())) return "";
  return dt.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

// ---------------------------------------------------------------------------
// Renderers puros (testáveis em node) da aba Conversa
// ---------------------------------------------------------------------------

export function renderOrbMsgHtml(m: OrbResidentMessage): string {
  const risk = m.kind === "tool" || m.kind === "approval" ? orbRiskBadge(m.riskTier) : null;
  if (m.kind === "tool") {
    return `
      <div class="orb-msg role-tool" data-msg-id="${escapeHtml(m.id)}">
        <span class="orb-msg-badge badge-tool">${escapeHtml(m.toolName || m.text)}</span>
        ${risk ? `<span class="orb-msg-risk ${risk.css}">${escapeHtml(risk.label)}</span>` : ""}
        ${m.toolDetail ? `<span class="orb-msg-detail">${escapeHtml(m.toolDetail)}</span>` : ""}
        ${m.active ? `<span class="orb-msg-spin">${icons.spinner}</span>` : ""}
      </div>
    `;
  }
  if (m.kind === "approval") {
    const decided = m.resolved === "approved"
      ? `<span class="orb-msg-resolved ok">${icons.check}<span>Aprovado</span></span>`
      : m.resolved === "denied"
        ? `<span class="orb-msg-resolved off"><span>Negado</span></span>`
        : "";
    return `
      <div class="orb-msg role-approval${m.resolved ? " resolved" : ""}" data-msg-id="${escapeHtml(m.id)}">
        <div class="orb-msg-top">
          <span class="orb-msg-badge badge-approval">${escapeHtml(m.label || "Aprovação do Agent Hive")}</span>
          ${risk ? `<span class="orb-msg-risk ${risk.css}">${escapeHtml(risk.label)}</span>` : ""}
        </div>
        <div class="orb-msg-body">${escapeHtml(m.text)}</div>
        ${m.toolName || m.toolDetail ? `<div class="orb-msg-detail">${escapeHtml(m.toolName || "")}${m.toolDetail ? ` · ${escapeHtml(m.toolDetail)}` : ""}</div>` : ""}
        ${decided}
      </div>
    `;
  }
  if (m.kind === "error") {
    return `
      <div class="orb-msg role-error" data-msg-id="${escapeHtml(m.id)}">
        <span class="orb-msg-badge badge-error">${escapeHtml(m.label || "Erro")}</span>
        <div class="orb-msg-body">${escapeHtml(m.text)}</div>
        <button type="button" class="orb-msg-retry" data-orb-retry="1">Tentar novamente</button>
      </div>
    `;
  }
  if (m.kind === "user") {
    return `
      <div class="orb-msg role-user" data-msg-id="${escapeHtml(m.id)}">
        <div class="orb-msg-body">${escapeHtml(m.text)}</div>
      </div>
    `;
  }
  const body = m.kind === "chief"
    ? renderMarkdownStreamed(`orb-${m.id}`, sanitizeAssistantText(m.text))
    : escapeHtml(m.text);
  return `
    <div class="orb-msg role-${escapeHtml(m.kind)}" data-msg-id="${escapeHtml(m.id)}">
      <div class="orb-msg-body md-prose">${body}</div>
    </div>
  `;
}

export function renderOrbChatListHtml(
  resident: OrbResidentState | undefined,
  loading: boolean,
  expandedIds?: ReadonlySet<string>,
): string {
  if (loading) {
    return `
      <div class="orb-chat-skeleton" aria-live="polite">
        <div class="orb-skeleton-line"></div>
        <div class="orb-skeleton-line short"></div>
        <div class="orb-skeleton-line"></div>
      </div>
    `;
  }
  const msgs = orbResidentMessages(resident);
  if (msgs.length === 0) {
    return `
      <div class="orb-chat-empty">
        <div class="orb-chat-empty-icon">${icons.sparkle}</div>
        <div class="orb-chat-empty-title">Agent Hive pronto</div>
        <div class="orb-chat-empty-sub">Converse com o Agent Hive sem abrir a IDE. Leitura do workspace é auditada; escrita, execução e rede exigem aprovação.</div>
        <div class="orb-suggestions">
          ${RESIDENT_SUGGESTIONS.map(
            (s) => `<button type="button" class="orb-suggestion" data-orb-suggest="${escapeHtml(s)}">${escapeHtml(s)}</button>`,
          ).join("")}
        </div>
      </div>
    `;
  }
  const timeline = groupConversationItems(msgs, Boolean(resident?.running));
  return timeline
    .map((item) => {
      if (item.kind === "activity") {
        const sum = summarizeActivityGroup(
          item.messages,
          item.active,
          Date.now(),
          (t) => t.toolDetail ? `${t.toolName || "ação"} · ${t.toolDetail}` : (t.toolName || t.text || "Executando"),
        );
        const isOpen = Boolean(expandedIds?.has(item.id));
        const inner = item.messages.map(renderOrbMsgHtml).join("");
        return `
          <details class="activity-fold orb-activity-fold ${item.active ? "is-active" : ""}" data-orb-activity-fold="${escapeHtml(item.id)}" ${isOpen ? "open" : ""}>
            <summary class="activity-fold-summary">
              ${item.active ? `<span class="activity-fold-spin">${icons.spinner}</span>` : ""}
              <span class="activity-fold-title">${escapeHtml(sum.title)}</span>
              ${sum.elapsedText ? `<span class="activity-fold-elapsed">${escapeHtml(sum.elapsedText)}</span>` : ""}
              <span class="spacer"></span>
              <span class="activity-fold-chev">${icons.chevronDown}</span>
            </summary>
            <div class="activity-fold-body">${inner}</div>
          </details>
        `;
      }
      return renderOrbMsgHtml(item.message);
    })
    .join("");
}

export function renderOrbApprovalDockHtml(resident: OrbResidentState | undefined): string {
  const pending = orbPendingApprovals(resident);
  if (pending.length === 0) return "";
  return pending
    .map((m) => {
      const risk = orbRiskBadge(m.riskTier);
      const detail = m.toolDetail
        ? `${m.toolName || "ação"} · ${m.toolDetail}`
        : m.text || m.toolName || "";
      return `
        <div class="orb-approval-row" data-approval-id="${escapeHtml(m.approvalId || m.id)}">
          <div class="orb-approval-info">
            <span class="orb-msg-risk ${risk.css}">${escapeHtml(risk.label)}</span>
            <span class="orb-approval-title">${escapeHtml(m.label || "Aprovação do Agent Hive")}</span>
            ${detail ? `<span class="orb-approval-detail">${escapeHtml(detail)}</span>` : ""}
          </div>
          <div class="orb-approval-actions">
            <button type="button" class="orb-btn-primary" data-orb-approve="${escapeHtml(m.approvalId || "")}">${icons.check}<span>Aprovar</span></button>
            <button type="button" class="orb-btn-danger" data-orb-deny="${escapeHtml(m.approvalId || "")}"><span>Recusar</span></button>
          </div>
        </div>
      `;
    })
    .join("");
}

export function renderOrbComposerHtml(resident: OrbResidentState | undefined, draft: string, loading: boolean): string {
  const running = Boolean(resident?.running);
  const stateLabel = resident?.state || "ocioso";
  return `
    <div class="orb-composer" id="orb-composer">
      <div class="orb-approval-dock" id="orb-approval-dock">${renderOrbApprovalDockHtml(resident)}</div>
      <textarea id="orb-chat-input" rows="1" placeholder="${
        loading
          ? "Conectando ao Agent Hive..."
          : running
            ? "Agent Hive pensando… (Enter envia, Shift+Enter quebra linha)"
            : "Fale com o Agent Hive… (Enter envia, Shift+Enter quebra linha)"
      }"${loading ? " disabled" : ""}>${escapeHtml(draft)}</textarea>
      <div class="orb-composer-actions">
        ${
          running
            ? `<span class="orb-thinking-note">${icons.spinner}<span>Agent Hive pensando…</span></span>`
            : stateLabel === "aguardando você"
              ? `<span class="orb-thinking-note waiting">${escapeHtml(stateLabel)}</span>`
              : ""
        }
        <span class="spacer"></span>
        ${
          running
            ? `<button type="button" class="orb-btn-danger" id="orb-cancel" title="Parar o turno atual">${icons.stop}<span>Parar</span></button>`
            : `<button type="button" class="orb-btn-primary" id="orb-send" ${loading ? "disabled" : ""}>${icons.send}<span>Enviar</span></button>`
        }
      </div>
    </div>
  `;
}

// ---------------------------------------------------------------------------
// Janela do Orb
// ---------------------------------------------------------------------------

function renderMascotMarkup(
  cfg: OrbAgentConfig,
  mood: string,
  count: number,
  variant: "full" | "mini",
  badgeHtml = "",
): string {
  const isMini = variant === "mini";
  const interactiveAttrs = isMini
    ? ""
    : `id="hivemind-mascot" role="button" tabindex="0" aria-label="Abrir painel do Agent Hive"`;

  if (cfg.mascot === "hivestriker") {
    const state = mapMoodToStrikerState(mood, count);
    return `
      <div ${interactiveAttrs}
           class="mascot-shell mascot-striker${isMini ? " is-mini" : ""}"
           data-state="${escapeHtml(state)}"
           data-palette="${escapeHtml(cfg.strikerPalette)}"
           data-visor="${cfg.strikerVisor ? "true" : "false"}">
        ${hiveStrikerSVG()}
        ${badgeHtml}
      </div>
    `;
  }

  const state = mapMoodToBeeState(mood, count);
  return `
    <div ${interactiveAttrs}
         class="mascot-shell mascot-bee${isMini ? " is-mini" : ""}"
         data-state="${escapeHtml(state)}"
         data-theme="${escapeHtml(cfg.beeTheme)}">
      ${hiveBeeSVG()}
      ${badgeHtml}
    </div>
  `;
}

function attachMascotLife(el: HTMLElement, mascot: OrbMascotId): MascotLifeController {
  if (mascot === "hivestriker") {
    const ctrl = attachStrikerLife(el);
    return {
      tapNow: () => ctrl.fistNow(),
      celebrateNow: () => ctrl.flexNow(),
      destroy: () => ctrl.destroy(),
    };
  }
  const ctrl = attachBeeLife(el);
  return {
    tapNow: () => ctrl.waveNow(),
    celebrateNow: () => ctrl.celebrateNow(),
    destroy: () => ctrl.destroy(),
  };
}

export function mountOrbWindow(): void {
  document.documentElement.classList.add("is-orb-mode");
  document.body.classList.add("is-orb-mode");

  const host = document.getElementById("app") || document.body;
  let expanded = false;
  let activeTab: OrbTab = "chat";
  let cfg: OrbAgentConfig = loadAgentConfig();
  let life: MascotLifeController | null = null;
  let snap: OrbSnapshot = {
    mood: "idle",
    ideVisible: true,
    pendingCount: 0,
    items: [],
  };
  let residentLoaded = false;
  let draftInput = "";
  let dragCleanup: (() => void) | null = null;
  const orbExpandedGroups = new Set<string>();

  const bindOrbFoldToggles = (scope: ParentNode = document): void => {
    scope.querySelectorAll<HTMLDetailsElement>("details[data-orb-activity-fold]").forEach((det) => {
      det.addEventListener("toggle", () => {
        const gid = det.dataset.orbActivityFold || "";
        if (!gid) return;
        if (det.open) orbExpandedGroups.add(gid);
        else orbExpandedGroups.delete(gid);
      });
    });
  };

  const cleanupLife = () => {
    if (life) {
      life.destroy();
      life = null;
    }
    if (dragCleanup) {
      dragCleanup();
      dragCleanup = null;
    }
  };

  const composerFocused = (): boolean =>
    typeof document !== "undefined" && document.activeElement?.id === "orb-chat-input";

  const bindComposerEvents = (): void => {
    const input = document.getElementById("orb-chat-input") as HTMLTextAreaElement | null;
    if (!input) return;
    autoResizeTextarea(input, 22, 132);
    input.addEventListener("input", () => {
      draftInput = input.value;
      autoResizeTextarea(input, 22, 132);
    });
    input.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter" && !ev.shiftKey) {
        ev.preventDefault();
        sendMessage();
      }
    });
  };

  const sendMessage = (overrideText?: string): void => {
    const text = (overrideText ?? draftInput).trim();
    if (!text) return;
    const ws = snap.resident?.workstream || "geral";
    if (overrideText === undefined) {
      draftInput = "";
      const input = document.getElementById("orb-chat-input") as HTMLTextAreaElement | null;
      if (input) {
        input.value = "";
        autoResizeTextarea(input, 22, 132);
      }
    }
    void orbApi()?.SendHiveMessage?.(text, ws);
  };

  const bindChatTabEvents = (): void => {
    bindComposerEvents();
    bindOrbFoldToggles(host);
    document.getElementById("orb-send")?.addEventListener("click", () => sendMessage());
    document.getElementById("orb-cancel")?.addEventListener("click", () => {
      void orbApi()?.CancelHiveTurn?.();
    });
    document.querySelectorAll<HTMLElement>("[data-orb-approve]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const id = btn.dataset.orbApprove || "";
        if (id) void orbApi()?.RespondHiveApproval?.(id, true);
      });
    });
    document.querySelectorAll<HTMLElement>("[data-orb-deny]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const id = btn.dataset.orbDeny || "";
        if (id) void orbApi()?.RespondHiveApproval?.(id, false);
      });
    });
    document.querySelectorAll<HTMLElement>("[data-orb-suggest]").forEach((btn) => {
      btn.addEventListener("click", () => sendMessage(btn.dataset.orbSuggest || ""));
    });
    document.querySelectorAll<HTMLElement>("[data-orb-retry]").forEach((btn) => {
      btn.addEventListener("click", () => {
        // Reenvia o último comando do usuário da timeline atual.
        const msgs = orbResidentMessages(snap.resident);
        const lastUser = [...msgs].reverse().find((m) => m.kind === "user");
        if (lastUser) sendMessage(lastUser.text);
      });
    });
  };

  const autoscrollChat = (force = false): void => {
    const list = document.getElementById("orb-chat-list");
    if (!list) return;
    const nearBottom = list.scrollHeight - list.scrollTop - list.clientHeight < 60;
    if (force || nearBottom) {
      list.scrollTop = list.scrollHeight;
    }
  };

  const collapsePanel = (): void => {
    if (!expanded) return;
    expanded = false;
    void orbApi()?.SetOrbExpanded?.(false);
    render();
  };

  const render = (): void => {
    cleanupLife();
    const mood = (snap.mood || "idle") as Mood;
    const count = snap.pendingCount || snap.items.length || 0;
    const badge = count > 0 ? `<span class="orb-badge">${count > 9 ? "9+" : count}</span>` : "";

    if (!expanded) {
      host.innerHTML = `
        <div class="orb-collapsed" data-mood="${escapeHtml(mood)}" data-has-notice="${count > 0 ? "1" : "0"}" title="Agent Hive — clique para conversar · duplo clique para abrir IDE">
          <div class="orb-aura" aria-hidden="true"></div>
          <div class="orb-drag-ring" title="Arraste para mover o agente"></div>
          ${renderMascotMarkup(cfg, mood, count, "full", badge)}
        </div>
      `;
      const collapsedEl = host.querySelector<HTMLElement>(".orb-collapsed");
      const mascotEl = host.querySelector<HTMLElement>("#hivemind-mascot");
      if (mascotEl) {
        life = attachMascotLife(mascotEl, cfg.mascot);
      }
      if (collapsedEl && mascotEl) {
        let clickTimer = 0;
        let pointerDown = false;
        let dragStarted = false;
        let startClientX = 0;
        let startClientY = 0;
        let downScreenX = 0;
        let downScreenY = 0;
        let justDraggedUntil = 0;

        collapsedEl.addEventListener("dragstart", (ev) => ev.preventDefault());

        collapsedEl.addEventListener("pointerdown", (ev) => {
          if (ev.button !== 0) return;
          pointerDown = true;
          dragStarted = false;
          startClientX = ev.clientX;
          startClientY = ev.clientY;
          downScreenX = ev.screenX;
          downScreenY = ev.screenY;
        });

        const maybeBeginDrag = (ev: PointerEvent | MouseEvent) => {
          if (!pointerDown || dragStarted || (ev.buttons & 1) === 0) return;
          const clientDist = Math.hypot(ev.clientX - startClientX, ev.clientY - startClientY);
          const screenDist = Math.hypot(ev.screenX - downScreenX, ev.screenY - downScreenY);
          if (clientDist >= 4 || screenDist >= 4) {
            dragStarted = true;
            pointerDown = false;
            justDraggedUntil = Date.now() + 450;
            collapsedEl.classList.add("is-dragging");
            if (clickTimer) {
              window.clearTimeout(clickTimer);
              clickTimer = 0;
            }
            void orbApi()?.StartOrbDrag?.();
          }
        };

        window.addEventListener("pointermove", maybeBeginDrag);

        const endPointer = () => {
          pointerDown = false;
          if (dragStarted) {
            justDraggedUntil = Date.now() + 450;
            dragStarted = false;
            collapsedEl.classList.remove("is-dragging");
          }
        };
        window.addEventListener("pointerup", endPointer);
        window.addEventListener("pointercancel", endPointer);
        dragCleanup = () => {
          if (clickTimer) {
            window.clearTimeout(clickTimer);
            clickTimer = 0;
          }
          window.removeEventListener("pointermove", maybeBeginDrag);
          window.removeEventListener("pointerup", endPointer);
          window.removeEventListener("pointercancel", endPointer);
        };

        mascotEl.addEventListener("click", (ev) => {
          const movedScreen = Math.hypot(ev.screenX - downScreenX, ev.screenY - downScreenY) >= 5;
          if (dragStarted || movedScreen || Date.now() < justDraggedUntil) {
            return;
          }
          if (clickTimer) {
            window.clearTimeout(clickTimer);
            clickTimer = 0;
            life?.celebrateNow();
            window.setTimeout(() => void orbApi()?.OpenHiveInIDE?.(), 180);
            return;
          }
          life?.tapNow();
          clickTimer = window.setTimeout(() => {
            clickTimer = 0;
            expanded = true;
            activeTab = "chat";
            void orbApi()?.SetOrbExpanded?.(true);
            render();
            // Foco imediato no composer ao abrir o painel (Spec 007 H005).
            window.setTimeout(() => {
              const input = document.getElementById("orb-chat-input") as HTMLTextAreaElement | null;
              input?.focus();
            }, 60);
          }, 220);
        });
        mascotEl.addEventListener("keydown", (ev) => {
          if (ev.key === "Enter" || ev.key === " ") {
            ev.preventDefault();
            expanded = true;
            activeTab = "chat";
            void orbApi()?.SetOrbExpanded?.(true);
            render();
          }
        });
      }
      return;
    }

    const resident = snap.resident;
    const statusLine = orbStatusLine(resident);
    const items = snap.items || [];
    const beeState = mapMoodToBeeState(mood, count);
    const strikerState = mapMoodToStrikerState(mood, count);
    const pendingApprovals = orbPendingApprovals(resident);
    const tabBadge = count > 0 ? ` <span class="orb-tab-badge">${count > 9 ? "9+" : count}</span>` : "";

    const bodyContent =
      activeTab === "chat"
        ? `
          <div class="orb-chat">
            <div class="orb-chat-list" id="orb-chat-list">${renderOrbChatListHtml(resident, !residentLoaded, orbExpandedGroups)}</div>
            ${renderOrbComposerHtml(resident, draftInput, !residentLoaded)}
          </div>
        `
        : activeTab === "agente"
          ? `
            <div class="orb-agent-config">
              <div class="orb-config-title">Agente flutuante</div>
              <div class="orb-agent-grid">
                <button type="button" class="orb-agent-card${cfg.mascot === "hivebee" ? " is-active" : ""}" data-select-mascot="hivebee">
                  <div class="mascot-shell mascot-bee is-preview" data-state="${escapeHtml(beeState)}" data-theme="${escapeHtml(cfg.beeTheme)}">
                    ${hiveBeeSVG()}
                  </div>
                  <span class="orb-agent-name">HiveBee</span>
                  <span class="orb-agent-desc">Abelha Chibi 3D</span>
                </button>
                <button type="button" class="orb-agent-card${cfg.mascot === "hivestriker" ? " is-active" : ""}" data-select-mascot="hivestriker">
                  <div class="mascot-shell mascot-striker is-preview" data-state="${escapeHtml(strikerState)}" data-palette="${escapeHtml(cfg.strikerPalette)}" data-visor="${cfg.strikerVisor ? "true" : "false"}">
                    ${hiveStrikerSVG()}
                  </div>
                  <span class="orb-agent-name">HiveStriker</span>
                  <span class="orb-agent-desc">Abelha Atlética</span>
                </button>
              </div>

              ${cfg.mascot === "hivebee"
                ? `
                  <div class="orb-config-group">
                    <span class="orb-config-label">Tema</span>
                    <div class="orb-pill-row">
                      <button type="button" class="orb-pill${cfg.beeTheme === "honey" ? " is-active" : ""}" data-bee-theme="honey">Mel Vivo</button>
                      <button type="button" class="orb-pill${cfg.beeTheme === "gold" ? " is-active" : ""}" data-bee-theme="gold">Obsidian Gold</button>
                    </div>
                  </div>
                `
                : `
                  <div class="orb-config-group">
                    <span class="orb-config-label">Paleta</span>
                    <div class="orb-pill-row">
                      <button type="button" class="orb-pill${cfg.strikerPalette === "obsidian" ? " is-active" : ""}" data-striker-palette="obsidian">Obsidian</button>
                      <button type="button" class="orb-pill${cfg.strikerPalette === "stealth" ? " is-active" : ""}" data-striker-palette="stealth">Stealth</button>
                      <button type="button" class="orb-pill${cfg.strikerPalette === "amber" ? " is-active" : ""}" data-striker-palette="amber">Amber</button>
                    </div>
                  </div>
                  <label class="orb-toggle-row">
                    <input type="checkbox" id="orb-striker-visor"${cfg.strikerVisor ? " checked" : ""} />
                    <span>Visor tático</span>
                  </label>
                `
              }
            </div>
          `
          : items.length === 0
            ? `
              <div class="orb-empty">
                <p>Tudo tranquilo por aqui.</p>
                <p class="orb-empty-sub">O Agent Hive continua vigiando seus PRs, menções do GitHub, Equipe IA e Residente em segundo plano.</p>
              </div>
            `
            : items
                .map(
                  (it) => `
                    <div class="orb-item" data-mood="${escapeHtml(String(it.mood || "attention"))}">
                      <button type="button" class="orb-item-main" data-orb-open="${escapeHtml(it.id)}">
                        <div class="orb-item-top">
                          <span class="orb-item-tag">${escapeHtml(surfaceLabel(it.surface))}</span>
                          ${it.createdAt ? `<span class="orb-item-time">${escapeHtml(formatTime(it.createdAt))}</span>` : ""}
                        </div>
                        <div class="orb-item-title">${escapeHtml(it.title)}</div>
                        ${it.body ? `<div class="orb-item-body">${escapeHtml(it.body)}</div>` : ""}
                      </button>
                      <button type="button" class="orb-icon-btn orb-item-x" data-orb-dismiss="${escapeHtml(it.id)}" aria-label="Dispensar">${icons.closeSmall}</button>
                    </div>
                  `,
                )
                .join("");

    host.innerHTML = `
      <section class="orb-panel" data-mood="${escapeHtml(mood)}" aria-label="Agent Hive">
        <header class="orb-head" id="orb-head-drag">
          <div class="orb-head-brand">
            ${renderMascotMarkup(cfg, mood, count, "mini")}
            <div class="orb-head-meta">
              <div class="orb-head-title">Agent Hive${snap.projectName ? ` · ${escapeHtml(snap.projectName)}` : ""}</div>
              <div class="orb-head-sub" id="orb-head-sub">${statusLine}</div>
            </div>
          </div>
          <div class="orb-head-actions">
            ${pendingApprovals.length > 0 ? `<span class="orb-head-waiting">${icons.bolt}<span>${pendingApprovals.length} aprovação${pendingApprovals.length > 1 ? "ões" : ""}</span></span>` : ""}
            <button type="button" class="orb-btn-primary" id="orb-open-ide" title="Abrir a HiveMind IDE na aba Agent Hive">Abrir IDE</button>
            <button type="button" class="orb-icon-btn" id="orb-collapse" aria-label="Colapsar Agent Hive" title="Colapsar">${icons.chevronDown}</button>
          </div>
        </header>

        <nav class="orb-tabs" role="tablist" aria-label="Modos do Agent Hive">
          <button type="button" role="tab" aria-selected="${activeTab === "chat"}" class="orb-tab${activeTab === "chat" ? " is-active" : ""}" data-orb-tab="chat">Conversa</button>
          <button type="button" role="tab" aria-selected="${activeTab === "avisos"}" class="orb-tab${activeTab === "avisos" ? " is-active" : ""}" data-orb-tab="avisos">Avisos${tabBadge}</button>
          <button type="button" role="tab" aria-selected="${activeTab === "agente"}" class="orb-tab${activeTab === "agente" ? " is-active" : ""}" data-orb-tab="agente">Agente</button>
        </nav>

        <div class="orb-body">${bodyContent}</div>

        <div class="orb-resize orb-resize-e" data-orb-resize="e" title="Arraste para a largura"></div>
        <div class="orb-resize orb-resize-s" data-orb-resize="s" title="Arraste para a altura"></div>
        <div class="orb-resize orb-resize-se" data-orb-resize="se" title="Arraste para aumentar"></div>
        <footer class="orb-foot">
          <div class="orb-foot-left">
            ${activeTab === "chat"
              ? `<button type="button" class="orb-foot-btn" id="orb-clear-chat">Limpar conversa</button>`
              : activeTab === "avisos" && items.length > 0
                ? `<button type="button" class="orb-foot-btn" id="orb-clear">Limpar</button>`
                : ""}
            <span class="orb-foot-hint" id="orb-foot-hint">${
              snap.resident?.sandboxAvailable
                ? "Sandbox ativa"
                : snap.ideVisible
                  ? "IDE aberta"
                  : "IDE em 2º plano"
            }</span>
          </div>
          <button type="button" class="orb-foot-btn danger" id="orb-quit-all" title="Encerrar a HiveMind IDE e o Agent Hive completamente">Sair de vez</button>
        </footer>
      </section>
    `;

    host.querySelector("#orb-collapse")?.addEventListener("click", () => collapsePanel());
    host.querySelectorAll<HTMLElement>("[data-orb-resize]").forEach((el) => {
      el.addEventListener("pointerdown", (ev) => {
        if (ev.button !== 0) return;
        ev.preventDefault();
        ev.stopPropagation();
        void orbApi()?.StartOrbResize?.(el.dataset.orbResize || "se");
      });
    });
    host.querySelector("#orb-open-ide")?.addEventListener("click", () => {
      void orbApi()?.OpenHiveInIDE?.();
    });
    host.querySelectorAll<HTMLElement>("[data-orb-tab]").forEach((btn) => {
      btn.addEventListener("click", () => {
        activeTab = (btn.dataset.orbTab as OrbTab) || "chat";
        render();
      });
    });
    host.querySelector("#orb-clear-chat")?.addEventListener("click", () => {
      draftInput = "";
      void orbApi()?.ClearHiveChat?.(snap.resident?.workstream || "geral");
      render();
    });
    host.querySelector("#orb-clear")?.addEventListener("click", () => {
      void orbApi()?.ClearNotices?.();
    });
    host.querySelector("#orb-quit-all")?.addEventListener("click", () => {
      void orbApi()?.QuitAll?.();
    });
    host.querySelectorAll<HTMLElement>("[data-select-mascot]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const next = btn.dataset.selectMascot === "hivestriker" ? "hivestriker" : "hivebee";
        if (cfg.mascot !== next) {
          cfg = { ...cfg, mascot: next };
          saveAgentConfig(cfg);
          render();
        }
      });
    });
    host.querySelectorAll<HTMLElement>("[data-bee-theme]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const next: HiveBeeTheme = btn.dataset.beeTheme === "gold" ? "gold" : "honey";
        cfg = { ...cfg, beeTheme: next };
        saveAgentConfig(cfg);
        render();
      });
    });
    host.querySelectorAll<HTMLElement>("[data-striker-palette]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const raw = btn.dataset.strikerPalette;
        const next: HiveStrikerPalette =
          raw === "stealth" || raw === "amber" ? raw : "obsidian";
        cfg = { ...cfg, strikerPalette: next };
        saveAgentConfig(cfg);
        render();
      });
    });
    host.querySelector<HTMLInputElement>("#orb-striker-visor")?.addEventListener("change", (ev) => {
      const checked = (ev.target as HTMLInputElement).checked;
      cfg = { ...cfg, strikerVisor: checked };
      saveAgentConfig(cfg);
      render();
    });
    host.querySelectorAll<HTMLElement>("[data-orb-open]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const id = btn.dataset.orbOpen || "";
        if (id) void orbApi()?.OpenNotice?.(id);
      });
    });
    host.querySelectorAll<HTMLElement>("[data-orb-dismiss]").forEach((btn) => {
      btn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        const id = btn.dataset.orbDismiss || "";
        if (id) void orbApi()?.DismissNotice?.(id);
      });
    });

    if (activeTab === "chat") {
      bindChatTabEvents();
      renderedRunning = Boolean(snap.resident?.running);
      renderedLoading = !residentLoaded;
      autoscrollChat(true);
    }
  };

  let renderedRunning: boolean | null = null;
  let renderedLoading = false;

  /**
   * Patch cirúrgico da aba Conversa durante streaming: atualiza header,
   * timeline, dock de aprovação, placeholder e botão Enviar/Parar sem tocar
   * no <textarea> (preserva rascunho, foco e seleção — Spec 007 H007).
   */
  const patchChatDom = (): void => {
    const resident = snap.resident;
    const sub = document.getElementById("orb-head-sub");
    if (sub) sub.innerHTML = orbStatusLine(resident);
    const list = document.getElementById("orb-chat-list");
    if (list) {
      list.innerHTML = renderOrbChatListHtml(resident, !residentLoaded, orbExpandedGroups);
      bindOrbFoldToggles(list);
      autoscrollChat();
    }
    const dock = document.getElementById("orb-approval-dock");
    if (dock) {
      const nextDock = renderOrbApprovalDockHtml(resident);
      if (dock.innerHTML !== nextDock) {
        dock.innerHTML = nextDock;
        document.querySelectorAll<HTMLElement>("[data-orb-approve]").forEach((btn) => {
          btn.addEventListener("click", () => {
            const id = btn.dataset.orbApprove || "";
            if (id) void orbApi()?.RespondHiveApproval?.(id, true);
          });
        });
        document.querySelectorAll<HTMLElement>("[data-orb-deny]").forEach((btn) => {
          btn.addEventListener("click", () => {
            const id = btn.dataset.orbDeny || "";
            if (id) void orbApi()?.RespondHiveApproval?.(id, false);
          });
        });
      }
    }
    // Placeholder e nota de estado do composer sem recriar o textarea.
    const running = Boolean(resident?.running);
    const loading = !residentLoaded;
    const input = document.getElementById("orb-chat-input") as HTMLTextAreaElement | null;
    if (input) {
      input.placeholder = loading
        ? "Conectando ao Agent Hive..."
        : running
          ? "Agent Hive pensando… (Enter envia, Shift+Enter quebra linha)"
          : "Fale com o Agent Hive… (Enter envia, Shift+Enter quebra linha)";
      input.disabled = loading;
    }
    if (running !== renderedRunning || loading !== renderedLoading) {
      const actions = document.querySelector<HTMLElement>(".orb-composer-actions");
      if (actions) {
        actions.innerHTML = `
          ${
            running
              ? `<span class="orb-thinking-note">${icons.spinner}<span>Agent Hive pensando…</span></span>`
              : resident?.state === "aguardando você"
                ? `<span class="orb-thinking-note waiting">${escapeHtml(resident.state)}</span>`
                : ""
          }
          <span class="spacer"></span>
          ${
            running
              ? `<button type="button" class="orb-btn-danger" id="orb-cancel" title="Parar o turno atual">${icons.stop}<span>Parar</span></button>`
              : `<button type="button" class="orb-btn-primary" id="orb-send" ${loading ? "disabled" : ""}>${icons.send}<span>Enviar</span></button>`
          }
        `;
        document.getElementById("orb-cancel")?.addEventListener("click", () => {
          void orbApi()?.CancelHiveTurn?.();
        });
        document.getElementById("orb-send")?.addEventListener("click", () => sendMessage());
      }
      renderedRunning = running;
      renderedLoading = loading;
    }
    const hint = document.getElementById("orb-foot-hint");
    if (hint) {
      hint.textContent = resident?.sandboxAvailable
        ? "Sandbox ativa"
        : snap.ideVisible
          ? "IDE aberta"
          : "IDE em 2º plano";
    }
  };

  const applyOrbSnapshot = (next: OrbSnapshot): void => {
    if (!next) return;
    const prevMood = snap.mood;
    snap = next;
    if (next.resident?.state) residentLoaded = true;
    if (!expanded) {
      render();
      if (next.mood === "success" && prevMood !== "success") {
        life?.celebrateNow();
      }
      return;
    }
    const strategy = orbChatDomStrategy(draftInput, composerFocused(), activeTab);
    if (strategy === "patch") {
      patchChatDom();
      return;
    }
    render();
  };

  render();

  void orbApi()?.GetOrbState?.().then((initial) => {
    if (initial) {
      applyOrbSnapshot(initial);
    }
  });

  window.runtime?.EventsOn?.("hivemind:orb-state", (next: OrbSnapshot) => {
    applyOrbSnapshot(next);
  });
  window.runtime?.EventsOn?.("hivemind:orb-collapse", () => collapsePanel());
}
