import { describe, expect, it } from "vitest";
import type { AgentRuntime, RunMode } from "./types";
import { chipForAgent, cursorToolPhrase, describeModelRoute, escapeHtml, exactTokens, formatElapsedDuration, formatMicroUsd, formatTokens, reasonPhrase, runModeLabel, shouldExpandChat, splitAssistantBlocks, updateAgentRuntimeStatus } from "./utils";

describe("reasonPhrase", () => {
  it("traduz os códigos de motivo do Jev", () => {
    expect(reasonPhrase(["cheap_default", "context_fit"])).toBe("o mais barato que cabe · cabe na janela");
    expect(reasonPhrase(["pin"])).toBe("modelo fixado por você");
    expect(reasonPhrase(["circuit_open"])).toBe("o anterior estava em pausa");
    expect(reasonPhrase(["qa_escalation"])).toBe("o QA pediu outra tentativa");
    expect(reasonPhrase(["vision"])).toBe("a tarefa tem imagem");
    expect(reasonPhrase(["spread"])).toBe("espalhado entre provedores");
    expect(reasonPhrase(["budget_wait"])).toBe("acima do teto, aguardando você");
    expect(reasonPhrase(["promo_-20%"])).toBe("promoção -20%");
    expect(reasonPhrase(null)).toBe("");
  });
});

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

describe("formatMicroUsd", () => {
  it("formata custos em microUSD com precisão adequada", () => {
    expect(formatMicroUsd(0)).toBe("$0.00");
    expect(formatMicroUsd(-5)).toBe("$0.00");
    expect(formatMicroUsd(500)).toBe("$0.0005");
    expect(formatMicroUsd(12345)).toBe("$0.0123");
    expect(formatMicroUsd(1_250_000)).toBe("$1.25");
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
    expect(agent.runningSince).toBeUndefined();
    expect(agent.tool).toBe("");
    updateAgentRuntimeStatus(agent, "running", undefined, 4_000);
    expect(agent.runningSince).toBe(4_000);
  });

  it("encerra o timer do chefe quando o dev entra em execução e para todos os agentes em erro/fim", async () => {
    const { completePreviousStageAgents, stopRunningAgentRuntimes, transitionAgentToRunning } = await import("./utils");
    const chefe: AgentRuntime = {
      name: "chefe",
      role: "architect",
      model: "agy",
      status: "idle",
      inTokens: 0,
      outTokens: 0,
      tokens: 0,
      calls: 0,
    };
    const dev: AgentRuntime = {
      name: "dev",
      role: "developer",
      model: "agy",
      status: "idle",
      inTokens: 0,
      outTokens: 0,
      tokens: 0,
      calls: 0,
    };
    const qa: AgentRuntime = {
      name: "qa",
      role: "qa",
      model: "agy",
      status: "idle",
      inTokens: 0,
      outTokens: 0,
      tokens: 0,
      calls: 0,
    };
    const agents = [chefe, dev, qa];

    transitionAgentToRunning(agents, chefe, "read_file", 1_000);
    expect(chefe.status).toBe("running");
    expect(chefe.runningSince).toBe(1_000);

    // Handoff para o Dev: o Chefe para imediatamente de contar tempo e passa para "done"
    transitionAgentToRunning(agents, dev, "search_text", 5_000);
    expect(chefe.status).toBe("done");
    expect(chefe.runningSince).toBeUndefined();
    expect(chefe.tool).toBe("");
    expect(dev.status).toBe("running");
    expect(dev.runningSince).toBe(5_000);

    // Transição de stage para QA: o Dev encerra e o QA assume
    completePreviousStageAgents(agents, "qa", 9_000);
    expect(dev.status).toBe("done");
    expect(dev.runningSince).toBeUndefined();

    // Se o Dev voltar a rodar e ocorrer erro na execução, todos param de contar tempo
    transitionAgentToRunning(agents, dev, "read_file", 10_000);
    stopRunningAgentRuntimes(agents, "error", "dev", "Desenvolvedor", 12_000);
    expect(dev.status).toBe("error");
    expect(dev.runningSince).toBeUndefined();
    expect(dev.tool).toBe("");
    expect(chefe.status).toBe("done");
    expect(qa.status).toBe("idle");
  });
});

describe("describeModelRoute", () => {
  it("traduz a rota real do Cursor e o pin", () => {
    const fromRoute = describeModelRoute("cursor", "cursor:auto");
    expect(fromRoute.label).toBe("Cursor Auto");
    expect(fromRoute.providerLabel).toBe("Cursor");
    expect(fromRoute.fullId).toBe("cursor/cursor:auto");

    const fromSlash = describeModelRoute("", "cursor/cursor:auto");
    expect(fromSlash.label).toBe("Cursor Auto");
    expect(fromSlash.fullId).toBe("cursor/cursor:auto");
  });

  it("humaniza GPT e Grok", () => {
    expect(describeModelRoute("openai", "gpt-5.2")).toMatchObject({
      label: "GPT-5.2",
      providerLabel: "OpenAI",
      fullId: "openai/gpt-5.2",
    });
    expect(describeModelRoute("", "grok-4.7").label).toBe("Grok 4.7");
    expect(describeModelRoute("anthropic", "claude-sonnet-4-5").label).toBe("Claude Sonnet 4.5");
  });

  it("prefere a rota já carimbada no agente", () => {
    const chip = chipForAgent({
      model: "cursor:auto",
      routeModel: "openai/gpt-5.2",
      routeProvider: "openai",
      routeFriendly: "GPT-5.2",
    });
    expect(chip.label).toBe("GPT-5.2");
    expect(chip.fullId).toBe("openai/gpt-5.2");
  });
});

describe("splitAssistantBlocks", () => {
  it("preserva espaços e agrupa ferramentas sem campos internos", () => {
    const raw = "Vou ler o arquivo.\n[[hmtool:read:.hivemind/cursor-task.md]]\nAgora busco.\n[[hmtool:grep:ide/frontend]]\n[[hmtool:getMcpTools:]]\n[[hmtool:startedAtMs:]]\n";
    const blocks = splitAssistantBlocks(raw);
    expect(blocks.map((b) => b.kind)).toEqual(["md", "tools", "md", "tools"]);
    expect(blocks[0]).toMatchObject({ kind: "md", text: "Vou ler o arquivo.\n" });
    expect(blocks[1]).toMatchObject({ kind: "tools", tools: [{ name: "read", target: ".hivemind/cursor-task.md" }] });
    const tools = blocks[3];
    if (tools.kind !== "tools") throw new Error("grupo");
    expect(tools.tools.map((t) => t.name)).toEqual(["grep", "getMcpTools"]);
    expect(cursorToolPhrase("read", ".hivemind/cursor-task.md")).toEqual({ verb: "Leu", target: "cursor-task.md" });
    expect(cursorToolPhrase("grep", "ide/frontend")).toEqual({ verb: "Buscou em", target: "ide/frontend" });
    expect(splitAssistantBlocks("Vou  ler")[0]).toMatchObject({ text: "Vou  ler" });
  });
});

describe("shouldExpandChat", () => {
  it("só expande com a preferência ligada e o centro vazio", () => {
    expect(shouldExpandChat(true, 0)).toBe(true);
    expect(shouldExpandChat(true, 1)).toBe(false);
    expect(shouldExpandChat(false, 0)).toBe(false);
  });
});

describe("runModeLabel", () => {
  it("inclui modo race", () => {
    expect(runModeLabel("race")).toBe("Race (Candidatos)");
  });
});
