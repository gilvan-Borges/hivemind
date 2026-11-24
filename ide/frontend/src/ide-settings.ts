import { icons } from "./icons";
import type { IdeSettingsSection } from "./types";
import { escapeHtml } from "./utils";
import { loadSettings, updateSettings } from "./companion/settings";
import { renderEditorBody, renderEditorBodyAsync, renderEditorTabs } from "./editor";
import { refreshInlineAIStatus, toggleGhostText } from "./inline-ai";
import {
  bindAIUsagePageEvents,
  bindProvidersContentEvents,
  openGitHubModal,
  openJiraModal,
  openMobileModal,
  openUpdatesModal,
  refreshAIUsageData,
  refreshJiraStatusAndProjects,
  refreshProvidersData,
  renderAIUsageSectionsHtml,
  renderModalLayer,
  renderProvidersContentHtml,
  runProvidersScan,
} from "./modals";
import { openResidentTab, refreshResidentData, residentState, updateResidentStatusChip } from "./resident";
import { refreshMCP, setMCPEnabled } from "./mcp";
import { openSidebarView, syncActivityButtons, toggleChatExpanded, updateStatusBar } from "./shell";
import { IDE_SETTINGS_TAB_KEY, api, setStatus, state, toast, toastError } from "./state";
import { openTeamConfigTab } from "./teamconfig";

export async function openIdeSettingsTab(
  section?: IdeSettingsSection,
  opts?: { providersTab?: "profiles" | "global" | "scan"; provider?: string; triggerScan?: boolean },
): Promise<void> {
  if (state.showAPIKeysModal) {
    state.showAPIKeysModal = false;
    renderModalLayer();
  }
  if (section) {
    state.ideSettingsSection = section;
  } else if (!state.ideSettingsSection) {
    state.ideSettingsSection = "providers";
  }
  if (opts?.providersTab) {
    state.apiKeysTab = opts.providersTab;
  }
  if (opts?.provider) {
    state.apiKeysTab = "profiles";
    if (opts.provider === "agy") {
      state.profileEditor = { open: false, id: "", provider: "agy", authMode: "adc" };
    } else {
      state.profileEditor = { open: true, id: "", provider: opts.provider, authMode: "key" };
    }
  }

  const existing = state.editorTabs.find((t) => t.kind === "ide-settings");
  if (!existing) {
    state.editorTabs.unshift({
      kind: "ide-settings",
      relPath: IDE_SETTINGS_TAB_KEY,
      name: "Configurações da IDE",
      content: "",
      dirty: false,
    });
  }
  state.activeEditorPath = IDE_SETTINGS_TAB_KEY;
  renderEditorTabs();
  renderEditorBody();
  syncActivityButtons();
  void refreshIdeSettingsSection(state.ideSettingsSection);
  if (opts?.triggerScan) {
    void runProvidersScan();
  }
}

export async function refreshIdeSettingsSection(section: IdeSettingsSection): Promise<void> {
  const rerender = () => {
    if (state.activeEditorPath === IDE_SETTINGS_TAB_KEY) {
      renderEditorBodyAsync();
    }
  };
  try {
    if (section === "providers") {
      await refreshProvidersData();
    } else if (section === "agent-hive") {
      await refreshResidentData();
      const running = await api()?.OrbRunning?.().catch(() => false);
      state.orbRunning = Boolean(running);
    } else if (section === "limits") {
      await refreshAIUsageData();
    } else if (section === "integrations") {
      const backend = api();
      const root = state.project?.root || "";
      await Promise.all([
        backend?.GitHubStatus ? backend.GitHubStatus(root).then((gh) => { state.ghStatus = gh; }).catch(() => {}) : Promise.resolve(),
        refreshJiraStatusAndProjects(),
        backend?.MobileBridgeStatus ? backend.MobileBridgeStatus().then((mb) => { state.mobileStatus = mb; }).catch(() => {}) : Promise.resolve(),
        backend?.RelayStatus ? backend.RelayStatus().then((rs) => { state.relayStatus = rs; }).catch(() => {}) : Promise.resolve(),
        refreshMCP(),
      ]);
    } else if (section === "general") {
      const backend = api();
      await Promise.all([
        backend?.AppVersion && !state.appVersion ? backend.AppVersion().then((v) => { state.appVersion = v || ""; }).catch(() => {}) : Promise.resolve(),
        refreshInlineAIStatus(),
      ]);
    }
  } catch {
    // mantém último estado visível
  }
  rerender();
}

function renderAgentHiveSectionHtml(): string {
  const st = residentState.status;
  const hiveEnabled = st ? st.enabled !== false : true;
  const orbEnabled = st ? st.orbEnabled !== false : true;
  const prov = st?.provider || "openrouter";
  const mod = st?.model || "openrouter/auto";
  const comp = loadSettings();
  const sbOk = Boolean(st?.sandboxAvailable);
  const vaultOk = Boolean(st?.vaultAvailable);
  const secrets = Array.isArray(st?.vaultSecrets) ? st.vaultSecrets : [];
  const binariesDraft = residentState.policyDirty
    ? residentState.binariesDraft
    : (Array.isArray(st?.allowedBinaries) ? st.allowedBinaries.join(", ") : residentState.binariesDraft);
  const hostsDraft = residentState.policyDirty
    ? residentState.hostsDraft
    : (Array.isArray(st?.allowedHosts) ? st.allowedHosts.join(", ") : residentState.hostsDraft);
  const orbVisible = hiveEnabled && orbEnabled && state.orbRunning;

  return `
    <div class="ide-settings-section">
      <section class="ide-settings-card">
        <div class="ide-settings-card-head">
          <div>
            <h3>Ativação &amp; Presença do Agent Hive</h3>
            <div class="dim">Controle global salvo neste PC. Desligar pausa rotinas agendadas, encerra o Orb e recusa novos turnos locais ou remotos.</div>
          </div>
          <div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap">
            <span class="ide-settings-pill ${hiveEnabled ? "is-on" : "is-off"}">${hiveEnabled ? "Ativado" : "Desativado"}</span>
            <span class="ide-settings-pill ${orbVisible ? "is-on" : "is-off"}">${orbVisible ? "Orb visível" : "Orb oculto"}</span>
            <button type="button" class="mini" id="ide-hive-open-chat">${icons.sparkle}<span>Abrir Conversa</span></button>
          </div>
        </div>

        <label class="ide-settings-toggle-row">
          <input type="checkbox" id="ide-hive-enabled" ${hiveEnabled ? "checked" : ""} />
          <div>
            <div class="ide-settings-toggle-title">Agent Hive ativado</div>
            <div class="dim">Mantém o assistente residente disponível na barra inferior, atalho do painel lateral e rotinas agendadas.</div>
          </div>
        </label>

        <label class="ide-settings-toggle-row">
          <input type="checkbox" id="ide-hive-orb-enabled" ${orbEnabled ? "checked" : ""} ${!hiveEnabled ? "disabled" : ""} />
          <div>
            <div class="ide-settings-toggle-title">Mascote / Orb flutuante na área de trabalho</div>
            <div class="dim">Exibe a janela flutuante independente sobre o desktop para acompanhar status, alertas e aprovações rápidas.</div>
          </div>
        </label>
      </section>

      <section class="ide-settings-card">
        <div class="ide-settings-card-head">
          <div>
            <h3>Modelo, Workstream &amp; Roteamento</h3>
            <div class="dim">Defina provedor, modelo padrão, fluxo de trabalho e failover automático via Jev para o Agent Hive.</div>
          </div>
          <button type="button" class="mini primary" id="ide-hive-save-cfg">${icons.save}<span>Salvar Modelo &amp; Rota</span></button>
        </div>

        <div class="ide-settings-grid">
          <div class="ide-settings-field">
            <label for="ide-hive-provider">Provedor HTTP</label>
            <select id="ide-hive-provider">
              ${["auto", "openrouter", "opencode", "gemini", "openai", "anthropic", "cursor"].map((p) =>
                `<option value="${p}" ${prov === p ? "selected" : ""}>${escapeHtml(p)}</option>`
              ).join("")}
            </select>
          </div>
          <div class="ide-settings-field">
            <label for="ide-hive-model">Modelo (slug ou auto)</label>
            <input id="ide-hive-model" value="${escapeHtml(mod)}" placeholder="openrouter/auto" />
          </div>
          <div class="ide-settings-field">
            <label for="ide-hive-workstream">Workstream padrão</label>
            <input id="ide-hive-workstream" value="${escapeHtml(residentState.workstream)}" placeholder="geral" />
          </div>
        </div>

        <label class="ide-settings-toggle-row">
          <input type="checkbox" id="ide-hive-event-triggers" ${st?.eventTriggers ? "checked" : ""} />
          <div>
            <div class="ide-settings-toggle-title">Acordar em eventos da IDE</div>
            <div class="dim">Sinaliza quando arquivos são salvos, uma lane termina ou surge uma aprovação (não executa ferramentas sozinho).</div>
          </div>
        </label>

        <label class="ide-settings-toggle-row">
          <input type="checkbox" id="ide-hive-jev-failover" ${st?.jevFailover ? "checked" : ""} />
          <div>
            <div class="ide-settings-toggle-title">Failover automático via Jev</div>
            <div class="dim">Se a rota escolhida falhar ao iniciar, o Jev tenta automaticamente o próximo provedor saudável elegível.</div>
          </div>
        </label>
      </section>

      <section class="ide-settings-card">
        <div class="ide-settings-card-head">
          <div>
            <h3>Notificações &amp; Companion</h3>
            <div class="dim">Comportamento visual, som de alerta e notificações nativas do sistema operacional.</div>
          </div>
        </div>

        <div class="ide-settings-grid">
          <label class="ide-settings-toggle-row">
            <input type="checkbox" id="ide-comp-enabled" ${comp.enabled ? "checked" : ""} />
            <div>
              <div class="ide-settings-toggle-title">Companion na IDE</div>
              <div class="dim">Balões e avisos contextuais dentro da janela principal.</div>
            </div>
          </label>
          <label class="ide-settings-toggle-row">
            <input type="checkbox" id="ide-comp-os" ${comp.osNotifications ? "checked" : ""} />
            <div>
              <div class="ide-settings-toggle-title">Notificações do sistema</div>
              <div class="dim">Só quando a janela da IDE está oculta ou minimizada.</div>
            </div>
          </label>
          <label class="ide-settings-toggle-row">
            <input type="checkbox" id="ide-comp-sound" ${comp.sound ? "checked" : ""} />
            <div>
              <div class="ide-settings-toggle-title">Som de alerta</div>
              <div class="dim">Toca um aviso sonoro em aprovações, conclusões ou erros.</div>
            </div>
          </label>
          <label class="ide-settings-toggle-row">
            <input type="checkbox" id="ide-comp-dnd" ${comp.doNotDisturb ? "checked" : ""} />
            <div>
              <div class="ide-settings-toggle-title">Não Perturbe (DND)</div>
              <div class="dim">Silencia todos os popups e sons temporariamente.</div>
            </div>
          </label>
        </div>
      </section>

      <section class="ide-settings-card">
        <div class="ide-settings-card-head">
          <div>
            <h3>Segurança, Isolamento, Cofre &amp; SOUL.md</h3>
            <div class="dim">Política local em <code>%LOCALAPPDATA%\\HiveMind\\resident\\policy.json</code> e credenciais no cofre do SO.</div>
          </div>
          <div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap">
            <span class="ide-settings-pill ${sbOk ? "is-on" : "is-off"}" title="${escapeHtml(st?.sandboxReason || "")}">${sbOk ? `Sandbox: ${escapeHtml(st?.sandboxMechanism || "ativa")}` : "Sandbox: indisponível"}</span>
            <span class="ide-settings-pill ${vaultOk ? "is-on" : "is-warn"}">${vaultOk ? "Cofre SO: ativo" : "Cofre SO: indisponível"}</span>
          </div>
        </div>

        <label class="ide-settings-toggle-row">
          <input type="checkbox" id="ide-hive-full-access" ${st?.fullAccess ? "checked" : ""} />
          <div>
            <div class="ide-settings-toggle-title">Permissão Full</div>
            <div class="dim">Desligado por padrão. Quando ligado, o Agent Hive não pede aprovação e não é barrado por política, sandbox, fence, netguard ou allowlist. Vale para o PC, o browser e a IDE. A auditoria continua gravando cada ação.</div>
          </div>
        </label>
        ${st?.fullAccess ? `<div class="tc-error">Permissão Full ligada: o Agent Hive age sem pedir confirmação.</div>` : ""}

        <label class="ide-settings-toggle-row">
          <input type="checkbox" id="ide-hive-full-unattended" ${st?.allowFullUnattended ? "checked" : ""} />
          <div>
            <div class="ide-settings-toggle-title">Full em loop desacompanhado</div>
            <div class="dim">Desligado por padrão. Só esta tela grava. Sem ele, loop de rotina ou de evento continua governado mesmo com o modo Full ativo. Um loop acompanhado segue o Full normal.</div>
          </div>
        </label>

        <label class="ide-settings-toggle-row">
          <input type="checkbox" id="ide-hive-experimental-unisolated" ${st?.experimentalUnisolated ? "checked" : ""} />
          <div>
            <div class="ide-settings-toggle-title">Browser e desktop SEM isolamento (experimental_unisolated)</div>
            <div class="dim">Desligado por padrão (falha fechada). Quando ligado, ações sem espaço isolado pedem confirmação fora de Full Acesso e nunca leem o cofre.</div>
          </div>
        </label>
        ${st?.experimentalUnisolated ? `<div class="tc-error">${escapeHtml(st.experimentalWarning || "SEM isolamento: a sessão real recebe ações de browser/desktop.")}</div>` : ""}

        <label class="ide-settings-toggle-row">
          <input type="checkbox" id="ide-hive-isolated-sandbox" ${st?.isolatedSandbox ? "checked" : ""} />
          <div>
            <div class="ide-settings-toggle-title">Windows Sandbox isolada (.wsb) para browser</div>
            <div class="dim">Quando disponível, abre URLs em ambiente Windows Sandbox descartável com egress restrito.</div>
          </div>
        </label>

        <div class="ide-settings-grid">
          <div class="ide-settings-field">
            <label for="ide-hive-policy-binaries">Binários autorizados (exec_command)</label>
            <input id="ide-hive-policy-binaries" placeholder="Ex.: git, go, npm (vazio = bloqueia tudo)" value="${escapeHtml(binariesDraft)}" />
          </div>
          <div class="ide-settings-field">
            <label for="ide-hive-policy-hosts">Domínios autorizados (net_fetch)</label>
            <input id="ide-hive-policy-hosts" placeholder="Ex.: api.github.com, *.golang.org (vazio = bloqueia rede)" value="${escapeHtml(hostsDraft)}" />
          </div>
        </div>
        <div style="display:flex;justify-content:flex-end">
          <button type="button" class="mini primary" id="ide-hive-save-policy">${icons.save}<span>Salvar Política Local</span></button>
        </div>

        <div style="border-top:1px solid var(--border-divider);padding-top:12px;display:flex;flex-direction:column;gap:8px">
          <strong style="font-size:12.5px">Credenciais no Cofre do Sistema (${secrets.length})</strong>
          <div style="display:flex;gap:8px;flex-wrap:wrap">
            <input id="ide-hive-secret-name" style="max-width:200px" placeholder="Nome lógico (ex.: github_ro)" value="${escapeHtml(residentState.secretNameDraft)}" />
            <input id="ide-hive-secret-val" type="password" style="flex:1;min-width:180px" placeholder="Valor do segredo (somente no cofre do SO)…" value="${escapeHtml(residentState.secretValueDraft)}" />
            <button type="button" class="mini primary" id="ide-hive-secret-add">${icons.plus}<span>Guardar no Cofre</span></button>
          </div>
          ${secrets.length === 0
            ? `<div class="dim">Nenhuma credencial cadastrada no cofre do residente.</div>`
            : secrets.map((name) => `
              <div style="display:flex;align-items:center;gap:8px;padding:4px 0;border-bottom:1px solid var(--border-divider)">
                <code>${escapeHtml(name)}</code>
                <span class="tok-pill">somente referência lógica</span>
                <span class="spacer"></span>
                <button type="button" class="mini ghost" data-ide-hive-sec-del="${escapeHtml(name)}">Remover</button>
              </div>
            `).join("")}
        </div>

        <div style="border-top:1px solid var(--border-divider);padding-top:12px;display:flex;flex-direction:column;gap:8px">
          <div class="ide-settings-card-head">
            <div>
              <strong style="font-size:12.5px">SOUL.md (Tom e Preferências Pessoais)</strong>
              <span class="dim"> · máx. 2 KiB</span>
            </div>
            <button type="button" class="mini primary" id="ide-hive-soul-save">${icons.save}<span>Salvar SOUL.md</span></button>
          </div>
          <textarea id="ide-hive-soul" rows="3" placeholder="Ex.: Seja direto em português, priorize análises objetivas e resumos de PR curtos…">${escapeHtml(residentState.soulDraft)}</textarea>
        </div>
      </section>
    </div>
  `;
}

function renderIntegrationsSectionHtml(): string {
  const gh = state.ghStatus;
  const jira = state.jiraStatus;
  const mb = state.mobileStatus;
  const relay = state.relayStatus;
  const mcpCount = state.mcpServers.length;
  const skillCount = state.skills.length;

  return `
    <div class="ide-settings-section">
      <div class="ide-settings-grid">
        <section class="ide-settings-card">
          <div class="ide-settings-card-head">
            <h3>GitHub Nativo</h3>
            <span class="ide-settings-pill ${gh?.loggedIn ? "is-on" : "is-warn"}">${gh?.loggedIn ? `@${escapeHtml(gh.user)}` : "Desconectado"}</span>
          </div>
          <div class="dim">${escapeHtml(gh?.detail || "Configure um token PAT ou sessão gh CLI global para PRs, issues e diffs.")}</div>
          <div style="display:flex;justify-content:flex-end">
            <button type="button" class="mini primary" id="ide-int-github">Configurar GitHub…</button>
          </div>
        </section>

        <section class="ide-settings-card">
          <div class="ide-settings-card-head">
            <h3>Jira Cloud (Multi-Organização)</h3>
            <span class="ide-settings-pill ${jira?.connected ? "is-on" : "is-warn"}">${jira?.connected ? "Conectado" : "Desconectado"}</span>
          </div>
          <div class="dim">${escapeHtml(jira?.detail || "Perfis Jira por organização ou fallback padrão, fora dos repositórios.")}</div>
          <div style="display:flex;justify-content:flex-end">
            <button type="button" class="mini primary" id="ide-int-jira">Configurar Jira…</button>
          </div>
        </section>

        <section class="ide-settings-card">
          <div class="ide-settings-card-head">
            <h3>App Mobile &amp; Relay Remoto</h3>
            <span class="ide-settings-pill ${relay?.online || mb?.running ? "is-on" : ""}">${relay?.online ? "Relay online" : mb?.running ? `LAN :${mb.port}` : "Desligado"}</span>
          </div>
          <div class="dim">Bridge LAN com pareamento por QR Code e túnel relay criptografado de ponta a ponta para comandar pelo celular.</div>
          <div style="display:flex;justify-content:flex-end">
            <button type="button" class="mini primary" id="ide-int-mobile">Configurar Mobile &amp; Relay…</button>
          </div>
        </section>

        <section class="ide-settings-card">
          <div class="ide-settings-card-head">
            <h3>Catálogos Globais (MCP &amp; Skills)</h3>
            <span class="ide-settings-pill" style="font-variant-numeric:tabular-nums">${mcpCount} MCP · ${skillCount} Skills</span>
          </div>
          <div class="dim">Skills nativas em <code>%LOCALAPPDATA%\\HiveMind</code>. O interruptor de cada servidor MCP fica abaixo e vale para este PC.</div>
          <div style="display:flex;justify-content:flex-end;gap:6px;flex-wrap:wrap">
            <button type="button" class="mini" id="ide-int-skills">${icons.skills}<span>Abrir Skills</span></button>
          </div>
        </section>
      </div>
      <section class="ide-settings-card">
        <div class="ide-settings-card-head">
          <div>
            <h3>Servidores MCP</h3>
            <div class="dim">O Playwright nasce ligado. Chrome DevTools e a extensão Agent Hive desligam quando ele está ligado. Desmarcar grava o interruptor neste PC.</div>
          </div>
        </div>
        ${renderMCPSettingsRows()}
      </section>
    </div>
  `;
}

function renderMCPSettingsRows(): string {
  if (state.mcpServers.length === 0) {
    return `<div class="dim">Nenhum servidor MCP carregado.</div>`;
  }
  return state.mcpServers.map((s) => {
    const detail = [s.description, s.detail].filter(Boolean).join(" · ");
    return `
      <label class="ide-settings-toggle-row">
        <input type="checkbox" data-ide-mcp="${escapeHtml(s.name)}" ${s.enabled ? "checked" : ""} />
        <div>
          <div class="ide-settings-toggle-title">${escapeHtml(s.name)}</div>
          <div class="dim">${escapeHtml(detail)}</div>
        </div>
      </label>
    `;
  }).join("");
}

function renderGeneralSectionHtml(): string {
  const versionText = state.appVersion || state.updateCheckState.version || "dev";
  const projName = state.project?.name || "";

  return `
    <div class="ide-settings-section">
      <section class="ide-settings-card">
        <div class="ide-settings-card-head">
          <div>
            <h3>Editor &amp; Interface</h3>
            <div class="dim">Preferências globais de edição assistida, visualização de diffs e layout do painel de equipe.</div>
          </div>
        </div>

        <label class="ide-settings-toggle-row">
          <input type="checkbox" id="ide-gen-ghost" ${state.ghostTextEnabled ? "checked" : ""} />
          <div>
            <div class="ide-settings-toggle-title">Ghost Text inline no editor</div>
            <div class="dim">${escapeHtml(state.inlineAIStatus?.ghostReason || "Autocompletar inline aceito com Tab durante a edição de código.")}</div>
          </div>
        </label>

        <label class="ide-settings-toggle-row">
          <input type="checkbox" id="ide-gen-inline-review" ${state.inlineReviewEnabled ? "checked" : ""} />
          <div>
            <div class="ide-settings-toggle-title">Revisão inline no editor</div>
            <div class="dim">Mostra botões para aceitar ou rejeitar blocos alterados pelos agentes direto no arquivo aberto.</div>
          </div>
        </label>

        <label class="ide-settings-toggle-row">
          <input type="checkbox" id="ide-gen-chat-expanded" ${state.chatExpandedPref ? "checked" : ""} />
          <div>
            <div class="ide-settings-toggle-title">Expandir Equipe IA quando o editor estiver sem abas</div>
            <div class="dim">Centraliza o chat em largura de leitura confortável (atalho <code>Ctrl+Shift+E</code>).</div>
          </div>
        </label>

        <div class="ide-settings-field" style="max-width:260px">
          <label for="ide-gen-diff-mode">Visualização padrão de Diff</label>
          <select id="ide-gen-diff-mode">
            <option value="unified" ${state.diffViewMode === "unified" ? "selected" : ""}>Unificado</option>
            <option value="split" ${state.diffViewMode === "split" ? "selected" : ""}>Lado a Lado</option>
          </select>
        </div>
      </section>

      <section class="ide-settings-card">
        <div class="ide-settings-card-head">
          <div>
            <h3>Atualizações &amp; Aplicativo</h3>
            <div class="dim" style="font-variant-numeric:tabular-nums">Versão instalada: <code>${escapeHtml(versionText)}</code></div>
          </div>
          <div style="display:flex;gap:8px;flex-wrap:wrap">
            <button type="button" class="mini primary" id="ide-gen-updates">${icons.refresh}<span>Verificar Atualizações…</span></button>
            <button type="button" class="mini danger" id="ide-gen-quit-all">${icons.closeSmall}<span>Sair completamente (IDE + Orb)</span></button>
          </div>
        </div>
      </section>

      <section class="ide-settings-card">
        <div class="ide-settings-card-head">
          <div>
            <h3>Escopo Global vs. Equipe do Projeto</h3>
            <div class="dim">
              Regras específicas de um projeto (<code>.hivemind/team.yaml</code>: modelos por papel Chefe/Dev/QA, autonomia, branches Base/Homolog, worktrees e atribuição de skills) ficam isoladas em <strong>Equipe → Config</strong>${projName ? ` para <strong>${escapeHtml(projName)}</strong>` : ""}.
            </div>
          </div>
          ${state.project ? `<button type="button" class="mini" id="ide-gen-open-team">${icons.team}<span>Abrir Config da Equipe</span></button>` : ""}
        </div>
      </section>
    </div>
  `;
}

export function renderIdeSettingsPageHtml(): string {
  const section: IdeSettingsSection = state.ideSettingsSection || "providers";
  const hiveOn = residentState.status ? residentState.status.enabled !== false : true;
  const navItems: Array<{ id: IdeSettingsSection; label: string; icon: string; badge?: string }> = [
    { id: "providers", label: "Provedores & Contas", icon: icons.cloud },
    {
      id: "agent-hive",
      label: "Agent Hive",
      icon: icons.sparkle,
      badge: `<span class="ide-settings-pill ${hiveOn ? "is-on" : "is-off"}">${hiveOn ? "Ativo" : "Desativado"}</span>`,
    },
    { id: "limits", label: "Limites & Jev", icon: icons.aiUsage },
    { id: "integrations", label: "Conectores & Mobile", icon: icons.mcp },
    { id: "general", label: "Editor & Geral", icon: icons.settings },
  ];

  let contentHtml = "";
  if (section === "providers") {
    contentHtml = `<div class="ide-settings-section ide-settings-card">${renderProvidersContentHtml({ inline: true })}</div>`;
  } else if (section === "agent-hive") {
    contentHtml = renderAgentHiveSectionHtml();
  } else if (section === "limits") {
    contentHtml = `
      <div class="ide-settings-section">
        <div class="ide-settings-card">
          <div class="ide-settings-card-head">
            <div>
              <h3>Limites, Cotas &amp; Política do Jev</h3>
              <div class="dim">Controle pesos do roteador Jev, teto diário em USD por provedor, saúde do circuit breaker e metas locais de tokens.</div>
            </div>
            <button type="button" class="mini" id="aiu-refresh" ${state.aiUsagePage.loading ? "disabled" : ""}>
              ${state.aiUsagePage.loading ? icons.spinner : icons.refresh}
              <span>Atualizar Cotas</span>
            </button>
          </div>
        </div>
        ${renderAIUsageSectionsHtml()}
      </div>
    `;
  } else if (section === "integrations") {
    contentHtml = renderIntegrationsSectionHtml();
  } else {
    contentHtml = renderGeneralSectionHtml();
  }

  return `
    <div class="ide-settings-page" id="ide-settings-root">
      <div class="tc-header">
        <div>
          <div class="tc-title-row">
            ${icons.settings}
            <h2>Configurações da IDE</h2>
            <span class="tok-pill">Escopo Global · Este PC</span>
          </div>
          <div class="dim">Provedores, contas, Agent Hive, limites do Jev, conectores e preferências do editor — independentes da configuração de cada equipe.</div>
        </div>
        <div class="tc-header-actions">
          ${state.project ? `<button type="button" class="mini" id="ide-set-open-team" title="Abrir configuração isolada desta equipe (.hivemind/team.yaml)">${icons.team}<span>Configurar Equipe do Projeto</span></button>` : ""}
        </div>
      </div>

      <div class="ide-settings-body">
        <nav class="ide-settings-nav" aria-label="Seções de Configurações da IDE">
          ${navItems.map((item) => `
            <button type="button" class="ide-settings-nav-btn ${section === item.id ? "active" : ""}" data-ide-settings-sec="${item.id}">
              <span class="ide-settings-nav-ico">${item.icon}</span>
              <span class="ide-settings-nav-label">${escapeHtml(item.label)}</span>
              ${item.badge || ""}
            </button>
          `).join("")}
        </nav>

        <div class="ide-settings-content">
          ${contentHtml}
        </div>
      </div>
    </div>
  `;
}

function splitPolicyList(raw: string): string[] {
  return raw
    .split(/[,\n]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

export function bindIdeSettingsEvents(): void {
  const rerenderSelf = () => {
    if (state.activeEditorPath === IDE_SETTINGS_TAB_KEY) {
      renderEditorBody();
    }
  };

  document.getElementById("ide-set-open-team")?.addEventListener("click", () => {
    void openTeamConfigTab();
  });

  document.querySelectorAll<HTMLElement>("[data-ide-settings-sec]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const next = (btn.dataset.ideSettingsSec as IdeSettingsSection) || "providers";
      if (state.ideSettingsSection === next) return;
      state.ideSettingsSection = next;
      rerenderSelf();
      void refreshIdeSettingsSection(next);
    });
  });

  const section: IdeSettingsSection = state.ideSettingsSection || "providers";

  if (section === "providers") {
    bindProvidersContentEvents(rerenderSelf);
    return;
  }

  if (section === "limits") {
    bindAIUsagePageEvents(rerenderSelf, () => refreshIdeSettingsSection("limits"));
    return;
  }

  if (section === "integrations") {
    document.getElementById("ide-int-github")?.addEventListener("click", () => void openGitHubModal());
    document.getElementById("ide-int-jira")?.addEventListener("click", () => void openJiraModal());
    document.getElementById("ide-int-mobile")?.addEventListener("click", () => void openMobileModal());
    document.getElementById("ide-int-skills")?.addEventListener("click", () => openSidebarView("skills"));
    document.querySelectorAll<HTMLInputElement>("[data-ide-mcp]").forEach((input) => {
      input.addEventListener("change", () => {
        const name = input.dataset.ideMcp || "";
        if (!name) return;
        void setMCPEnabled(name, input.checked, false).then(rerenderSelf);
      });
    });
    return;
  }

  if (section === "general") {
    document.getElementById("ide-gen-ghost")?.addEventListener("change", (ev) => {
      const want = Boolean((ev.target as HTMLInputElement | null)?.checked);
      if (want !== state.ghostTextEnabled) {
        toggleGhostText();
      }
      rerenderSelf();
    });
    document.getElementById("ide-gen-inline-review")?.addEventListener("change", (ev) => {
      state.inlineReviewEnabled = Boolean((ev.target as HTMLInputElement | null)?.checked);
      try {
        localStorage.setItem("hivemind.inlineReviewEnabled", state.inlineReviewEnabled ? "1" : "0");
      } catch {
        // ambiente sem storage
      }
      setStatus(state.inlineReviewEnabled ? "Revisão inline no editor ativada." : "Revisão inline no editor desativada.");
    });
    document.getElementById("ide-gen-chat-expanded")?.addEventListener("change", (ev) => {
      const want = Boolean((ev.target as HTMLInputElement | null)?.checked);
      if (want !== state.chatExpandedPref) {
        toggleChatExpanded();
      }
    });
    document.getElementById("ide-gen-diff-mode")?.addEventListener("change", (ev) => {
      const mode = ((ev.target as HTMLSelectElement | null)?.value === "split" ? "split" : "unified");
      state.diffViewMode = mode;
      try {
        localStorage.setItem("hivemind.diffViewMode", mode);
      } catch {
        // ambiente sem storage
      }
      setStatus(`Visualização padrão de diff definida como ${mode === "split" ? "Lado a Lado" : "Unificado"}.`);
    });
    document.getElementById("ide-gen-updates")?.addEventListener("click", () => void openUpdatesModal());
    document.getElementById("ide-gen-quit-all")?.addEventListener("click", () => void api()?.QuitAll?.());
    document.getElementById("ide-gen-open-team")?.addEventListener("click", () => void openTeamConfigTab());
    return;
  }

  // section === "agent-hive"
  document.getElementById("ide-hive-open-chat")?.addEventListener("click", () => openResidentTab());

  document.getElementById("ide-hive-enabled")?.addEventListener("change", async (ev) => {
    const on = Boolean((ev.target as HTMLInputElement | null)?.checked);
    const backend = api();
    if (!backend?.ResidentSetEnabled) return;
    try {
      const st = await backend.ResidentSetEnabled(on);
      residentState.status = st;
      if (!on) residentState.running = false;
      state.orbRunning = Boolean(await backend.OrbRunning?.().catch(() => false));
      updateResidentStatusChip();
      toast(on ? "Agent Hive ativado." : "Agent Hive desativado (Orb oculto e rotinas pausadas).", "success");
      rerenderSelf();
    } catch (err: any) {
      toastError("alterar ativação do Agent Hive", err);
      rerenderSelf();
    }
  });

  document.getElementById("ide-hive-orb-enabled")?.addEventListener("change", async (ev) => {
    const on = Boolean((ev.target as HTMLInputElement | null)?.checked);
    const backend = api();
    if (!backend?.ResidentSetOrbEnabled) return;
    try {
      const st = await backend.ResidentSetOrbEnabled(on);
      residentState.status = st;
      state.orbRunning = Boolean(await backend.OrbRunning?.().catch(() => false));
      updateResidentStatusChip();
      toast(on ? "Mascote / Orb flutuante ativado." : "Mascote / Orb flutuante desativado.", "success");
      rerenderSelf();
    } catch (err: any) {
      toastError("alterar Orb flutuante", err);
      rerenderSelf();
    }
  });

  document.getElementById("ide-hive-event-triggers")?.addEventListener("change", async (ev) => {
    const on = Boolean((ev.target as HTMLInputElement | null)?.checked);
    const backend = api();
    if (!backend?.ResidentSetEventTriggers) return;
    try {
      residentState.status = await backend.ResidentSetEventTriggers(on);
      toast(on ? "Despertares por evento ligados." : "Despertares por evento desligados.", "success");
    } catch (err: any) {
      toastError("salvar eventos do Agent Hive", err);
      rerenderSelf();
    }
  });

  document.getElementById("ide-hive-jev-failover")?.addEventListener("change", async (ev) => {
    const on = Boolean((ev.target as HTMLInputElement | null)?.checked);
    const backend = api();
    if (!backend?.ResidentSetJevFailover) return;
    try {
      residentState.status = await backend.ResidentSetJevFailover(on);
      toast(on ? "Failover automático via Jev ligado." : "Failover automático via Jev desligado.", "success");
    } catch (err: any) {
      toastError("salvar failover Jev do Agent Hive", err);
      rerenderSelf();
    }
  });

  document.getElementById("ide-hive-save-cfg")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend?.ResidentSaveConfig) return;
    const prov = (document.getElementById("ide-hive-provider") as HTMLSelectElement | null)?.value || "auto";
    const mod = (document.getElementById("ide-hive-model") as HTMLInputElement | null)?.value || "";
    const ws = (document.getElementById("ide-hive-workstream") as HTMLInputElement | null)?.value || "geral";
    try {
      const st = await backend.ResidentSaveConfig(prov === "auto" ? "" : prov, mod, ws);
      residentState.status = st;
      residentState.workstream = st.workstream || "geral";
      updateResidentStatusChip();
      updateStatusBar();
      toast(`Modelo do Agent Hive salvo (${st.provider}/${st.model}).`, "success");
      rerenderSelf();
    } catch (err: any) {
      toastError("salvar modelo do Agent Hive", err);
    }
  });

  document.getElementById("ide-comp-enabled")?.addEventListener("change", (ev) => {
    updateSettings({ enabled: Boolean((ev.target as HTMLInputElement | null)?.checked) });
  });
  document.getElementById("ide-comp-os")?.addEventListener("change", (ev) => {
    updateSettings({ osNotifications: Boolean((ev.target as HTMLInputElement | null)?.checked) });
  });
  document.getElementById("ide-comp-sound")?.addEventListener("change", (ev) => {
    updateSettings({ sound: Boolean((ev.target as HTMLInputElement | null)?.checked) });
  });
  document.getElementById("ide-comp-dnd")?.addEventListener("change", (ev) => {
    updateSettings({ doNotDisturb: Boolean((ev.target as HTMLInputElement | null)?.checked) });
  });

  document.getElementById("ide-hive-full-access")?.addEventListener("change", async (ev) => {
    const backend = api();
    const on = Boolean((ev.target as HTMLInputElement | null)?.checked);
    if (!backend?.ResidentSetFullAccess) return;
    try {
      residentState.status = await backend.ResidentSetFullAccess(on);
      toast(on ? "Permissão Full ligada. A auditoria continua ativa." : "Permissão Full desligada. O Agent Hive volta ao modo governado.", "success");
      rerenderSelf();
    } catch (err: any) {
      toastError("salvar a permissão Full do Agent Hive", err);
      rerenderSelf();
    }
  });

  document.getElementById("ide-hive-full-unattended")?.addEventListener("change", async (ev) => {
    const backend = api();
    const on = Boolean((ev.target as HTMLInputElement | null)?.checked);
    if (!backend?.ResidentSetAllowFullUnattended) return;
    try {
      residentState.status = await backend.ResidentSetAllowFullUnattended(on);
      toast(on ? "Loops de rotina e evento podem usar a Permissão Full." : "Loops desacompanhados voltam ao modo governado.", "success");
      rerenderSelf();
    } catch (err: any) {
      toastError("salvar Full desacompanhado", err);
      rerenderSelf();
    }
  });

  document.getElementById("ide-hive-experimental-unisolated")?.addEventListener("change", async (ev) => {
    const on = Boolean((ev.target as HTMLInputElement | null)?.checked);
    const backend = api();
    if (!backend?.ResidentSetExperimentalUnisolated) return;
    try {
      residentState.status = await backend.ResidentSetExperimentalUnisolated(on);
      updateResidentStatusChip();
      toast(on ? "Browser e desktop do Agent Hive ligados SEM isolamento." : "Modo sem isolamento desligado.", "success");
      rerenderSelf();
    } catch (err: any) {
      toastError("salvar experimental_unisolated", err);
      rerenderSelf();
    }
  });

  document.getElementById("ide-hive-isolated-sandbox")?.addEventListener("change", async (ev) => {
    const on = Boolean((ev.target as HTMLInputElement | null)?.checked);
    const backend = api();
    if (!backend?.ResidentSetIsolatedSandbox) return;
    try {
      residentState.status = await backend.ResidentSetIsolatedSandbox(on);
      toast(on ? "Windows Sandbox (.wsb) ativada para browser do Agent Hive." : "Windows Sandbox (.wsb) desativada.", "success");
      rerenderSelf();
    } catch (err: any) {
      toastError("salvar isolated_sandbox", err);
      rerenderSelf();
    }
  });

  const binInput = document.getElementById("ide-hive-policy-binaries") as HTMLInputElement | null;
  binInput?.addEventListener("input", () => {
    residentState.binariesDraft = binInput.value;
    residentState.policyDirty = true;
  });
  const hostInput = document.getElementById("ide-hive-policy-hosts") as HTMLInputElement | null;
  hostInput?.addEventListener("input", () => {
    residentState.hostsDraft = hostInput.value;
    residentState.policyDirty = true;
  });
  document.getElementById("ide-hive-save-policy")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend?.ResidentSavePolicy) return;
    const bins = splitPolicyList(binInput?.value ?? residentState.binariesDraft);
    const hosts = splitPolicyList(hostInput?.value ?? residentState.hostsDraft);
    try {
      const st = await backend.ResidentSavePolicy(bins, hosts);
      residentState.status = st;
      residentState.binariesDraft = Array.isArray(st.allowedBinaries) ? st.allowedBinaries.join(", ") : "";
      residentState.hostsDraft = Array.isArray(st.allowedHosts) ? st.allowedHosts.join(", ") : "";
      residentState.policyDirty = false;
      toast("Política local de execução e rede salva.", "success");
      rerenderSelf();
    } catch (err: any) {
      toastError("salvar política local", err);
    }
  });

  const secNameInput = document.getElementById("ide-hive-secret-name") as HTMLInputElement | null;
  secNameInput?.addEventListener("input", () => {
    residentState.secretNameDraft = secNameInput.value;
  });
  const secValInput = document.getElementById("ide-hive-secret-val") as HTMLInputElement | null;
  secValInput?.addEventListener("input", () => {
    residentState.secretValueDraft = secValInput.value;
  });
  document.getElementById("ide-hive-secret-add")?.addEventListener("click", async () => {
    const backend = api();
    const name = residentState.secretNameDraft.trim();
    const val = residentState.secretValueDraft;
    if (!backend?.ResidentSetSecret || !name || !val) return;
    try {
      residentState.status = await backend.ResidentSetSecret(name, val);
      residentState.secretNameDraft = "";
      residentState.secretValueDraft = "";
      toast(`Segredo '${name}' gravado no cofre do sistema.`, "success");
      rerenderSelf();
    } catch (err: any) {
      toastError("gravar segredo no cofre", err);
    }
  });
  document.querySelectorAll<HTMLElement>("[data-ide-hive-sec-del]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const name = btn.dataset.ideHiveSecDel || "";
      if (!name || !api()?.ResidentDeleteSecret) return;
      try {
        residentState.status = await api()!.ResidentDeleteSecret!(name);
        toast(`Segredo '${name}' removido do cofre.`, "success");
        rerenderSelf();
      } catch (err: any) {
        toastError("remover segredo do cofre", err);
      }
    });
  });

  const soulInput = document.getElementById("ide-hive-soul") as HTMLTextAreaElement | null;
  soulInput?.addEventListener("input", () => {
    residentState.soulDraft = soulInput.value;
    residentState.soulDirty = true;
  });
  document.getElementById("ide-hive-soul-save")?.addEventListener("click", async () => {
    const backend = api();
    if (!backend?.ResidentSaveSoul) return;
    try {
      await backend.ResidentSaveSoul(residentState.soulDraft);
      residentState.soulDirty = false;
      toast("SOUL.md atualizado e injetado no Agent Hive.", "success");
    } catch (err: any) {
      toastError("salvar SOUL.md", err);
    }
  });
}
