import { icons } from "./icons";
import type { SkillInfo, SkillTargetRole } from "./types";
import { escapeHtml } from "./utils";
import { renderEditorBody } from "./editor";
import { renderLeftSidebar, updateTeamStatusPills } from "./explorer";
import { activeLane } from "./lanes";
import { TEAM_CONFIG_TAB_KEY, api, setStatus, state, toastError } from "./state";
import { updateMissionBoardIfOpen } from "./teamconfig";

export type ModelProviderId = "default" | "agy" | "opencode-go" | "opencode" | "gemini" | "anthropic" | "openai" | "openrouter";

export function inferModelProvider(modelSpec: string, includeDefaultBlank = false): ModelProviderId {
  const cur = (modelSpec || "").trim();
  if (!cur && includeDefaultBlank) return "default";
  const low = cur.toLowerCase();
  if (low.startsWith("opencode-go:") || low.startsWith("opencode-go/")) return "opencode-go";
  if (low.startsWith("opencode:") || low.startsWith("opencode/") || low === "opencode") return "opencode";
  if (low === "agy" || low.startsWith("agy:") || state.models.some((m) => m.slug.toLowerCase() === low)) return "agy";
  if (low.includes("/")) return "openrouter";
  if (low.startsWith("gemini")) return "gemini";
  if (low.startsWith("claude")) return "anthropic";
  if (low.startsWith("gpt") || low.startsWith("o1") || low.startsWith("o3") || low.startsWith("o4") || low.startsWith("grok")) {
    return "openai";
  }
  if (state.openCodeGoModels.some((m) => m.slug.toLowerCase() === low)) return "opencode-go";
  if (state.openCodeModels.some((m) => m.slug.toLowerCase() === low)) return "opencode";
  if (!cur && state.openCodeStatus?.configured && state.openCodeGoModels.length > 0) return "opencode-go";
  if (!cur && state.models.length === 0 && state.openCodeModels.length > 0) return "opencode";
  return "agy";
}

export function getProviderList(includeDefaultBlank = false): Array<{ id: ModelProviderId; label: string }> {
  const out: Array<{ id: ModelProviderId; label: string }> = [];
  if (includeDefaultBlank) {
    out.push({ id: "default", label: "(usar modelo padrão)" });
  }
  const agyCount = state.models.length > 0 ? ` (${state.models.length})` : "";
  const goCount = state.openCodeGoModels.length > 0 ? ` (${state.openCodeGoModels.length})` : "";
  const zenCount = state.openCodeModels.length > 0 ? ` (${state.openCodeModels.length})` : "";
  out.push(
    { id: "agy", label: `Antigravity (agy)${agyCount}` },
    { id: "opencode-go", label: `OpenCode Go${goCount}` },
    { id: "opencode", label: `OpenCode Zen${zenCount}` },
    { id: "gemini", label: "Google Gemini" },
    { id: "anthropic", label: "Anthropic (Claude)" },
    { id: "openai", label: "OpenAI / Grok (API)" },
    { id: "openrouter", label: "OpenRouter (gateway)" },
  );
  return out;
}

export function getModelsForProvider(providerId: ModelProviderId, currentModel = ""): Array<{ value: string; label: string }> {
  const cur = (currentModel || "").trim();
  let items: Array<{ value: string; label: string }> = [];
  switch (providerId) {
    case "default":
      return [{ value: "", label: "(herdar modelo padrão)" }];
    case "agy":
      items = state.models.length > 0
        ? state.models.map((m) => ({ value: `agy:${m.slug}`, label: m.slug }))
        : [{ value: "agy", label: "agy (padrão)" }];
      break;
    case "opencode-go":
      items = state.openCodeGoModels.length > 0
        ? state.openCodeGoModels.map((m) => ({ value: `opencode-go:${m.slug}`, label: m.slug }))
        : [{ value: "opencode-go:glm-5.3", label: "glm-5.3" }];
      break;
    case "opencode":
      items = state.openCodeModels.length > 0
        ? state.openCodeModels.map((m) => ({ value: `opencode:${m.slug}`, label: m.slug }))
        : [{ value: "opencode:big-pickle", label: "big-pickle" }];
      break;
    case "gemini":
      items = [
        { value: "gemini-2.5-pro", label: "gemini-2.5-pro" },
        { value: "gemini-2.5-flash", label: "gemini-2.5-flash" },
        { value: "gemini-flash-latest", label: "gemini-flash-latest" },
      ];
      break;
    case "anthropic":
      items = [
        { value: "claude-sonnet-4-5", label: "claude-sonnet-4-5" },
        { value: "claude-opus-4-1", label: "claude-opus-4-1" },
        { value: "claude-haiku-4-5", label: "claude-haiku-4-5" },
      ];
      break;
    case "openai":
      items = [
        { value: "gpt-5", label: "gpt-5" },
        { value: "gpt-4.1", label: "gpt-4.1" },
        { value: "o3", label: "o3" },
        { value: "grok-4", label: "grok-4 (OPENAI_BASE_URL)" },
      ];
      break;
    case "openrouter":
      items = state.openRouterModels.length > 0
        ? state.openRouterModels.map((m) => ({ value: m.id, label: m.id }))
        : [
            { value: "anthropic/claude-sonnet-4.5", label: "anthropic/claude-sonnet-4.5 (carregando catálogo…)" },
            { value: "openai/gpt-4.1", label: "openai/gpt-4.1" },
            { value: "google/gemini-2.5-pro", label: "google/gemini-2.5-pro" },
            { value: "deepseek/deepseek-chat", label: "deepseek/deepseek-chat" },
            { value: "qwen/qwen3-coder", label: "qwen/qwen3-coder" },
          ];
      break;
  }
  const normalizedCur = cur.replace("/", ":");
  if (cur && inferModelProvider(cur, false) === providerId && !items.some((x) => x.value === cur || x.value === normalizedCur)) {
    const shortLabel = cur.replace(/^(agy|opencode-go|opencode)[:/]/i, "");
    items = [{ value: cur, label: shortLabel || cur }, ...items];
  }
  return items;
}

export function buildModelOptionsForProviderHtml(providerId: ModelProviderId, currentModel = ""): string {
  const cur = (currentModel || "").trim();
  const normalizedCur = cur.replace("/", ":");
  const items = getModelsForProvider(providerId, cur);
  let matched = false;
  const html = items
    .map((it, i) => {
      const isMatch = cur
        ? it.value === cur || it.value === normalizedCur || it.label === cur
        : i === 0;
      if (isMatch) matched = true;
      return `<option value="${escapeHtml(it.value)}" ${isMatch ? "selected" : ""}>${escapeHtml(it.label)}</option>`;
    })
    .join("");
  if (!matched && items.length > 0) {
    return items
      .map((it, i) => `<option value="${escapeHtml(it.value)}" ${i === 0 ? "selected" : ""}>${escapeHtml(it.label)}</option>`)
      .join("");
  }
  return html;
}

export function renderModelPickerPairHtml(opts: {
  pairKey: string;
  selectId?: string;
  dataAttr?: string;
  dataAttrVal?: string;
  currentModel: string;
  includeDefaultBlank?: boolean;
  disabled?: boolean;
  compact?: boolean;
}): string {
  const includeBlank = Boolean(opts.includeDefaultBlank);
  const prov = inferModelProvider(opts.currentModel, includeBlank);
  const providers = getProviderList(includeBlank);
  const provOpts = providers
    .map((p) => `<option value="${p.id}" ${p.id === prov ? "selected" : ""}>${escapeHtml(p.label)}</option>`)
    .join("");
  const modelOpts = buildModelOptionsForProviderHtml(prov, opts.currentModel);
  const idAttr = opts.selectId ? `id="${escapeHtml(opts.selectId)}"` : "";
  const extraAttr = opts.dataAttr && opts.dataAttrVal ? `${opts.dataAttr}="${escapeHtml(opts.dataAttrVal)}"` : "";
  const disAttr = opts.disabled ? "disabled" : "";
  const modelDis = opts.disabled || prov === "default" ? "disabled" : "";
  return `
    <div class="model-picker-pair ${opts.compact ? "compact" : ""}" data-mp-pair="${escapeHtml(opts.pairKey)}">
      <select class="mp-provider-select" data-mp-provider="${escapeHtml(opts.pairKey)}" data-mp-allow-blank="${includeBlank ? "1" : "0"}" aria-label="Provedor" ${disAttr}>
        ${provOpts}
      </select>
      <select ${idAttr} class="mp-model-select" data-mp-model="${escapeHtml(opts.pairKey)}" ${extraAttr} aria-label="Modelo" ${modelDis}>
        ${modelOpts}
      </select>
    </div>
  `;
}

export function bindModelPickerPairs(
  container: ParentNode | null,
  onModelChange?: (modelSelect: HTMLSelectElement, newValue: string) => void
) {
  if (!container) return;
  const loadOpenRouterCatalog = async (modelSel: HTMLSelectElement) => {
    const backend = api();
    if (!backend?.ListOpenRouterModels) return;
    if (state.openRouterModels.length === 0) {
      try {
        state.openRouterModels = (await backend.ListOpenRouterModels()) || [];
      } catch {
        return; // mantém a lista resumida de fallback
      }
    }
    const current = modelSel.value;
    modelSel.innerHTML = buildModelOptionsForProviderHtml("openrouter", current || "");
    if (current && Array.from(modelSel.options).some((o) => o.value === current)) {
      modelSel.value = current;
    }
  };
  container.querySelectorAll<HTMLSelectElement>("[data-mp-provider]").forEach((provSel) => {
    provSel.addEventListener("change", () => {
      const key = provSel.dataset.mpProvider!;
      const modelSel = container.querySelector<HTMLSelectElement>(`[data-mp-model="${CSS.escape(key)}"]`);
      if (!modelSel) return;
      const provId = provSel.value as ModelProviderId;
      modelSel.innerHTML = buildModelOptionsForProviderHtml(provId, "");
      modelSel.disabled = provId === "default" || provSel.disabled;
      if (provId === "openrouter") void loadOpenRouterCatalog(modelSel);
      if (onModelChange) {
        onModelChange(modelSel, modelSel.value);
      }
    });
    if (provSel.value === "openrouter") {
      const key = provSel.dataset.mpProvider!;
      const modelSel = container.querySelector<HTMLSelectElement>(`[data-mp-model="${CSS.escape(key)}"]`);
      if (modelSel) void loadOpenRouterCatalog(modelSel);
    }
  });
  if (onModelChange) {
    container.querySelectorAll<HTMLSelectElement>("[data-mp-model]").forEach((modelSel) => {
      modelSel.addEventListener("change", () => {
        onModelChange(modelSel, modelSel.value);
      });
    });
  }
}

export async function handleInlineModelChange(agentName: string, newModel: string) {
  const backend = api();
  const lane = activeLane();
  if (!backend || !state.project || !lane || !newModel) return;
  try {
    const cfg = await backend.UpdateAgentModel(state.project.root, lane.teamName, agentName, newModel);
    state.project.teamConfig = cfg;
    for (const ag of lane.agents) {
      if (ag.name === agentName) ag.model = newModel;
    }
    if (state.sidebarView === "team" && state.showLeftSidebar) updateTeamStatusPills();
    updateMissionBoardIfOpen();
    setStatus(`Modelo de ${agentName} atualizado para ${newModel}.`);
  } catch (err: any) {
    toastError(`trocar o modelo do agente "${agentName}"`, err);
  }
}

export function skillTargetLabel(role: SkillTargetRole): string {
  switch (role) {
    case "all":
      return "Todos";
    case "chief":
      return "Chefe";
    case "dev":
      return "Dev";
    case "qa":
      return "QA";
  }
}

export function getAssignedSkillRoles(name: string, source?: string): SkillTargetRole[] {
  const list = state.project?.teamConfig?.settings?.skill_assignments || [];
  const normName = name.toLowerCase();
  const hit =
    list.find((a) => a.name.toLowerCase() === normName && (!a.source || a.source === source)) ||
    list.find((a) => a.name.toLowerCase() === normName);
  return hit?.roles || [];
}

export function renderSkillRowHtml(s: SkillInfo, isNative: boolean): string {
  const assignedRoles = getAssignedSkillRoles(s.name, s.source);
  const roleBadges =
    assignedRoles.length === 0
      ? `<span class="skill-role-badge auto" title="Sem papel fixo: o Chefe decide quando usar">sob demanda</span>`
      : assignedRoles
          .map(
            (r) =>
              `<span class="skill-role-badge ${escapeHtml(r)}" title="Injetada automaticamente para ${escapeHtml(skillTargetLabel(r))}">${escapeHtml(skillTargetLabel(r))}</span>`
          )
          .join("");
  return `
    <div class="skill-row">
      <div class="skill-row-head">
        <span class="skill-name">${escapeHtml(s.name)}</span>
        <span class="skill-tag ${isNative ? "native" : "project"}">${isNative ? "nativa" : "projeto"}</span>
      </div>
      <div class="skill-roles-strip">${roleBadges}</div>
      <div class="skill-desc" title="${escapeHtml(s.description)}">${escapeHtml(s.description || "sem descrição")}</div>
      <div class="skill-meta">${escapeHtml(s.updatedAt || "")}${s.sizeBytes > 0 ? ` · ${(s.sizeBytes / 1024).toFixed(1)} KB` : ""}</div>
      <div class="skill-actions">
        <button class="mini" data-skill-assign="${escapeHtml(s.name)}" title="Definir para quem esta skill vai (Chefe, Dev, QA ou Todos)">${icons.team}<span>Papéis</span></button>
        ${isNative
          ? `<button class="mini" data-skill-edit="${escapeHtml(s.name)}" title="Editar no editor de skills">${icons.editPen}<span>Editar</span></button>
             <button class="mini" data-skill-install="${escapeHtml(s.name)}" title="Copiar para .agents/skills deste projeto">${icons.download}<span>Instalar no projeto</span></button>
             <button class="mini danger" data-skill-delete-native="${escapeHtml(s.name)}" title="Excluir skill nativa da IDE">${icons.trash}</button>`
          : `<button class="mini danger" data-skill-remove-project="${escapeHtml(s.name)}" title="Remover do projeto (.agents/skills)">${icons.trash}<span>Remover</span></button>`
        }
      </div>
    </div>
  `;
}

export async function refreshSkills(): Promise<void> {
  const backend = api();
  if (!backend || !state.project) return;
  try {
    state.skills = (await backend.ListSkills(state.project.root)) || [];
  } catch {
    state.skills = [];
  }
  if (state.sidebarView === "skills" && state.showLeftSidebar) {
    renderLeftSidebar();
  }
  if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) {
    renderEditorBody();
  }
}
