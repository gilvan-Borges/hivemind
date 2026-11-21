import { icons } from "./icons";
import type { AgyAccountQuota, AIProviderUsage, AIUsageReport, GitHubStatus, JevPolicy, JiraProfileSummary, ProviderHealthView, ProviderProfileSummary, ProvidersScanReport } from "./types";
import { escapeHtml, formatTokens, safeImageSrc } from "./utils";
import { openFileInEditor, renderEditorBody, renderEditorTabs } from "./editor";
import { refreshExplorerTree, renderLeftSidebar } from "./explorer";
import { loadGitHubPRs, loadProjectIssues, refreshDiffs } from "./git";
import { trapFocusIn, type FocusTrapHandle } from "./focus-trap";
import { notifyRegionRendered, renderGuard } from "./render-guard";
import { refreshSkills } from "./models";
import { updateStatusBar } from "./shell";
import { AI_USAGE_TAB_KEY, TEAM_CONFIG_TAB_KEY, api, setStatus, state, toast, toastError } from "./state";
import { updateMissionBoardIfOpen } from "./teamconfig";

export async function runProvidersScan() {
  const backend = api();
  if (!backend) return;
  state.providersScanning = true;
  if (state.showAPIKeysModal) renderModalLayer();
  setStatus("Varrendo provedores e contas…");
  const root = state.project?.root || "";
  try {
    if (backend.ScanAllProviders) {
      const report = await backend.ScanAllProviders(root);
      state.providersScan = report;
      const agyItem = report.providers.find((p) => p.id === "agy");
      if (agyItem && backend.BootstrapAgy) {
        backend.BootstrapAgy(false).then((res) => {
          state.agyStatus = res.status;
          if (res.models && res.models.length > 0) state.models = res.models;
          updateStatusBar();
        }).catch(() => {});
      }
      if (backend.GetProviderProfilesOverview) {
        state.profilesOverview = await backend.GetProviderProfilesOverview(root);
      }
      if (backend.GetAPIKeysStatus) {
        state.apiKeysStatus = await backend.GetAPIKeysStatus();
      }
      if (backend.GetProviderHealth) {
        const h = await backend.GetProviderHealth();
        if (Array.isArray(h)) state.providerHealth = h;
      }
      setStatus(`Varredura concluída: ${report.connectedProviders}/${report.totalProviders} provedor(es) conectados.`);
    } else {
      const status = await backend.DoctorAgy();
      state.agyStatus = status;
      if (status.LoggedIn) {
        state.models = (await backend.ListModels()) || state.models;
      }
      setStatus("Pronto");
    }
  } catch (err: any) {
    toastError("varrer provedores e contas", err);
    setStatus("Erro ao varrer provedores.");
  } finally {
    state.providersScanning = false;
    updateStatusBar();
    if (state.showAPIKeysModal) renderModalLayer();
  }
}

export async function openProvidersScanModal() {
  await openAPIKeysModal("scan");
  await runProvidersScan();
}

export async function openDoctorModal() {
  await openProvidersScanModal();
}

export async function openGitHubModal() {
  state.showGitHubModal = true;
  renderModalLayer();
  const backend = api();
  if (!backend?.GitHubStatus) return;
  try {
    state.ghStatus = await backend.GitHubStatus(state.project?.root || "");
    renderGuard.guardedRender("left-sidebar", renderLeftSidebar);
    renderGuard.guardedRender("modal-layer", renderModalLayer);
  } catch {
    // keep current status visible
  }
}

export async function refreshJiraStatusAndProjects() {
  const backend = api();
  const root = state.project?.root || "";
  if (!backend?.JiraStatus && !backend?.JiraStatusForWorkspace) return;
  try {
    state.jiraStatus = backend.JiraStatusForWorkspace
      ? await backend.JiraStatusForWorkspace(root)
      : await backend.JiraStatus!();
    if (state.jiraStatus?.connected && (backend.ListJiraProjectsForWorkspace || backend.ListJiraProjects)) {
      state.jiraProjectsLoading = true;
      try {
        state.jiraProjects = backend.ListJiraProjectsForWorkspace
          ? ((await backend.ListJiraProjectsForWorkspace(root)) || [])
          : ((await backend.ListJiraProjects!()) || []);
      } catch {
        // mantém lista anterior em caso de erro transitório
      } finally {
        state.jiraProjectsLoading = false;
      }
    } else if (!state.jiraStatus?.connected) {
      state.jiraProjects = [];
    }
    if (state.jiraStatus?.connected) {
      if (state.project?.teamConfig?.settings) {
        state.project.teamConfig.settings.jira_enabled = true;
      }
      if (state.teamConfigDraft) {
        state.teamConfigDraft.jiraEnabled = true;
      }
      if (state.sidebarView === "git" && state.gitTab === "prs_issues" && !state.issues.loading) {
        void loadProjectIssues();
      }
    }
    updateStatusBar();
    if (state.showJiraModal) renderGuard.guardedRender("modal-layer", renderModalLayer);
    if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) renderEditorBody();
  } catch {
    // mantém último estado visível
  }
}

export async function refreshProviderProfiles() {
  const backend = api();
  const root = state.project?.root || "";
  if (!backend?.GetProviderProfilesOverview) return;
  try {
    state.profilesOverview = await backend.GetProviderProfilesOverview(root);
    updateStatusBar();
    if (state.showAPIKeysModal) renderGuard.guardedRender("modal-layer", renderModalLayer);
  } catch {
    // mantém estado anterior
  }
}

export async function openJiraModal() {
  state.showJiraModal = true;
  state.jiraEditingProfileId = null;
  renderModalLayer();
  await refreshJiraStatusAndProjects();
}

export async function openOpenCodeModal() {
  await openAPIKeysModal("profiles", "opencode");
}

export async function openAPIKeysModal(initialTab?: "profiles" | "global" | "scan", initialProvider?: string) {
  if (initialTab) state.apiKeysTab = initialTab;
  if (initialProvider) {
    state.apiKeysTab = "profiles";
    if (initialProvider === "agy") {
      state.profileEditor = { open: false, id: "", provider: "agy", authMode: "adc" };
    } else {
      state.profileEditor = { open: true, id: "", provider: initialProvider, authMode: "key" };
    }
  }
  state.showAPIKeysModal = true;
  renderModalLayer();
  const backend = api();
  try {
    const root = state.project?.root || "";
    const tasks: Array<Promise<unknown>> = [];
    if (backend?.GetProviderProfilesOverview) {
      tasks.push(backend.GetProviderProfilesOverview(root).then((ov) => { state.profilesOverview = ov; }).catch(() => {}));
    }
    if (backend?.GetAPIKeysStatus) {
      tasks.push(backend.GetAPIKeysStatus().then((st) => { state.apiKeysStatus = st; }).catch(() => {}));
    }
    if (backend?.CursorCLIStatus) {
      tasks.push(backend.CursorCLIStatus().then((st) => { state.cursorCLI = st; }).catch(() => {}));
    }
    if (backend?.GetProviderHealth) {
      tasks.push(backend.GetProviderHealth().then((h) => { if (Array.isArray(h)) state.providerHealth = h; }).catch(() => {}));
    }
    await Promise.all(tasks);
    updateStatusBar();
    renderGuard.guardedRender("modal-layer", renderModalLayer);
  } catch {
    // mantém o modal aberto sem status
  }
}

export function renderProvidersScanTabHtml(
  report: ProvidersScanReport | null,
  scanning: boolean,
  expanded: Record<string, boolean>,
): string {
  const headerBar = `
    <div style="display:flex;align-items:center;justify-content:space-between;gap:10px;padding:12px 14px;border-radius:10px;background:var(--bg-card);border:1px solid var(--border);flex-wrap:wrap">
      <div>
        <div style="font-weight:700;font-size:13px;color:var(--text)">Diagnóstico e Varredura de Provedores</div>
        <div class="dim" style="font-size:11.5px;margin-top:2px;font-variant-numeric:tabular-nums">
          ${scanning
            ? "Varrendo todas as contas Google (Antigravity), OpenCode, OpenRouter, Claude, OpenAI, Gemini e Cursor CLI…"
            : report
              ? `${report.connectedProviders} de ${report.totalProviders} provedores conectados · ${report.totalAccounts} conta(s)/credencial(is) detectada(s)`
              : "Clique em Varrer Agora para inspecionar todos os provedores e contas configuradas neste computador."
          }
        </div>
      </div>
      <button type="button" class="primary mini" id="btn-scan-all-providers" ${scanning ? "disabled" : ""}>
        ${scanning ? icons.spinner : icons.refresh}
        <span>${scanning ? "Varrendo…" : "Varrer Agora"}</span>
      </button>
    </div>
  `;

  if (!report) {
    return `
      ${headerBar}
      <div class="dim" style="padding:26px 14px;text-align:center;font-size:12px">
        ${scanning ? `${icons.spinner} Verificando CLIs locais, contas Google isoladas e chaves de API…` : "Nenhuma varredura executada nesta sessão."}
      </div>
    `;
  }

  const cards = report.providers.map((item) => {
    const pillClass = item.connected
      ? "approved"
      : item.statusLabel === "Parcial"
        ? "pending"
        : "neutral";
    const statusColor = item.connected
      ? "var(--green)"
      : item.statusLabel === "Parcial"
        ? "var(--amber)"
        : "var(--text-dim)";
    const isExpanded = Boolean(expanded[item.id]);
    const hasModels = (item.modelsSample?.length || 0) > 0;
    const accRows = (item.accounts || []).map((a) => `
      <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;padding:5px 8px;border-radius:7px;background:var(--bg-deep);border:1px solid var(--border);font-size:11.5px">
        <div style="display:flex;align-items:center;gap:6px;min-width:0;flex:1">
          <span style="display:inline-flex;color:${a.ready ? "var(--green)" : "var(--amber)"}">${icons.dot}</span>
          <strong style="color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${escapeHtml(a.name)}</strong>
          ${a.active ? `<span class="flow-pill approved" style="font-size:10px;padding:1px 6px">Ativa</span>` : ""}
          <span class="tok-pill" style="font-size:10px">${escapeHtml(a.source)}</span>
        </div>
        ${a.maskedHint ? `<span class="dim" style="font-family:var(--mono);font-size:10.5px;flex-shrink:0">${escapeHtml(a.maskedHint)}</span>` : ""}
      </div>
    `).join("");

    return `
      <div style="padding:12px 14px;border-radius:10px;background:var(--bg-card);border:1px solid ${item.connected ? "rgba(52,211,153,0.28)" : "var(--border)"};display:flex;flex-direction:column;gap:8px">
        <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap">
          <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;min-width:0">
            <span style="display:inline-flex;color:${statusColor}">${icons.dot}</span>
            <strong style="font-size:13px;color:var(--text)">${escapeHtml(item.name)}</strong>
            <span class="flow-pill ${pillClass}">${escapeHtml(item.statusLabel)}</span>
            ${item.source ? `<span class="tok-pill">${escapeHtml(item.source)}</span>` : ""}
            ${item.modelCount > 0 ? `<span class="tok-pill" style="font-variant-numeric:tabular-nums">${item.modelCount} modelo(s)</span>` : ""}
          </div>
          <div style="display:flex;align-items:center;gap:6px;flex-shrink:0">
            ${hasModels ? `<button type="button" class="mini ghost" data-scan-toggle-models="${escapeHtml(item.id)}">${isExpanded ? "Ocultar modelos" : "Ver modelos"}</button>` : ""}
            <button type="button" class="mini" data-scan-configure="${escapeHtml(item.id)}">${item.connected ? "Gerenciar" : "+ Conectar"}</button>
          </div>
        </div>
        <div class="dim" style="font-size:11.5px;line-height:1.45">${escapeHtml(item.detail)}${item.path ? ` · <code>${escapeHtml(item.path)}</code>` : ""}</div>
        ${accRows ? `<div style="display:flex;flex-direction:column;gap:4px">${accRows}</div>` : ""}
        ${isExpanded && hasModels ? `
          <div style="max-height:140px;overflow:auto;background:var(--bg-deep);padding:8px;border-radius:6px;font-size:11px;font-family:var(--mono);border:1px solid var(--border)">
            ${(item.modelsSample || []).map((m) => `<div>${escapeHtml(m)}</div>`).join("")}
          </div>
        ` : ""}
      </div>
    `;
  }).join("");

  return `${headerBar}<div style="display:flex;flex-direction:column;gap:8px">${cards}</div>`;
}

function relayStatusLine(): string {
  const rs = state.relayStatus;
  if (!rs?.loggedIn) return "Conta do relay: desconectada";
  const link = !rs.linked
    ? "sem IDE vinculada"
    : rs.online
      ? "túnel online"
      : rs.enabled
        ? "túnel reconectando"
        : "vinculada, túnel desligado";
  const e2e = rs.e2e ? " · criptografia ponta a ponta pronta" : "";
  const name = rs.deviceName ? ` · ${escapeHtml(rs.deviceName)}` : "";
  const err = rs.lastError ? `<div style="color:var(--red);margin-top:4px">${escapeHtml(rs.lastError)}</div>` : "";
  return `Conta ${escapeHtml(rs.email)}${name} · ${link}${e2e}${err}`;
}

let relayTimer: number | undefined;

function stopRelayPoll() {
  if (relayTimer !== undefined) window.clearInterval(relayTimer);
  relayTimer = undefined;
}

async function refreshRelayQuiet() {
  const backend = api();
  if (!backend?.RelayStatus || !state.showMobileModal) return;
  try {
    state.relayStatus = await backend.RelayStatus();
    const el = document.getElementById("relay-status-line");
    if (el) el.innerHTML = relayStatusLine();
    updateStatusBar();
  } catch {
    // o modal continua com o último estado
  }
}

function startRelayPoll() {
  stopRelayPoll();
  relayTimer = window.setInterval(() => void refreshRelayQuiet(), 3000);
}

export async function openMobileModal() {
  state.showMobileModal = true;
  state.relayDraft.password = "";
  renderModalLayer();
  const backend = api();
  if (!backend?.MobileBridgeStatus) return;
  try {
    state.mobileStatus = await backend.MobileBridgeStatus();
    if (backend.RelayStatus) {
      state.relayStatus = await backend.RelayStatus();
      if (!state.relayDraft.baseUrl) state.relayDraft.baseUrl = state.relayStatus?.baseUrl || "";
      if (!state.relayDraft.email) state.relayDraft.email = state.relayStatus?.email || "";
    }
    updateStatusBar();
    renderModalLayer();
    await refreshMobilePairing();
  } catch {
    // mantém o modal aberto sem status
  }
}

async function refreshMobilePairing() {
  const backend = api();
  if (!backend?.MobilePairing || !state.mobileStatus?.running) {
    state.mobilePairing = null;
    renderModalLayer();
    return;
  }
  try {
    state.mobilePairing = await backend.MobilePairing();
  } catch {
    state.mobilePairing = null;
  }
  renderModalLayer();
}

let activeModalTrap: FocusTrapHandle | null = null;

/** IDs de botões que descartam o modal com segurança quando Escape é pressionado. */
const DISMISSIBLE_MODAL_BUTTONS = ["confirm-modal-cancel", "img-preview-close", "path-modal-cancel"];

function dismissDismissibleModal(): void {
  for (const id of DISMISSIBLE_MODAL_BUTTONS) {
    const btn = document.getElementById(id);
    if (btn) {
      (btn as HTMLButtonElement).click();
      return;
    }
  }
}

/**
 * renderModalLayer renders the active modal (if any) and keeps its keyboard
 * accessibility in sync: ARIA labelling, Tab focus trap, Escape dismissal for
 * disposable dialogs and focus restoration when everything closes (W4-07).
 */
export function renderModalLayer() {
  renderModalLayerInner();
  const layer = document.getElementById("modal-layer");
  if (!layer) return;
  const box = layer.querySelector<HTMLElement>(".modal-box");
  if (!box) {
    if (activeModalTrap) {
      activeModalTrap.release();
      activeModalTrap = null;
    }
    return;
  }
  // ARIA: every dialog gets an accessible name from its visible title.
  if (!box.hasAttribute("aria-labelledby")) {
    const title = box.querySelector<HTMLElement>(".modal-title");
    if (title) {
      if (!title.id) title.id = `modal-title-${Math.random().toString(36).slice(2, 9)}`;
      box.setAttribute("aria-labelledby", title.id);
    }
  }
  if (!box.hasAttribute("role")) box.setAttribute("role", "dialog");
  if (!box.hasAttribute("aria-modal")) box.setAttribute("aria-modal", "true");
  if (!activeModalTrap) {
    activeModalTrap = trapFocusIn(layer, { onEscape: dismissDismissibleModal });
  }
}

function renderModalLayerInner() {
  const layer = document.getElementById("modal-layer");
  if (!layer) return;
  // A modal region is being re-rendered: deferred async renders are satisfied.
  notifyRegionRendered("modal-layer");

  if (state.confirmModal) {
    const cm = state.confirmModal;
    layer.innerHTML = `
      <div class="modal-backdrop" id="confirm-modal-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:430px">
          <h3 class="modal-title">${escapeHtml(cm.title)}</h3>
          <p style="font-size:12.5px;line-height:1.5;color:var(--text-dim)">${escapeHtml(cm.body)}</p>
          <div class="modal-footer">
            <button id="confirm-modal-cancel">Cancelar</button>
            <button class="${cm.danger ? "danger" : "primary"}" id="confirm-modal-ok">${escapeHtml(cm.confirmLabel)}</button>
          </div>
        </div>
      </div>
    `;
    const closeConfirm = () => {
      state.confirmModal = null;
      renderModalLayer();
    };
    document.getElementById("confirm-modal-cancel")?.addEventListener("click", closeConfirm);
    document.getElementById("confirm-modal-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement).id === "confirm-modal-backdrop") closeConfirm();
    });
    const okBtn = document.getElementById("confirm-modal-ok") as HTMLButtonElement | null;
    okBtn?.focus();
    okBtn?.addEventListener("click", async () => {
      const action = cm.onConfirm;
      closeConfirm();
      await action();
    });
    return;
  }

  if (state.imagePreviewUrl) {
    const safeUrl = safeImageSrc(state.imagePreviewUrl);
    if (!safeUrl) {
      state.imagePreviewUrl = "";
      layer.innerHTML = "";
      return;
    }
    layer.innerHTML = `
      <div class="modal-backdrop" id="img-preview-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:auto;max-width:92vw;padding:12px;text-align:center">
          <img src="${escapeHtml(safeUrl)}" style="max-width:86vw;max-height:80vh;border-radius:6px;display:block" alt="preview" />
          <div class="modal-footer" style="margin-top:10px">
            <button class="primary mini" id="img-preview-close">Fechar</button>
          </div>
        </div>
      </div>
    `;
    document.getElementById("img-preview-close")?.addEventListener("click", () => {
      state.imagePreviewUrl = "";
      renderModalLayer();
    });
    document.getElementById("img-preview-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement).id === "img-preview-backdrop") {
        state.imagePreviewUrl = "";
        renderModalLayer();
      }
    });
    return;
  }

  if (state.pathModal) {
    const pm = state.pathModal;
    const isDelete = pm.mode === "delete";
    const titles: Record<typeof pm.mode, string> = {
      newFile: "Novo Arquivo",
      newDir: "Nova Pasta",
      rename: "Renomear Caminho",
      delete: "Confirmar Exclusão",
    };
    layer.innerHTML = `
      <div class="modal-backdrop" id="path-modal-backdrop">
        <div class="modal-box" style="width:420px">
          <h3 class="modal-title">${titles[pm.mode]}</h3>
          ${isDelete
            ? `<p style="font-size:12.5px">Deseja excluir permanentemente <code>${escapeHtml(pm.targetRel)}</code>?</p>`
            : `<input id="path-modal-input" placeholder="Ex: src/NovoArquivo.java" value="${escapeHtml(pm.value)}" />`
          }
          <div id="path-modal-err" style="color:var(--red);font-size:12px;margin-top:8px;display:none"></div>
          <div class="modal-footer">
            <button id="path-modal-cancel">Cancelar</button>
            <button class="${isDelete ? "danger" : "primary"}" id="path-modal-confirm">${isDelete ? "Excluir" : "Confirmar"}</button>
          </div>
        </div>
      </div>
    `;
    const input = document.getElementById("path-modal-input") as HTMLInputElement | null;
    if (input) {
      input.focus();
      input.selectionStart = input.selectionEnd = input.value.length;
    }

    const closePm = () => {
      state.pathModal = null;
      renderModalLayer();
    };
    document.getElementById("path-modal-cancel")?.addEventListener("click", closePm);

    const submitPm = async () => {
      const backend = api();
      if (!backend || !state.project) return;
      const val = (input?.value || "").trim();
      try {
        if (pm.mode === "newFile") {
          if (!val) return;
          await backend.CreateFile(state.project.root, val);
          closePm();
          await refreshExplorerTree();
          await openFileInEditor(val, val.split("/").pop() || val);
          setStatus(`Arquivo criado: ${val}`);
        } else if (pm.mode === "newDir") {
          if (!val) return;
          await backend.CreateDir(state.project.root, val);
          state.expandedDirs.add(val);
          closePm();
          await refreshExplorerTree();
          setStatus(`Pasta criada: ${val}`);
        } else if (pm.mode === "rename") {
          if (!val || val === pm.targetRel) {
            closePm();
            return;
          }
          await backend.RenamePath(state.project.root, pm.targetRel, val);
          for (const t of state.editorTabs) {
            if (t.relPath === pm.targetRel) {
              t.relPath = val;
              t.name = val.split("/").pop() || val;
            }
          }
          if (state.activeEditorPath === pm.targetRel) state.activeEditorPath = val;
          closePm();
          await refreshExplorerTree();
          renderEditorTabs();
          renderEditorBody();
          setStatus(`Renomeado para ${val}`);
        } else if (pm.mode === "delete") {
          await backend.DeletePath(state.project.root, pm.targetRel);
          state.editorTabs = state.editorTabs.filter((t) => t.relPath !== pm.targetRel && !t.relPath.startsWith(pm.targetRel + "/"));
          if (!state.editorTabs.some((t) => t.relPath === state.activeEditorPath)) {
            state.activeEditorPath = state.editorTabs[0]?.relPath || "";
          }
          closePm();
          await refreshExplorerTree();
          renderEditorTabs();
          renderEditorBody();
          refreshDiffs();
          setStatus(`Excluído: ${pm.targetRel}`);
        }
      } catch (err: any) {
        const errEl = document.getElementById("path-modal-err");
        if (errEl) {
          const action = pm.mode === "newFile" ? "criar o arquivo" : pm.mode === "newDir" ? "criar a pasta" : pm.mode === "rename" ? "renomear o caminho" : "excluir";
          const reason = err instanceof Error ? err.message : String(err);
          errEl.textContent = `Não foi possível ${action}: ${reason}. Verifique o caminho e tente novamente.`;
          errEl.style.display = "block";
        }
      }
    };

    input?.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        submitPm();
      }
    });
    document.getElementById("path-modal-confirm")?.addEventListener("click", submitPm);
    return;
  }

  if (state.showAPIKeysModal) {
    const tab = state.apiKeysTab || "profiles";
    const ov = state.profilesOverview;
    const saved = state.apiKeysStatus || {};
    const scan = state.providersScan;
    const field = (id: string, keyName: string, label: string, placeholder: string, savedMask?: string) => `
      <label for="${id}">${escapeHtml(label)}${savedMask ? ` <span class="dim" style="font-weight:500">(salvo: ${escapeHtml(savedMask)})</span>` : ""}</label>
      <div style="display:flex;gap:6px;align-items:center">
        <input id="${id}" type="${keyName.includes("KEY") ? "password" : "text"}" placeholder="${savedMask ? "Cole novo valor apenas se quiser trocar" : escapeHtml(placeholder)}" autocomplete="off" style="flex:1" />
        ${savedMask ? `<button type="button" class="mini danger ghost" data-ak-clear="${escapeHtml(keyName)}" title="Remover chave salva">${icons.trash}</button>` : ""}
      </div>
    `;

    const provLabels: Record<string, string> = {
      agy: "Antigravity (Google)",
      typesafe: "TypeSafe AI (Jev Oficial)",
      openrouter: "OpenRouter",
      opencode: "OpenCode Go & Zen",
      anthropic: "Anthropic (Claude)",
      openai: "OpenAI / Grok",
      gemini: "Google Gemini API",
      cursor: "Cursor CLI",
    };
    const nonAgyProvs: Record<string, string> = {
      typesafe: "TypeSafe AI (Jev Oficial)",
      openrouter: "OpenRouter",
      opencode: "OpenCode Go & Zen",
      anthropic: "Anthropic (Claude)",
      openai: "OpenAI / Grok",
      gemini: "Google Gemini API",
      cursor: "Cursor CLI",
    };
    const provKeys: Record<string, Array<{ key: string; label: string; placeholder: string; secret?: boolean }>> = {
      typesafe: [{ key: "TYPESAFE_API_KEY", label: "TypeSafe AI API Key (console.typesafe.ai/keys)", placeholder: "ts_…", secret: true }],
      openrouter: [{ key: "OPENROUTER_API_KEY", label: "OpenRouter API Key", placeholder: "sk-or-v1-…", secret: true }],
      opencode: [{ key: "OPENCODE_API_KEY", label: "OpenCode Token", placeholder: "oc_…", secret: true }],
      anthropic: [{ key: "ANTHROPIC_API_KEY", label: "Anthropic API Key", placeholder: "sk-ant-…", secret: true }],
      openai: [{ key: "OPENAI_API_KEY", label: "OpenAI API Key", placeholder: "sk-…", secret: true }],
      gemini: [{ key: "GOOGLE_API_KEY", label: "Gemini API Key", placeholder: "AIza…", secret: true }],
      cursor: [{ key: "CURSOR_API_KEY", label: "Cursor API Key", placeholder: "crsr_…", secret: true }],
    };

    const profilesList = ov?.profiles || [];
    const agyAccounts = profilesList.filter((p) => p.provider === "agy");
    const otherAccounts = profilesList.filter((p) => p.provider !== "agy");
    const ed = state.profileEditor;
    const editingProf: ProviderProfileSummary | undefined = ed.id
      ? profilesList.find((p) => p.id === ed.id)
      : undefined;
    const curProv = (ed.provider && ed.provider !== "agy" ? ed.provider : editingProf?.provider) || "openrouter";
    const wsRoot = state.project?.root || "";

    const renderKeyAccountEditor = () => {
      if (!ed.open) return "";
      const keyFields = (provKeys[curProv] || []).map((f) => {
        const mask = editingProf?.envMasked?.[f.key] || "";
        return `
          <div style="display:flex;flex-direction:column;gap:4px">
            <label for="prof-env-${f.key}" style="font-size:11.5px;color:var(--text-dim);font-weight:600">
              ${escapeHtml(f.label)}${mask ? ` <span class="dim">(atual: ${escapeHtml(mask)})</span>` : ""}
            </label>
            <input id="prof-env-${f.key}" type="password" placeholder="${mask ? "Cole nova chave apenas se quiser trocar" : escapeHtml(f.placeholder)}" autocomplete="off" style="padding:8px 10px;font-size:12.5px" />
          </div>
        `;
      }).join("");

      return `
        <div style="margin-top:10px;padding:14px;border-radius:12px;background:var(--bg-deep);border:1px solid var(--border-strong);display:flex;flex-direction:column;gap:10px">
          <div style="display:flex;align-items:center;justify-content:space-between">
            <strong style="font-size:13px">${editingProf ? `Editar Conta · ${escapeHtml(editingProf.name)}` : "Adicionar Conta por Chave de API"}</strong>
            <button type="button" class="mini" id="prof-cancel-edit">Cancelar</button>
          </div>
          <div style="display:grid;grid-template-columns:1fr 1fr;gap:10px">
            <div style="display:flex;flex-direction:column;gap:4px">
              <label for="prof-provider" style="font-size:11.5px;color:var(--text-dim);font-weight:600">Provedor</label>
              <select id="prof-provider" ${editingProf ? "disabled" : ""} style="padding:7px 10px;font-size:12.5px">
                ${Object.entries(nonAgyProvs).map(([id, lbl]) => `<option value="${id}" ${curProv === id ? "selected" : ""}>${escapeHtml(lbl)}</option>`).join("")}
              </select>
            </div>
            <div style="display:flex;flex-direction:column;gap:4px">
              <label for="prof-name" style="font-size:11.5px;color:var(--text-dim);font-weight:600">Identificação / E-mail</label>
              <input id="prof-name" value="${escapeHtml(editingProf?.name || "")}" placeholder="ex.: OpenRouter Reserva ou email@empresa.com" style="padding:7px 10px;font-size:12.5px" />
            </div>
          </div>
          ${keyFields}
          <div style="display:flex;justify-content:flex-end;gap:8px;margin-top:4px">
            <button type="button" class="primary" id="prof-save" style="padding:6px 14px">${icons.check}<span>Salvar Conta no Pool</span></button>
          </div>
        </div>
      `;
    };

    layer.innerHTML = `
      <div class="modal-backdrop" id="ak-modal-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:660px;max-height:88vh;display:flex;flex-direction:column;padding:18px 20px">
          <div style="display:flex;align-items:center;justify-content:space-between;gap:10px;margin-bottom:12px;flex-wrap:wrap">
            <div>
              <h3 class="modal-title" style="margin:0;font-size:16px">Provedores &amp; Contas LLM</h3>
              <div class="dim" style="font-size:11.5px;margin-top:2px">Gerencie contas Google (Antigravity), pool de chaves de API, chaves globais e faça varredura de todos os provedores.</div>
            </div>
            <div style="display:flex;gap:6px;flex-shrink:0;flex-wrap:wrap">
              <button type="button" class="mini ${tab === "profiles" ? "primary" : ""}" id="ak-tab-profiles">Pool de Contas (${profilesList.length})</button>
              <button type="button" class="mini ${tab === "global" ? "primary" : ""}" id="ak-tab-global">Chaves Globais</button>
              <button type="button" class="mini ${tab === "scan" ? "primary" : ""}" id="ak-tab-scan">Varredura${scan ? ` (${scan.connectedProviders}/${scan.totalProviders})` : ""}</button>
            </div>
          </div>

          <div style="overflow-y:auto;padding-right:4px;flex:1;display:flex;flex-direction:column;gap:12px">
            ${tab === "profiles" ? `
              <!-- Card Principal: Contas Google Antigravity (agy) -->
              <div style="padding:14px 16px;border-radius:12px;background:linear-gradient(180deg, rgba(56,189,248,0.08), var(--accent-subtle));border:1px solid rgba(56,189,248,0.28);display:flex;flex-direction:column;gap:12px">
                <div style="display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap">
                  <div style="display:flex;align-items:center;gap:10px">
                    <div style="width:36px;height:36px;border-radius:10px;background:rgba(56,189,248,0.16);border:1px solid rgba(56,189,248,0.4);display:flex;align-items:center;justify-content:center;font-weight:800;font-size:16px;color:#7dd3fc;flex-shrink:0">G</div>
                    <div>
                      <div style="font-weight:700;font-size:13.5px;color:var(--text)">Contas Google · Antigravity (agy)</div>
                      <div class="dim" style="font-size:11.5px">Se o token do <code>argon</code> acabar na Conta 1, o Jev mantém os outros modelos na Conta 1 e muda só o <code>argon</code> para a Conta 2.</div>
                    </div>
                  </div>
                  <button type="button" class="primary" id="btn-connect-google-agy" style="padding:7px 14px;font-weight:600;font-size:12.5px;border-radius:8px;flex-shrink:0">
                    ${icons.sparkle}<span>${state.agyOAuthWaitingCode ? "Reabrir Login Google" : "+ Logar Nova Conta Google"}</span>
                  </button>
                </div>

                ${state.agyOAuthWaitingCode ? `
                  <div style="padding:12px 14px;border-radius:10px;background:rgba(15,23,42,0.85);border:1px solid rgba(56,189,248,0.55);display:flex;flex-direction:column;gap:8px">
                    <div style="display:flex;align-items:center;justify-content:space-between;gap:8px">
                      <strong style="font-size:12.5px;color:#7dd3fc">${icons.spinner} Aguardando seleção da Conta Google no navegador…</strong>
                      <button type="button" class="mini" id="btn-cancel-agy-oauth">Cancelar</button>
                    </div>
                    <div class="dim" style="font-size:11.5px;line-height:1.45">
                      1. Escolha sua conta Google e clique em <strong>Continuar</strong> no navegador — o HiveMind recebe o retorno automaticamente em <code>localhost</code> e já salva na pasta isolada pelo e-mail.<br/>
                      2. Caso seu navegador não redirecione sozinho, você também pode colar o código <code>4/0A…</code> ou a URL abaixo:
                    </div>
                    <div style="display:flex;gap:8px;align-items:center">
                      <input id="agy-oauth-code-input" type="text" value="${escapeHtml(state.agyOAuthCodeInput || "")}" placeholder="Opcional: cole aqui o código 4/0A... ou a URL de callback" style="flex:1;font-family:var(--font-mono);font-size:12px" />
                      <button type="button" class="mini" id="btn-paste-agy-oauth" title="Colar da área de transferência e conectar">Colar &amp; Conectar</button>
                      <button type="button" class="primary mini" id="btn-submit-agy-oauth">${icons.check}<span>Confirmar Código</span></button>
                    </div>
                  </div>
                ` : ""}

                ${agyAccounts.length === 0 ? `
                  <div style="padding:14px;border-radius:10px;background:rgba(0,0,0,0.22);border:1px dashed var(--border);text-align:center;font-size:12px;color:var(--text-dim)">
                    Nenhuma conta Google salva no Pool ainda. Clique em <strong>+ Logar Nova Conta Google</strong> acima para autenticar no navegador e salvar automaticamente pelo seu e-mail.
                  </div>
                ` : `
                  <div style="display:flex;flex-direction:column;gap:8px">
                    ${agyAccounts.map((p, idx) => {
                      const email = p.configValues?.["AGY_EMAIL"] || p.name;
                      const initial = (email[0] || "G").toUpperCase();
                      const rankBadge = idx === 0 ? "Conta #1 · Principal" : `Conta #${idx + 1} · Reserva ${idx}`;
                      const isActiveNow = ov?.resolved?.["agy"]?.profileId === p.id || (idx === 0 && !ov?.resolved?.["agy"]?.profileId);
                      return `
                        <div style="padding:10px 12px;border-radius:10px;background:var(--bg-card);border:1px solid ${isActiveNow ? "rgba(52,211,153,0.45)" : "var(--border)"};display:flex;align-items:center;justify-content:space-between;gap:10px">
                          <div style="display:flex;align-items:center;gap:10px;min-width:0;flex:1">
                            <div style="width:32px;height:32px;border-radius:50%;background:${idx === 0 ? "rgba(52,211,153,0.18)" : "var(--accent-subtle)"};border:1px solid ${idx === 0 ? "rgba(52,211,153,0.45)" : "var(--accent-border)"};color:${idx === 0 ? "#6ee7b7" : "var(--accent-strong)"};display:flex;align-items:center;justify-content:center;font-weight:700;font-size:13px;flex-shrink:0">${escapeHtml(initial)}</div>
                            <div style="min-width:0;flex:1">
                              <div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap">
                                <strong style="font-size:13px;color:var(--text)">${escapeHtml(email)}</strong>
                                <span class="flow-pill ${idx === 0 ? "approved" : "pending"}">${escapeHtml(rankBadge)}</span>
                                <span style="font-size:10.5px;color:var(--green)">● Pasta isolada salva</span>
                              </div>
                              <div class="dim" style="font-size:11px;margin-top:2px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">
                                Vault: <code>%APPDATA%\\HiveMind\\agy-profiles\\${escapeHtml(p.id)}\\oauth-session.json</code>
                              </div>
                            </div>
                          </div>
                          <div style="display:flex;align-items:center;gap:6px;flex-shrink:0">
                            <button type="button" class="mini ${isActiveNow ? "primary" : ""}" data-activate-agy-account="${escapeHtml(p.id)}" title="Ativar esta conta Google como primária agora">${isActiveNow ? "Ativa" : "Ativar"}</button>
                            <button type="button" class="danger mini prof-del" data-id="${escapeHtml(p.id)}" title="Remover conta do Pool">${icons.trash}</button>
                          </div>
                        </div>
                      `;
                    }).join("")}
                  </div>
                `}
              </div>

              <!-- Card Secundário: Contas Extras de Outros Provedores (OpenRouter, OpenCode, Claude, etc.) -->
              <div style="padding:12px 14px;border-radius:12px;background:var(--bg-card);border:1px solid var(--border);display:flex;flex-direction:column;gap:8px">
                <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap">
                  <div>
                    <strong style="font-size:12.5px">Outras Contas por Chave de API (OpenRouter, OpenCode, Claude, Cursor)</strong>
                    <div class="dim" style="font-size:11px">Opcional: cadastre chaves extras de reserva para failover automático.</div>
                  </div>
                  <button type="button" class="mini" id="prof-new">+ Adicionar Chave Extra</button>
                </div>

                ${renderKeyAccountEditor()}

                ${otherAccounts.length > 0 ? `
                  <div style="display:flex;flex-direction:column;gap:6px;margin-top:4px">
                    ${otherAccounts.map((p, idx) => {
                      const maskedList = Object.entries(p.envMasked || {}).map(([k, v]) => `${escapeHtml(k)}=${escapeHtml(v)}`).join(" · ");
                      return `
                        <div style="padding:8px 10px;border-radius:8px;background:var(--bg-deep);border:1px solid var(--border);display:flex;align-items:center;justify-content:space-between;gap:8px">
                          <div style="min-width:0;flex:1">
                            <div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap">
                              <span class="tok-pill">${escapeHtml(provLabels[p.provider] || p.provider)}</span>
                              <strong style="font-size:12.5px">${escapeHtml(p.name)}</strong>
                              <span class="flow-pill pending">Conta #${idx + 1}</span>
                              <span style="font-size:11px;color:${p.ready ? "var(--green)" : "var(--amber)"}">${p.ready ? "● Pronta" : "Pendente"}</span>
                            </div>
                            ${maskedList ? `<div class="dim" style="font-size:11px;margin-top:2px">${maskedList}</div>` : ""}
                          </div>
                          <div style="display:flex;gap:5px;flex-shrink:0">
                            <button type="button" class="mini prof-edit" data-id="${escapeHtml(p.id)}">Editar</button>
                            <button type="button" class="danger mini prof-del" data-id="${escapeHtml(p.id)}">${icons.trash}</button>
                          </div>
                        </div>
                      `;
                    }).join("")}
                  </div>
                ` : ""}
              </div>
            ` : tab === "global" ? `
              <p class="dim" style="font-size:12px;margin-top:2px;margin-bottom:10px;line-height:1.5">
                Chaves globais padrão salvas fora do repositório (<code>api-keys.json</code> e credenciais OpenCode). Campos deixados em branco mantêm o valor já salvo; use o botão de lixeira para remover uma chave específica.
              </p>
              ${state.apiKeysStatus?.["env_TYPESAFE_API_KEY"] ? `<div style="color:var(--amber);font-size:12px;margin-bottom:6px">A variável de ambiente <code>TYPESAFE_API_KEY</code> está definida no sistema e tem prioridade.</div>` : ""}
              ${state.apiKeysStatus?.["env_OPENROUTER_API_KEY"] ? `<div style="color:var(--amber);font-size:12px;margin-bottom:6px">A variável de ambiente <code>OPENROUTER_API_KEY</code> está definida no sistema e tem prioridade.</div>` : ""}
              ${state.apiKeysStatus?.["env_OPENCODE_API_KEY"] ? `<div style="color:var(--amber);font-size:12px;margin-bottom:6px">A variável de ambiente <code>OPENCODE_API_KEY</code> está definida no sistema e tem prioridade.</div>` : ""}
              <div class="form-grid">
                ${field("ak-TYPESAFE_API_KEY", "TYPESAFE_API_KEY", "TypeSafe AI Key (Jev · console.typesafe.ai):", "ts_… (TYPESAFE_API_KEY)", saved["TYPESAFE_API_KEY"])}
                ${field("ak-TYPESAFE_BASE_URL", "TYPESAFE_BASE_URL", "TypeSafe Base URL:", "https://api.typesafe.ai", saved["TYPESAFE_BASE_URL"])}
                ${field("ak-OPENCODE_API_KEY", "OPENCODE_API_KEY", "OpenCode Go & Zen Token:", "oc_… (OPENCODE_API_KEY)", saved["OPENCODE_API_KEY"])}
                ${field("ak-OPENCODE_BASE_URL", "OPENCODE_BASE_URL", "OpenCode Base URL:", "https://opencode.ai/zen/v1", saved["OPENCODE_BASE_URL"])}
                ${field("ak-OPENROUTER_API_KEY", "OPENROUTER_API_KEY", "OpenRouter API Key:", "sk-or-v1-…", saved["OPENROUTER_API_KEY"])}
                ${field("ak-OPENROUTER_BASE_URL", "OPENROUTER_BASE_URL", "OpenRouter Base URL:", "https://openrouter.ai/api/v1", saved["OPENROUTER_BASE_URL"])}
                ${field("ak-CURSOR_API_KEY", "CURSOR_API_KEY", "Cursor API Key (agente CLI):", "crsr_… (painel Cursor → API Keys)", saved["CURSOR_API_KEY"])}
                ${field("ak-ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY", "Anthropic API Key:", "sk-ant-…", saved["ANTHROPIC_API_KEY"])}
                ${field("ak-OPENAI_API_KEY", "OPENAI_API_KEY", "OpenAI / Grok API Key:", "sk-…", saved["OPENAI_API_KEY"])}
                ${field("ak-OPENAI_BASE_URL", "OPENAI_BASE_URL", "OpenAI Base URL:", "https://api.openai.com/v1", saved["OPENAI_BASE_URL"])}
                ${field("ak-GOOGLE_API_KEY", "GOOGLE_API_KEY", "Google Gemini API Key:", "AIza…", saved["GOOGLE_API_KEY"])}
                ${field("ak-GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT", "Vertex — Projeto:", "meu-projeto-gcp", saved["GOOGLE_CLOUD_PROJECT"])}
                ${field("ak-GOOGLE_CLOUD_LOCATION", "GOOGLE_CLOUD_LOCATION", "Vertex — Região:", "us-central1", saved["GOOGLE_CLOUD_LOCATION"])}
              </div>
            ` : `
              ${renderProvidersScanTabHtml(scan, state.providersScanning, state.providersScanExpanded)}
              ${renderJevProviderHealthPanelHtml(state.providerHealth, state.providerHealthProbing)}
            `}
            <div id="ak-modal-err" style="color:var(--red);font-size:12px;margin-top:4px;display:none"></div>
          </div>

          <div class="modal-footer" style="margin-top:12px;padding-top:10px;border-top:1px solid var(--border)">
            <button id="ak-close">Fechar</button>
            <div class="spacer"></div>
            ${tab === "global" ? `<button class="primary" id="ak-save">${icons.check}<span>Salvar Chaves Globais</span></button>` : ""}
          </div>
        </div>
      </div>
    `;

    const closeAk = () => {
      if (state.agyOAuthWaitingCode) {
        const backend = api();
        state.agyOAuthWaitingCode = false;
        state.agyOAuthCodeInput = "";
        void backend?.CancelAgyOAuthLogin?.(wsRoot);
      }
      state.showAPIKeysModal = false;
      state.profileEditor.open = false;
      renderModalLayer();
    };
    document.getElementById("ak-close")?.addEventListener("click", closeAk);
    document.getElementById("ak-modal-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement)?.id === "ak-modal-backdrop") closeAk();
    });
    document.getElementById("ak-tab-profiles")?.addEventListener("click", () => {
      state.apiKeysTab = "profiles";
      renderModalLayer();
    });
    document.getElementById("ak-tab-global")?.addEventListener("click", () => {
      state.apiKeysTab = "global";
      renderModalLayer();
    });
    document.getElementById("ak-tab-scan")?.addEventListener("click", () => {
      state.apiKeysTab = "scan";
      renderModalLayer();
      if (!state.providersScan && !state.providersScanning) {
        void runProvidersScan();
      }
    });
    document.getElementById("btn-scan-all-providers")?.addEventListener("click", () => {
      void runProvidersScan();
    });
    document.querySelectorAll<HTMLElement>("[data-scan-toggle-models]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const id = btn.dataset.scanToggleModels || "";
        if (!id) return;
        state.providersScanExpanded[id] = !state.providersScanExpanded[id];
        renderModalLayer();
      });
    });
    document.querySelectorAll<HTMLElement>("[data-scan-configure]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const id = btn.dataset.scanConfigure || "";
        state.apiKeysTab = "profiles";
        if (id && id !== "agy") {
          state.profileEditor = { open: true, id: "", provider: id, authMode: "key" };
        } else {
          state.profileEditor.open = false;
        }
        renderModalLayer();
      });
    });
    document.getElementById("aiu-probe-providers")?.addEventListener("click", async () => {
      const b = api();
      if (!b?.ProbeProviders) return;
      state.providerHealthProbing = true;
      renderModalLayer();
      try {
        const views = await b.ProbeProviders();
        if (Array.isArray(views)) {
          state.providerHealth = views;
          updateStatusBar();
        }
      } catch (err: any) {
        toastError("sondar provedores Jev", err);
      } finally {
        state.providerHealthProbing = false;
        if (state.showAPIKeysModal) renderModalLayer();
      }
    });

    document.getElementById("btn-connect-google-agy")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.ConnectNewAgyGoogleAccount) return;
      const btn = document.getElementById("btn-connect-google-agy") as HTMLButtonElement | null;
      const prevIds = new Set(agyAccounts.map((a) => a.id));
      const prevCount = agyAccounts.length;
      if (btn) {
        btn.disabled = true;
        btn.innerHTML = `${icons.spinner}<span>Abrindo login Google no navegador…</span>`;
      }
      try {
        state.profilesOverview = await backend.ConnectNewAgyGoogleAccount(wsRoot);
        state.agyOAuthWaitingCode = true;
        state.agyOAuthCodeInput = "";
        renderModalLayer();
        toast("Escolha sua conta Google no navegador que abriu — o HiveMind conecta e salva na pasta isolada automaticamente!", "info");

        let checks = 0;
        const timer = window.setInterval(async () => {
          checks++;
          if (!state.showAPIKeysModal || !state.agyOAuthWaitingCode || checks > 90 || !backend.GetProviderProfilesOverview) {
            window.clearInterval(timer);
            return;
          }
          try {
            const latest = await backend.GetProviderProfilesOverview(wsRoot);
            const newAgy = (latest?.profiles || []).filter((p) => p.provider === "agy");
            const added = newAgy.find((p) => !prevIds.has(p.id));
            if (added || newAgy.length > prevCount) {
              window.clearInterval(timer);
              state.profilesOverview = latest;
              state.agyOAuthWaitingCode = false;
              state.agyOAuthCodeInput = "";
              updateStatusBar();
              updateMissionBoardIfOpen();
              renderModalLayer();
              toast(`Nova conta Google conectada e salva em pasta isolada: ${added?.name || "conectada"}!`, "success");
            }
          } catch {
            // ignore poll error
          }
        }, 1200);
      } catch (err: any) {
        toastError("iniciar login Google do Antigravity", err);
        if (btn) {
          btn.disabled = false;
          btn.innerHTML = `${icons.sparkle}<span>+ Logar Nova Conta Google</span>`;
        }
      }
    });

    const submitAgyOAuthCodeFlow = async (rawCode: string) => {
      const backend = api();
      if (!backend?.SubmitAgyOAuthCode) return;
      const code = rawCode.trim();
      if (!code) {
        toast("Cole o código 4/0A... gerado na página do Google Antigravity.", "error");
        return;
      }
      const submitBtn = document.getElementById("btn-submit-agy-oauth") as HTMLButtonElement | null;
      const pasteBtn = document.getElementById("btn-paste-agy-oauth") as HTMLButtonElement | null;
      if (submitBtn) {
        submitBtn.disabled = true;
        submitBtn.innerHTML = `${icons.spinner}<span>Validando código…</span>`;
      }
      if (pasteBtn) pasteBtn.disabled = true;
      try {
        const prevIds = new Set(agyAccounts.map((a) => a.id));
        const latest = await backend.SubmitAgyOAuthCode(code, wsRoot);
        state.profilesOverview = latest;
        state.agyOAuthWaitingCode = false;
        state.agyOAuthCodeInput = "";
        const newAgy = (latest?.profiles || []).filter((p) => p.provider === "agy");
        const added = newAgy.find((p) => !prevIds.has(p.id)) || newAgy[newAgy.length - 1];
        if (added && wsRoot && backend.BindProviderWorkspace) {
          state.profilesOverview = await backend.BindProviderWorkspace(wsRoot, "agy", added.id);
        }
        updateStatusBar();
        updateMissionBoardIfOpen();
        renderModalLayer();
        toast(`Conta Google conectada e salva em pasta isolada: ${added?.name || "conectada"}!`, "success");
      } catch (err: any) {
        toastError("validar código OAuth do Google Antigravity", err);
        renderModalLayer();
      }
    };

    document.getElementById("agy-oauth-code-input")?.addEventListener("input", (e) => {
      state.agyOAuthCodeInput = (e.target as HTMLInputElement).value;
    });
    document.getElementById("agy-oauth-code-input")?.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        const val = (document.getElementById("agy-oauth-code-input") as HTMLInputElement | null)?.value || state.agyOAuthCodeInput;
        void submitAgyOAuthCodeFlow(val);
      }
    });
    document.getElementById("btn-submit-agy-oauth")?.addEventListener("click", () => {
      const val = (document.getElementById("agy-oauth-code-input") as HTMLInputElement | null)?.value || state.agyOAuthCodeInput;
      void submitAgyOAuthCodeFlow(val);
    });
    document.getElementById("btn-paste-agy-oauth")?.addEventListener("click", async () => {
      try {
        const clip = await navigator.clipboard.readText();
        if (clip && clip.trim()) {
          state.agyOAuthCodeInput = clip.trim();
          const inp = document.getElementById("agy-oauth-code-input") as HTMLInputElement | null;
          if (inp) inp.value = state.agyOAuthCodeInput;
          await submitAgyOAuthCodeFlow(state.agyOAuthCodeInput);
          return;
        }
      } catch {
        // clipboard permission fallback
      }
      const val = (document.getElementById("agy-oauth-code-input") as HTMLInputElement | null)?.value || state.agyOAuthCodeInput;
      await submitAgyOAuthCodeFlow(val);
    });
    document.getElementById("btn-cancel-agy-oauth")?.addEventListener("click", async () => {
      const backend = api();
      state.agyOAuthWaitingCode = false;
      state.agyOAuthCodeInput = "";
      if (backend?.CancelAgyOAuthLogin) {
        state.profilesOverview = await backend.CancelAgyOAuthLogin(wsRoot);
      }
      renderModalLayer();
      toast("Login cancelado. Sua conta Google anterior continua ativa.", "info");
    });

    document.querySelectorAll<HTMLElement>("[data-activate-agy-account]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const backend = api();
        const pid = btn.dataset.activateAgyAccount || "";
        if (!backend?.ActivateAgyAccount || !pid) return;
        try {
          state.profilesOverview = await backend.ActivateAgyAccount(pid, wsRoot);
          if (wsRoot && backend.BindProviderWorkspace) {
            state.profilesOverview = await backend.BindProviderWorkspace(wsRoot, "agy", pid);
          }
          updateStatusBar();
          updateMissionBoardIfOpen();
          renderModalLayer();
          const acc = (state.profilesOverview?.profiles || []).find((p) => p.id === pid);
          toast(`Conta Google ativa trocada para ${acc?.name || pid}.`, "success");
        } catch (err: any) {
          toastError("ativar conta Google", err);
        }
      });
    });

    document.getElementById("prof-new")?.addEventListener("click", () => {
      state.profileEditor = { open: true, id: "", provider: "openrouter", authMode: "key" };
      renderModalLayer();
    });
    document.getElementById("prof-cancel-edit")?.addEventListener("click", () => {
      state.profileEditor.open = false;
      renderModalLayer();
    });
    document.getElementById("prof-provider")?.addEventListener("change", (e) => {
      const pv = (e.target as HTMLSelectElement).value;
      state.profileEditor.provider = pv;
      state.profileEditor.authMode = "key";
      renderModalLayer();
    });

    const saveProfileAction = async () => {
      const backend = api();
      if (!backend?.SaveProviderProfile) return;
      const nameVal = (document.getElementById("prof-name") as HTMLInputElement | null)?.value.trim() || provLabels[curProv] || curProv;
      const env: Record<string, string> = {};
      document.querySelectorAll<HTMLInputElement>("[id^='prof-env-']").forEach((inp) => {
        const key = inp.id.replace("prof-env-", "");
        env[key] = inp.value.trim();
      });
      try {
        state.profilesOverview = await backend.SaveProviderProfile({
          id: state.profileEditor.id || "",
          provider: curProv,
          name: nameVal,
          authMode: "key",
          command: "",
          env,
          orgPatterns: [],
          isDefault: false,
          bindWorkspace: "",
        });
        state.profileEditor.open = false;
        updateStatusBar();
        updateMissionBoardIfOpen();
        toast("Conta adicionada ao Pool.", "success");
        renderModalLayer();
      } catch (err: any) {
        const errEl = document.getElementById("ak-modal-err");
        if (errEl) {
          errEl.textContent = err?.message || String(err);
          errEl.style.display = "block";
        }
      }
    };
    document.getElementById("prof-save")?.addEventListener("click", () => void saveProfileAction());

    document.querySelectorAll<HTMLElement>(".prof-edit").forEach((btn) => {
      btn.addEventListener("click", () => {
        const pid = btn.dataset.id || "";
        const found = profilesList.find((x) => x.id === pid);
        if (!found) return;
        state.profileEditor = {
          open: true,
          id: found.id,
          provider: found.provider,
          authMode: "key",
        };
        renderModalLayer();
      });
    });
    document.querySelectorAll<HTMLElement>(".prof-del").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const backend = api();
        const pid = btn.dataset.id || "";
        if (!backend?.DeleteProviderProfile || !pid) return;
        try {
          state.profilesOverview = await backend.DeleteProviderProfile(pid, wsRoot);
          updateStatusBar();
          updateMissionBoardIfOpen();
          renderModalLayer();
        } catch (err: any) {
          toastError("excluir conta", err);
        }
      });
    });

    document.querySelectorAll<HTMLElement>("[data-ak-clear]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const backend = api();
        const keyName = btn.dataset.akClear || "";
        if (!backend?.SaveAPIKeys || !keyName) return;
        try {
          state.apiKeysStatus = await backend.SaveAPIKeys({ [keyName]: "" });
          if (keyName === "OPENCODE_API_KEY" || keyName === "OPENCODE_BASE_URL") {
            const ocRes = await backend.BootstrapOpenCode?.(true);
            if (ocRes) state.openCodeStatus = ocRes.status;
          }
          state.providersScan = null;
          updateStatusBar();
          toast(`Chave ${keyName} removida.`, "info");
          renderModalLayer();
        } catch (err: any) {
          toastError(`remover ${keyName}`, err);
        }
      });
    });

    document.getElementById("ak-save")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.SaveAPIKeys) return;
      const entries: Record<string, string> = {};
      for (const name of ["TYPESAFE_API_KEY", "TYPESAFE_BASE_URL", "OPENCODE_API_KEY", "OPENCODE_BASE_URL", "CURSOR_API_KEY", "OPENROUTER_API_KEY", "OPENROUTER_BASE_URL", "OPENAI_API_KEY", "OPENAI_BASE_URL", "ANTHROPIC_API_KEY", "GOOGLE_API_KEY", "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION"]) {
        const el = document.getElementById(`ak-${name}`) as HTMLInputElement | null;
        const val = el?.value?.trim() || "";
        if (val !== "") {
          entries[name] = val;
        }
      }
      if (Object.keys(entries).length === 0) {
        toast("Nenhuma nova chave informada para atualizar.", "info");
        return;
      }
      try {
        state.apiKeysStatus = await backend.SaveAPIKeys(entries);
        if (entries["OPENCODE_API_KEY"] || entries["OPENCODE_BASE_URL"]) {
          const ocRes = await backend.BootstrapOpenCode?.(true);
          if (ocRes) {
            state.openCodeStatus = ocRes.status;
            if (ocRes.goModels?.length) state.openCodeGoModels = ocRes.goModels;
            if (ocRes.models?.length) state.openCodeModels = ocRes.models;
          }
        }
        state.providersScan = null;
        updateStatusBar();
        toast("Chaves globais salvas.", "success");
        setStatus("Chaves de API atualizadas.");
        renderModalLayer();
      } catch (err: any) {
        const errEl = document.getElementById("ak-modal-err");
        if (errEl) {
          errEl.textContent = `Falha ao salvar: ${err?.message || String(err)}`;
          errEl.style.display = "block";
        }
      }
    });
    return;
  }

  if (state.showMobileModal) {
    const mb = state.mobileStatus;
    const pr = state.mobilePairing;
    // QR só entra no DOM se for um data:image válido (W4-07: safeImageSrc).
    const qrSrc = mb?.running ? safeImageSrc(pr?.qrDataUrl) : "";
    layer.innerHTML = `
      <div class="modal-backdrop" id="mb-modal-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:580px">
          <h3 class="modal-title">App Mobile — Continuar Trabalhos no Celular</h3>
          <p class="dim" style="font-size:12px;margin-top:-4px;margin-bottom:10px;line-height:1.5">
            Pareie o <strong>HiveMind Mobile</strong> para acompanhar equipes, responder aprovações e ver diffs.
            O motor continua no PC. Na mesma rede, use a LAN abaixo. De qualquer lugar, use o relay (os dois lados
            só abrem conexão de saída).
          </p>
          <div style="display:flex;align-items:center;gap:10px;margin-bottom:12px">
            <span style="font-weight:600">Estado:</span>
            <span style="color:${mb?.running ? "var(--green)" : "var(--text-dim)"}">${mb?.running ? `ativo · porta ${mb.port}` : "desligado"}</span>
            <div class="spacer"></div>
            <button id="mb-toggle" class="${mb?.running ? "danger mini" : "primary mini"}">${mb?.running ? "Desligar Bridge" : "Ligar Bridge"}</button>
          </div>
          ${qrSrc ? `
            <div style="display:flex;gap:16px;align-items:center;flex-wrap:wrap">
              <div style="background:#fff;padding:8px;border-radius:8px">
                <img src="${escapeHtml(qrSrc)}" alt="QR de pareamento" style="width:208px;height:208px;image-rendering:pixelated" />
              </div>
              <div style="flex:1;min-width:220px">
                <label style="font-size:11px;color:var(--text-dim)">URLs (Tailscale primeiro):</label>
                <div class="dim" style="font-family:var(--mono);font-size:11px;margin:4px 0 10px;word-break:break-all">
                  ${((pr?.urls) || []).map((u) => `<div>• ${escapeHtml(u)}</div>`).join("") || "—"}
                </div>
                <label style="font-size:11px;color:var(--text-dim)">Oferta de uso único</label>
                <div class="dim" style="font-size:11px;margin:4px 0 10px">Válida até ${escapeHtml(pr?.exp ? new Date(pr.exp * 1000).toLocaleString("pt-BR") : "—")}. Um resgate só.</div>
                <div style="display:flex;gap:6px;align-items:center;margin:4px 0 10px">
                  <button id="mb-copy" class="mini" type="button">Copiar oferta</button>
                </div>
                <button id="mb-rotate" class="mini" type="button" title="Invalida todas as conexões ativas">Rotacionar sessão / Desconectar celulares</button>
              </div>
            </div>
          ` : `
            <div class="dim" style="font-size:12px;padding:12px;border:1px dashed var(--border);border-radius:8px;text-align:center">
              ${mb?.running ? "Gerando QR…" : "Ligue o Bridge para exibir o QR de pareamento."}
            </div>
          `}
          <div class="relay-panel">
            <h4 class="relay-title">Acesso remoto</h4>
            <p class="dim" style="font-size:12px;line-height:1.5;margin:0 0 10px">
              Entre na conta do relay que você hospeda. Esta IDE e o celular se conectam a ele por
              <strong>WebSocket de saída</strong>. O tráfego do bridge vai cifrado de ponta a ponta; a LAN acima não muda.
            </p>
            <div id="relay-status-line" class="relay-status">${relayStatusLine()}</div>
            ${!state.relayStatus?.loggedIn ? `
              <div class="dim" style="font-size:11px;line-height:1.45;margin:8px 0;padding:8px 10px;border:1px dashed var(--border);border-radius:6px">
                <strong>Primeiro acesso:</strong> o usuário inicial é criado no servidor do relay com
                <code>HIVEMIND_SEED_EMAIL</code> / <code>HIVEMIND_SEED_PASSWORD</code> (ou gerado em
                <code>initial-password.txt</code>) ou via CLI:
                <code>hivemind-relay account set --email &lt;usuário&gt; --password &lt;senha&gt; --role owner</code>.
              </div>
            ` : ""}
            <div class="form-grid" style="margin-top:10px">
              <label for="relay-url">URL do relay</label>
              <input id="relay-url" type="url" placeholder="https://relay.seudominio" value="${escapeHtml(state.relayDraft.baseUrl)}" autocomplete="off" />
              <label for="relay-email">E-mail ou usuário</label>
              <input id="relay-email" type="text" placeholder="gilvan.b ou voce@exemplo" value="${escapeHtml(state.relayDraft.email)}" autocomplete="username" />
              <label for="relay-password">Senha</label>
              <input id="relay-password" type="password" placeholder="mínimo 8 caracteres (não fica salva)" value="${escapeHtml(state.relayDraft.password)}" autocomplete="current-password" />
            </div>
            <div class="relay-actions">
              <button class="primary mini" id="relay-login" type="button">Entrar</button>
              <button class="mini" id="relay-toggle" type="button">${state.relayStatus?.enabled ? "Desligar acesso remoto" : "Ligar acesso remoto"}</button>
              <button class="mini" id="relay-logout" type="button">Sair da conta</button>
            </div>
            ${state.relayStatus?.loggedIn ? `
              <details style="margin-top:10px;border:1px solid var(--border);border-radius:8px;padding:8px 10px">
                <summary style="cursor:pointer;font-size:12px;font-weight:600">Gerenciar conta do relay (${escapeHtml(state.relayStatus.email)}${state.relayStatus.role ? ` · ${escapeHtml(state.relayStatus.role)}` : ""})</summary>
                <div style="margin-top:10px">
                  <div style="font-size:11px;font-weight:600;margin-bottom:6px">Trocar senha</div>
                  <div class="form-grid">
                    <label for="relay-cur-pass">Senha atual</label>
                    <input id="relay-cur-pass" type="password" placeholder="Senha atual" value="${escapeHtml(state.relayDraft.currentPassword)}" autocomplete="current-password" />
                    <label for="relay-new-pass">Nova senha</label>
                    <input id="relay-new-pass" type="password" placeholder="Mínimo 8 caracteres" value="${escapeHtml(state.relayDraft.newPassword)}" autocomplete="new-password" />
                  </div>
                  <div class="relay-actions" style="margin-top:6px">
                    <button class="mini" id="relay-change-pass-btn" type="button">Atualizar senha</button>
                  </div>
                </div>
                ${state.relayStatus.role === "owner" ? `
                  <div style="margin-top:12px;padding-top:10px;border-top:1px solid var(--border)">
                    <div style="font-size:11px;font-weight:600;margin-bottom:6px">Criar novo usuário (membro)</div>
                    <div class="form-grid">
                      <label for="relay-new-user-email">E-mail ou usuário</label>
                      <input id="relay-new-user-email" type="text" placeholder="novo.usuario ou email@exemplo" value="${escapeHtml(state.relayDraft.newUserEmail)}" autocomplete="off" />
                      <label for="relay-new-user-pass">Senha inicial</label>
                      <input id="relay-new-user-pass" type="password" placeholder="Mínimo 8 caracteres" value="${escapeHtml(state.relayDraft.newUserPassword)}" autocomplete="new-password" />
                    </div>
                    <div class="relay-actions" style="margin-top:6px">
                      <button class="mini" id="relay-create-user-btn" type="button">Criar usuário</button>
                    </div>
                  </div>
                ` : ""}
              </details>
            ` : ""}
            ${state.relayStatus?.loggedIn ? `
              <div class="relay-actions" style="margin-top:8px">
                <button class="primary mini" id="relay-make-qr-login" type="button">Gerar QR Code para entrar no celular (5G / Remoto)</button>
              </div>
            ` : ""}
            ${state.relayStatus?.linked ? `
              <div class="dim" style="font-size:12px;margin-top:8px">
                IDE vinculada${state.relayStatus.deviceName ? `: ${escapeHtml(state.relayStatus.deviceName)}` : ""}${state.relayStatus.e2e ? " · chave do celular instalada" : " · aguardando a chave do celular"}
                <button class="mini" id="relay-unlink" type="button" style="margin-left:8px">Desvincular</button>
              </div>
            ` : `
              <div class="relay-link-row">
                <button class="mini" id="relay-make-code" type="button">Gerar código para o celular</button>
                <input id="relay-claim" placeholder="Código gerado no celular" value="${escapeHtml(state.relayDraft.claim)}" autocomplete="off" />
                <button class="mini" id="relay-claim-btn" type="button">Confirmar código</button>
              </div>
            `}
            ${state.relayLink ? `
              <div class="relay-code-box" style="margin-top:10px">
                ${safeImageSrc(state.relayLink.qrDataUrl) ? `<img src="${escapeHtml(safeImageSrc(state.relayLink.qrDataUrl))}" alt="QR do acesso remoto" width="176" height="176" style="background:#fff;padding:6px;border-radius:8px" />` : ""}
                <div>
                  <div style="font-family:var(--mono);font-variant-numeric:tabular-nums;font-size:16px;letter-spacing:0.06em">${state.relayLink.code === "QR-LOGIN" ? "Escaneie no app do celular" : escapeHtml(state.relayLink.code)}</div>
                  <div class="dim" style="font-size:11px;margin-top:4px">${state.relayLink.code === "QR-LOGIN" ? "Faz login automático no relay sem digitar URL, e-mail ou senha." : ""}</div>
                  <div class="dim" style="font-size:11px;margin-top:4px">Expira ${escapeHtml(state.relayLink.expiresAt)}</div>
                </div>
              </div>
            ` : ""}
          </div>
          <div id="mb-modal-err" style="color:var(--red);font-size:12px;margin-top:8px;display:none"></div>
          <div class="modal-footer">
            <button id="mb-close">Fechar</button>
          </div>
        </div>
      </div>
    `;

    const closeMb = () => {
      state.showMobileModal = false;
      stopRelayPoll();
      renderModalLayer();
    };
    startRelayPoll();
    const bindDraft = (
      id: string,
      key: "baseUrl" | "email" | "password" | "claim" | "currentPassword" | "newPassword" | "newUserEmail" | "newUserPassword",
    ) => {
      document.getElementById(id)?.addEventListener("input", (ev) => {
        state.relayDraft[key] = (ev.target as HTMLInputElement).value;
      });
    };
    bindDraft("relay-url", "baseUrl");
    bindDraft("relay-email", "email");
    bindDraft("relay-password", "password");
    bindDraft("relay-claim", "claim");
    bindDraft("relay-cur-pass", "currentPassword");
    bindDraft("relay-new-pass", "newPassword");
    bindDraft("relay-new-user-email", "newUserEmail");
    bindDraft("relay-new-user-pass", "newUserPassword");
    const showMbErr = (err: unknown) => {
      const errEl = document.getElementById("mb-modal-err");
      if (errEl) {
        const reason = err instanceof Error ? err.message : String(err);
        errEl.textContent = reason;
        errEl.style.display = "block";
      }
    };
    document.getElementById("mb-close")?.addEventListener("click", closeMb);
    document.getElementById("mb-modal-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement)?.id === "mb-modal-backdrop") closeMb();
    });
    document.getElementById("mb-toggle")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.SetMobileBridgeEnabled) return;
      const next = !(state.mobileStatus?.running);
      try {
        state.mobileStatus = await backend.SetMobileBridgeEnabled(next);
        updateStatusBar();
        await refreshMobilePairing();
      } catch (err: any) {
        const errEl = document.getElementById("mb-modal-err");
        if (errEl) {
          errEl.textContent = `Falha: ${err?.message || String(err)}`;
          errEl.style.display = "block";
        }
      }
    });
    document.getElementById("mb-rotate")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.RotateMobileToken) return;
      try {
        state.mobileStatus = await backend.RotateMobileToken();
        updateStatusBar();
        await refreshMobilePairing();
        toast("Token rotacionado — todos os celulares foram desconectados.", "success");
      } catch (err: any) {
        const errEl = document.getElementById("mb-modal-err");
        if (errEl) {
          errEl.textContent = `Falha: ${err?.message || String(err)}`;
          errEl.style.display = "block";
        }
      }
    });
    document.getElementById("mb-copy")?.addEventListener("click", async () => {
      const uri = state.mobilePairing?.uri;
      if (!uri) return;
      try {
        await navigator.clipboard.writeText(uri);
        toast("Oferta copiada.", "success");
      } catch {
        // clipboard indisponível
      }
    });
    document.getElementById("relay-login")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.RelayLogin) return;
      const btn = document.getElementById("relay-login") as HTMLButtonElement | null;
      if (btn) btn.textContent = "Entrando…";
      try {
        state.relayStatus = await backend.RelayLogin(state.relayDraft.baseUrl.trim(), state.relayDraft.email.trim(), state.relayDraft.password);
        state.relayDraft.password = "";
        updateStatusBar();
        renderModalLayer();
        toast("Conta do relay conectada.", "success");
      } catch (err) {
        showMbErr(err);
        if (btn) btn.textContent = "Entrar";
      }
    });
    document.getElementById("relay-toggle")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.RelaySetEnabled) return;
      try {
        state.relayStatus = await backend.RelaySetEnabled(!state.relayStatus?.enabled);
        updateStatusBar();
        renderModalLayer();
      } catch (err) {
        showMbErr(err);
      }
    });
    document.getElementById("relay-logout")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.RelayLogout) return;
      try {
        state.relayStatus = await backend.RelayLogout();
        state.relayLink = null;
        updateStatusBar();
        renderModalLayer();
      } catch (err) {
        showMbErr(err);
      }
    });
    document.getElementById("relay-change-pass-btn")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.RelayChangePassword) return;
      if (!state.relayDraft.currentPassword || state.relayDraft.newPassword.length < 8) {
        showMbErr("Informe a senha atual e uma nova senha com pelo menos 8 caracteres.");
        return;
      }
      try {
        state.relayStatus = await backend.RelayChangePassword(state.relayDraft.currentPassword, state.relayDraft.newPassword);
        state.relayDraft.currentPassword = "";
        state.relayDraft.newPassword = "";
        updateStatusBar();
        renderModalLayer();
        toast("Senha do relay atualizada.", "success");
      } catch (err) {
        showMbErr(err);
      }
    });
    document.getElementById("relay-create-user-btn")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.RelayCreateAccount) return;
      const user = state.relayDraft.newUserEmail.trim();
      const pass = state.relayDraft.newUserPassword;
      if (user.length < 3 || pass.length < 8) {
        showMbErr("Informe um e-mail ou usuário (mín. 3 caracteres) e senha com pelo menos 8 caracteres.");
        return;
      }
      try {
        await backend.RelayCreateAccount(user, pass);
        state.relayDraft.newUserEmail = "";
        state.relayDraft.newUserPassword = "";
        renderModalLayer();
        toast(`Usuário ${user} criado no relay.`, "success");
      } catch (err) {
        showMbErr(err);
      }
    });
    document.getElementById("relay-unlink")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.RelayUnlink) return;
      try {
        state.relayStatus = await backend.RelayUnlink();
        state.relayLink = null;
        updateStatusBar();
        renderModalLayer();
      } catch (err) {
        showMbErr(err);
      }
    });
    document.getElementById("relay-make-qr-login")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.RelayCreateQRLogin) return;
      try {
        state.relayLink = await backend.RelayCreateQRLogin();
        renderModalLayer();
      } catch (err) {
        showMbErr(err);
      }
    });
    document.getElementById("relay-make-code")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.RelayCreateLinkCode) return;
      try {
        state.relayLink = await backend.RelayCreateLinkCode();
        renderModalLayer();
      } catch (err) {
        showMbErr(err);
      }
    });
    document.getElementById("relay-claim-btn")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.RelayClaimLinkCode) return;
      try {
        state.relayStatus = await backend.RelayClaimLinkCode(state.relayDraft.claim.trim());
        state.relayDraft.claim = "";
        updateStatusBar();
        renderModalLayer();
        toast("IDE vinculada à conta.", "success");
      } catch (err) {
        showMbErr(err);
      }
    });
    return;
  }

  if (state.showGitHubModal) {
    const gh = state.ghStatus;
    const baseUrl = gh?.baseUrl || "https://api.github.com";
    const statusBadge = gh?.loggedIn
      ? `<span style="color:var(--green);font-weight:600">Conectado como @${escapeHtml(gh.user)} · ${escapeHtml(gh.connectorLabel || "API Nativa")}${gh.tokenMasked ? ` (${escapeHtml(gh.tokenMasked)})` : ""}</span>`
      : `<span style="color:var(--amber)">${escapeHtml(gh?.detail || "Desconectado — informe um Token GitHub (PAT) ou autentique o gh CLI")}</span>`;

    layer.innerHTML = `
      <div class="modal-backdrop" id="gh-modal-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:540px">
          <h3 class="modal-title">Conector GitHub Nativo (API REST v2022-11-28)</h3>
          <p class="dim" style="font-size:12px;margin-top:-4px;margin-bottom:10px">
            O HiveMind conversa diretamente com a API oficial do GitHub (PRs, revisões, comentários inline no código, diffs e issues) — sem depender de wrappers frágeis de CLI.
            Se você já está logado no <code>gh</code>, o conector usa sua sessão via API nativa automaticamente; ou salve um Personal Access Token dedicado abaixo.
          </p>
          <div style="font-size:12px;margin-bottom:8px"><strong>Status:</strong> ${statusBadge}</div>
          ${gh?.remote ? `<div class="dim" style="font-size:11px;margin-bottom:10px;font-family:var(--mono);word-break:break-all">Remote: ${escapeHtml(gh.remote)}</div>` : ""}
          <div class="form-grid">
            <label>GitHub Token (PAT):</label>
            <input id="gh-api-token" type="password" placeholder="${gh?.source === "config" ? "Cole novo token para substituir…" : "ghp_… ou github_pat_… (escopos: repo, read:org)"}" autocomplete="off" />

            <label>API Base URL:</label>
            <input id="gh-base-url" value="${escapeHtml(baseUrl)}" placeholder="https://api.github.com" />
          </div>
          <div class="dim" style="font-size:11px;margin-top:8px;line-height:1.45">
            Prioridade de autenticação: <code>GITHUB_TOKEN</code>/<code>GH_TOKEN</code> → Token salvo neste conector (<code>github-connector.json</code>, fora do repo) → sessão <code>gh auth token</code>.
          </div>
          <div id="gh-modal-err" style="color:var(--red);font-size:12px;margin-top:8px;display:none"></div>
          <div class="modal-footer">
            ${gh?.source === "config" ? `<button class="danger mini" id="gh-clear-token">Remover Token Salvo</button>` : ""}
            <div class="spacer"></div>
            <button id="gh-close">Fechar</button>
            <button class="primary" id="gh-save">Salvar & Testar</button>
          </div>
        </div>
      </div>
    `;

    const closeGh = () => {
      state.showGitHubModal = false;
      renderModalLayer();
    };
    document.getElementById("gh-close")?.addEventListener("click", closeGh);
    document.getElementById("gh-modal-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement)?.id === "gh-modal-backdrop") closeGh();
    });

    document.getElementById("gh-clear-token")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.SaveGitHubConfig) return;
      try {
        state.ghStatus = await backend.SaveGitHubConfig("", "", state.project?.root || "");
        renderLeftSidebar();
        renderModalLayer();
        setStatus("Token salvo do Conector GitHub removido.");
      } catch (err: any) {
        const errEl = document.getElementById("gh-modal-err");
        if (errEl) {
          errEl.textContent = `Falha ao remover token: ${err?.message || String(err)}`;
          errEl.style.display = "block";
        }
      }
    });

    document.getElementById("gh-save")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.SaveGitHubConfig) return;
      const tokIn = (document.getElementById("gh-api-token") as HTMLInputElement | null)?.value.trim() || "";
      const baseIn = (document.getElementById("gh-base-url") as HTMLInputElement | null)?.value.trim() || "https://api.github.com";
      const errEl = document.getElementById("gh-modal-err");
      const btn = document.getElementById("gh-save") as HTMLButtonElement | null;
      if (btn) {
        btn.disabled = true;
        btn.textContent = "Testando…";
      }
      try {
        if (!tokIn) {
          // Re-test current active connector (config, env or gh auth token) without wiping a saved token.
          state.ghStatus = await backend.GitHubStatus(state.project?.root || "");
        } else {
          state.ghStatus = await backend.SaveGitHubConfig(tokIn, baseIn, state.project?.root || "");
        }
        renderLeftSidebar();
        if (state.ghStatus?.loggedIn) {
          closeGh();
          toast(`GitHub conectado como @${state.ghStatus.user} (${state.ghStatus.connectorLabel || "API Nativa"}).`, "success");
          if (state.project?.branch) {
            void loadGitHubPRs();
            void loadProjectIssues();
          }
        } else {
          renderModalLayer();
          const refreshedErr = document.getElementById("gh-modal-err");
          if (refreshedErr) {
            refreshedErr.textContent = state.ghStatus?.detail || "Não foi possível autenticar no GitHub.";
            refreshedErr.style.display = "block";
          }
        }
      } catch (err: any) {
        if (errEl) {
          errEl.textContent = `Falha ao salvar configuração GitHub: ${err?.message || String(err)}`;
          errEl.style.display = "block";
        }
        if (btn) {
          btn.disabled = false;
          btn.textContent = "Salvar & Testar";
        }
      }
    });
    return;
  }

  if (state.showJiraModal) {
    const jira = state.jiraStatus;
    const profiles = jira?.profiles || [];
    const wsRoot = state.project?.root || "";
    const isNewMode = state.jiraEditingProfileId === "__new__";
    const selectedProfile: JiraProfileSummary | undefined = isNewMode
      ? undefined
      : (profiles.find((p) => p.id === (state.jiraEditingProfileId || jira?.activeProfileId)) || profiles[0]);

    const statusBadge = jira?.connected
      ? `<span style="color:var(--green);font-weight:600">Conectado como ${escapeHtml(jira.user || jira.email)}${jira.activeProfileName ? ` · ${escapeHtml(jira.activeProfileName)}` : ""}${jira.matchMode === "auto_org" ? ` (auto: ${escapeHtml(jira.matchedRule || jira.detectedOrg || "")})` : jira.matchMode === "manual" ? " (fixado)" : ""}</span>`
      : jira?.matchMode === "isolated_none"
        ? `<span style="color:var(--text-dim)">${escapeHtml(jira.detail || "Sem Jira vinculado a este projeto")}</span>`
        : `<span style="color:var(--amber)">${escapeHtml(jira?.detail || "Desconectado — adicione um perfil ou importe do mcp_config.json")}</span>`;

    const projects = state.jiraProjects || [];
    const formBase = selectedProfile ? selectedProfile.baseUrl : (isNewMode ? "" : (jira?.baseUrl || ""));
    const formEmail = selectedProfile ? selectedProfile.email : (isNewMode ? "" : (jira?.email || ""));
    const formName = selectedProfile ? selectedProfile.name : "";
    const formOrgs = selectedProfile?.orgPatterns?.join(", ") || "";
    const formDefProj = (selectedProfile ? selectedProfile.defaultProject : (isNewMode ? "" : (jira?.defaultProject || ""))).toUpperCase();
    const formHasTok = selectedProfile ? selectedProfile.hasToken : (jira?.source === "config");
    const hasMatchProj = !isNewMode && projects.some((p) => p.key.toUpperCase() === formDefProj);
    const suggestedOrg = jira?.detectedOrg || jira?.suggestedPatterns?.[0] || "";

    layer.innerHTML = `
      <div class="modal-backdrop" id="jira-modal-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:620px;max-height:86vh;display:flex;flex-direction:column">
          <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:6px;flex-wrap:wrap">
            <h3 class="modal-title" style="margin:0">Conector Jira Cloud (Multi-Organização)</h3>
            <div style="display:flex;gap:6px">
              <button type="button" class="mini" id="jira-import-mcp" title="Importar automaticamente de .agents/mcp_config.json, .mcp.json ou .cursor/mcp.json">${icons.download}<span>Importar MCP</span></button>
              <button type="button" class="mini" id="jira-open-token-help" title="Abrir página da Atlassian para criar um API Token">${icons.openInNew}<span>Gerar Token ↗</span></button>
            </div>
          </div>

          <div style="overflow-y:auto;padding-right:4px;flex:1">
            ${wsRoot ? `
              <div style="padding:10px 12px;border-radius:10px;background:var(--bg-card);border:1px solid var(--border);margin-bottom:10px;display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap">
                <div style="font-size:12px">
                  <strong>Projeto atual:</strong> ${escapeHtml(state.project?.name || "")}
                  ${jira?.detectedOrg ? `<span class="dim"> · org Git: <code>${escapeHtml(jira.detectedOrg)}</code></span>` : `<span class="dim"> · sem org Git</span>`}
                  <div style="margin-top:3px">${statusBadge}</div>
                </div>
                <select id="jira-ws-bind" style="font-size:12px;padding:5px 8px;min-width:190px">
                  <option value="auto" ${!jira?.bindingMode ? "selected" : ""}>Auto (detectar pela org)</option>
                  <option value="none" ${jira?.bindingMode === "none" ? "selected" : ""}>Sem Jira neste projeto</option>
                  ${profiles.map((p) => `<option value="${escapeHtml(p.id)}" ${jira?.bindingMode === p.id ? "selected" : ""}>Fixar: ${escapeHtml(p.name)}</option>`).join("")}
                </select>
              </div>
            ` : `<div style="font-size:12px;margin-bottom:10px"><strong>Status:</strong> ${statusBadge}</div>`}

            <div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap;margin-bottom:10px">
              <button type="button" class="mini ${isNewMode || profiles.length === 0 ? "primary" : ""}" id="jira-profile-new">+ Novo Perfil Jira</button>
              ${profiles.map((p) => {
                const isSel = selectedProfile?.id === p.id;
                const isAct = jira?.activeProfileId === p.id && jira?.connected;
                return `<button type="button" class="mini jira-profile-chip ${isSel ? "primary" : ""}" data-id="${escapeHtml(p.id)}">${escapeHtml(p.name)}${isAct ? " ✓" : ""}</button>`;
              }).join("")}
            </div>

            <div class="form-grid">
              <label for="jira-prof-name">Nome do Perfil:</label>
              <input id="jira-prof-name" value="${escapeHtml(formName)}" placeholder="ex.: Vibetex ou Empresa B" />

              <label for="jira-prof-orgs">Orgs Git / Pastas:</label>
              <div style="display:flex;gap:6px">
                <input id="jira-prof-orgs" style="flex:1" value="${escapeHtml(formOrgs)}" placeholder="ex.: vibetex, projeto-vibetex (auto-detecção)" />
                ${suggestedOrg ? `<button type="button" class="mini" id="jira-use-org">+ ${escapeHtml(suggestedOrg)}</button>` : ""}
              </div>

              <label for="jira-base-url">URL do Jira:</label>
              <input id="jira-base-url" value="${escapeHtml(formBase)}" placeholder="sua-empresa.atlassian.net" />

              <label for="jira-email">E-mail (Atlassian):</label>
              <input id="jira-email" value="${escapeHtml(formEmail)}" placeholder="voce@empresa.com" autocomplete="off" />

              <label for="jira-api-token">API Token:</label>
              <input id="jira-api-token" type="password" placeholder="${formHasTok ? "Token salvo — cole outro apenas se quiser trocar" : "Cole o API Token gerado na Atlassian"}" autocomplete="off" />

              <label for="jira-project">Projeto padrão:</label>
              ${!isNewMode && projects.length > 0 && selectedProfile?.id === jira?.activeProfileId
                ? `<select id="jira-project">
                    <option value="">(Selecionar projeto dinamicamente…)</option>
                    ${projects.map((p) => `<option value="${escapeHtml(p.key)}" ${p.key.toUpperCase() === formDefProj ? "selected" : ""}>${escapeHtml(p.key)} — ${escapeHtml(p.name)}</option>`).join("")}
                    ${formDefProj && !hasMatchProj ? `<option value="${escapeHtml(formDefProj)}" selected>${escapeHtml(formDefProj)} (atual)</option>` : ""}
                  </select>`
                : `<input id="jira-project" value="${escapeHtml(formDefProj)}" placeholder="Opcional — ex.: IPG" />`
              }

              <label></label>
              <label style="display:flex;align-items:center;gap:6px;font-weight:400;font-size:12px;cursor:pointer">
                <input type="checkbox" id="jira-prof-default" ${selectedProfile?.isDefault ? "checked" : ""} />
                Perfil padrão fallback (usado quando o projeto não bater com nenhuma org)
              </label>
            </div>
            <div class="dim" style="font-size:11px;margin-top:8px;line-height:1.45">
              Quando há perfis com <strong>Orgs Git / Pastas</strong>, repositórios de outra organização sem regra vinculada ficam isolados sem Jira para evitar erros entre empresas.
            </div>
            <div id="jira-modal-err" style="color:var(--red);font-size:12px;margin-top:8px;display:none"></div>
          </div>

          <div class="modal-footer" style="margin-top:10px">
            ${selectedProfile ? `<button class="danger mini" id="jira-delete-profile">Excluir perfil</button>` : ""}
            <div class="spacer"></div>
            <button id="jira-close">Fechar</button>
            ${wsRoot ? `<button id="jira-save-pin">Salvar &amp; Fixar aqui</button>` : ""}
            <button class="primary" id="jira-save">Salvar &amp; Testar</button>
          </div>
        </div>
      </div>
    `;

    const closeJira = () => {
      state.showJiraModal = false;
      state.jiraEditingProfileId = null;
      renderModalLayer();
    };
    document.getElementById("jira-close")?.addEventListener("click", closeJira);
    document.getElementById("jira-open-token-help")?.addEventListener("click", () => {
      void api()?.OpenExternalURL("https://id.atlassian.com/manage-profile/security/api-tokens");
    });
    document.getElementById("jira-modal-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement)?.id === "jira-modal-backdrop") closeJira();
    });

    document.getElementById("jira-profile-new")?.addEventListener("click", () => {
      state.jiraEditingProfileId = "__new__";
      renderModalLayer();
    });
    document.querySelectorAll<HTMLElement>(".jira-profile-chip").forEach((btn) => {
      btn.addEventListener("click", () => {
        state.jiraEditingProfileId = btn.dataset.id || null;
        renderModalLayer();
      });
    });
    document.getElementById("jira-use-org")?.addEventListener("click", () => {
      const el = document.getElementById("jira-prof-orgs") as HTMLInputElement | null;
      if (el && suggestedOrg) {
        const cur = el.value.split(",").map((s) => s.trim()).filter(Boolean);
        if (!cur.some((c) => c.toLowerCase() === suggestedOrg.toLowerCase())) cur.push(suggestedOrg);
        el.value = cur.join(", ");
      }
    });

    document.getElementById("jira-ws-bind")?.addEventListener("change", async (e) => {
      const target = (e.target as HTMLSelectElement).value;
      const backend = api();
      if (!backend?.BindJiraWorkspace || !wsRoot) return;
      try {
        state.jiraStatus = await backend.BindJiraWorkspace(wsRoot, target);
        state.jiraEditingProfileId = null;
        await refreshJiraStatusAndProjects();
        void loadProjectIssues();
      } catch (err: any) {
        toastError("vincular Jira ao projeto", err);
      }
    });

    document.getElementById("jira-import-mcp")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.ImportJiraFromMCP) return;
      const errEl = document.getElementById("jira-modal-err");
      try {
        state.jiraStatus = await backend.ImportJiraFromMCP(wsRoot);
        state.jiraEditingProfileId = null;
        await refreshJiraStatusAndProjects();
        if (state.jiraStatus?.connected) {
          if (state.teamConfigDraft) {
            state.teamConfigDraft.jiraEnabled = true;
            if (!state.teamConfigDraft.jiraProject && state.jiraProjects[0]?.key) {
              state.teamConfigDraft.jiraProject = state.jiraProjects[0].key;
            }
          }
          if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) renderEditorBody();
          toast(`Jira importado do MCP e conectado como ${state.jiraStatus.user || state.jiraStatus.email}!`, "success");
          void loadProjectIssues();
        } else if (errEl) {
          errEl.textContent = state.jiraStatus?.detail || "Credenciais importadas, mas falha ao autenticar.";
          errEl.style.display = "block";
        }
      } catch (err: any) {
        if (errEl) {
          errEl.textContent = err?.message || String(err);
          errEl.style.display = "block";
        }
      }
    });

    document.getElementById("jira-delete-profile")?.addEventListener("click", async () => {
      const backend = api();
      if (!selectedProfile) return;
      try {
        if (backend?.DeleteJiraProfile) {
          state.jiraStatus = await backend.DeleteJiraProfile(selectedProfile.id, wsRoot);
        } else if (backend?.SaveJiraConfig) {
          state.jiraStatus = await backend.SaveJiraConfig("", "", "", "", true);
        }
        state.jiraEditingProfileId = null;
        await refreshJiraStatusAndProjects();
        setStatus("Perfil Jira removido.");
      } catch (err: any) {
        const errEl = document.getElementById("jira-modal-err");
        if (errEl) {
          errEl.textContent = `Falha ao excluir perfil: ${err?.message || String(err)}`;
          errEl.style.display = "block";
        }
      }
    });

    const saveJiraAction = async (pinWorkspace: boolean) => {
      const backend = api();
      const nameIn = (document.getElementById("jira-prof-name") as HTMLInputElement | null)?.value.trim() || "";
      const orgsRaw = (document.getElementById("jira-prof-orgs") as HTMLInputElement | null)?.value || "";
      const orgPatterns = orgsRaw.split(/[,;\n]/).map((s) => s.trim()).filter(Boolean);
      const baseIn = (document.getElementById("jira-base-url") as HTMLInputElement | null)?.value.trim() || "";
      const emailIn = (document.getElementById("jira-email") as HTMLInputElement | null)?.value.trim() || "";
      const tokIn = (document.getElementById("jira-api-token") as HTMLInputElement | null)?.value.trim() || "";
      const projIn = (document.getElementById("jira-project") as HTMLInputElement | HTMLSelectElement | null)?.value.trim().toUpperCase() || "";
      const isDefIn = Boolean((document.getElementById("jira-prof-default") as HTMLInputElement | null)?.checked);
      const errEl = document.getElementById("jira-modal-err");
      const btn = document.getElementById("jira-save") as HTMLButtonElement | null;
      if (btn) {
        btn.disabled = true;
        btn.textContent = "Conectando…";
      }
      try {
        if (backend?.SaveJiraProfile) {
          state.jiraStatus = await backend.SaveJiraProfile(
            {
              id: selectedProfile?.id || "",
              name: nameIn,
              base_url: baseIn,
              email: emailIn,
              token: tokIn,
              default_project: projIn,
              org_patterns: orgPatterns,
              is_default: isDefIn,
            },
            pinWorkspace ? wsRoot : "",
          );
        } else if (backend?.SaveJiraConfig) {
          state.jiraStatus = await backend.SaveJiraConfig(baseIn, emailIn, tokIn, projIn, false);
        }
        state.jiraEditingProfileId = null;
        await refreshJiraStatusAndProjects();
        if (state.jiraStatus?.connected) {
          if (state.teamConfigDraft) {
            state.teamConfigDraft.jiraEnabled = true;
            if (!state.teamConfigDraft.jiraProject && (projIn || state.jiraProjects[0]?.key)) {
              state.teamConfigDraft.jiraProject = projIn || state.jiraProjects[0]?.key || "";
            }
          }
          updateStatusBar();
          if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) renderEditorBody();
          toast(`Jira conectado (${state.jiraStatus.activeProfileName || state.jiraStatus.user || emailIn}).`, "success");
          void loadProjectIssues();
          if (pinWorkspace) closeJira();
          else renderModalLayer();
        } else {
          updateStatusBar();
          renderModalLayer();
          const refreshedErr = document.getElementById("jira-modal-err");
          if (refreshedErr) {
            refreshedErr.textContent = state.jiraStatus?.detail || "Perfil salvo, mas este projeto usa outra organização ou falhou a autenticação.";
            refreshedErr.style.display = "block";
          }
        }
      } catch (err: any) {
        if (errEl) {
          errEl.textContent = `Falha ao salvar perfil Jira: ${err?.message || String(err)}`;
          errEl.style.display = "block";
        }
        if (btn) {
          btn.disabled = false;
          btn.textContent = "Salvar & Testar";
        }
      }
    };
    document.getElementById("jira-save")?.addEventListener("click", () => void saveJiraAction(false));
    document.getElementById("jira-save-pin")?.addEventListener("click", () => void saveJiraAction(true));
    return;
  }

  if (state.skillEditor) {
    const ed = state.skillEditor;
    layer.innerHTML = `
      <div class="modal-backdrop" id="skill-editor-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:620px">
          <h3 class="modal-title">${ed.mode === "create" ? "Nova Skill Nativa da IDE" : `Editar Skill Nativa — ${escapeHtml(ed.name)}`}</h3>
          <p class="dim" style="font-size:11.5px;margin-top:-6px;margin-bottom:10px">
            Skills nativas vivem em %LOCALAPPDATA%\\HiveMind\\skills e valem para todos os projetos.
            Comece com <code># Título</code> e uma frase dizendo <strong>quando usá-la</strong> — é isso que o Chefe lê para decidir se aplica à tarefa.
          </p>
          <div class="form-grid" style="grid-template-columns:120px 1fr;margin-bottom:10px">
            <label>Nome:</label>
            <input id="skill-editor-name" value="${escapeHtml(ed.name)}" placeholder="ex: code-review" ${ed.mode === "edit" ? "disabled" : ""} />
          </div>
          <textarea id="skill-editor-body" style="width:100%;height:300px;font-family:var(--mono);font-size:12px;line-height:1.5">${escapeHtml(ed.body)}</textarea>
          <div id="skill-editor-err" style="color:var(--red);font-size:12px;margin-top:8px;display:none"></div>
          <div class="modal-footer">
            <button id="skill-editor-cancel">Cancelar</button>
            <button class="primary" id="skill-editor-save">${icons.save}<span>Salvar Skill</span></button>
          </div>
        </div>
      </div>
    `;
    const closeEd = () => {
      state.skillEditor = null;
      renderModalLayer();
    };
    document.getElementById("skill-editor-cancel")?.addEventListener("click", closeEd);
    document.getElementById("skill-editor-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement).id === "skill-editor-backdrop") closeEd();
    });
    document.getElementById("skill-editor-save")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend) return;
      const nameIn = (document.getElementById("skill-editor-name") as HTMLInputElement | null)?.value.trim() || "";
      const bodyIn = (document.getElementById("skill-editor-body") as HTMLTextAreaElement | null)?.value || "";
      const errEl = document.getElementById("skill-editor-err");
      if (!nameIn) {
        if (errEl) {
          errEl.textContent = "Dê um nome para a skill (ex: code-review).";
          errEl.style.display = "block";
        }
        return;
      }
      try {
        await backend.SaveNativeSkill(nameIn, bodyIn);
        closeEd();
        setStatus(`Skill nativa "${nameIn.toLowerCase()}" salva.`);
        await refreshSkills();
      } catch (err: any) {
        const reason = err instanceof Error ? err.message : String(err);
        if (errEl) {
          errEl.textContent = `Não foi possível salvar a skill: ${reason}`;
          errEl.style.display = "block";
        }
      }
    });
    return;
  }

  layer.innerHTML = "";
}

let jevPolicyDraft: JevPolicy | null = null;

function numField(id: string, fallback: number): number {
  const raw = (document.getElementById(id) as HTMLInputElement | null)?.value ?? "";
  const n = Number(raw);
  return Number.isFinite(n) ? n : fallback;
}

export function renderJevPolicyEditorHtml(pol: JevPolicy | null): string {
  const p = pol ?? {
    version: 1,
    enabled: false,
    weights: { quality: 0.35, cost: 0.5, latency: 0.15 },
    autoEscalateUsd: 0.02,
    maxEscalateRatio: 3,
    dailyUsd: { openrouter: 1, opencode: 1, gemini: 1, openai: 1, anthropic: 1 },
    allowDataClass: ["cli_account", "http_api", "workspace_agent"],
    classifyEnabled: true,
  };
  const daily = p.dailyUsd || {};
  const allow = new Set(p.allowDataClass || []);
  const check = (id: string, on: boolean, label: string) =>
    `<label class="dim"><input type="checkbox" id="${id}" ${on ? "checked" : ""}/> ${label}</label>`;
  return `
    <div class="aiu-card">
      <div class="aiu-card-head">
        <div class="aiu-card-title">
          <strong>Política do Jev</strong>
          <span class="dim" style="font-size:11px">só neste PC · o bridge não grava</span>
        </div>
        <button type="button" class="primary mini" id="aiu-save-jev-policy">Salvar</button>
      </div>
      <div class="aiu-card-body" style="display:grid;gap:6px;font-size:12px">
        ${check("jev-enabled", p.enabled, "Jev ligado")}
        ${check("jev-classify", p.classifyEnabled !== false, "Classificador ligado (desligue para gravar false)")}
        <div style="display:flex;gap:8px;flex-wrap:wrap">
          <label class="dim">qualidade <input id="jev-w-quality" type="number" step="0.01" min="0" value="${p.weights.quality}" style="width:70px"/></label>
          <label class="dim">custo <input id="jev-w-cost" type="number" step="0.01" min="0" value="${p.weights.cost}" style="width:70px"/></label>
          <label class="dim">latência <input id="jev-w-latency" type="number" step="0.01" min="0" value="${p.weights.latency}" style="width:70px"/></label>
          <label class="dim">teto US$ <input id="jev-escalate" type="number" step="0.01" min="0" value="${p.autoEscalateUsd}" style="width:70px"/></label>
        </div>
        <div style="display:flex;gap:8px;flex-wrap:wrap">
          ${["openrouter", "opencode", "gemini", "openai", "anthropic"].map((name) =>
            `<label class="dim">${name} <input id="jev-daily-${name}" type="number" step="0.1" min="0" value="${daily[name] ?? 1}" style="width:64px"/></label>`
          ).join("")}
        </div>
        <div style="display:flex;gap:10px;flex-wrap:wrap">
          ${check("jev-dc-cli", allow.has("cli_account"), "conta CLI")}
          ${check("jev-dc-http", allow.has("http_api"), "API HTTP")}
          ${check("jev-dc-ws", allow.has("workspace_agent"), "agente no workspace")}
        </div>
      </div>
    </div>
  `;
}

export async function openAIUsagePage() {
  const existing = state.editorTabs.find((t) => t.relPath === AI_USAGE_TAB_KEY);
  if (!existing) {
    state.editorTabs.unshift({
      kind: "ai-usage",
      relPath: AI_USAGE_TAB_KEY,
      name: "Uso & Limites das IAs",
      content: "",
      dirty: false,
    });
  }
  state.activeEditorPath = AI_USAGE_TAB_KEY;
  state.aiUsagePage.editingProvider = "";
  state.aiUsagePage.loading = true;
  renderEditorTabs();
  renderEditorBody();

  const backend = api();
  if (!backend || !backend.GetAIUsage) {
    state.aiUsagePage.loading = false;
    if (state.activeEditorPath === AI_USAGE_TAB_KEY) renderEditorBody();
    return;
  }
  try {
    const [rep, health, policy] = await Promise.all([
      backend.GetAIUsage(),
      backend.GetProviderHealth ? backend.GetProviderHealth() : Promise.resolve(state.providerHealth),
      backend.GetJevPolicy ? backend.GetJevPolicy() : Promise.resolve(jevPolicyDraft),
    ]);
    if (policy) jevPolicyDraft = policy;
    state.aiUsagePage.report = rep;
    if (Array.isArray(health)) {
      state.providerHealth = health;
      updateStatusBar();
    }
  } catch (err: any) {
    toastError("carregar uso e limites das IAs", err);
  } finally {
    state.aiUsagePage.loading = false;
    if (state.activeEditorPath === AI_USAGE_TAB_KEY) renderEditorBody();
  }
}

/** @deprecated use openAIUsagePage */
export async function openAIUsageModal() {
  return openAIUsagePage();
}

function formatHealthUsdMicro(micro?: number): string {
  const val = Number(micro || 0) / 1_000_000;
  return `$${val.toFixed(2)}`;
}

export function renderJevProviderHealthPanelHtml(list: ProviderHealthView[], probing: boolean): string {
  const rows = list.length === 0
    ? `<div class="dim" style="font-size:11.5px;padding:6px 0">Nenhum dado de circuito registrado ainda.</div>`
    : list.map((p) => {
        const st = String(p.state || "closed");
        const pillCls = st === "open" ? "changes_requested" : st === "half_open" ? "commented" : "approved";
        const pillText = st === "open" ? "aberto (pausa)" : st === "half_open" ? "sondando" : "ativo";
        const credText = p.hasCredential ? "" : " · sem credencial";
        const cooldown = p.until ? ` · ${formatResetCountdown(p.until)}` : "";
        const reason = p.reason ? ` · ${escapeHtml(p.reason)}` : "";
        const spend = p.dailyCapMicroUsd && p.dailyCapMicroUsd > 0
          ? ` · hoje ${formatHealthUsdMicro(p.dailySpentMicroUsd)}/${formatHealthUsdMicro(p.dailyCapMicroUsd)}`
          : (p.dailySpentMicroUsd && p.dailySpentMicroUsd > 0 ? ` · hoje ${formatHealthUsdMicro(p.dailySpentMicroUsd)}` : "");
        const note = quotaChipNote([p], state.aiUsagePage.report);
        const quota = note
          ? ` · ${note}${p.quotaStale ? " (desatual.)" : ""}`
          : (typeof p.quotaRemainingPct === "number" && p.quotaRemainingPct >= 0
            ? ` · cota ${Math.round(p.quotaRemainingPct)}%${p.quotaStale ? " (desatual.)" : ""}`
            : "");
        return `
          <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;padding:5px 0;border-top:1px solid var(--border-subtle)">
            <div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap;font-size:11.5px">
              <strong>${escapeHtml(p.provider)}</strong>
              <span class="flow-pill ${pillCls}">${pillText}</span>
              <span class="dim">${reason.replace(/^ · /, "")}${cooldown}${spend}${quota}${credText}</span>
            </div>
          </div>
        `;
      }).join("");

  const allExhausted = list.length > 0 && list.every((p) => !p.hasCredential || p.state === "open");
  const warnBanner = allExhausted
    ? `<div style="margin:6px 0;padding:6px 8px;border-radius:6px;background:rgba(248,81,73,0.12);border:1px solid rgba(248,81,73,0.35);font-size:11.5px;color:#ff8a80">Todos os provedores configurados estão esgotados ou em pausa. Clique em "Sondar agora" ou aguarde o fim do cooldown.</div>`
    : "";

  return `
    <div class="aiu-card">
      <div class="aiu-card-head">
        <div class="aiu-card-title">
          <strong>Saúde dos Provedores (Jev)</strong>
          <span class="dim" style="font-size:11px">circuit breaker · cooldown · teto diário</span>
        </div>
        <button type="button" class="mini" id="aiu-probe-providers" ${probing ? "disabled" : ""}>
          ${probing ? icons.spinner : icons.refresh}
          <span>Sondar agora</span>
        </button>
      </div>
      <div class="aiu-card-body">
        ${warnBanner}
        ${rows}
      </div>
    </div>
  `;
}

export function formatResetCountdown(iso?: string): string {
  if (!iso) return "";
  const ms = Date.parse(iso) - Date.now();
  if (!Number.isFinite(ms) || ms <= 0) return "liberando em breve";
  const mins = Math.ceil(ms / 60000);
  if (mins < 60) return `reseta em ${mins}min`;
  const hrs = Math.floor(mins / 60);
  const remMin = mins % 60;
  if (hrs < 24) return `reseta em ${hrs}h ${remMin}m`;
  const days = Math.floor(hrs / 24);
  const remHrs = hrs % 24;
  return `reseta em ${days}d ${remHrs}h`;
}

export function formatRollingReleaseCountdown(oldestAt?: string, windowHours = 5): string {
  if (!oldestAt) return "";
  const ts = Date.parse(oldestAt);
  if (!Number.isFinite(ts)) return "";
  const releaseAt = ts + windowHours * 3600 * 1000;
  const ms = releaseAt - Date.now();
  if (ms <= 0) return "";
  const mins = Math.ceil(ms / 60000);
  if (mins < 60) return `próxima liberação em ${mins}min`;
  const hrs = Math.floor(mins / 60);
  const remMin = mins % 60;
  if (hrs < 24) return `próxima liberação em ${hrs}h ${remMin}m`;
  const days = Math.floor(hrs / 24);
  const remHrs = hrs % 24;
  return `próxima liberação em ${days}d ${remHrs}h`;
}

function manualRemainingPct(report: AIUsageReport | null, provider: string): number | null {
  const row = report?.providers?.find((p) => p.provider === provider);
  if (!row) return null;
  const left: number[] = [];
  const five = row.limits?.fiveHourTokens || 0;
  const week = row.limits?.weeklyTokens || 0;
  if (five > 0) left.push((1 - row.window5h.totalTokens / five) * 100);
  if (week > 0) left.push((1 - row.windowWeekly.totalTokens / week) * 100);
  if (left.length === 0) return null;
  return Math.min(...left);
}

// quotaChipNote is the status text for a measured remainder (<=20% warns) or an unmeasured provider.
export function quotaChipNote(health: ProviderHealthView[], report: AIUsageReport | null): string {
  let warn = "";
  let unmeasured = false;
  for (const p of health) {
    const official = typeof p.quotaRemainingPct === "number" ? p.quotaRemainingPct : -1;
    if (official >= 0) {
      if (official <= 20) warn = `aviso ${Math.round(official)}%`;
      continue;
    }
    const manual = manualRemainingPct(report, p.provider);
    if (manual != null) {
      if (manual <= 20) warn = `aviso ${Math.round(manual)}%`;
      continue;
    }
    unmeasured = true;
  }
  const parts: string[] = [];
  if (warn) parts.push(warn);
  if (unmeasured) parts.push("cota não medida");
  return parts.join(" · ");
}

export function usageBarTone(pct: number): string {
  if (pct >= 85) return "danger";
  if (pct >= 65) return "warn";
  return "ok";
}

export function renderQuotaBarRowHtml(label: string, pct: number, rightText: string, subText: string): string {
  const clamped = Math.max(0, Math.min(100, Math.round(pct * 10) / 10));
  const tone = usageBarTone(clamped);
  return `
    <div class="aiu-window-row">
      <div class="aiu-window-top">
        <span class="aiu-window-label">${escapeHtml(label)}</span>
        <span class="aiu-window-val">${escapeHtml(rightText)}</span>
      </div>
      <div class="aiu-bar-track">
        <div class="aiu-bar-fill ${tone}" style="width:${clamped}%"></div>
      </div>
      ${subText ? `<div class="aiu-window-sub">${escapeHtml(subText)}</div>` : ""}
    </div>
  `;
}

export function renderLocalWindowRowHtml(label: string, win: { inputTokens: number; outputTokens: number; totalTokens: number; calls: number; oldestAt?: string }, capTokens: number, windowHours: number): string {
  const tokStr = `${formatTokens(win.totalTokens)} tok`;
  const callsStr = `${win.calls} chamada${win.calls === 1 ? "" : "s"}`;
  const releaseStr = formatRollingReleaseCountdown(win.oldestAt, windowHours);
  const detailParts = [
    `↑${formatTokens(win.inputTokens)} ↓${formatTokens(win.outputTokens)}`,
    callsStr,
  ];
  if (releaseStr) detailParts.push(releaseStr);

  if (capTokens > 0) {
    const pct = Math.min(100, (win.totalTokens / capTokens) * 100);
    return renderQuotaBarRowHtml(
      label,
      pct,
      `${Math.round(pct)}% · ${tokStr} / ${formatTokens(capTokens)}`,
      detailParts.join(" · ")
    );
  }
  return `
    <div class="aiu-window-row">
      <div class="aiu-window-top">
        <span class="aiu-window-label">${escapeHtml(label)}</span>
        <span class="aiu-window-val">${escapeHtml(tokStr)}</span>
      </div>
      <div class="aiu-window-sub">${escapeHtml(detailParts.join(" · "))}</div>
    </div>
  `;
}

function renderAgyQuotaGroupsHtml(aq: { available?: boolean; groups?: { name: string; description?: string; buckets: { usedPercent: number; remainingPercent: number; window: string; name?: string; resetTime?: string }[] }[] } | undefined): string {
  const groups = aq?.available && aq.groups && aq.groups.length > 0 ? aq.groups : [];
  if (groups.length === 0) return "";
  return groups.map((g) => {
    const cleanDesc = (g.description || "").replace(/^Models within this group:\s*/i, "");
    const rows = g.buckets.map((b) => {
      const used = Math.round(b.usedPercent * 10) / 10;
      const left = Math.round(b.remainingPercent * 10) / 10;
      const lbl = b.window === "5h"
        ? "Limite 5h"
        : b.window === "weekly"
          ? "Limite Semanal"
          : b.name || "Limite";
      return renderQuotaBarRowHtml(
        lbl,
        used,
        `${used}% usado (${left}% livre)`,
        formatResetCountdown(b.resetTime)
      );
    }).join("");
    return `
      <div class="aiu-agy-group">
        <div class="aiu-agy-group-head">
          <span class="aiu-agy-group-name">${escapeHtml(g.name)}</span>
          ${cleanDesc && cleanDesc.length <= 110 ? `<span class="aiu-agy-group-desc">${escapeHtml(cleanDesc)}</span>` : ""}
        </div>
        ${rows}
      </div>
    `;
  }).join("");
}

export function renderAgyAccountCardHtml(acc: AgyAccountQuota, local?: AIProviderUsage, showLocal = false): string {
  const aq = acc.quota;
  const groupsHtml = renderAgyQuotaGroupsHtml(aq);
  const title = acc.email ? `${acc.name} · ${acc.email}` : acc.name;
  const statusPill = aq.available
    ? `<span class="flow-pill approved">API oficial (/usage)</span>`
    : `<span class="flow-pill changes">Indisponível</span>`;
  const localSub = showLocal && local
    ? `
      <div class="aiu-local-strip">
        <span>Telemetria HiveMind (todas as contas agy):</span>
        <strong>5h: ${formatTokens(local.window5h.totalTokens)} tok (${local.window5h.calls}x)</strong>
        <span>·</span>
        <strong>7d: ${formatTokens(local.windowWeekly.totalTokens)} tok (${local.windowWeekly.calls}x)</strong>
      </div>
    `
    : "";
  const body = groupsHtml
    || (local
      ? `
        ${renderLocalWindowRowHtml("Janela de 5h", local.window5h, local.limits.fiveHourTokens || 0, 5)}
        ${renderLocalWindowRowHtml("Janela Semanal (7 dias)", local.windowWeekly, local.limits.weeklyTokens || 0, 24 * 7)}
      `
      : `<div class="dim" style="font-size:11.5px">Sem cota oficial nesta conta.</div>`);
  return `
    <div class="aiu-card">
      <div class="aiu-card-head">
        <div class="aiu-card-title">
          <strong>Antigravity · ${escapeHtml(title)}</strong>
          ${statusPill}
        </div>
      </div>
      <div class="aiu-card-body">
        ${body}
        ${localSub}
      </div>
      ${aq.detail ? `<div class="aiu-footnote">${escapeHtml(aq.detail)}</div>` : ""}
    </div>
  `;
}

export function renderProviderUsageCardHtml(p: AIProviderUsage, rep: AIUsageReport): string {
  const isEditing = state.aiUsagePage.editingProvider === p.provider;
  const isAgy = p.provider === "agy";
  const isOpenCodeGo = p.provider === "opencode-go";
  const isOpenCodeZen = p.provider === "opencode";

  let statusPill = `<span class="tok-pill">Local</span>`;
  let extraHeaderBtn = "";
  let bodyHtml = "";
  let footnote = "";

  if (isAgy) {
    const aq = rep.agyQuota;
    const groupsHtml = renderAgyQuotaGroupsHtml(aq);
    const agyOk = Boolean(state.agyStatus?.LoggedIn) || Boolean(aq?.available);
    statusPill = agyOk
      ? groupsHtml
        ? `<span class="flow-pill approved">API oficial (/usage)${state.models.length > 0 ? ` · ${state.models.length} modelos` : ""}</span>`
        : `<span class="flow-pill approved">Conectado · ${state.models.length} modelos</span>`
      : `<span class="flow-pill changes">Não autenticado</span>`;

    const localSub = `
      <div class="aiu-local-strip">
        <span>Telemetria HiveMind:</span>
        <strong>5h: ${formatTokens(p.window5h.totalTokens)} tok (${p.window5h.calls}x)</strong>
        <span>·</span>
        <strong>7d: ${formatTokens(p.windowWeekly.totalTokens)} tok (${p.windowWeekly.calls}x)</strong>
      </div>
    `;

    if (groupsHtml) {
      bodyHtml = groupsHtml + localSub;
      footnote = aq?.detail || "Cota oficial obtida via agy /usage";
    } else {
      bodyHtml = `
        ${renderLocalWindowRowHtml("Janela de 5h", p.window5h, p.limits.fiveHourTokens || 0, 5)}
        ${renderLocalWindowRowHtml("Janela Semanal (7 dias)", p.windowWeekly, p.limits.weeklyTokens || 0, 24 * 7)}
      `;
      footnote = aq?.detail || "Rode `agy` para autenticar e exibir as cotas oficiais de 5h e semanal.";
    }
  } else if (isOpenCodeGo) {
    const ocOk = Boolean(state.openCodeStatus?.configured);
    const q = rep.openCodeQuota;
    statusPill = ocOk
      ? q.available
        ? `<span class="flow-pill approved">API oficial · ativo</span>`
        : `<span class="flow-pill pending">Token configurado</span>`
      : `<span class="flow-pill changes">Sem token</span>`;
    if (!ocOk) {
      extraHeaderBtn = `<button type="button" class="mini primary" id="aiu-open-opencode-cfg">Configurar Token</button>`;
    }

    const remoteRows: string[] = [];
    if (q.available) {
      if (q.rolling5h.hasData) {
        const pct = Math.round(q.rolling5h.percent * 10) / 10;
        const left = Math.max(0, Math.round((100 - q.rolling5h.percent) * 10) / 10);
        remoteRows.push(renderQuotaBarRowHtml(
          "Limite 5h (Rolling — API OpenCode Go)",
          pct,
          `${pct}% usado (${left}% livre)`,
          formatResetCountdown(q.rolling5h.resetsAt)
        ));
      }
      if (q.weekly.hasData) {
        const pct = Math.round(q.weekly.percent * 10) / 10;
        const left = Math.max(0, Math.round((100 - q.weekly.percent) * 10) / 10);
        remoteRows.push(renderQuotaBarRowHtml(
          "Limite Semanal (API OpenCode Go)",
          pct,
          `${pct}% usado (${left}% livre)`,
          formatResetCountdown(q.weekly.resetsAt)
        ));
      }
      if (q.monthly.hasData) {
        const pct = Math.round(q.monthly.percent * 10) / 10;
        const left = Math.max(0, Math.round((100 - q.monthly.percent) * 10) / 10);
        remoteRows.push(renderQuotaBarRowHtml(
          "Limite Mensal (API OpenCode Go)",
          pct,
          `${pct}% usado (${left}% livre)`,
          formatResetCountdown(q.monthly.resetsAt)
        ));
      }
    }

    const localSub = `
      <div class="aiu-local-strip">
        <span>Telemetria HiveMind:</span>
        <strong>5h: ${formatTokens(p.window5h.totalTokens)} tok (${p.window5h.calls}x)</strong>
        <span>·</span>
        <strong>7d: ${formatTokens(p.windowWeekly.totalTokens)} tok (${p.windowWeekly.calls}x)</strong>
      </div>
    `;

    bodyHtml = remoteRows.length > 0
      ? remoteRows.join("") + localSub
      : `
          ${renderLocalWindowRowHtml("Janela de 5h (local)", p.window5h, p.limits.fiveHourTokens || 0, 5)}
          ${renderLocalWindowRowHtml("Janela Semanal (7 dias — local)", p.windowWeekly, p.limits.weeklyTokens || 0, 24 * 7)}
        `;
    footnote = q.detail || "";
  } else {
    if (isOpenCodeZen) {
      statusPill = state.openCodeStatus?.configured
        ? `<span class="flow-pill approved">Pay-as-you-go / Free</span>`
        : `<span class="flow-pill commented">Modelos -free</span>`;
    }
    bodyHtml = `
      ${renderLocalWindowRowHtml("Janela de 5h", p.window5h, p.limits.fiveHourTokens || 0, 5)}
      ${renderLocalWindowRowHtml("Janela Semanal (7 dias)", p.windowWeekly, p.limits.weeklyTokens || 0, 24 * 7)}
    `;
  }

  const limitFormHtml = isEditing
    ? `
      <div class="aiu-limits-form">
        <div class="aiu-limits-grid">
          <label>Meta 5h (tokens, 0 = desligado)</label>
          <input type="number" min="0" step="50000" id="aiu-limit-5h" value="${p.limits.fiveHourTokens || 0}" placeholder="Ex: 500000" />
          <label>Meta semanal (tokens, 0 = desligado)</label>
          <input type="number" min="0" step="100000" id="aiu-limit-7d" value="${p.limits.weeklyTokens || 0}" placeholder="Ex: 3000000" />
        </div>
        <div style="display:flex;gap:6px;justify-content:flex-end;margin-top:8px">
          <button type="button" class="mini" id="aiu-limit-cancel">Cancelar</button>
          <button type="button" class="mini primary" data-aiu-save-limits="${escapeHtml(p.provider)}">Salvar Metas</button>
        </div>
      </div>
    `
    : "";

  return `
    <div class="aiu-card">
      <div class="aiu-card-head">
        <div class="aiu-card-title">
          <strong>${escapeHtml(p.label)}</strong>
          ${statusPill}
        </div>
        <div style="display:flex;gap:6px;align-items:center">
          ${extraHeaderBtn}
          ${!(isOpenCodeGo && rep.openCodeQuota.available) && !(isAgy && rep.agyQuota?.available)
            ? `<button type="button" class="mini ghost" data-aiu-edit-limits="${escapeHtml(p.provider)}" title="Configurar meta opcional de tokens para 5h e 7 dias">${icons.settings}<span>Metas</span></button>`
            : ""
          }
        </div>
      </div>
      <div class="aiu-card-body">
        ${bodyHtml}
      </div>
      ${limitFormHtml}
      ${footnote ? `<div class="aiu-footnote">${escapeHtml(footnote)}</div>` : ""}
    </div>
  `;
}

function renderAIUsageCardsHtml(rep: AIUsageReport | null, loading: boolean): string {
  if (loading && !rep) {
    return `<div class="dim" style="padding:26px;text-align:center">${icons.spinner} Consultando uso e cotas das IAs…</div>`;
  }
  if (!rep) {
    return `<div class="dim" style="padding:20px;text-align:center">Não foi possível carregar os dados de uso.</div>`;
  }
  const accounts = Array.isArray(rep.agyAccounts) ? rep.agyAccounts : [];
  const localAgy = rep.providers.find((p) => p.provider === "agy");
  const cards: string[] = [];
  if (accounts.length > 0) {
    accounts.forEach((acc, idx) => {
      cards.push(renderAgyAccountCardHtml(acc, localAgy, idx === 0));
    });
  }
  for (const p of rep.providers) {
    if (p.provider === "agy" && accounts.length > 0) continue;
    cards.push(renderProviderUsageCardHtml(p, rep));
  }
  return cards.join("");
}

export function renderAIUsagePageHtml(): string {
  const m = state.aiUsagePage;
  const rep = m.report;
  return `
    <div class="ai-usage-page">
      <div class="tc-header">
        <div>
          <div class="tc-title-row">
            <span class="aiu-head-icon">${icons.aiUsage}</span>
            <h2>Uso & Limites das IAs</h2>
          </div>
          <div class="dim" style="font-size:11.5px">Cada conta do Pool aparece em cartão próprio (ex.: várias Antigravity). Cotas oficiais + telemetria local.</div>
        </div>
        <div class="tc-header-actions">
          <button type="button" class="mini" id="aiu-refresh" ${m.loading ? "disabled" : ""}>
            ${m.loading ? icons.spinner : icons.refresh}
            <span>Atualizar</span>
          </button>
        </div>
      </div>

      <div class="aiu-cards-wrap">
        ${renderJevPolicyEditorHtml(jevPolicyDraft)}
        ${renderJevProviderHealthPanelHtml(state.providerHealth, state.providerHealthProbing)}
        ${renderAIUsageCardsHtml(rep, m.loading)}
      </div>

      <div class="dim" style="font-size:11px;padding:4px 2px">Dados locais em %AppData%\\HiveMind\\ai-usage.json</div>
    </div>
  `;
}

export function bindAIUsagePageEvents() {
  document.getElementById("aiu-refresh")?.addEventListener("click", () => void openAIUsagePage());
  document.getElementById("aiu-save-jev-policy")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend?.SaveJevPolicy) return;
    const base = jevPolicyDraft ?? {
      version: 1,
      enabled: false,
      classifyEnabled: true,
      weights: { quality: 0.35, cost: 0.5, latency: 0.15 },
      autoEscalateUsd: 0.02,
      maxEscalateRatio: 3,
    };
    const allow: string[] = [];
    if ((document.getElementById("jev-dc-cli") as HTMLInputElement | null)?.checked) allow.push("cli_account");
    if ((document.getElementById("jev-dc-http") as HTMLInputElement | null)?.checked) allow.push("http_api");
    if ((document.getElementById("jev-dc-ws") as HTMLInputElement | null)?.checked) allow.push("workspace_agent");
    const classifyEl = document.getElementById("jev-classify") as HTMLInputElement | null;
    const next: JevPolicy = {
      ...base,
      enabled: Boolean((document.getElementById("jev-enabled") as HTMLInputElement | null)?.checked),
      classifyEnabled: classifyEl ? classifyEl.checked : base.classifyEnabled !== false,
      weights: {
        quality: numField("jev-w-quality", base.weights.quality),
        cost: numField("jev-w-cost", base.weights.cost),
        latency: numField("jev-w-latency", base.weights.latency),
      },
      autoEscalateUsd: numField("jev-escalate", base.autoEscalateUsd),
      dailyUsd: {
        openrouter: numField("jev-daily-openrouter", 1),
        opencode: numField("jev-daily-opencode", 1),
        gemini: numField("jev-daily-gemini", 1),
        openai: numField("jev-daily-openai", 1),
        anthropic: numField("jev-daily-anthropic", 1),
      },
      allowDataClass: allow,
    };
    try {
      jevPolicyDraft = await backend.SaveJevPolicy(next);
      toast("Política do Jev salva neste PC.", "success");
    } catch (err: any) {
      toastError("salvar política do Jev", err);
    }
  });
  document.getElementById("aiu-probe-providers")?.addEventListener("click", async () => {
    const b = api();
    if (!b?.ProbeProviders) return;
    state.providerHealthProbing = true;
    if (state.activeEditorPath === AI_USAGE_TAB_KEY) renderEditorBody();
    try {
      const views = await b.ProbeProviders();
      if (Array.isArray(views)) {
        state.providerHealth = views;
        updateStatusBar();
      }
    } catch (err: any) {
      toastError("sondar provedores Jev", err);
    } finally {
      state.providerHealthProbing = false;
      if (state.activeEditorPath === AI_USAGE_TAB_KEY) renderEditorBody();
    }
  });
  document.getElementById("aiu-open-opencode-cfg")?.addEventListener("click", () => {
    openOpenCodeModal();
  });

  document.querySelectorAll("[data-aiu-edit-limits]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const prov = (btn as HTMLElement).dataset.aiuEditLimits || "";
      state.aiUsagePage.editingProvider = state.aiUsagePage.editingProvider === prov ? "" : prov;
      if (state.activeEditorPath === AI_USAGE_TAB_KEY) renderEditorBody();
    });
  });

  document.getElementById("aiu-limit-cancel")?.addEventListener("click", () => {
    state.aiUsagePage.editingProvider = "";
    if (state.activeEditorPath === AI_USAGE_TAB_KEY) renderEditorBody();
  });

  document.querySelectorAll("[data-aiu-save-limits]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const prov = (btn as HTMLElement).dataset.aiuSaveLimits || "";
      const v5h = Math.max(0, parseInt((document.getElementById("aiu-limit-5h") as HTMLInputElement | null)?.value || "0", 10) || 0);
      const v7d = Math.max(0, parseInt((document.getElementById("aiu-limit-7d") as HTMLInputElement | null)?.value || "0", 10) || 0);
      if (!api()?.SaveAIUsageLimits) return;
      try {
        state.aiUsagePage.report = await api()!.SaveAIUsageLimits(prov, v5h, v7d);
        state.aiUsagePage.editingProvider = "";
        if (state.activeEditorPath === AI_USAGE_TAB_KEY) renderEditorBody();
        toast("Metas de uso atualizadas.", "success");
      } catch (err: any) {
        toastError("salvar metas de uso", err);
      }
    });
  });
}
