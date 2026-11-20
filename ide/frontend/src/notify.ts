/** Hive companion: aviso flutuante na IDE + toast nativo do SO quando a janela está fora de foco. */

export type HiveNotifyKind = "info" | "success" | "error" | "attention";

export type HiveNotifyPayload = {
  title: string;
  body?: string;
  kind?: HiveNotifyKind;
  /** Força toast do SO mesmo com a janela em foco (raro). */
  forceDesktop?: boolean;
};

type RuntimeNotify = {
  EventsOn?(eventName: string, cb: (data: any) => void): void;
  InitializeNotifications?(): Promise<void>;
  IsNotificationAvailable?(): Promise<boolean>;
  RequestNotificationAuthorization?(): Promise<boolean>;
  SendNotification?(opts: { id: string; title: string; body?: string }): Promise<void>;
  WindowIsMinimised?(): Promise<boolean>;
  WindowUnminimise?(): void;
  WindowShow?(): void;
};

const DISMISS_MS = 6500;
const BODY_MAX = 180;

let ready = false;
let initPromise: Promise<void> | null = null;
let dismissTimer = 0;
let seq = 0;

export function truncateNotifyBody(text: string, max = BODY_MAX): string {
  const t = String(text || "").replace(/\s+/g, " ").trim();
  if (t.length <= max) return t;
  return `${t.slice(0, Math.max(0, max - 1)).trimEnd()}…`;
}

export async function isHiveAway(): Promise<boolean> {
  if (typeof document !== "undefined" && document.hidden) return true;
  const rt = runtime();
  if (!rt?.WindowIsMinimised) return typeof document !== "undefined" ? !document.hasFocus() : false;
  try {
    if (await rt.WindowIsMinimised()) return true;
  } catch {
    /* preview / runtime incompleto */
  }
  if (typeof document !== "undefined" && typeof document.hasFocus === "function" && !document.hasFocus()) {
    return true;
  }
  return false;
}

function runtime(): RuntimeNotify | undefined {
  return (window as Window & { runtime?: RuntimeNotify }).runtime;
}

export async function initHiveNotify(): Promise<void> {
  if (ready) return;
  if (initPromise) return initPromise;
  initPromise = (async () => {
    const rt = runtime();
    if (!rt?.InitializeNotifications) {
      ready = true;
      return;
    }
    try {
      const available = rt.IsNotificationAvailable ? await rt.IsNotificationAvailable() : true;
      if (!available) {
        ready = true;
        return;
      }
      await rt.InitializeNotifications();
      if (rt.RequestNotificationAuthorization) {
        await rt.RequestNotificationAuthorization().catch(() => false);
      }
    } catch {
      /* SO sem suporte ou permissão negada — companion in-app continua. */
    } finally {
      ready = true;
    }
  })();
  return initPromise;
}

function ensureCompanion(): HTMLElement {
  let root = document.getElementById("hive-companion");
  if (root) return root;
  root = document.createElement("div");
  root.id = "hive-companion";
  root.className = "hive-companion";
  root.setAttribute("aria-live", "polite");
  root.innerHTML = `
    <button type="button" class="hive-companion-avatar" aria-label="Avisos do HiveMind" title="HiveMind">
      <img src="/brand/hivemind-icon.png" width="40" height="40" alt="" />
    </button>
    <div class="hive-companion-bubble" hidden>
      <div class="hive-companion-title"></div>
      <div class="hive-companion-body"></div>
      <button type="button" class="hive-companion-dismiss" aria-label="Fechar aviso">×</button>
    </div>
  `;
  document.body.appendChild(root);
  const bubble = root.querySelector<HTMLElement>(".hive-companion-bubble");
  const hide = () => {
    if (!bubble) return;
    bubble.hidden = true;
    root!.classList.remove("is-speaking", "kind-info", "kind-success", "kind-error", "kind-attention");
  };
  root.querySelector(".hive-companion-dismiss")?.addEventListener("click", (e) => {
    e.stopPropagation();
    hide();
  });
  root.querySelector(".hive-companion-avatar")?.addEventListener("click", () => {
    const rt = runtime();
    try {
      rt?.WindowUnminimise?.();
      rt?.WindowShow?.();
    } catch {
      /* noop */
    }
    if (bubble && !bubble.hidden) hide();
  });
  return root;
}

function showCompanion(payload: HiveNotifyPayload): void {
  if (typeof document === "undefined") return;
  const root = ensureCompanion();
  const bubble = root.querySelector<HTMLElement>(".hive-companion-bubble");
  const titleEl = root.querySelector<HTMLElement>(".hive-companion-title");
  const bodyEl = root.querySelector<HTMLElement>(".hive-companion-body");
  if (!bubble || !titleEl || !bodyEl) return;
  const kind = payload.kind || "info";
  root.classList.remove("kind-info", "kind-success", "kind-error", "kind-attention");
  root.classList.add("is-speaking", `kind-${kind}`);
  titleEl.textContent = payload.title;
  const body = truncateNotifyBody(payload.body || "");
  bodyEl.textContent = body;
  bodyEl.hidden = !body;
  bubble.hidden = false;
  if (dismissTimer) window.clearTimeout(dismissTimer);
  dismissTimer = window.setTimeout(() => {
    bubble.hidden = true;
    root.classList.remove("is-speaking", `kind-${kind}`);
  }, DISMISS_MS);
}

async function sendDesktop(payload: HiveNotifyPayload): Promise<void> {
  await initHiveNotify();
  const rt = runtime();
  if (!rt?.SendNotification) return;
  try {
    seq += 1;
    await rt.SendNotification({
      id: `hivemind-${Date.now()}-${seq}`,
      title: payload.title,
      body: truncateNotifyBody(payload.body || "", 240) || undefined,
    });
  } catch {
    /* falha silenciosa: companion já cobre o caso em foco */
  }
}

/** Notifica o usuário: companion na tela; toast nativo do SO se a IDE estiver fora de foco. */
export async function hiveNotify(payload: HiveNotifyPayload): Promise<void> {
  const title = String(payload.title || "").trim();
  if (!title) return;
  const clean: HiveNotifyPayload = {
    ...payload,
    title,
    body: payload.body ? truncateNotifyBody(payload.body) : undefined,
  };
  const away = await isHiveAway();
  if (away || payload.forceDesktop) {
    await sendDesktop(clean);
  }
  if (!away) {
    showCompanion(clean);
  }
}

/** Atalho para eventos de lane/agente que o usuário precisa perceber. */
export function hiveNotifyFromLaneEvent(ev: {
  kind?: string;
  text?: string;
  label?: string;
  gate?: string;
}): void {
  const kind = String(ev?.kind || "");
  if (kind === "finish") {
    void hiveNotify({
      title: "HiveMind terminou",
      body: String(ev.text || "Execução concluída."),
      kind: "success",
    });
    return;
  }
  if (kind === "error") {
    void hiveNotify({
      title: "HiveMind — erro",
      body: String(ev.text || "Erro desconhecido"),
      kind: "error",
    });
    return;
  }
  if (kind === "plan_ready") {
    void hiveNotify({
      title: "Plano pronto",
      body: String(ev.label || ev.text || "Aguardando sua aprovação."),
      kind: "attention",
    });
    return;
  }
  if (kind === "approval_required") {
    void hiveNotify({
      title: "Permissão pedida",
      body: String(ev.label || ev.text || "O Chefe precisa da sua autorização."),
      kind: "attention",
    });
    return;
  }
  if (kind === "sdd_gate") {
    const gateLabel =
      ev.gate === "jira"
        ? "Gate 2 · Backlog Jira pronto"
        : String(ev.label || "Gate SDD pronto");
    void hiveNotify({
      title: gateLabel,
      body: String(ev.text || "Revise e aprove para continuar."),
      kind: "attention",
    });
  }
}
