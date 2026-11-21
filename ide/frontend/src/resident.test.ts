import { describe, expect, it } from "vitest";
import { renderResidentViewHtml, residentState } from "./resident";
import type { ResidentStatus } from "./types";

function status(experimentalUnisolated: boolean): ResidentStatus {
  return {
    state: "ocioso",
    readOnly: false,
    accountId: "local",
    workstream: "geral",
    provider: "openrouter",
    model: "auto",
    sandboxAvailable: true,
    sandboxMechanism: "bubblewrap",
    experimentalUnisolated,
  };
}

describe("badge do Hive Residente", () => {
  it("mostra sandbox obrigatória quando o teste sem isolamento está desligado", () => {
    residentState.status = status(false);
    residentState.messages = [];
    residentState.subTab = "chat";
    const html = renderResidentViewHtml();
    expect(html).toContain('<span class="resident-badge readonly">Sandbox obrigatória</span>');
    expect(html).not.toContain("Sem isolamento (teste)");
    expect(html).not.toContain("Fase ");
    expect(html).toContain("Hive Residente pronto");
  });

  it("mostra sem isolamento quando experimentalUnisolated está ligado", () => {
    residentState.status = status(true);
    residentState.messages = [];
    residentState.subTab = "chat";
    const html = renderResidentViewHtml();
    expect(html).toContain('<span class="resident-badge risk-destructive">Sem isolamento (teste)</span>');
    expect(html).not.toContain("Sandbox obrigatória");
    expect(html).not.toContain("Fase ");
  });
});
