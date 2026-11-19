import { icons } from "./icons";
import type { AIProviderUsage, AIUsageReport, GitHubStatus } from "./types";
import { escapeHtml, formatTokens, safeImageSrc } from "./utils";
import { openFileInEditor, renderEditorBody, renderEditorTabs } from "./editor";
import { refreshExplorerTree, renderLeftSidebar } from "./explorer";
import { loadGitHubPRs, loadProjectIssues, refreshDiffs } from "./git";
import { refreshSkills } from "./models";
import { updateStatusBar } from "./shell";
import { TEAM_CONFIG_TAB_KEY, api, setStatus, state, toast, toastError } from "./state";
import { updateMissionBoardIfOpen } from "./teamconfig";

export async function openDoctorModal() {
  const backend = api();
  if (!backend) return;
  setStatus("Verificando agy…");
  const status = await backend.DoctorAgy();
  state.agyStatus = status;
  if (status.LoggedIn) {
    state.models = (await backend.ListModels()) || state.models;
  }
  state.doctorModal = status;
  updateStatusBar();
  renderModalLayer();
  setStatus("Pronto");
}

export async function openGitHubModal() {
  state.showGitHubModal = true;
  renderModalLayer();
  const backend = api();
  if (!backend?.GitHubStatus) return;
  try {
    state.ghStatus = await backend.GitHubStatus(state.project?.root || "");
    renderLeftSidebar();
    renderModalLayer();
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
    updateStatusBar();
    if (state.showJiraModal) renderModalLayer();
    if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) renderEditorBody();
  } catch {
    // mantém último estado visível
  }
}

export async function openJiraModal() {
  state.showJiraModal = true;
  renderModalLayer();
  await refreshJiraStatusAndProjects();
}

export async function openOpenCodeModal() {
  state.showOpenCodeModal = true;
  renderModalLayer();
  const backend = api();
  if (backend?.GetAPIKeysStatus) {
    try {
      state.apiKeysStatus = await backend.GetAPIKeysStatus();
      renderModalLayer();
    } catch {
      // mantém status anterior
    }
  }
  if (!backend?.BootstrapOpenCode) return;
  try {
    const res = await backend.BootstrapOpenCode(true);
    state.openCodeStatus = res.status;
    if (res.goModels && res.goModels.length > 0) {
      state.openCodeGoModels = res.goModels;
    }
    if (res.models && res.models.length > 0) {
      state.openCodeModels = res.models;
    }
    updateStatusBar();
    renderModalLayer();
  } catch {
    // keep modal open for token entry
  }
}

export async function openAPIKeysModal() {
  state.showAPIKeysModal = true;
  renderModalLayer();
  const backend = api();
  if (!backend?.GetAPIKeysStatus) return;
  try {
    state.apiKeysStatus = await backend.GetAPIKeysStatus();
    if (backend.CursorCLIStatus) state.cursorCLI = await backend.CursorCLIStatus();
    renderModalLayer();
  } catch {
    // mantém o modal aberto sem status
  }
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

export function renderModalLayer() {
  const layer = document.getElementById("modal-layer");
  if (!layer) return;

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

  if (state.doctorModal) {
    const d = state.doctorModal;
    layer.innerHTML = `
      <div class="modal-backdrop">
        <div class="modal-box">
          <h3 class="modal-title">Doctor — Antigravity CLI (agy)</h3>
          <p><strong>Encontrado:</strong> ${d.Found ? "Sim" : "Não"}</p>
          <p><strong>Caminho:</strong> ${escapeHtml(d.Path || "-")}</p>
          <p><strong>Autenticado:</strong> ${d.LoggedIn ? "Sim (Google Pro)" : "Não"}</p>
          <p><strong>Detalhe:</strong> ${escapeHtml(d.Detail)}</p>
          <p><strong>Modelos (${state.models.length}):</strong></p>
          <div style="max-height:160px;overflow:auto;background:var(--bg-deep);padding:8px;border-radius:6px;font-size:12px;border:1px solid var(--border)">
            ${state.models.length === 0 ? "Nenhum modelo carregado." : state.models.map((m) => `<div>${escapeHtml(m.slug)} — <span class="dim">${escapeHtml(m.name)}</span></div>`).join("")}
          </div>
          <div class="modal-footer">
            <button id="doc-api-keys">${icons.settings}<span>Chaves de API (Provedores LLM)…</span></button>
            <div class="spacer"></div>
            <button class="primary" id="doc-close">Fechar</button>
          </div>
        </div>
      </div>
    `;
    document.getElementById("doc-close")?.addEventListener("click", () => {
      state.doctorModal = null;
      renderModalLayer();
    });
    document.getElementById("doc-api-keys")?.addEventListener("click", () => {
      state.doctorModal = null;
      void openAPIKeysModal();
    });
    return;
  }

  if (state.showAPIKeysModal) {
    const saved = state.apiKeysStatus || {};
    const field = (id: string, label: string, placeholder: string, savedMask?: string) => `
      <label for="${id}">${escapeHtml(label)}${savedMask ? ` <span class="dim" style="font-weight:500">(salvo: ${escapeHtml(savedMask)})</span>` : ""}</label>
      <input id="${id}" type="${id.includes("KEY") ? "password" : "text"}" placeholder="${escapeHtml(placeholder)}" autocomplete="off" />
    `;
    layer.innerHTML = `
      <div class="modal-backdrop" id="ak-modal-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:560px">
          <h3 class="modal-title">Chaves de API — Provedores LLM</h3>
          <p class="dim" style="font-size:12px;margin-top:-4px;margin-bottom:10px;line-height:1.5">
            Salve aqui as chaves dos provedores (arquivo <code>api-keys.json</code> em <code>%APPDATA%\\HiveMind</code>, fora do repositório).
            Variáveis de ambiente sempre têm prioridade sobre o que está salvo. Deixar o campo vazio remove o valor salvo.
          </p>
          ${state.apiKeysStatus?.["env_OPENROUTER_API_KEY"] ? `<div style="color:var(--amber);font-size:12px;margin-bottom:10px">⚠ A variável de ambiente <code>OPENROUTER_API_KEY</code> está definida no sistema e TEM prioridade sobre o valor salvo aqui.</div>` : ""}
          <div class="form-grid">
            ${field("ak-CURSOR_API_KEY", "Cursor API Key (agente CLI):", "crsr_… (painel Cursor → API Keys)", saved["CURSOR_API_KEY"])}
            <p class="dim" style="font-size:11.5px;margin:-4px 0 8px;line-height:1.45">Não é uma API de chat. O HiveMind chama o <code>agent -p</code> no workspace do agente. ${state.cursorCLI?.detail ? escapeHtml(state.cursorCLI.detail) : "Se o CLI não estiver instalado, no Windows: irm 'https://cursor.com/install?win32=true' | iex"}</p>
            ${state.apiKeysStatus?.["env_CURSOR_API_KEY"] ? `<div style="color:var(--amber);font-size:12px;margin-bottom:10px">⚠ A variável <code>CURSOR_API_KEY</code> do sistema tem prioridade sobre o valor salvo aqui.</div>` : ""}
            ${field("ak-OPENROUTER_API_KEY", "OpenRouter API Key:", "sk-or-v1-… (chave em https://openrouter.ai/keys)", saved["OPENROUTER_API_KEY"])}
            ${field("ak-OPENROUTER_BASE_URL", "OpenRouter Base URL:", "https://openrouter.ai/api/v1", saved["OPENROUTER_BASE_URL"])}
            ${field("ak-OPENAI_API_KEY", "OpenAI API Key:", "sk-…", saved["OPENAI_API_KEY"])}
            ${field("ak-OPENAI_BASE_URL", "OpenAI Base URL:", "https://api.openai.com/v1", saved["OPENAI_BASE_URL"])}
            ${field("ak-ANTHROPIC_API_KEY", "Anthropic API Key:", "sk-ant-…", saved["ANTHROPIC_API_KEY"])}
            ${field("ak-GOOGLE_API_KEY", "Google Gemini API Key:", "AIza…", saved["GOOGLE_API_KEY"])}
            ${field("ak-GOOGLE_CLOUD_PROJECT", "Vertex — Projeto:", "meu-projeto-gcp", saved["GOOGLE_CLOUD_PROJECT"])}
            ${field("ak-GOOGLE_CLOUD_LOCATION", "Vertex — Região:", "us-central1", saved["GOOGLE_CLOUD_LOCATION"])}
          </div>
          <div id="ak-modal-err" style="color:var(--red);font-size:12px;margin-top:8px;display:none"></div>
          <div class="modal-footer">
            <button id="ak-close">Fechar</button>
            <div class="spacer"></div>
            <button class="primary" id="ak-save">${icons.check}<span>Salvar Chaves</span></button>
          </div>
        </div>
      </div>
    `;

    const closeAk = () => {
      state.showAPIKeysModal = false;
      renderModalLayer();
    };
    document.getElementById("ak-close")?.addEventListener("click", closeAk);
    document.getElementById("ak-modal-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement)?.id === "ak-modal-backdrop") closeAk();
    });
    document.getElementById("ak-save")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.SaveAPIKeys) return;
      const entries: Record<string, string> = {};
      for (const name of ["CURSOR_API_KEY", "OPENROUTER_API_KEY", "OPENROUTER_BASE_URL", "OPENAI_API_KEY", "OPENAI_BASE_URL", "ANTHROPIC_API_KEY", "GOOGLE_API_KEY", "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION"]) {
        const el = document.getElementById(`ak-${name}`) as HTMLInputElement | null;
        entries[name] = el?.value?.trim() || "";
      }
      try {
        state.apiKeysStatus = await backend.SaveAPIKeys(entries);
        toast("Chaves de API salvas (variáveis de ambiente continuam com prioridade).", "success");
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
          ${mb?.running && pr?.qrDataUrl ? `
            <div style="display:flex;gap:16px;align-items:center;flex-wrap:wrap">
              <div style="background:#fff;padding:8px;border-radius:8px">
                <img src="${escapeHtml(pr.qrDataUrl)}" alt="QR de pareamento" style="width:208px;height:208px;image-rendering:pixelated" />
              </div>
              <div style="flex:1;min-width:220px">
                <label style="font-size:11px;color:var(--text-dim)">URLs (Tailscale primeiro):</label>
                <div class="dim" style="font-family:var(--mono);font-size:11px;margin:4px 0 10px;word-break:break-all">
                  ${(pr.urls || []).map((u) => `<div>• ${escapeHtml(u)}</div>`).join("") || "—"}
                </div>
                <label style="font-size:11px;color:var(--text-dim)">Token (pareamento manual):</label>
                <div style="display:flex;gap:6px;align-items:center;margin:4px 0 10px">
                  <code style="font-size:11px;word-break:break-all">${escapeHtml(pr.token)}</code>
                  <button id="mb-copy" class="mini">Copiar</button>
                </div>
                <button id="mb-rotate" class="mini" title="Invalida todas as conexões ativas">Rotacionar Token / Desconectar Celulares</button>
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
            <div class="form-grid" style="margin-top:10px">
              <label for="relay-url">URL do relay</label>
              <input id="relay-url" type="url" placeholder="https://relay.seudominio" value="${escapeHtml(state.relayDraft.baseUrl)}" autocomplete="off" />
              <label for="relay-email">E-mail</label>
              <input id="relay-email" type="email" placeholder="voce@exemplo" value="${escapeHtml(state.relayDraft.email)}" autocomplete="username" />
              <label for="relay-password">Senha</label>
              <input id="relay-password" type="password" placeholder="não fica salva" value="${escapeHtml(state.relayDraft.password)}" autocomplete="current-password" />
            </div>
            <div class="relay-actions">
              <button class="primary mini" id="relay-login" type="button">Entrar</button>
              <button class="mini" id="relay-toggle" type="button">${state.relayStatus?.enabled ? "Desligar acesso remoto" : "Ligar acesso remoto"}</button>
              <button class="mini" id="relay-logout" type="button">Sair da conta</button>
            </div>
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
              ${state.relayLink ? `
                <div class="relay-code-box">
                  ${safeImageSrc(state.relayLink.qrDataUrl) ? `<img src="${escapeHtml(safeImageSrc(state.relayLink.qrDataUrl))}" alt="QR de vínculo" width="160" height="160" />` : ""}
                  <div>
                    <div style="font-family:var(--mono);font-variant-numeric:tabular-nums;font-size:18px;letter-spacing:0.08em">${escapeHtml(state.relayLink.code)}</div>
                    <div class="dim" style="font-size:11px;margin-top:4px">Expira ${escapeHtml(state.relayLink.expiresAt)}</div>
                  </div>
                </div>
              ` : ""}
            `}
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
    const bindDraft = (id: string, key: "baseUrl" | "email" | "password" | "claim") => {
      document.getElementById(id)?.addEventListener("input", (ev) => {
        state.relayDraft[key] = (ev.target as HTMLInputElement).value;
      });
    };
    bindDraft("relay-url", "baseUrl");
    bindDraft("relay-email", "email");
    bindDraft("relay-password", "password");
    bindDraft("relay-claim", "claim");
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
      const tok = state.mobilePairing?.token;
      if (!tok) return;
      try {
        await navigator.clipboard.writeText(tok);
        toast("Token copiado.", "success");
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
    const statusBadge = jira?.connected
      ? `<span style="color:var(--green);font-weight:600">Conectado como ${escapeHtml(jira.user || jira.email)}${jira.source ? ` (${jira.source === "env" ? "ENV" : jira.source.startsWith("mcp:") ? `MCP · ${jira.source.slice(4)}` : "Conector Nativo"})` : ""}${jira.tokenMasked ? ` · token ${escapeHtml(jira.tokenMasked)}` : ""}</span>`
      : `<span style="color:var(--amber)">${escapeHtml(jira?.detail || "Desconectado — informe URL, E-mail e API Token ou importe do mcp_config.json")}</span>`;
    const projects = state.jiraProjects || [];
    const currentDefProj = (jira?.defaultProject || "").toUpperCase();
    const hasMatchProj = projects.some((p) => p.key.toUpperCase() === currentDefProj);

    layer.innerHTML = `
      <div class="modal-backdrop" id="jira-modal-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:560px">
          <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:6px;flex-wrap:wrap">
            <h3 class="modal-title" style="margin:0">Conector Jira Cloud (REST v3)</h3>
            <div style="display:flex;gap:6px">
              <button type="button" class="mini" id="jira-import-mcp" title="Importar automaticamente de .agents/mcp_config.json, .mcp.json ou .cursor/mcp.json">${icons.download}<span>Importar do MCP</span></button>
              <button type="button" class="mini" id="jira-open-token-help" title="Abrir página da Atlassian para criar um API Token">${icons.openInNew}<span>Gerar Token ↗</span></button>
            </div>
          </div>
          <p class="dim" style="font-size:12px;margin-top:0;margin-bottom:10px">
            Detecta automaticamente seu <code>mcp_config.json</code> (ex.: <code>.agents/mcp_config.json</code>) ou salve aqui uma única vez fora do repo. Pode colar só <code>sua-empresa.atlassian.net</code> — os projetos aparecem dinamicamente.
          </p>
          <div style="font-size:12px;margin-bottom:10px"><strong>Status:</strong> ${statusBadge}</div>
          <div class="form-grid">
            <label>URL do Jira:</label>
            <input id="jira-base-url" value="${escapeHtml(jira?.baseUrl || "")}" placeholder="sua-empresa.atlassian.net" />

            <label>E-mail (Atlassian):</label>
            <input id="jira-email" value="${escapeHtml(jira?.email || "")}" placeholder="voce@empresa.com" autocomplete="off" />

            <label>API Token:</label>
            <input id="jira-api-token" type="password" placeholder="${jira?.source === "config" ? "Token salvo — cole outro apenas se quiser trocar" : "Cole o API Token gerado na Atlassian"}" autocomplete="off" />

            <label>Projeto padrão:</label>
            ${projects.length > 0
              ? `<select id="jira-project">
                  <option value="">(Selecionar projeto dinamicamente…)</option>
                  ${projects.map((p) => `<option value="${escapeHtml(p.key)}" ${p.key.toUpperCase() === currentDefProj ? "selected" : ""}>${escapeHtml(p.key)} — ${escapeHtml(p.name)}</option>`).join("")}
                  ${currentDefProj && !hasMatchProj ? `<option value="${escapeHtml(currentDefProj)}" selected>${escapeHtml(currentDefProj)} (atual)</option>` : ""}
                </select>`
              : `<input id="jira-project" value="${escapeHtml(currentDefProj)}" placeholder="Opcional — após conectar aparece a lista de projetos" />`
            }
          </div>
          <div class="dim" style="font-size:11px;margin-top:8px;line-height:1.45">
            Prioridade: variáveis <code>JIRA_BASE_URL</code>/<code>JIRA_EMAIL</code>/<code>JIRA_API_TOKEN</code> → conector nativo (<code>jira-connector.json</code>, nunca commitado).
          </div>
          <div id="jira-modal-err" style="color:var(--red);font-size:12px;margin-top:8px;display:none"></div>
          <div class="modal-footer">
            ${jira?.source === "config" ? `<button class="danger mini" id="jira-clear-token">Desconectar</button>` : ""}
            <div class="spacer"></div>
            <button id="jira-close">Fechar</button>
            <button class="primary" id="jira-save">Salvar & Testar</button>
          </div>
        </div>
      </div>
    `;

    const closeJira = () => {
      state.showJiraModal = false;
      renderModalLayer();
    };
    document.getElementById("jira-close")?.addEventListener("click", closeJira);
    document.getElementById("jira-open-token-help")?.addEventListener("click", () => {
      void api()?.OpenExternalURL("https://id.atlassian.com/manage-profile/security/api-tokens");
    });
    document.getElementById("jira-import-mcp")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.ImportJiraFromMCP) return;
      const errEl = document.getElementById("jira-modal-err");
      try {
        state.jiraStatus = await backend.ImportJiraFromMCP(state.project?.root || "");
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
    document.getElementById("jira-modal-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement)?.id === "jira-modal-backdrop") closeJira();
    });

    document.getElementById("jira-clear-token")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.SaveJiraConfig) return;
      try {
        state.jiraStatus = await backend.SaveJiraConfig("", "", "", "", true);
        state.jiraProjects = [];
        updateStatusBar();
        renderModalLayer();
        if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) renderEditorBody();
        setStatus("Credenciais do Conector Jira removidas.");
      } catch (err: any) {
        const errEl = document.getElementById("jira-modal-err");
        if (errEl) {
          errEl.textContent = `Falha ao remover credenciais: ${err?.message || String(err)}`;
          errEl.style.display = "block";
        }
      }
    });

    document.getElementById("jira-save")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.SaveJiraConfig) return;
      const baseIn = (document.getElementById("jira-base-url") as HTMLInputElement | null)?.value.trim() || "";
      const emailIn = (document.getElementById("jira-email") as HTMLInputElement | null)?.value.trim() || "";
      const tokIn = (document.getElementById("jira-api-token") as HTMLInputElement | null)?.value.trim() || "";
      const projIn = (document.getElementById("jira-project") as HTMLInputElement | HTMLSelectElement | null)?.value.trim().toUpperCase() || "";
      const errEl = document.getElementById("jira-modal-err");
      const btn = document.getElementById("jira-save") as HTMLButtonElement | null;
      if (btn) {
        btn.disabled = true;
        btn.textContent = "Conectando…";
      }
      try {
        state.jiraStatus = await backend.SaveJiraConfig(baseIn, emailIn, tokIn, projIn, false);
        if (state.jiraStatus?.connected) {
          try {
            const projs = backend.ListJiraProjectsForWorkspace
              ? await backend.ListJiraProjectsForWorkspace(state.project?.root || "")
              : await backend.ListJiraProjects?.();
            state.jiraProjects = projs || [];
          } catch {
            // segue mesmo se listar projetos falhar
          }
          if (state.teamConfigDraft) {
            state.teamConfigDraft.jiraEnabled = true;
            if (!state.teamConfigDraft.jiraProject && (projIn || state.jiraProjects[0]?.key)) {
              state.teamConfigDraft.jiraProject = projIn || state.jiraProjects[0]?.key || "";
            }
          }
          updateStatusBar();
          if (state.activeEditorPath === TEAM_CONFIG_TAB_KEY) renderEditorBody();
          closeJira();
          const projNote = state.jiraProjects.length > 0 ? ` (${state.jiraProjects.length} projeto(s) carregado(s))` : "";
          toast(`Jira conectado como ${state.jiraStatus.user || emailIn}${projNote}.`, "success");
        } else {
          updateStatusBar();
          renderModalLayer();
          const refreshedErr = document.getElementById("jira-modal-err");
          if (refreshedErr) {
            refreshedErr.textContent = state.jiraStatus?.detail || "Não foi possível autenticar no Jira.";
            refreshedErr.style.display = "block";
          }
        }
      } catch (err: any) {
        if (errEl) {
          errEl.textContent = `Falha ao salvar configuração Jira: ${err?.message || String(err)}`;
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

  if (state.showOpenCodeModal) {
    const oc = state.openCodeStatus;
    const baseUrl = oc?.baseUrl || "https://opencode.ai/zen/v1";
    const statusBadge = oc?.configured
      ? `<span style="color:var(--green);font-weight:600">Conectado (${escapeHtml(oc.keyMasked)}${oc.source === "env" ? " via OPENCODE_API_KEY" : " salvo localmente"})</span>`
      : `<span style="color:var(--amber)">Sem token salvo (apenas modelos <code>-free</code> liberados)</span>`;

    layer.innerHTML = `
      <div class="modal-backdrop" id="oc-modal-backdrop">
        <div class="modal-box" role="dialog" aria-modal="true" style="width:520px">
          <h3 class="modal-title">OpenCode Zen — Autenticação via Token (Portátil)</h3>
          <p class="dim" style="font-size:12px;margin-top:-4px;margin-bottom:10px">
            Funciona via HTTPS puro em qualquer máquina sem precisar do CLI do OpenCode instalado.
            O token é salvo fora do repositório Git (em <code>credentials.json</code> do seu usuário) ou lido de <code>OPENCODE_API_KEY</code>.
          </p>
          <div style="font-size:12px;margin-bottom:10px"><strong>Status:</strong> ${statusBadge}</div>
          <div class="form-grid">
            <label>Token OpenCode:</label>
            <input id="oc-api-key" type="password" placeholder="${oc?.configured ? "Cole novo token para substituir…" : "Cole sua chave OpenCode Zen (OPENCODE_API_KEY)"}" autocomplete="off" />

            <label>Base URL:</label>
            <input id="oc-base-url" value="${escapeHtml(baseUrl)}" placeholder="https://opencode.ai/zen/v1" />
          </div>
          <div style="margin-top:12px;border-top:1px solid var(--border);padding-top:10px">
            <div class="dim" style="font-size:11.5px;margin-bottom:6px;font-weight:600;color:var(--text-dim)">
              ${icons.bolt} OpenRouter — Chave de API (modelos <code>fornecedor/modelo</code>)
              ${state.apiKeysStatus?.["OPENROUTER_API_KEY"] ? `<span style="color:var(--green)">(salvo: ${escapeHtml(state.apiKeysStatus["OPENROUTER_API_KEY"])})</span>` : `<span style="color:var(--amber)">(não salvo)</span>`}
            </div>
            ${state.apiKeysStatus?.["env_OPENROUTER_API_KEY"] ? `<div style="color:var(--amber);font-size:11.5px;margin-bottom:6px">⚠ A variável de ambiente <code>OPENROUTER_API_KEY</code> está definida no sistema e TEM prioridade sobre o valor salvo aqui. Se der 401, corrija ou remova essa variável (Configurações → Variáveis de Ambiente).</div>` : ""}
            <div class="form-grid">
              <label>OpenRouter API Key:</label>
              <input id="or-api-key" type="password" placeholder="sk-or-v1-… (chave em https://openrouter.ai/keys)" autocomplete="off" />

              <label>OpenRouter Base URL:</label>
              <input id="or-base-url" placeholder="https://openrouter.ai/api/v1" />
            </div>
            <div id="or-modal-err" style="color:var(--red);font-size:12px;margin-top:6px;display:none"></div>
            <div class="modal-footer" style="padding:8px 0 0 0">
              ${state.apiKeysStatus?.["OPENROUTER_API_KEY"] ? `<button class="danger mini" id="or-clear-key">Remover Chave Salva</button>` : ""}
              <div class="spacer"></div>
              <button class="primary mini" id="or-save">Salvar Chave OpenRouter</button>
            </div>
          </div>
          <div style="margin-top:10px">
            <div class="dim" style="font-size:11.5px;margin-bottom:4px">
              Catálogo OpenCode Go (${state.openCodeGoModels.length}) + OpenCode Zen (${state.openCodeModels.length}) — use <code>opencode-go:&lt;id&gt;</code> ou <code>opencode:&lt;id&gt;</code>:
            </div>
            <div style="max-height:170px;overflow:auto;background:var(--bg-deep);padding:8px;border-radius:6px;font-size:11.5px;font-family:var(--mono);border:1px solid var(--border)">
              ${state.openCodeGoModels.length === 0 && state.openCodeModels.length === 0
                ? `<span class="dim">Carregando catálogos OpenCode Go e Zen…</span>`
                : [
                    ...(state.openCodeGoModels.length > 0
                      ? [`<div style="color:var(--accent);font-weight:600;margin-bottom:3px">— OpenCode Go (Assinatura: ${state.openCodeGoModels.length}) —</div>`]
                          .concat(state.openCodeGoModels.map((m) => `<div>opencode-go:${escapeHtml(m.slug)}</div>`))
                      : []),
                    ...(state.openCodeModels.length > 0
                      ? [`<div style="color:var(--accent);font-weight:600;margin:6px 0 3px 0">— OpenCode Zen (${state.openCodeModels.length}) —</div>`]
                          .concat(state.openCodeModels.map((m) => `<div>opencode:${escapeHtml(m.slug)}</div>`))
                      : []),
                  ].join("")
              }
            </div>
          </div>
          <div id="oc-modal-err" style="color:var(--red);font-size:12px;margin-top:8px;display:none"></div>
          <div class="modal-footer">
            ${oc?.configured && oc.source !== "env" ? `<button class="danger mini" id="oc-clear-token">Remover Token</button>` : ""}
            <div class="spacer"></div>
            <button id="oc-close">Fechar</button>
            <button class="primary" id="oc-save">Salvar & Testar</button>
          </div>
        </div>
      </div>
    `;

    const closeOc = () => {
      state.showOpenCodeModal = false;
      renderModalLayer();
    };
    document.getElementById("oc-close")?.addEventListener("click", closeOc);
    document.getElementById("oc-modal-backdrop")?.addEventListener("click", (e) => {
      if ((e.target as HTMLElement).id === "oc-modal-backdrop") closeOc();
    });
    document.getElementById("oc-clear-token")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.SaveOpenCodeConfig) return;
      try {
        const res = await backend.SaveOpenCodeConfig("", "");
        state.openCodeStatus = res.status;
        if (res.goModels) state.openCodeGoModels = res.goModels;
        if (res.models) state.openCodeModels = res.models;
        updateStatusBar();
        updateMissionBoardIfOpen();
        if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
        renderModalLayer();
        setStatus("Token do OpenCode removido.");
      } catch (err: any) {
        const errEl = document.getElementById("oc-modal-err");
        if (errEl) {
          const reason = err instanceof Error ? err.message : String(err);
          errEl.textContent = `Não foi possível remover o token: ${reason}`;
          errEl.style.display = "block";
        }
      }
    });
    document.getElementById("oc-save")?.addEventListener("click", async () => {
      const backend = api();
      if (!backend?.SaveOpenCodeConfig) return;
      const keyIn = (document.getElementById("oc-api-key") as HTMLInputElement | null)?.value.trim() || "";
      const urlIn = (document.getElementById("oc-base-url") as HTMLInputElement | null)?.value.trim() || "";
      if (!keyIn && !state.openCodeStatus?.configured) {
        const errEl = document.getElementById("oc-modal-err");
        if (errEl) {
          errEl.textContent = "Cole seu token do OpenCode antes de salvar.";
          errEl.style.display = "block";
        }
        return;
      }
      try {
        setStatus("Salvando e validando token OpenCode (Go + Zen)…");
        const res = keyIn
          ? await backend.SaveOpenCodeConfig(keyIn, urlIn)
          : await backend.BootstrapOpenCode(true);
        state.openCodeStatus = res.status;
        if (res.goModels && res.goModels.length > 0) state.openCodeGoModels = res.goModels;
        if (res.models && res.models.length > 0) state.openCodeModels = res.models;
        updateStatusBar();
        updateMissionBoardIfOpen();
        if (state.sidebarView === "team" && state.showLeftSidebar) renderLeftSidebar();
        renderModalLayer();
        setStatus(`OpenCode pronto: ${res.status.detail}`);
      } catch (err: any) {
        const errEl = document.getElementById("oc-modal-err");
        if (errEl) {
          const reason = err instanceof Error ? err.message : String(err);
          errEl.textContent = `Não foi possível salvar e testar o token: ${reason}. Confira a chave e a Base URL e tente novamente.`;
          errEl.style.display = "block";
        }
      }
    });

    const saveOpenRouterKey = async (key: string, baseURL: string) => {
      const backend = api();
      if (!backend?.SaveAPIKeys) return;
      try {
        state.apiKeysStatus = await backend.SaveAPIKeys({
          OPENROUTER_API_KEY: key,
          OPENROUTER_BASE_URL: baseURL,
        });
        toast(key ? "Chave do OpenRouter salva." : "Chave do OpenRouter removida.", "success");
        setStatus("OpenRouter atualizado.");
        renderModalLayer();
      } catch (err: any) {
        const errEl = document.getElementById("or-modal-err");
        if (errEl) {
          errEl.textContent = `Falha ao salvar a chave do OpenRouter: ${err?.message || String(err)}`;
          errEl.style.display = "block";
        }
      }
    };
    document.getElementById("or-save")?.addEventListener("click", () => {
      const keyIn = (document.getElementById("or-api-key") as HTMLInputElement | null)?.value.trim() || "";
      const urlIn = (document.getElementById("or-base-url") as HTMLInputElement | null)?.value.trim() || "";
      if (!keyIn && !state.apiKeysStatus?.["OPENROUTER_API_KEY"]) {
        const errEl = document.getElementById("or-modal-err");
        if (errEl) {
          errEl.textContent = "Cole sua chave OpenRouter (sk-or-v1-…) antes de salvar.";
          errEl.style.display = "block";
        }
        return;
      }
      void saveOpenRouterKey(keyIn, urlIn);
    });
    document.getElementById("or-clear-key")?.addEventListener("click", () => {
      void saveOpenRouterKey("", "");
    });
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

  if (state.aiUsageModal.open) {
    renderAIUsageModal(layer);
    return;
  }

  layer.innerHTML = "";
}

export async function openAIUsageModal() {
  state.aiUsageModal.open = true;
  state.aiUsageModal.loading = true;
  state.aiUsageModal.editingProvider = "";
  renderModalLayer();
  const backend = api();
  if (!backend || !backend.GetAIUsage) {
    state.aiUsageModal.loading = false;
    renderModalLayer();
    return;
  }
  try {
    state.aiUsageModal.report = await backend.GetAIUsage();
  } catch (err: any) {
    toastError("carregar uso e limites das IAs", err);
  } finally {
    state.aiUsageModal.loading = false;
    if (state.aiUsageModal.open) renderModalLayer();
  }
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

export function renderProviderUsageCardHtml(p: AIProviderUsage, rep: AIUsageReport): string {
  const isEditing = state.aiUsageModal.editingProvider === p.provider;
  const isAgy = p.provider === "agy";
  const isOpenCodeGo = p.provider === "opencode-go";
  const isOpenCodeZen = p.provider === "opencode";

  let statusPill = `<span class="tok-pill">Local</span>`;
  let extraHeaderBtn = "";
  let bodyHtml = "";
  let footnote = "";

  if (isAgy) {
    const agyOk = Boolean(state.agyStatus?.LoggedIn) || Boolean(rep.agyQuota?.available);
    const aq = rep.agyQuota;
    const groups = aq?.available && aq.groups && aq.groups.length > 0 ? aq.groups : [];
    statusPill = agyOk
      ? groups.length > 0
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

    if (groups.length > 0) {
      const groupsHtml = groups.map((g) => {
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
      bodyHtml = groupsHtml + localSub;
      footnote = aq?.detail || "Cota oficial obtida via agy /usage (0 tokens consumidos)";
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

export function renderAIUsageModal(layer: HTMLElement) {
  const m = state.aiUsageModal;
  const rep = m.report;
  layer.innerHTML = `
    <div class="modal-backdrop" id="aiu-backdrop">
      <div class="modal-box ai-usage-modal">
        <div class="aiu-modal-head">
          <div style="display:flex;align-items:center;gap:8px">
            <span class="aiu-head-icon">${icons.aiUsage}</span>
            <div>
              <h3 class="modal-title" style="margin:0">Uso & Limites das IAs</h3>
              <div class="dim" style="font-size:11.5px">Acompanhe janelas de 5 horas e semanal do Antigravity, OpenCode e demais provedores.</div>
            </div>
          </div>
          <div style="display:flex;gap:6px;align-items:center">
            <button type="button" class="mini" id="aiu-refresh" ${m.loading ? "disabled" : ""}>
              ${m.loading ? icons.spinner : icons.refresh}
              <span>Atualizar</span>
            </button>
            <button type="button" class="icon-btn" id="aiu-close" title="Fechar">${icons.closeSmall}</button>
          </div>
        </div>

        <div class="aiu-cards-wrap">
          ${m.loading && !rep
            ? `<div class="dim" style="padding:26px;text-align:center">${icons.spinner} Consultando uso e cotas das IAs…</div>`
            : !rep
              ? `<div class="dim" style="padding:20px;text-align:center">Não foi possível carregar os dados de uso.</div>`
              : rep.providers.map((p) => renderProviderUsageCardHtml(p, rep)).join("")
          }
        </div>

        <div class="modal-footer" style="margin-top:12px">
          <span class="dim" style="font-size:11px">Dados locais salvos em %AppData%\\HiveMind\\ai-usage.json</span>
          <div class="spacer"></div>
          <button type="button" class="primary" id="aiu-close-footer">Fechar</button>
        </div>
      </div>
    </div>
  `;

  const closeModal = () => {
    state.aiUsageModal.open = false;
    state.aiUsageModal.editingProvider = "";
    renderModalLayer();
  };
  document.getElementById("aiu-close")?.addEventListener("click", closeModal);
  document.getElementById("aiu-close-footer")?.addEventListener("click", closeModal);
  document.getElementById("aiu-backdrop")?.addEventListener("click", (e) => {
    if ((e.target as HTMLElement).id === "aiu-backdrop") closeModal();
  });
  document.getElementById("aiu-refresh")?.addEventListener("click", () => void openAIUsageModal());
  document.getElementById("aiu-open-opencode-cfg")?.addEventListener("click", () => {
    state.aiUsageModal.open = false;
    openOpenCodeModal();
  });

  layer.querySelectorAll("[data-aiu-edit-limits]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const prov = (btn as HTMLElement).dataset.aiuEditLimits || "";
      state.aiUsageModal.editingProvider = state.aiUsageModal.editingProvider === prov ? "" : prov;
      renderModalLayer();
    });
  });

  document.getElementById("aiu-limit-cancel")?.addEventListener("click", () => {
    state.aiUsageModal.editingProvider = "";
    renderModalLayer();
  });

  layer.querySelectorAll("[data-aiu-save-limits]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const prov = (btn as HTMLElement).dataset.aiuSaveLimits || "";
      const v5h = Math.max(0, parseInt((document.getElementById("aiu-limit-5h") as HTMLInputElement | null)?.value || "0", 10) || 0);
      const v7d = Math.max(0, parseInt((document.getElementById("aiu-limit-7d") as HTMLInputElement | null)?.value || "0", 10) || 0);
      if (!api()?.SaveAIUsageLimits) return;
      try {
        state.aiUsageModal.report = await api()!.SaveAIUsageLimits(prov, v5h, v7d);
        state.aiUsageModal.editingProvider = "";
        renderModalLayer();
        toast("Metas de uso atualizadas.", "success");
      } catch (err: any) {
        toastError("salvar metas de uso", err);
      }
    });
  });
}
