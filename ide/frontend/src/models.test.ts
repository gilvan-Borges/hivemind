import { describe, expect, it } from "vitest";
import { getModelsForProvider, getProviderList, inferModelProvider } from "./models";
import { renderJevProviderHealthPanelHtml, renderProvidersScanTabHtml } from "./modals";
import { computeUnifiedProviderChip } from "./shell";
import { state } from "./state";

describe("seletor de provedor Jev", () => {
  it("inclui a opção Jev automático na lista de provedores", () => {
    const list = getProviderList();
    expect(list.some((p) => p.id === "jev")).toBe(true);
  });

  it("reconhece specs jev e auto sem alterar pins explícitos", () => {
    expect(inferModelProvider("jev:auto")).toBe("jev");
    expect(inferModelProvider("auto")).toBe("jev");
    expect(inferModelProvider("cursor:auto")).toBe("cursor");
    expect(inferModelProvider("anthropic/claude-sonnet-4.5")).toBe("openrouter");
    expect(getModelsForProvider("jev")[0]?.value).toBe("jev:auto");
  });

  it("renderiza painel de saúde dos provedores sem vazar segredos e sinaliza esgotamento", () => {
    const html = renderJevProviderHealthPanelHtml([
      {
        provider: "openrouter",
        state: "open",
        reason: "rate_limit",
        hasCredential: true,
        dailySpentMicroUsd: 500000,
        dailyCapMicroUsd: 2000000,
      },
      {
        provider: "opencode",
        state: "closed",
        hasCredential: true,
      },
    ], false);
    expect(html).toContain("Saúde dos Provedores (Jev)");
    expect(html).toContain("openrouter");
    expect(html).toContain("aberto (pausa)");
    expect(html).toContain("rate_limit");
    expect(html).toContain("$0.50/$2.00");
    expect(html).toContain("ativo");
    expect(html).not.toContain("Todos os provedores configurados estão esgotados");

    const allDownHtml = renderJevProviderHealthPanelHtml([
      { provider: "openrouter", state: "open", reason: "quota", hasCredential: true },
      { provider: "openai", state: "closed", hasCredential: false },
    ], false);
    expect(allDownHtml).toContain("Todos os provedores configurados estão esgotados");
  });

  it("renderiza varredura unificada de todos os provedores e contas sem vazar segredos", () => {
    const html = renderProvidersScanTabHtml({
      scannedAt: "2026-11-20T20:00:00Z",
      totalProviders: 7,
      connectedProviders: 3,
      totalAccounts: 4,
      providers: [
        {
          id: "agy",
          name: "Antigravity (Google)",
          found: true,
          connected: true,
          statusLabel: "Conectado",
          source: "Pool (2 conta(s) Google)",
          detail: "2 conta(s) pronta(s)",
          modelCount: 23,
          modelsSample: ["gemini-3.8-flash-high"],
          accounts: [
            { id: "a1", name: "conta1@gmail.com", source: "pool", active: true, ready: true, maskedHint: "Pasta isolada salva" },
            { id: "a2", name: "conta2@gmail.com", source: "pool", active: false, ready: true, maskedHint: "Pasta isolada salva" },
          ],
        },
        {
          id: "opencode",
          name: "OpenCode Go & Zen",
          found: true,
          connected: true,
          statusLabel: "Conectado",
          source: "Chave global",
          detail: "Go (31) + Zen (73)",
          modelCount: 104,
          accounts: [{ name: "Token global OpenCode", source: "global", active: true, ready: true, maskedHint: "••••QVDh" }],
        },
        {
          id: "openrouter",
          name: "OpenRouter",
          found: false,
          connected: false,
          statusLabel: "Desconectado",
          detail: "Nenhuma chave configurada.",
          modelCount: 0,
          accounts: [],
        },
      ],
    }, false, { agy: true });

    expect(html).toContain("Diagnóstico e Varredura de Provedores");
    expect(html).toContain("3 de 7 provedores conectados");
    expect(html).toContain("conta1@gmail.com");
    expect(html).toContain("conta2@gmail.com");
    expect(html).toContain("OpenCode Go &amp; Zen");
    expect(html).toContain("OpenRouter");
    expect(html).toContain("gemini-3.8-flash-high");
    expect(html).not.toContain("Doctor — Antigravity");
  });

  it("gera chip único 'provedor' na barra de status com contagem dos conectados", () => {
    state.providersScan = {
      scannedAt: "2026-11-20T20:00:00Z",
      totalProviders: 7,
      connectedProviders: 2,
      totalAccounts: 3,
      providers: [
        { id: "agy", name: "Antigravity (Google)", found: true, connected: true, statusLabel: "Conectado", detail: "ok", modelCount: 10, accounts: [] },
        { id: "openrouter", name: "OpenRouter", found: true, connected: true, statusLabel: "Conectado", detail: "ok", modelCount: 5, accounts: [] },
      ],
    };
    state.providerHealth = [];
    const chip = computeUnifiedProviderChip();
    expect(chip.text).toBe("provedor: 2");
    expect(chip.title).toContain("Antigravity (Google)");
    expect(chip.title).toContain("OpenRouter");
    state.providersScan = null;
  });

  it("avisa no chip quando a cota restante chega a 20% e mantém abaixo de 10%", () => {
    state.providersScan = {
      scannedAt: "2026-11-20T20:00:00Z",
      totalProviders: 1,
      connectedProviders: 1,
      totalAccounts: 1,
      providers: [
        { id: "agy", name: "Antigravity (Google)", found: true, connected: true, statusLabel: "Conectado", detail: "ok", modelCount: 1, accounts: [] },
      ],
    };
    state.aiUsagePage.report = null;
    state.providerHealth = [{ provider: "agy", state: "closed", hasCredential: true, quotaRemainingPct: 19 }];
    expect(computeUnifiedProviderChip().text).toContain("aviso");
    state.providerHealth = [{ provider: "agy", state: "closed", hasCredential: true, quotaRemainingPct: 9 }];
    expect(computeUnifiedProviderChip().text).toContain("aviso");
    state.providerHealth = [{ provider: "cursor", state: "closed", hasCredential: true, quotaRemainingPct: -1 }];
    const unmeasured = computeUnifiedProviderChip().text;
    expect(unmeasured).toContain("cota não medida");
    expect(unmeasured).not.toMatch(/\d+%/);
    state.providerHealth = [];
    state.providersScan = null;
  });

  it("exibe contagem exata de contas no Pool Auto do Antigravity (sem +1 fantasma)", () => {
    const prevOverview = state.profilesOverview;
    state.profilesOverview = {
      defaultProvider: "agy",
      profiles: Array.from({ length: 5 }, (_, i) => ({
        id: `agy-${i + 1}`,
        name: `conta${i + 1}@gmail.com`,
        provider: "agy",
        authMode: "vault",
        ready: true,
        isDefault: i === 0,
      })),
    } as any;
    const agyItem = getProviderList().find((p) => p.id === "agy");
    expect(agyItem?.label).toContain("Conta 1 → 5");
    expect(agyItem?.label).not.toContain("1 → 6");
    const models = getModelsForProvider("agy");
    expect(models[0]?.label).toContain("Conta 1 → 5 por modelo");
    expect(models[0]?.label).not.toContain("1 → 6");
    state.profilesOverview = prevOverview;
  });
});
