import { describe, expect, it } from "vitest";
import { getModelsForProvider, getProviderList, inferModelProvider } from "./models";
import { renderJevProviderHealthPanelHtml } from "./modals";

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
});
