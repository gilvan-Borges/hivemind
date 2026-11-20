import { icons } from "./icons";
import type { SkillInfo, SkillTargetRole } from "./types";
import { escapeHtml } from "./utils";
import { renderEditorBody } from "./editor";
import { renderLeftSidebar, updateTeamStatusPills } from "./explorer";
import { activeLane } from "./lanes";
import { TEAM_CONFIG_TAB_KEY, api, setStatus, state, toastError } from "./state";
import { updateMissionBoardIfOpen } from "./teamconfig";

export type ModelProviderId =
  | "default"
  | "jev"
  | "agy"
  | "opencode-go"
  | "opencode"
  | "gemini"
  | "anthropic"
  | "openai"
  | "openrouter"
  | "cursor"
  | string;

function splitProviderAccount(providerId: string): { baseProv: string; accountId: string } {
  const raw = (providerId || "").trim();
  const atIdx = raw.indexOf("@");
  if (atIdx > 0) {
    return { baseProv: raw.slice(0, atIdx).toLowerCase(), accountId: raw.slice(atIdx + 1) };
  }
  return { baseProv: raw.toLowerCase(), accountId: "" };
}

export function inferModelProvider(modelSpec: string, includeDefaultBlank = false): ModelProviderId {
  const cur = (modelSpec || "").trim();
  if (!cur && includeDefaultBlank) return "default";
  const low = cur.toLowerCase();
  const atIdx = low.indexOf("@");
  if (atIdx > 0) {
    const pref = low.slice(0, atIdx);
    const rest = cur.slice(atIdx + 1);
    const colonIdx = rest.search(/[:/]/);
    const accId = (colonIdx > 0 ? rest.slice(0, colonIdx) : rest).trim();
    const normPref = pref === "antigravity" ? "agy" : pref;
    if (accId && (state.profilesOverview?.profiles || []).some((p) => p.provider === normPref && p.id.toLowerCase() === accId.toLowerCase())) {
      return `${normPref}@${accId}`;
    }
    if (pref === "agy" || pref === "antigravity") return "agy";
    if (pref === "openrouter") return "openrouter";
    if (pref === "opencode-go") return "opencode-go";
    if (pref === "opencode") return "opencode";
    if (pref === "gemini") return "gemini";
    if (pref === "anthropic") return "anthropic";
    if (pref === "openai") return "openai";
    if (pref === "cursor") return "cursor";
  }
  if (low === "openrouter:auto" || low === "openrouter/auto" || low === "openrouter:jev") return "openrouter";
  if (low === "opencode-go:auto" || low === "opencode-go/auto") return "opencode-go";
  if (low === "opencode:auto" || low === "opencode/auto") return "opencode";
  if (low === "agy:auto" || low === "agy/auto") return "agy";
  if (low === "gemini:auto" || low === "gemini/auto") return "gemini";
  if (low === "jev" || low === "jev:auto" || low === "jev/auto" || low.startsWith("jev:") || low === "auto") return "jev";
  if (low === "cursor" || low.startsWith("cursor:") || low.startsWith("cursor/")) return "cursor";
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
  const allProfiles = state.profilesOverview?.profiles || [];
  const agyProfiles = allProfiles.filter((p) => p.provider === "agy");
  const orProfiles = allProfiles.filter((p) => p.provider === "openrouter");
  const ocProfiles = allProfiles.filter((p) => p.provider === "opencode");

  out.push({ id: "jev", label: "Jev (Auto)" });
  if (agyProfiles.length > 0) {
    out.push({ id: "agy", label: `Antigravity (Pool Auto: Conta 1 → ${agyProfiles.length + 1})${agyCount}` });
    agyProfiles.forEach((p, idx) => {
      out.push({ id: `agy@${p.id}`, label: `Antigravity · Conta ${idx + 1} (${p.name})` });
    });
  } else {
    out.push({ id: "agy", label: `Antigravity (agy)${agyCount}` });
  }
  out.push(
    { id: "opencode-go", label: `OpenCode Go${goCount}` },
    { id: "opencode", label: `OpenCode Zen${zenCount}` },
  );
  ocProfiles.forEach((p, idx) => {
    out.push({ id: `opencode@${p.id}`, label: `OpenCode · Conta ${idx + 1} (${p.name})` });
  });
  out.push(
    { id: "gemini", label: "Google Gemini" },
    { id: "anthropic", label: "Anthropic (Claude)" },
    { id: "openai", label: "OpenAI / Grok" },
  );
  if (orProfiles.length > 0) {
    out.push({ id: "openrouter", label: `OpenRouter (Pool Auto: Conta 1 → ${orProfiles.length})` });
    orProfiles.forEach((p, idx) => {
      out.push({ id: `openrouter@${p.id}`, label: `OpenRouter · Conta ${idx + 1} (${p.name})` });
    });
  } else {
    out.push({ id: "openrouter", label: "OpenRouter" });
  }
  out.push({ id: "cursor", label: "Cursor CLI" });
  return out;
}

export function getModelsForProvider(providerId: ModelProviderId, currentModel = ""): Array<{ value: string; label: string }> {
  const cur = (currentModel || "").trim();
  const { baseProv, accountId } = splitProviderAccount(providerId);
  const accPref = accountId ? `@${accountId}` : "";
  let items: Array<{ value: string; label: string }> = [];
  switch (baseProv) {
    case "default":
      return [{ value: "", label: "(herdar modelo padrão)" }];
    case "jev":
      return [
        { value: "jev:auto", label: "auto — Todos os provedores ativos (por dificuldade + promoção)" },
        { value: "jev:openrouter", label: "openrouter — Só modelos do OpenRouter (Jev escolhe c/ promoção)" },
        { value: "jev:opencode", label: "opencode — Só modelos do OpenCode Go/Zen (Jev escolhe)" },
        { value: "jev:agy", label: "agy — Só modelos do Antigravity (Jev escolhe c/ Pool de Contas)" },
        { value: "jev:gemini", label: "gemini — Só modelos do Google Gemini (Jev escolhe)" },
      ];
    case "agy": {
      const agyProfiles = (state.profilesOverview?.profiles || []).filter((p) => p.provider === "agy");
      const pinnedProf = accountId ? agyProfiles.find((p) => p.id.toLowerCase() === accountId.toLowerCase()) : undefined;
      const poolHint = pinnedProf
        ? ` (${pinnedProf.name} → reserva nas demais)`
        : agyProfiles.length > 0
          ? ` — Pool (Conta 1 → ${agyProfiles.length + 1} por modelo)`
          : "";
      const baseModels = state.models.length > 0
        ? state.models.map((m) => ({ slug: m.slug, value: `agy${accPref}:${m.slug}`, label: `${m.slug}${poolHint}` }))
        : [{ slug: "agy", value: `agy${accPref}`, label: `agy (padrão)${poolHint}` }];
      items = [
        { value: `agy${accPref}:auto`, label: `auto (Jev escolhe no Antigravity${poolHint})` },
        ...baseModels.map((m) => ({ value: m.value, label: m.label })),
      ];
      if (!accountId) {
        for (const prof of agyProfiles) {
          for (const m of baseModels) {
            const val = m.slug === "agy" ? `agy@${prof.id}` : `agy@${prof.id}:${m.slug}`;
            items.push({ value: val, label: `[Conta: ${prof.name}] ${m.slug}` });
          }
        }
      }
      break;
    }
    case "opencode-go":
      items = [
        { value: "opencode:auto", label: "auto (Jev escolhe no OpenCode)" },
        ...(state.openCodeGoModels.length > 0
          ? state.openCodeGoModels.map((m) => ({ value: `opencode-go:${m.slug}`, label: m.slug }))
          : [{ value: "opencode-go:glm-5.3", label: "glm-5.3" }]),
      ];
      break;
    case "opencode":
      items = [
        { value: "opencode:auto", label: "auto (Jev escolhe no OpenCode)" },
        ...(state.openCodeModels.length > 0
          ? state.openCodeModels.map((m) => ({ value: `opencode:${m.slug}`, label: m.slug }))
          : [{ value: "opencode:big-pickle", label: "big-pickle" }]),
      ];
      break;
    case "gemini":
      items = [
        { value: "gemini:auto", label: "auto (Jev escolhe no Gemini)" },
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
    case "cursor":
      items = [{ value: "cursor:auto", label: "auto (padrão, a Cursor escolhe)" }];
      for (const slug of state.cursorModels) {
        if (!slug || slug.toLowerCase() === "auto") continue;
        items.push({ value: `cursor:${slug}`, label: slug });
      }
      break;
    case "openrouter": {
      const curatedOR: Array<{ value: string; label: string }> = [
        { value: "google/gemini-4-carbon", label: "★ Google Gemini 4 Carbon -50% (google/gemini-4-carbon · 1M · Promoção)" },
        { value: "deepseek/deepseek-v4.1-flash-1017", label: "★ DeepSeek V4.1 Flash 1017 (deepseek/deepseek-v4.1-flash-1017 · 1.05M)" },
        { value: "deepseek/deepseek-v4.1-flash", label: "★ DeepSeek V4.1 Flash (deepseek/deepseek-v4.1-flash · 1.05M)" },
        { value: "deepseek/deepseek-v4-pro", label: "★ DeepSeek V4 Pro (deepseek/deepseek-v4-pro · 1M · Engenharia pesada)" },
        { value: "google/gemini-4-neon", label: "★ Google Gemini 4 Neon (google/gemini-4-neon · 1M · Coding agêntico)" },
        { value: "google/gemini-4-argon", label: "★ Google Gemini 4 Argon (google/gemini-4-argon · 1M · Frontier)" },
        { value: "google/gemini-3.8-flash", label: "★ Google Gemini 3.8 Flash (google/gemini-3.8-flash · 1M · Multimodal)" },
        { value: "openai/gpt-6.1-sol", label: "★ OpenAI GPT-6.1 Sol (openai/gpt-6.1-sol · 1M · Multi-arquivo)" },
        { value: "openai/gpt-6.1-terra", label: "★ OpenAI GPT-6.1 Terra (openai/gpt-6.1-terra · 1M · Coding & Testes)" },
        { value: "openai/gpt-6-luna", label: "★ OpenAI GPT-6 Luna (openai/gpt-6-luna · 1M · Rápido/JSON)" },
        { value: "anthropic/claude-opus-5.5", label: "★ Claude Opus 5.5 (anthropic/claude-opus-5.5 · 1M · Arquitetura/Revisão)" },
        { value: "anthropic/claude-haiku-5.5", label: "★ Claude Haiku 5.5 (anthropic/claude-haiku-5.5 · 200K · Ultra rápido)" },
        { value: "x-ai/grok-4.7", label: "★ Grok 4.7 (x-ai/grok-4.7 · 1M · Long-running tasks)" },
        { value: "z-ai/glm-5.3", label: "★ Z.ai GLM 5.3 (z-ai/glm-5.3 · 1M · Programação)" },
        { value: "z-ai/glm-5.3-flash", label: "★ Z.ai GLM 5.3 Flash (z-ai/glm-5.3-flash · 1M · Baixo custo)" },
        { value: "moonshotai/kimi-k3.1-pro", label: "★ Moonshot Kimi K3.1 Pro (moonshotai/kimi-k3.1-pro · 1M · Contexto longo)" },
        { value: "qwen/qwen3-coder", label: "★ Qwen3 Coder 480B A35B (qwen/qwen3-coder · 1M · MoE Coding)" },
        { value: "tencent/hy4", label: "★ Tencent Hy4 (tencent/hy4 · 1.05M · Agêntico/Visual)" },
        { value: "tencent/hy4-flash", label: "★ Tencent Hy4 Flash (tencent/hy4-flash · 1.05M · Alto volume)" },
        { value: "particle-ai/particle-1:free", label: "★ Particle-1 Gratuito (particle-ai/particle-1:free · 1.05M · Free)" },
        { value: "cohere/north-mini-code:free", label: "★ Cohere North Mini Code Gratuito (cohere/north-mini-code:free · 256K · Free)" },
      ];
      const curatedSet = new Set(curatedOR.map((c) => c.value.toLowerCase()));
      const extraOR = state.openRouterModels
        .filter((m) => !curatedSet.has(m.id.toLowerCase()))
        .map((m) => ({
          value: m.id,
          label: m.discountPct && m.discountPct > 0 ? `${m.id} (-${m.discountPct}% OFF)` : m.id,
        }));
      items = [
        { value: "openrouter:auto", label: "auto (Jev escolhe por dificuldade da tarefa + promoções do dia)" },
        ...curatedOR,
        ...extraOR,
      ];
      break;
    }
  }
  const normalizedCur = cur.replace("/", ":");
  if (cur && inferModelProvider(cur, false) === providerId && !items.some((x) => x.value === cur || x.value === normalizedCur)) {
    const shortLabel = cur.replace(/^(agy|opencode-go|opencode|jev)[:/]/i, "");
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
  const loadCursorCatalog = async (modelSel: HTMLSelectElement) => {
    const backend = api();
    if (!backend?.ListCursorModels) return;
    try {
      const listed = (await backend.ListCursorModels()) || [];
      state.cursorModels = listed.map((m) => m.slug).filter((slug) => slug && slug.toLowerCase() !== "auto");
      if (backend.CursorCLIStatus) state.cursorCLI = await backend.CursorCLIStatus();
    } catch {
      return;
    }
    const current = modelSel.value;
    modelSel.innerHTML = buildModelOptionsForProviderHtml("cursor", current || "cursor:auto");
    if (current && Array.from(modelSel.options).some((o) => o.value === current)) {
      modelSel.value = current;
    }
    if (state.cursorCLI && !state.cursorCLI.found) {
      setStatus(state.cursorCLI.detail);
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
      if (provId === "cursor") void loadCursorCatalog(modelSel);
      if (onModelChange) {
        onModelChange(modelSel, modelSel.value);
      }
    });
    if (provSel.value === "cursor") {
      const key = provSel.dataset.mpProvider!;
      const modelSel = container.querySelector<HTMLSelectElement>(`[data-mp-model="${CSS.escape(key)}"]`);
      if (modelSel) void loadCursorCatalog(modelSel);
    }
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
        ${s.enabled === false ? `<span class="skill-tag project">desligada</span>` : ""}
      </div>
      <div class="skill-roles-strip">${roleBadges}</div>
      <div class="skill-desc" title="${escapeHtml(s.description)}">${escapeHtml(s.description || "sem descrição")}</div>
      <div class="skill-meta">${escapeHtml(s.updatedAt || "")}${s.sizeBytes > 0 ? ` · ${(s.sizeBytes / 1024).toFixed(1)} KB` : ""}</div>
      <div class="skill-actions">
        <button class="mini" data-skill-assign="${escapeHtml(s.name)}" title="Definir para quem esta skill vai (Chefe, Dev, QA ou Todos)">${icons.team}<span>Papéis</span></button>
        ${isNative && s.enabled === false
          ? `<button class="mini primary" data-skill-enable="${escapeHtml(s.name)}">${icons.plus}<span>Ativar</span></button>`
          : ""}
        ${isNative && s.name === "untangle-codebase" && s.enabled !== false
          ? `<button class="mini" data-skill-disable="${escapeHtml(s.name)}">${icons.minus}<span>Desligar</span></button>`
          : ""}
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
