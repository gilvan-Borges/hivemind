import type { Mood } from "../companion/logic";
import { icons } from "../icons";
import { escapeHtml } from "../utils";
import { attachBeeLife, hiveBeeSVG, mapMoodToBeeState, type BeeLifeController, type HiveBeeTheme } from "./hivebee";

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
  ShowIDE?(): Promise<void>;
  OpenNotice?(id: string): Promise<void>;
  DismissNotice?(id: string): Promise<void>;
  ClearNotices?(): Promise<void>;
  QuitAll?(): Promise<void>;
  CloseOrbOnly?(): Promise<void>;
}

const BEE_THEME_KEY = "hivemind:orb-bee-theme";

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

function loadBeeTheme(): HiveBeeTheme {
  try {
    return localStorage.getItem(BEE_THEME_KEY) === "gold" ? "gold" : "honey";
  } catch {
    return "honey";
  }
}

function saveBeeTheme(theme: HiveBeeTheme): void {
  try {
    localStorage.setItem(BEE_THEME_KEY, theme);
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

export function mountOrbWindow(): void {
  document.documentElement.classList.add("is-orb-mode");
  document.body.classList.add("is-orb-mode");

  const host = document.getElementById("app") || document.body;
  let expanded = false;
  let beeTheme: HiveBeeTheme = loadBeeTheme();
  let beeLife: BeeLifeController | null = null;
  let snap: OrbSnapshot = {
    mood: "idle",
    ideVisible: true,
    pendingCount: 0,
    items: [],
  };

  const cleanupLife = () => {
    if (beeLife) {
      beeLife.destroy();
      beeLife = null;
    }
  };

  const render = () => {
    cleanupLife();
    const mood = (snap.mood || "idle") as Mood;
    const count = snap.pendingCount || snap.items.length || 0;
    const beeState = mapMoodToBeeState(mood, count);
    const badge = count > 0 ? `<span class="orb-badge">${count > 9 ? "9+" : count}</span>` : "";

    if (!expanded) {
      host.innerHTML = `
        <div class="orb-collapsed" data-mood="${escapeHtml(mood)}" title="HiveMind Orb — clique para abrir painel · duplo clique para abrir IDE">
          <div class="orb-drag-ring" title="Arraste pela borda para mover a abelha"></div>
          <div id="hivemind-mascot" class="mascot-shell" data-state="${escapeHtml(beeState)}" data-theme="${escapeHtml(beeTheme)}" role="button" tabindex="0" aria-label="Abrir painel do HiveMind Orb">
            ${hiveBeeSVG()}
            ${badge}
          </div>
        </div>
      `;
      const mascotEl = host.querySelector<HTMLElement>("#hivemind-mascot");
      if (mascotEl) {
        beeLife = attachBeeLife(mascotEl);
        let clickTimer = 0;
        mascotEl.addEventListener("click", () => {
          if (clickTimer) {
            window.clearTimeout(clickTimer);
            clickTimer = 0;
            beeLife?.celebrateNow();
            window.setTimeout(() => void orbApi()?.ShowIDE?.(), 180);
            return;
          }
          beeLife?.waveNow();
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
    host.innerHTML = `
      <section class="orb-panel" data-mood="${escapeHtml(mood)}" aria-label="HiveMind Orb">
        <header class="orb-head">
          <div class="orb-head-brand">
            <div class="mascot-shell is-mini" data-state="${escapeHtml(beeState)}" data-theme="${escapeHtml(beeTheme)}">
              ${hiveBeeSVG()}
            </div>
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

        <div class="orb-list">
          ${items.length === 0
            ? `<div class="orb-empty">
                <p>Tudo tranquilo por aqui.</p>
                <p class="orb-empty-sub">O HiveMind continua vigiando seus PRs, menções do GitHub, Equipe IA e Residente em segundo plano.</p>
              </div>`
            : items.map((it) => `
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
            `).join("")
          }
        </div>

        <footer class="orb-foot">
          <div style="display:flex;align-items:center;gap:6px">
            <button type="button" class="orb-foot-btn" id="orb-theme-toggle" title="Alternar visual da abelha (Mel Vivo / Obsidian Gold)">${beeTheme === "honey" ? "Mel Vivo" : "Gold"}</button>
            ${items.length > 0 ? `<button type="button" class="orb-foot-btn" id="orb-clear">Limpar</button>` : ""}
          </div>
          <button type="button" class="orb-foot-btn danger" id="orb-quit-all" title="Encerrar a HiveMind IDE e o Orb completamente">Sair de vez</button>
        </footer>
      </section>
    `;

    host.querySelector("#orb-collapse")?.addEventListener("click", () => {
      expanded = false;
      void orbApi()?.SetOrbExpanded?.(false);
      render();
    });
    host.querySelector("#orb-open-ide")?.addEventListener("click", () => {
      void orbApi()?.ShowIDE?.();
    });
    host.querySelector("#orb-theme-toggle")?.addEventListener("click", () => {
      beeTheme = beeTheme === "honey" ? "gold" : "honey";
      saveBeeTheme(beeTheme);
      render();
    });
    host.querySelector("#orb-clear")?.addEventListener("click", () => {
      void orbApi()?.ClearNotices?.();
    });
    host.querySelector("#orb-quit-all")?.addEventListener("click", () => {
      void orbApi()?.QuitAll?.();
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
      beeLife?.celebrateNow();
    }
  });
}
