import { icons } from "./icons";
import { api, setStatus, state, toastError } from "./state";
import type { MCPServerInfo } from "./types";

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

export async function refreshMCP(): Promise<void> {
  const backend = api();
  if (!backend?.ListMCPServers) return;
  try {
    state.mcpServers = (await backend.ListMCPServers(state.project?.root || "")) || [];
  } catch {
    state.mcpServers = [];
  }
}

export function renderMCPSidebar(): string {
  const rows = state.mcpServers
    .map((s) => mcpRow(s))
    .join("");
  return `
    <div class="sidebar-header">
      <span class="eyebrow">MCP</span>
      <div class="spacer"></div>
      <button class="icon-btn" id="btn-mcp-refresh" title="Atualizar">${icons.refresh}</button>
    </div>
    <div class="sidebar-body">
      <div class="dim" style="margin-bottom:8px">Context7, o Code Review Graph e o Playwright nascem ligados. O grafo fica indisponível sem uvx e Python 3.10+. Playwright, Chrome DevTools e a extensão Agent Hive não ficam ligados juntos. O mesmo interruptor está em Configurações da IDE. Segredos ficam em variáveis de ambiente, não no Git.</div>
      ${rows || `<div class="dim">Nenhum servidor MCP.</div>`}
    </div>
  `;
}

function mcpBadge(s: MCPServerInfo): { label: string; on: boolean } {
  switch (s.status) {
    case "needs-secret":
    case "error":
    case "unavailable":
      return { label: "indisponível", on: false };
    default:
      return s.enabled ? { label: "ligado", on: true } : { label: "desligado", on: false };
  }
}

function mcpRow(s: MCPServerInfo): string {
  const badge = mcpBadge(s);
  const on = s.enabled;
  return `
    <div class="skill-row">
      <div class="skill-row-head">
        <span class="skill-name">${escapeHtml(s.name)}</span>
        <span class="skill-tag ${badge.on ? "native" : "project"}">${escapeHtml(badge.label)}</span>
      </div>
      <div class="skill-desc">${escapeHtml(s.description || "")}</div>
      <div class="skill-meta">${escapeHtml(s.transport)}${s.status ? ` · ${escapeHtml(s.status)}` : ""}${s.detail ? ` · ${escapeHtml(s.detail)}` : ""}</div>
      <div class="skill-actions">
        <button class="mini ${on ? "" : "primary"}" data-mcp-toggle="${escapeHtml(s.name)}" data-mcp-enabled="${on ? "1" : "0"}">
          ${on ? `${icons.minus}<span>Desligar</span>` : `${icons.plus}<span>Ligar</span>`}
        </button>
        <button class="mini" data-mcp-toggle-project="${escapeHtml(s.name)}" data-mcp-enabled="${on ? "1" : "0"}" title="Grava o interruptor só neste projeto">
          ${icons.folderClosed}<span>Só neste projeto</span>
        </button>
      </div>
    </div>
  `;
}

export function bindMCPSidebarEvents(rerender: () => void): void {
  document.getElementById("btn-mcp-refresh")?.addEventListener("click", () => {
    void refreshMCP().then(rerender);
  });
  const bind = (selector: string, projectOnly: boolean) => {
    document.querySelectorAll(selector).forEach((btn) => {
      btn.addEventListener("click", () => {
        const el = btn as HTMLElement;
        const name = projectOnly ? el.dataset.mcpToggleProject! : el.dataset.mcpToggle!;
        const enabled = el.dataset.mcpEnabled !== "1";
        void setMCPEnabled(name, enabled, projectOnly).then(rerender);
      });
    });
  };
  bind("[data-mcp-toggle]", false);
  bind("[data-mcp-toggle-project]", true);
}

export async function setMCPEnabled(name: string, enabled: boolean, projectOnly: boolean): Promise<void> {
  const backend = api();
  if (!backend?.SetMCPServerEnabled) return;
  if (projectOnly && !state.project) return;
  try {
    state.mcpServers = (await backend.SetMCPServerEnabled(state.project?.root || "", name, enabled, projectOnly)) || [];
    setStatus(`MCP ${name} ${enabled ? "ligado" : "desligado"}${projectOnly ? " neste projeto" : ""}.`);
  } catch (err: any) {
    toastError(`alterar o MCP ${name}`, err);
  }
}
