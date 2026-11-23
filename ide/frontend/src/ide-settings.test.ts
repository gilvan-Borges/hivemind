import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { renderIdeSettingsPageHtml } from "./ide-settings";
import { residentState } from "./resident";
import { IDE_SETTINGS_TAB_KEY, state } from "./state";
import type { ResidentStatus } from "./types";

function makeResidentStatus(overrides: Partial<ResidentStatus> = {}): ResidentStatus {
  return {
    state: "ocioso",
    enabled: true,
    orbEnabled: true,
    jevFailover: false,
    readOnly: false,
    accountId: "local",
    workstream: "geral",
    provider: "openrouter",
    model: "openrouter/auto",
    sandboxAvailable: true,
    sandboxMechanism: "bubblewrap",
    vaultAvailable: true,
    ...overrides,
  };
}

describe("Configurações Globais da IDE (ide-settings)", () => {
  const prevSection = state.ideSettingsSection;
  const prevActive = state.activeEditorPath;
  const prevProject = state.project;
  const prevResident = residentState.status;
  const prevOrbRunning = state.orbRunning;

  beforeEach(() => {
    state.ideSettingsSection = "providers";
    state.activeEditorPath = IDE_SETTINGS_TAB_KEY;
    state.apiKeysTab = "profiles";
    state.orbRunning = true;
    residentState.status = makeResidentStatus();
  });

  afterEach(() => {
    state.ideSettingsSection = prevSection;
    state.activeEditorPath = prevActive;
    state.project = prevProject;
    residentState.status = prevResident;
    state.orbRunning = prevOrbRunning;
  });

  it("renderiza todas as 5 seções globais na navegação lateral", () => {
    const html = renderIdeSettingsPageHtml();
    expect(html).toContain('data-ide-settings-sec="providers"');
    expect(html).toContain('data-ide-settings-sec="agent-hive"');
    expect(html).toContain('data-ide-settings-sec="limits"');
    expect(html).toContain('data-ide-settings-sec="integrations"');
    expect(html).toContain('data-ide-settings-sec="general"');

    // Seção providers
    expect(html).toContain("Provedores &amp; Contas LLM");
    expect(html).toContain("Pool de Contas");

    // Seção agent-hive
    state.ideSettingsSection = "agent-hive";
    const hiveHtml = renderIdeSettingsPageHtml();
    expect(hiveHtml).toContain("Ativação &amp; Presença do Agent Hive");
    expect(hiveHtml).toContain('id="ide-hive-enabled"');
    expect(hiveHtml).toContain('id="ide-hive-orb-enabled"');
    expect(hiveHtml).toContain('id="ide-hive-full-access"');
    expect(hiveHtml).toContain('id="ide-hive-full-unattended"');
    expect(hiveHtml).toContain("Full em loop desacompanhado");
    expect(hiveHtml).not.toContain("Permissão Full ligada");

    // Seção limits
    state.ideSettingsSection = "limits";
    const limitsHtml = renderIdeSettingsPageHtml();
    expect(limitsHtml).toContain("Limites, Cotas &amp; Política do Jev");
    expect(limitsHtml).toContain("Política do Jev");

    // Seção integrations
    state.ideSettingsSection = "integrations";
    const intHtml = renderIdeSettingsPageHtml();
    expect(intHtml).toContain("GitHub Nativo");
    expect(intHtml).toContain("Jira Cloud (Multi-Organização)");
    expect(intHtml).toContain("App Mobile &amp; Relay Remoto");

    // Seção general
    state.ideSettingsSection = "general";
    const genHtml = renderIdeSettingsPageHtml();
    expect(genHtml).toContain("Editor &amp; Interface");
    expect(genHtml).toContain("Atualizações &amp; Aplicativo");
  });

  it("reflete estado do Agent Hive e do Orb flutuante (ativo vs desativado)", () => {
    state.ideSettingsSection = "agent-hive";
    residentState.status = makeResidentStatus({ enabled: true, orbEnabled: true });
    state.orbRunning = true;
    const onHtml = renderIdeSettingsPageHtml();
    expect(onHtml).toContain('id="ide-hive-enabled" checked');
    expect(onHtml).toContain('id="ide-hive-orb-enabled" checked');
    expect(onHtml).toContain("Ativado");
    expect(onHtml).toContain("Orb visível");

    residentState.status = makeResidentStatus({ enabled: false, orbEnabled: false, state: "desativado" });
    state.orbRunning = false;
    const offHtml = renderIdeSettingsPageHtml();
    expect(offHtml).not.toContain('id="ide-hive-enabled" checked');
    expect(offHtml).toContain('id="ide-hive-orb-enabled"  disabled');
    expect(offHtml).toContain("Desativado");
    expect(offHtml).toContain("Orb oculto");
  });

  it("separa configurações globais da IDE das configurações de equipe do projeto", () => {
    state.project = {
      root: "/tmp/proj-alpha",
      name: "proj-alpha",
      branch: "main",
      stack: { root: "/tmp/proj-alpha", name: "proj-alpha", stacks: [], buildTools: [], testHint: "", summary: "" },
      hasTeam: true,
      teamConfig: {
        version: 1,
        active: "principal",
        settings: { jira_enabled: false, auto_commit: false, auto_pr: false, base_branch: "main" },
        teams: [],
      },
      files: [],
    };
    const html = renderIdeSettingsPageHtml();
    expect(html).toContain("Escopo Global · Este PC");
    expect(html).toContain('id="ide-set-open-team"');
    expect(html).not.toContain('id="tc-base-branch"');
    expect(html).not.toContain('id="tc-homolog-branch"');
    expect(html).not.toContain('id="tc-model-chief"');
  });

  it("botão de engrenagem na Activity Bar abre Configurações da IDE (#act-ide-settings) e não Team Config", async () => {
    const mod = "node:fs/promises";
    const { readFile } = (await import(/* @vite-ignore */ mod)) as {
      readFile: (path: URL, encoding: string) => Promise<string>;
    };
    const shellSrc = await readFile(new URL("./shell.ts", import.meta.url), "utf8");
    expect(shellSrc).toContain('id="act-ide-settings"');
    expect(shellSrc).toContain("openIdeSettingsTab");
    expect(shellSrc).not.toContain('id="act-wizard"');
  });

  it("alinha todos os itens do menu lateral à esquerda sem herdar justify-content: center", async () => {
    const html = renderIdeSettingsPageHtml();
    expect((html.match(/class="ide-settings-nav-ico"/g) || []).length).toBe(5);
    expect((html.match(/class="ide-settings-nav-label"/g) || []).length).toBe(5);

    const mod = "node:fs/promises";
    const { readFile } = (await import(/* @vite-ignore */ mod)) as {
      readFile: (path: URL, encoding: string) => Promise<string>;
    };
    const css = await readFile(new URL("./styles.css", import.meta.url), "utf8");
    const navBtnBlock = css.match(/\.ide-settings-nav-btn\s*\{[^}]*\}/);
    expect(navBtnBlock?.[0]).toContain("justify-content: flex-start;");
  });
});
