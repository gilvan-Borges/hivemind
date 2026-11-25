import type { AgentRuntime, PermissionMode, RunMode } from "./types";

const REASON_PHRASE: Record<string, string> = {
  cheap_default: "o mais barato que cabe",
  pin: "modelo fixado por você",
  context_fit: "cabe na janela",
  circuit_open: "o anterior estava em pausa",
  qa_escalation: "o QA pediu outra tentativa",
  vision: "a tarefa tem imagem",
  spread: "espalhado entre provedores",
  budget_wait: "acima do teto, aguardando você",
  budget_approved: "escalada autorizada",
  account_pool_failover: "a conta estava sem cota",
  dificuldade_alta: "tarefa difícil",
  dificuldade_media: "tarefa média",
};

// reasonPhrase turns Jev reason codes into a short sentence. The model does not write it.
export function reasonPhrase(codes: string[] | undefined | null): string {
  if (!codes || codes.length === 0) return "";
  return codes
    .map((raw) => {
      const code = String(raw || "").trim();
      if (!code) return "";
      if (REASON_PHRASE[code]) return REASON_PHRASE[code];
      if (code.startsWith("promo_")) return `promoção ${code.slice("promo_".length)}`;
      return code;
    })
    .filter(Boolean)
    .join(" · ");
}

export function escapeHtml(text: string | undefined | null): string {
  return String(text ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

const SAFE_DATA_IMAGE_RE = /^data:image\/(?:png|jpeg|jpg|webp|gif|bmp);base64,[A-Za-z0-9+/=\s]+$/i;

export function safeImageSrc(raw: string | undefined | null): string {
  const trimmed = String(raw ?? "").trim();
  if (SAFE_DATA_IMAGE_RE.test(trimmed)) {
    return trimmed;
  }
  return "";
}

export function formatTokens(n: number | undefined | null): string {
  const val = Number(n || 0);
  if (val <= 0) return "0";
  if (val >= 1_000_000) return `${(val / 1_000_000).toFixed(2)}M`;
  if (val >= 1_000) return `${(val / 1_000).toFixed(1)}k`;
  return String(val);
}

export function exactTokens(n: number | undefined | null): string {
  return Number(n || 0).toLocaleString("pt-BR");
}

export function formatMicroUsd(microUsd: number | undefined | null): string {
  const val = Number(microUsd || 0);
  if (!Number.isFinite(val) || val <= 0) return "$0.00";
  const usd = val / 1_000_000;
  if (usd < 1) return `$${usd.toFixed(4)}`;
  return `$${usd.toFixed(2)}`;
}

export function formatElapsedDuration(ms: number): string {
  const totalSec = Math.max(0, Math.floor(ms / 1000));
  const hours = Math.floor(totalSec / 3600);
  const mins = Math.floor((totalSec % 3600) / 60);
  const secs = totalSec % 60;
  if (hours > 0) return `${hours}h ${mins}m ${secs}s`;
  if (mins > 0) return `${mins}m ${secs}s`;
  return `${secs}s`;
}

export function updateAgentRuntimeStatus(
  agent: AgentRuntime,
  status: AgentRuntime["status"],
  tool: string | undefined,
  now = Date.now()
): void {
  if (status === "running") {
    if (agent.status !== "running" || !agent.runningSince) {
      agent.runningSince = now;
    }
  } else {
    agent.runningSince = undefined;
  }
  agent.status = status;
  if (tool !== undefined) {
    agent.tool = tool;
  } else if (status !== "running") {
    agent.tool = "";
  }
}

export function agentRoleStageGroup(role: string): "chief" | "dev" | "qa" {
  if (role === "developer") return "dev";
  if (role === "qa") return "qa";
  return "chief";
}

export function normalizeStageGroup(stage: string): "chief" | "dev" | "qa" | "" {
  const s = (stage || "").trim().toLowerCase();
  if (s === "dev" || s === "developer") return "dev";
  if (s === "qa") return "qa";
  if (s === "chief" || s === "closing" || s === "triage" || s === "architect") return "chief";
  return "";
}

export function completePreviousStageAgents(
  agents: AgentRuntime[],
  activeStage: string,
  now = Date.now()
): void {
  const targetGroup = normalizeStageGroup(activeStage);
  if (!targetGroup) return;
  for (const a of agents) {
    if (a.status === "running" && agentRoleStageGroup(a.role) !== targetGroup) {
      updateAgentRuntimeStatus(a, "done", "", now);
    }
  }
}

export function transitionAgentToRunning(
  agents: AgentRuntime[],
  target: AgentRuntime,
  tool?: string,
  now = Date.now()
): void {
  const targetGroup = agentRoleStageGroup(target.role);
  for (const a of agents) {
    if (a !== target && a.status === "running" && agentRoleStageGroup(a.role) !== targetGroup) {
      updateAgentRuntimeStatus(a, "done", "", now);
    }
  }
  updateAgentRuntimeStatus(target, "running", tool, now);
}

export function stopRunningAgentRuntimes(
  agents: AgentRuntime[],
  finalStatus: "done" | "idle" | "error" = "done",
  erroredAuthor?: string,
  erroredLabel?: string,
  now = Date.now()
): void {
  const matchedErrored =
    finalStatus === "error" && (erroredAuthor || erroredLabel)
      ? matchAgentRuntime(agents, erroredAuthor || "", erroredLabel)
      : undefined;
  for (const a of agents) {
    if (a.status === "running") {
      if (finalStatus === "error") {
        const isErroredAgent = matchedErrored ? a === matchedErrored : true;
        updateAgentRuntimeStatus(a, isErroredAgent ? "error" : "done", "", now);
      } else {
        updateAgentRuntimeStatus(a, finalStatus, "", now);
      }
    } else {
      a.runningSince = undefined;
      if (a.tool) a.tool = "";
    }
  }
}

export function roleLabelPt(role: string): string {
  switch (role) {
    case "architect":
      return "Arquiteto (Chefe)";
    case "project_manager":
      return "Gerente de Projeto (Chefe)";
    case "tech_lead":
      return "Tech Lead";
    case "developer":
      return "Desenvolvedor";
    case "qa":
      return "QA";
    default:
      return role;
  }
}

export function runModeLabel(mode: RunMode): string {
  switch (mode) {
    case "direct":
      return "Direto (1 Agente)";
    case "plan":
      return "Planejar · Feature";
    case "plan:sdd":
      return "Planejar · Projeto SDD";
    case "plan:sdd-jira":
      return "SDD · Backlog Jira";
    case "squad":
      return "Squad Completo";
    case "race":
      return "Race (Candidatos)";
    default:
      return "Auto (Triagem)";
  }
}

export function permissionModeLabel(mode: PermissionMode | string | undefined): string {
  switch (mode) {
    case "full_access":
      return "Full Acesso";
    case "auto_edit":
      return "Auto-Edição";
    default:
      return "Governado";
  }
}

export function matchAgentRuntime(
  agents: AgentRuntime[],
  author: string,
  label?: string
): AgentRuntime | undefined {
  const auth = (author || "").toLowerCase();
  const lbl = (label || "").toLowerCase();
  const byLen = [...agents].sort((a, b) => b.name.length - a.name.length);
  for (const a of byLen) {
    const nm = a.name.toLowerCase();
    if (
      auth === nm ||
      auth.endsWith("_" + nm) ||
      auth.startsWith(nm + "_") ||
      auth.includes(nm) ||
      lbl.includes(nm)
    ) {
      return a;
    }
  }
  for (const a of byLen) {
    const rl = a.role.toLowerCase();
    if (auth.includes(rl) || lbl.includes(rl)) {
      return a;
    }
  }
  return agents[0];
}

export interface ModelChip {
  label: string;
  providerLabel: string;
  fullId: string;
}

const PROVIDER_LABEL: Record<string, string> = {
  cursor: "Cursor",
  openai: "OpenAI",
  anthropic: "Anthropic",
  gemini: "Google",
  google: "Google",
  openrouter: "OpenRouter",
  agy: "Antigravity",
  antigravity: "Antigravity",
  opencode: "OpenCode",
  "opencode-go": "OpenCode",
  xai: "xAI",
  jev: "Jev",
};

function providerLabelOf(prov: string): string {
  const key = prov.trim().toLowerCase();
  if (!key) return "";
  return PROVIDER_LABEL[key] || key.charAt(0).toUpperCase() + key.slice(1);
}

function humanizeModelSlug(slug: string): string {
  const low = slug.toLowerCase();
  if (low.startsWith("gpt-")) return "GPT-" + slug.slice(4);
  if (/^o\d/.test(low)) return slug.toUpperCase();
  if (low.startsWith("grok-")) return "Grok " + slug.slice(5).replace(/-/g, " ");
  if (low.startsWith("composer-")) return "Composer " + slug.slice(9).replace(/-/g, " ");
  if (low.startsWith("gemini")) {
    const rest = slug.slice(6).replace(/^[-_]/, "").replace(/-/g, " ");
    return ("Gemini " + rest).trim();
  }
  if (low.startsWith("claude-")) {
    const parts = slug.slice(7).split("-").filter(Boolean);
    const words: string[] = [];
    for (let i = 0; i < parts.length; i++) {
      const nxt = parts[i + 1] || "";
      if (i === parts.length - 2 && /^\d+$/.test(parts[i]) && /^\d+$/.test(nxt) && nxt.length <= 2) {
        words.push(`${parts[i]}.${nxt}`);
        break;
      }
      words.push(/^\d/.test(parts[i]) ? parts[i] : parts[i].charAt(0).toUpperCase() + parts[i].slice(1));
    }
    return ("Claude " + words.join(" ")).trim();
  }
  return slug
    .split(/[-_]/)
    .filter(Boolean)
    .map((p) => (/^\d/.test(p) ? p : p.charAt(0).toUpperCase() + p.slice(1)))
    .join(" ");
}

// describeModelRoute turns a Jev/pin id into the chip the Equipe IA shows.
// "cursor/cursor:auto" becomes "Cursor Auto"; the full id stays for the hover.
export function describeModelRoute(provider: string, model: string): ModelChip {
  const original = (model || "").trim();
  const given = (provider || "").trim();
  let prov = given;
  const at = prov.indexOf("@");
  if (at > 0) prov = prov.slice(0, at);
  let rest = original;
  if (rest.includes("/")) {
    const i = rest.indexOf("/");
    const head = rest.slice(0, i);
    const tail = rest.slice(i + 1);
    if (!prov) prov = head;
    if (head.toLowerCase() === prov.toLowerCase()) rest = tail;
  }
  if (rest.includes(":")) {
    const i = rest.indexOf(":");
    const head = rest.slice(0, i);
    const tail = rest.slice(i + 1);
    if (!prov) {
      prov = head;
      rest = tail;
    } else if (head.toLowerCase() === prov.toLowerCase()) {
      rest = tail;
    }
  }
  if (prov && rest.toLowerCase().startsWith(prov.toLowerCase() + ":")) {
    rest = rest.slice(prov.length + 1);
  }
  const slug = rest.trim();
  const slugLow = slug.toLowerCase();
  if (!prov) {
    if (slugLow.startsWith("grok")) prov = "xai";
    else if (slugLow.startsWith("gpt") || /^o\d/.test(slugLow)) prov = "openai";
    else if (slugLow.startsWith("claude")) prov = "anthropic";
    else if (slugLow.startsWith("gemini")) prov = "gemini";
    else if (slugLow.startsWith("composer")) prov = "cursor";
  }
  const providerLabel = providerLabelOf(prov);
  const explicit = Boolean(given) || original.includes("/") || original.includes(":");
  let fullId = original;
  if (explicit && prov) {
    const low = original.toLowerCase();
    if (!original) fullId = prov;
    else if (low.startsWith(prov.toLowerCase() + "/")) fullId = original;
    else fullId = `${prov}/${original}`;
  }
  const auto = !slug || slugLow === "auto" || slugLow.endsWith(":auto") || (prov !== "" && slugLow === prov.toLowerCase());
  if (!slug && !prov) return { label: "", providerLabel: "", fullId: "" };
  if (auto) {
    return { label: providerLabel ? `${providerLabel} Auto` : "Auto", providerLabel, fullId };
  }
  return { label: humanizeModelSlug(slug), providerLabel, fullId };
}

export function chipForAgent(agent: Pick<AgentRuntime, "model" | "routeModel" | "routeProvider" | "routeFriendly">): ModelChip {
  if (agent.routeFriendly && agent.routeModel) {
    return {
      label: agent.routeFriendly,
      providerLabel: providerLabelOf(agent.routeProvider || ""),
      fullId: agent.routeModel,
    };
  }
  return describeModelRoute("", agent.model || "");
}

export function modelChipHtml(chip: ModelChip | null | undefined): string {
  if (!chip?.label) return "";
  const tip = [chip.providerLabel, chip.fullId].filter(Boolean).join(" · ");
  const aria = [`Modelo ${chip.label}`, chip.providerLabel ? `provedor ${chip.providerLabel}` : "", chip.fullId ? `identificador ${chip.fullId}` : ""]
    .filter(Boolean)
    .join(", ");
  return `<span class="model-chip" title="${escapeHtml(tip)}" aria-label="${escapeHtml(aria)}">${escapeHtml(chip.label)}</span>`;
}

export function roleHeading(label: string): string {
  const head = (label || "").split("·")[0].trim();
  if (head === "Arquiteto") return "Arquiteto (Chefe)";
  if (head === "Gerente de projeto" || head === "Gerente de Projeto") return "Gerente de Projeto (Chefe)";
  return head || label;
}

export function shouldExpandChat(pref: boolean, openTabs: number): boolean {
  return Boolean(pref) && openTabs <= 0;
}

export type AssistantBlock =
  | { kind: "md"; text: string }
  | { kind: "tools"; tools: Array<{ name: string; target: string }> };

const TOOL_RE = /\[\[hmtool:([A-Za-z0-9_]+):([^\]]*)\]\]/g;
const HIDDEN_TOOL_NAMES = new Set(["startedatms", "completedatms", "toolcallid", "hookadditionalcontexts"]);

export function splitAssistantBlocks(raw: string): AssistantBlock[] {
  const text = raw || "";
  const blocks: AssistantBlock[] = [];
  let last = 0;
  let tools: Array<{ name: string; target: string }> | null = null;
  const flushMd = (chunk: string) => {
    if (!chunk) return;
    const prev = blocks[blocks.length - 1];
    if (prev && prev.kind === "md") prev.text += chunk;
    else blocks.push({ kind: "md", text: chunk });
  };
  const flushTools = () => {
    if (!tools || tools.length === 0) {
      tools = null;
      return;
    }
    blocks.push({ kind: "tools", tools });
    tools = null;
  };
  for (const m of text.matchAll(TOOL_RE)) {
    const idx = m.index ?? 0;
    const before = text.slice(last, idx);
    const name = m[1];
    if (HIDDEN_TOOL_NAMES.has(name.toLowerCase())) {
      if (before.trim() === "") {
        if (!tools) flushMd(before);
      } else {
        flushTools();
        flushMd(before);
      }
      last = idx + m[0].length;
      continue;
    }
    if (before.trim() === "") {
      if (!tools) flushMd(before);
    } else {
      flushTools();
      flushMd(before);
    }
    if (!tools) tools = [];
    tools.push({ name, target: m[2] });
    last = idx + m[0].length;
  }
  flushTools();
  const rest = text.slice(last);
  if (rest.trim() !== "" || blocks.length === 0 || blocks[blocks.length - 1]?.kind === "md") {
    flushMd(rest);
  }
  return blocks;
}

export function cursorToolPhrase(name: string, target: string): { verb: string; target: string } {
  const n = name.toLowerCase();
  const rel = target.replace(/\\/g, "/").replace(/^\.\//, "").trim();
  const base = rel.split("/").filter(Boolean).pop() || rel;
  const fileLike = /[^/]+\.[A-Za-z0-9]+$/.test(rel);
  const place = !rel ? "o projeto" : fileLike && rel.includes("/") ? rel.slice(0, rel.lastIndexOf("/")) : rel;
  if (n === "read" || n === "read_file") return { verb: "Leu", target: base || "arquivo" };
  if (n.includes("grep") || n === "search" || n === "search_text" || n.includes("ripgrep")) {
    return { verb: "Buscou em", target: place };
  }
  if (n.includes("glob") || n === "find" || n === "find_files") return { verb: "Buscou", target: rel || "arquivos" };
  if (n.includes("edit") || n.includes("strreplace") || n === "search_replace") return { verb: "Editou", target: base || "arquivo" };
  if (n.includes("write")) return { verb: "Gravou", target: base || "arquivo" };
  if (n.includes("shell") || n === "bash" || n === "run") return { verb: "Executou", target: rel || "comando" };
  if (n.includes("mcp")) return { verb: "Consultou", target: rel || "ferramentas" };
  if (n.includes("list")) return { verb: "Listou", target: rel || "diretório" };
  return { verb: "Executou", target: rel || name };
}

export function renderSkeletonCardsHtml(count = 3, ariaLabel = "Carregando…"): string {
  const n = Math.max(1, Math.min(6, count));
  const cards: string[] = [];
  for (let i = 0; i < n; i++) {
    cards.push(`
      <div class="hm-skeleton-card" aria-hidden="true">
        <div class="hm-skeleton-head">
          <span class="hm-skeleton-pill"></span>
          <span class="hm-skeleton-line w-35"></span>
        </div>
        <div class="hm-skeleton-line w-85"></div>
        <div class="hm-skeleton-line w-60"></div>
        <div class="hm-skeleton-foot">
          <span class="hm-skeleton-pill wide"></span>
          <span class="hm-skeleton-pill"></span>
        </div>
      </div>
    `);
  }
  return `<div class="hm-skeleton-stack" role="status" aria-live="polite" aria-label="${escapeHtml(ariaLabel)}">${cards.join("")}</div>`;
}

export function renderSkeletonRowsHtml(count = 5, ariaLabel = "Carregando…"): string {
  const n = Math.max(1, Math.min(10, count));
  const widths = ["w-75", "w-60", "w-85", "w-50", "w-70"];
  const rows: string[] = [];
  for (let i = 0; i < n; i++) {
    rows.push(`
      <div class="hm-skeleton-row" aria-hidden="true">
        <span class="hm-skeleton-dot"></span>
        <span class="hm-skeleton-line ${widths[i % widths.length]}"></span>
      </div>
    `);
  }
  return `<div class="hm-skeleton-rows" role="status" aria-live="polite" aria-label="${escapeHtml(ariaLabel)}">${rows.join("")}</div>`;
}
