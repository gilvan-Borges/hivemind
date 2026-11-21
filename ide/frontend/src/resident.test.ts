import { describe, expect, it } from "vitest";
import {
  applyResidentLaneEvent,
  formatRiskBadge,
  renderResidentViewHtml,
  residentState,
  type ResidentEventTarget,
} from "./resident";
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

function freshTarget(): ResidentEventTarget {
  return {
    running: false,
    status: { state: "ocioso" },
    messages: [],
  };
}

describe("badge do Agent Hive", () => {
  it("mostra sandbox obrigatória quando o teste sem isolamento está desligado", () => {
    residentState.status = status(false);
    residentState.messages = [];
    residentState.subTab = "chat";
    const html = renderResidentViewHtml();
    expect(html).toContain('<span class="resident-badge readonly">Sandbox obrigatória</span>');
    expect(html).not.toContain("Sem isolamento (teste)");
    expect(html).not.toContain("Fase ");
    expect(html).toContain("Agent Hive pronto");
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

  it("identidade unificada: cabeçalho e título da aba chamam Agent Hive", () => {
    residentState.status = status(false);
    residentState.messages = [];
    residentState.subTab = "chat";
    const html = renderResidentViewHtml();
    expect(html).toContain("<h2>Agent Hive</h2>");
    expect(html).not.toContain("<h2>Hive Residente</h2>");
  });
});

describe("formatRiskBadge", () => {
  it("traduz tiers para rótulos legíveis", () => {
    expect(formatRiskBadge("write").label).toBe("Risco Médio · Escrita no Workspace");
    expect(formatRiskBadge("exec").css).toBe("risk-exec");
    expect(formatRiskBadge("destructive").label).toBe("Risco Crítico · Ação Destrutiva");
  });
});

describe("applyResidentLaneEvent (streaming da conversa — Spec 007 H003)", () => {
  it("mensagem do Orb chega como 'Você'; de outras origens como 'Remoto'", () => {
    const orb = freshTarget();
    applyResidentLaneEvent(orb, { event: { kind: "user_message", text: "status do git", origin: "orb" } });
    const relay = freshTarget();
    applyResidentLaneEvent(relay, { event: { kind: "user_message", text: "via celular" } });
    expect(orb.messages[0]).toMatchObject({ kind: "user", label: "Você", text: "status do git" });
    expect(relay.messages[0].label).toBe("Remoto");
  });

  it("chunks parciais de texto se acumulam num único balão chief", () => {
    const target = freshTarget();
    applyResidentLaneEvent(target, { event: { kind: "text", text: "Vou ver o ", partial: true, origin: "orb" } });
    applyResidentLaneEvent(target, { event: { kind: "text", text: "git log.", partial: true, origin: "orb" } });
    expect(target.messages).toHaveLength(1);
    expect(target.messages[0]).toMatchObject({ kind: "chief", text: "Vou ver o git log." });
  });

  it("tool_call cria cartão ativo e tool_result o encerra", () => {
    const target = freshTarget();
    applyResidentLaneEvent(target, { event: { kind: "tool_call", toolName: "run_shell", detail: "go test ./...", gate: "exec" } });
    expect(target.messages[0]).toMatchObject({ kind: "tool", active: true, riskTier: "exec" });
    applyResidentLaneEvent(target, { event: { kind: "tool_result" } });
    expect(target.messages[0].active).toBe(false);
  });

  it("approval_required muda o estado para aguardando você e registra a aprovação", () => {
    const target = freshTarget();
    applyResidentLaneEvent(target, {
      event: { kind: "approval_required", text: "Posso gravar?", approvalId: "resident:appr:1", gate: "write", toolName: "write_file" },
    });
    expect(target.status?.state).toBe("aguardando você");
    expect(target.messages[0]).toMatchObject({ kind: "approval", approvalId: "resident:appr:1", riskTier: "write" });
  });

  it("approval_resolved marca approved/denied pelo texto do evento", () => {
    const target = freshTarget();
    applyResidentLaneEvent(target, { event: { kind: "approval_required", text: "x", approvalId: "resident:appr:2" } });
    applyResidentLaneEvent(target, { event: { kind: "approval_resolved", approvalId: "resident:appr:2", text: "Aprovação recusada." } });
    expect(target.messages[0].resolved).toBe("denied");
    applyResidentLaneEvent(target, { event: { kind: "approval_required", text: "y", approvalId: "resident:appr:3" } });
    applyResidentLaneEvent(target, { event: { kind: "approval_resolved", approvalId: "resident:appr:3", text: "Aprovação concedida." } });
    expect(target.messages[1].resolved).toBe("approved");
  });

  it("error e finish normalizam running e estado", () => {
    const target = freshTarget();
    applyResidentLaneEvent(target, { event: { kind: "stage", text: "pensando" } });
    expect(target.running).toBe(true);
    applyResidentLaneEvent(target, { event: { kind: "error", text: "falhou o modelo" } });
    expect(target.running).toBe(false);
    expect(target.messages.at(-1)).toMatchObject({ kind: "error", text: "falhou o modelo" });
    applyResidentLaneEvent(target, { event: { kind: "stage" } });
    applyResidentLaneEvent(target, { event: { kind: "finish" } });
    expect(target.running).toBe(false);
    expect(target.status?.state).toBe("ocioso");
  });

  it("ignora payloads sem evento", () => {
    const target = freshTarget();
    applyResidentLaneEvent(target, {});
    applyResidentLaneEvent(target, undefined);
    expect(target.messages).toHaveLength(0);
  });
});
