import { describe, expect, it } from "vitest";
import type { AgentRuntime, RunMode } from "./types";
import { escapeHtml, exactTokens, formatElapsedDuration, formatTokens, runModeLabel, updateAgentRuntimeStatus } from "./utils";

describe("escapeHtml", () => {
  it("escapa os cinco caracteres perigosos", () => {
    // Construído por código de caractere para imunizar o teste a qualquer
    // transformação de aspas por editores/ferramentas no caminho.
    const q = String.fromCharCode(34); // "
    const sq = String.fromCharCode(39); // '
    const amp = String.fromCharCode(38); // &
    const input = `<img src=${q}x${q} onerror=${sq}a()${sq}>${amp}`;
    expect(escapeHtml(input)).toBe(
      "&lt;img src=&quot;x&quot; onerror=&#39;a()&#39;&gt;&amp;"
    );
  });

  it("não altera texto simples", () => {
    expect(escapeHtml("hivemind-ide")).toBe("hivemind-ide");
  });
});

describe("runModeLabel", () => {
  it("conhece os modos base", () => {
    expect(runModeLabel("auto")).toContain("Triagem");
    expect(runModeLabel("direct")).toContain("Direto");
    expect(runModeLabel("squad")).toContain("Squad");
  });

  it("conhece os modos SDD adicionados recentemente", () => {
    expect(runModeLabel("plan:sdd")).toBe("Planejar · Projeto SDD");
    expect(runModeLabel("plan:sdd-jira")).toBe("SDD · Backlog Jira");
  });

  it("modo desconhecido volta ao rótulo de triagem", () => {
    // O branch default do switch é inacessível pelo tipo: o teste valida o
    // comportamento defensivo em runtime.
    expect(runModeLabel("modo-inexistente" as unknown as RunMode)).toContain("Triagem");
  });
});

describe("formatação de tokens", () => {
  it("abrevia milhões e milhares", () => {
    expect(formatTokens(1234567)).toBe("1.23M");
    expect(formatTokens(2500)).toBe("2.5k");
    expect(formatTokens(500)).toBe("500");
    expect(formatTokens(0)).toBe("0");
    expect(formatTokens(undefined)).toBe("0");
  });

  it("tokens exatos usam separador pt-BR", () => {
    expect(exactTokens(1234567)).toBe("1.234.567");
  });
});

describe("tempo de atividade dos agentes", () => {
  it("formata segundos, minutos e horas sem esconder execuções longas", () => {
    expect(formatElapsedDuration(0)).toBe("0s");
    expect(formatElapsedDuration(42_999)).toBe("42s");
    expect(formatElapsedDuration(4 * 60_000 + 9_000)).toBe("4m 9s");
    expect(formatElapsedDuration(3_723_000)).toBe("1h 2m 3s");
    expect(formatElapsedDuration(-1)).toBe("0s");
  });

  it("preserva o início durante eventos running e reinicia em um novo turno", () => {
    const agent: AgentRuntime = {
      name: "dev",
      role: "developer",
      model: "agy",
      status: "idle",
      inTokens: 0,
      outTokens: 0,
      tokens: 0,
      calls: 0,
    };

    updateAgentRuntimeStatus(agent, "running", "read_file", 1_000);
    expect(agent.runningSince).toBe(1_000);
    updateAgentRuntimeStatus(agent, "running", undefined, 2_000);
    expect(agent.runningSince).toBe(1_000);
    expect(agent.tool).toBe("read_file");

    updateAgentRuntimeStatus(agent, "done", "", 3_000);
    updateAgentRuntimeStatus(agent, "running", undefined, 4_000);
    expect(agent.runningSince).toBe(4_000);
  });
});
