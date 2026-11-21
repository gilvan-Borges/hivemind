import type { Mood } from "../companion/logic";
import { icons } from "../icons";
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

export interface OrbSnapshot {
  mood: Mood | string;
  ideVisible: boolean;
  projectName?: string;
  pendingCount: number;
  items: OrbNoticeItem[];
  updatedAt?: string;
}

export interface WailsOrbApp {
  IsOrbMode?(): Promise<boolean>;
  GetOrbState?(): Promise<OrbSnapshot>;
  SetOrbExpanded?(expanded: boolean): Promise<void>;
  StartOrbDrag?(): Promise<boolean>;
  ShowIDE?(): Promise<void>;
  OpenNotice?(id: string): Promise<void>;
  DismissNotice?(id: string): Promise<void>;
  ClearNotices?(): Promise<void>;
  QuitAll?(): Promise<void>;
  CloseOrbOnly?(): Promise<void>;
}

export type OrbMascotId = "hivebee" | "hivestriker";

export interface OrbAgentConfig {
  mascot: OrbMascotId;
  beeTheme: HiveBeeTheme;
  strikerPalette: HiveStrikerPalette;
  strikerVisor: boolean;
}

interface MascotLifeController {
  tapNow(): void;
  celebrateNow(): void;
  destroy(): void;
}

const LEGACY_BEE_THEME_KEY = "hivemind:orb-bee-theme";
const AGENT_CONFIG_KEY = "hivemind:orb-agent-config";

const MOOD_STATUS: Record<string, string> = {
  idle: "Ocioso",
  thinking: "Pensando…",
  working: "Trabalhando…",
  success: "Concluído",
  error: "Erro detectado",
  attention: "Precisa de você",
};

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
  if (surface === "resident") return "Residente";
  return "Equipe IA";
}

function formatTime(iso?: string): string {
  if (!iso) return "";
  const dt = new Date(iso);
  if (Number.isNaN(dt.getTime())) return "";
  return dt.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

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
    : `id="hivemind-mascot" role="button" tabindex="0" aria-label="Abrir painel do HiveMind Orb"`;

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
  let showAgentConfig = false;
  let cfg: OrbAgentConfig = loadAgentConfig();
  let life: MascotLifeController | null = null;
  let snap: OrbSnapshot = {
    mood: "idle",
    ideVisible: true,
    pendingCount: 0,
    items: [],
  };

  let dragCleanup: (() => void) | null = null;

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

  const render = () => {
    cleanupLife();
    const mood = (snap.mood || "idle") as Mood;
    const count = snap.pendingCount || snap.items.length || 0;
    const badge = count > 0 ? `<span class="orb-badge">${count > 9 ? "9+" : count}</span>` : "";

    if (!expanded) {
      host.innerHTML = `
        <div class="orb-collapsed" data-mood="${escapeHtml(mood)}" data-has-notice="${count > 0 ? "1" : "0"}" title="HiveMind Orb — clique para abrir painel · duplo clique para abrir IDE">
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
            window.setTimeout(() => void orbApi()?.ShowIDE?.(), 180);
            return;
          }
          life?.tapNow();
          clickTimer = window.setTimeout(() => {
            clickTimer = 0;
            expanded = true;
            void orbApi()?.SetOrbExpanded?.(true);
            render();
          }, 220);
        });
        mascotEl.addEventListener("keydown", (ev) => {
          if (ev.key === "Enter" || ev.key === " ") {
            ev.preventDefault();
            expanded = true;
            void orbApi()?.SetOrbExpanded?.(true);
            render();
          }
        });
      }
      return;
    }

    const statusLine = `${MOOD_STATUS[mood] || "Ativo"} · ${snap.ideVisible ? "IDE aberta" : "IDE em 2º plano"}`;
    const items = snap.items || [];
    const beeState = mapMoodToBeeState(mood, count);
    const strikerState = mapMoodToStrikerState(mood, count);

    const bodyContent = showAgentConfig
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
            <p class="orb-empty-sub">O HiveMind continua vigiando seus PRs, menções do GitHub, Equipe IA e Residente em segundo plano.</p>
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
      <section class="orb-panel" data-mood="${escapeHtml(mood)}" aria-label="HiveMind Orb">
        <header class="orb-head">
          <div class="orb-head-brand">
            ${renderMascotMarkup(cfg, mood, count, "mini")}
            <div class="orb-head-meta">
              <div class="orb-head-title">HiveMind${snap.projectName ? ` · ${escapeHtml(snap.projectName)}` : ""}</div>
              <div class="orb-head-sub">${escapeHtml(statusLine)}</div>
            </div>
          </div>
          <div class="orb-head-actions">
            <button type="button" class="orb-btn-primary" id="orb-open-ide" title="Abrir ou trazer a HiveMind IDE para frente">Abrir IDE</button>
            <button type="button" class="orb-icon-btn" id="orb-collapse" aria-label="Colapsar Orb" title="Colapsar">${icons.chevronDown}</button>
          </div>
        </header>

        <div class="orb-list">${bodyContent}</div>

        <footer class="orb-foot">
          <div class="orb-foot-left">
            <button type="button" class="orb-foot-btn${showAgentConfig ? " is-active" : ""}" id="orb-agent-toggle" title="Escolher o agente flutuante e aparência">
              ${showAgentConfig ? "Avisos" : "Agente"}
            </button>
            ${!showAgentConfig && items.length > 0 ? `<button type="button" class="orb-foot-btn" id="orb-clear">Limpar</button>` : ""}
          </div>
          <button type="button" class="orb-foot-btn danger" id="orb-quit-all" title="Encerrar a HiveMind IDE e o Orb completamente">Sair de vez</button>
        </footer>
      </section>
    `;

    host.querySelector("#orb-collapse")?.addEventListener("click", () => {
      expanded = false;
      showAgentConfig = false;
      void orbApi()?.SetOrbExpanded?.(false);
      render();
    });
    host.querySelector("#orb-open-ide")?.addEventListener("click", () => {
      void orbApi()?.ShowIDE?.();
    });
    host.querySelector("#orb-agent-toggle")?.addEventListener("click", () => {
      showAgentConfig = !showAgentConfig;
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
  };

  render();

  void orbApi()?.GetOrbState?.().then((initial) => {
    if (initial) {
      snap = initial;
      render();
    }
  });

  window.runtime?.EventsOn?.("hivemind:orb-state", (next: OrbSnapshot) => {
    if (!next) return;
    const prevMood = snap.mood;
    snap = next;
    render();
    if (!expanded && next.mood === "success" && prevMood !== "success") {
      life?.celebrateNow();
    }
  });
}
