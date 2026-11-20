import { describe, expect, it } from "vitest";
import { getModelsForProvider, getProviderList, inferModelProvider } from "./models";

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
});
