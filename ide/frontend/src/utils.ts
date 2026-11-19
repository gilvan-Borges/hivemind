import type { AgentRuntime, PermissionMode, RunMode } from "./types";

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
  if (status === "running" && (agent.status !== "running" || !agent.runningSince)) {
    agent.runningSince = now;
  }
  agent.status = status;
  if (tool !== undefined) agent.tool = tool;
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
