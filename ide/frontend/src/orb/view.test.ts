import { describe, expect, it } from "vitest";
import {
  orbChatDomStrategy,
  orbPendingApprovals,
  orbResidentMessages,
  orbRiskBadge,
  orbStatusLine,
  renderOrbApprovalDockHtml,
  renderOrbChatListHtml,
  renderOrbComposerHtml,
  renderOrbMsgHtml,
  type OrbResidentMessage,
  type OrbResidentState,
} from "./view";

const resident = (over: Partial<OrbResidentState> = {}): OrbResidentState => ({
  state: "ocioso",
  workstream: "geral",
  workspace: "C:\\hivemind",
  provider: "openrouter",
  model: "auto",
  sandboxAvailable: true,
  sandboxMechanism: "appcontainer",
  running: false,
  messages: [],
  ...over,
});

describe("orbRiskBadge", () => {
  it("mapeia os tiers de risco do Agent Hive", () => {
    expect(orbRiskBadge("write").label).toBe("Risco Médio · Escrita");
    expect(orbRiskBadge("exec").label).toBe("Risco Alto · Execução em Sandbox");
    expect(orbRiskBadge("read").css).toBe("risk-read");
    expect(orbRiskBadge("").label).toBe("Aprovação Necessária");
  });
});

describe("orbStatusLine (sublinha do cabeçalho)", () => {
  it("mostra estado · rota · workstream", () => {
    expect(orbStatusLine(resident())).toBe("ocioso · openrouter/auto · geral");
  });
  it("usa defaults quando o snapshot ainda está vazio", () => {
    expect(orbStatusLine(undefined)).toBe("ocioso · auto · geral");
  });
});

describe("renderOrbChatListHtml (estados obrigatórios D5)", () => {
  it("estado carregando exibe skeleton estável", () => {
    const html = renderOrbChatListHtml(resident(), true);
    expect(html).toContain("orb-chat-skeleton");
    expect(html).not.toContain("orb-suggestion");
  });

  it("estado vazio mostra Agent Hive pronto com sugestões clicáveis", () => {
    const html = renderOrbChatListHtml(resident(), false);
    expect(html).toContain("Agent Hive pronto");
    expect(html).toContain('data-orb-suggest="Resumo do workspace"');
    expect(html).toContain('data-orb-suggest="Git status e branch"');
    expect(html).toContain('data-orb-suggest="Revisar alterações"');
    expect(html).not.toContain("emoji");
  });

  it("estado dados renderiza timeline com markdown e aprovação", () => {
    const msgs: OrbResidentMessage[] = [
      { id: "1", kind: "user", text: "lista os commits" },
      { id: "2", kind: "chief", text: "Vou ver o **git log**." },
      { id: "3", kind: "tool", text: "run_shell", toolName: "run_shell", toolDetail: "git log", riskTier: "read", active: false },
      { id: "4", kind: "approval", text: "Posso gravar?", approvalId: "resident:appr:9", riskTier: "write" },
    ];
    const html = renderOrbChatListHtml(resident({ messages: msgs }), false);
    expect(html).toContain('data-msg-id="1"');
    expect(html).toContain("<strong>git log</strong>");
    expect(html).toContain("Risco Baixo · Leitura");
    expect(html).toContain('data-msg-id="4"');
  });
});

describe("renderOrbMsgHtml", () => {
  it("aprovação pendente não mostra decisão; resolvida mostra selo", () => {
    const pending = renderOrbMsgHtml({ id: "a", kind: "approval", text: "x", approvalId: "id-1", riskTier: "exec" });
    expect(pending).toContain("Risco Alto · Execução em Sandbox");
    expect(pending).not.toContain("Aprovado");
    const done = renderOrbMsgHtml({ id: "b", kind: "approval", text: "x", approvalId: "id-2", resolved: "approved" });
    expect(done).toContain("Aprovado");
    expect(done).toContain("resolved");
  });

  it("erro é legível e oferece tentar novamente", () => {
    const html = renderOrbMsgHtml({ id: "e", kind: "error", text: "Falha de conexão com o processo principal" });
    expect(html).toContain("Falha de conexão");
    expect(html).toContain("Tentar novamente");
  });
});

describe("dock de aprovação fixo acima do composer (Spec 007)", () => {
  it("vazio quando não há pendentes", () => {
    expect(renderOrbApprovalDockHtml(resident())).toBe("");
    expect(renderOrbApprovalDockHtml(resident({ messages: [{ id: "x", kind: "approval", text: "ok", resolved: "approved" }] }))).toBe("");
  });

  it("pendentes ancoram Aprovar/Recusar com nível de risco", () => {
    const st = resident({
      messages: [{ id: "p1", kind: "approval", text: "gravar relatório", approvalId: "resident:appr:9", riskTier: "write", toolName: "write_file", toolDetail: "REL.md" }],
    });
    expect(orbPendingApprovals(st)).toHaveLength(1);
    const html = renderOrbApprovalDockHtml(st);
    expect(html).toContain('data-orb-approve="resident:appr:9"');
    expect(html).toContain('data-orb-deny="resident:appr:9"');
    expect(html).toContain("Risco Médio · Escrita");
    expect(html).toContain("write_file · REL.md");
  });
});

describe("renderOrbComposerHtml (preservação do rascunho H007)", () => {
  it("mantém o texto digitado dentro do textarea", () => {
    const html = renderOrbComposerHtml(resident(), "roda os testes e revisa", false);
    expect(html).toContain('id="orb-chat-input"');
    expect(html).toContain("roda os testes e revisa");
  });

  it("running troca Enviar por Parar", () => {
    const running = renderOrbComposerHtml(resident({ running: true, state: "pensando" }), "", false);
    expect(running).toContain('id="orb-cancel"');
    expect(running).toContain("Agent Hive pensando…");
    expect(running).not.toContain('id="orb-send"');
  });

  it("loading desabilita o composer com placeholder de conexão", () => {
    const loading = renderOrbComposerHtml(resident(), "", true);
    expect(loading).toContain("Conectando ao Agent Hive...");
    expect(loading).toContain(" disabled");
  });
});

describe("orbChatDomStrategy (estratégia de update sem destruir o input)", () => {
  it("preserva o DOM quando há rascunho ou foco no composer", () => {
    expect(orbChatDomStrategy("texto sendo digitado", false, "chat")).toBe("patch");
    expect(orbChatDomStrategy("", true, "chat")).toBe("patch");
    expect(orbChatDomStrategy("   ", true, "chat")).toBe("patch");
  });

  it("render completo quando a conversa está limpa e sem foco", () => {
    expect(orbChatDomStrategy("", false, "chat")).toBe("full");
  });

  it("outras abas sempre renderizam completo (não há input de rascunho)", () => {
    expect(orbChatDomStrategy("rascunho", true, "avisos")).toBe("full");
    expect(orbChatDomStrategy("rascunho", true, "agente")).toBe("full");
  });
});

describe("orbResidentMessages (normalização do snapshot Go)", () => {
  it("trata messages null/undefined como lista vazia", () => {
    expect(orbResidentMessages(undefined)).toEqual([]);
    expect(orbResidentMessages({ messages: null })).toEqual([]);
    expect(orbResidentMessages(resident({ messages: [{ id: "z", kind: "user", text: "oi" }] }))).toHaveLength(1);
  });
});
