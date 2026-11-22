import { describe, expect, it } from "vitest";
import {
  autoResizeTextarea,
  groupConversationItems,
  sanitizeAssistantText,
  summarizeActivityGroup,
} from "./chat-timeline";
import type { ChatMessage } from "./types";

function msg(over: Partial<ChatMessage> & { id: string; kind: ChatMessage["kind"] }): ChatMessage {
  return {
    label: "",
    text: "",
    ...over,
  };
}

describe("sanitizeAssistantText", () => {
  it("remove envelope JSON vazio apenas com tool_calls", () => {
    const raw = '{"text":"","tool_calls":[{"arguments":{"path":"."},"id":"tc_list_root","name":"list_dir"}]}';
    expect(sanitizeAssistantText(raw)).toBe("");
  });

  it("extrai o texto interno de um envelope completo", () => {
    const raw = '{"text":"Olá, encontrei 3 arquivos.","tool_calls":[]}';
    expect(sanitizeAssistantText(raw)).toBe("Olá, encontrei 3 arquivos.");
  });

  it("não vaza JSON parcial de tool_calls em chunks incompletos", () => {
    expect(sanitizeAssistantText('{"text":"","tool_calls":[{"id":"1"')).toBe("");
    expect(sanitizeAssistantText('{"text":"Analisando\\nlinha 2')).toBe("Analisando\nlinha 2");
  });

  it("preserva JSON normal escrito como resposta real e espaços em chunks simples", () => {
    expect(sanitizeAssistantText('{"status":"ok","count":3}')).toBe('{"status":"ok","count":3}');
    expect(sanitizeAssistantText("Vou ver o ")).toBe("Vou ver o ");
  });
});

describe("groupConversationItems", () => {
  it("recolhe thinking, Rota Jev, tools e handoff consecutivos num único bloco e filtra chief JSON vazio", () => {
    const messages: ChatMessage[] = [
      msg({ id: "u1", kind: "user", label: "Usuário", text: "Explique o repo" }),
      msg({ id: "t1", kind: "thinking", label: "Pensando", text: "lendo arquivos", durationMs: 9000 }),
      msg({ id: "r1", kind: "system", label: "Rota Jev", text: "Jev escolheu agy/agy" }),
      msg({
        id: "c-leak",
        kind: "chief",
        label: "Arquiteto (Chefe)",
        text: '{"text":"","tool_calls":[{"id":"tc1","name":"list_dir","arguments":{"path":"."}}]}',
      }),
      msg({ id: "tl1", kind: "tool", label: "Chefe", toolName: "list_dir", toolDetail: ".", text: "list_dir" }),
      msg({ id: "tl2", kind: "tool", label: "Chefe", toolName: "read_file", toolDetail: "README.md", text: "read_file" }),
      msg({ id: "ho1", kind: "handoff", label: "Handoff da Equipe", text: "Devs acionados" }),
      msg({ id: "c1", kind: "chief", label: "Arquiteto (Chefe)", text: "Aqui está o resumo." }),
    ];

    const items = groupConversationItems(messages, false);
    expect(items).toHaveLength(3);
    expect(items[0]).toMatchObject({ kind: "message", id: "u1" });
    expect(items[1]).toMatchObject({ kind: "activity", active: false });
    if (items[1].kind === "activity") {
      expect(items[1].messages).toHaveLength(5);
    }
    expect(items[2]).toMatchObject({ kind: "message", id: "c1" });
  });

  it("mantém aprovações e erros fora do bloco recolhível e marca ativo quando ainda rodando no final", () => {
    const messages: ChatMessage[] = [
      msg({ id: "u1", kind: "user", label: "Você", text: "Editar" }),
      msg({ id: "tl1", kind: "tool", label: "Agent Hive", toolName: "read_file", toolDetail: "go.mod", text: "read_file", active: false }),
      msg({ id: "ap1", kind: "approval", label: "Aprovação", text: "Autorizar escrita?" }),
      msg({ id: "tl2", kind: "tool", label: "Agent Hive", toolName: "write_file", toolDetail: "a.go", text: "write_file", active: true }),
    ];

    const items = groupConversationItems(messages, true);
    expect(items).toHaveLength(4);
    expect(items[1]).toMatchObject({ kind: "activity", active: false });
    expect(items[2]).toMatchObject({ kind: "message", id: "ap1" });
    expect(items[3]).toMatchObject({ kind: "activity", active: true });
  });
});

describe("summarizeActivityGroup", () => {
  it("gera títulos concisos para estado ativo e concluído", () => {
    const activeMsgs: ChatMessage[] = [
      msg({ id: "t1", kind: "thinking", label: "Pensando", active: true, startedAt: 1_000 }),
      msg({ id: "tl1", kind: "tool", label: "Chefe", toolName: "read_file", toolDetail: "README.md", text: "read_file", active: true }),
      msg({ id: "tl2", kind: "tool", label: "Chefe", toolName: "list_dir", toolDetail: "ide", text: "list_dir", active: false }),
    ];
    const activeSum = summarizeActivityGroup(activeMsgs, true, 6_000, () => "Analisando README.md");
    expect(activeSum.title).toBe("Analisando README.md · 2 passos");
    expect(activeSum.elapsedText).toBe("5s");

    const doneMsgs: ChatMessage[] = [
      msg({ id: "t1", kind: "thinking", label: "Pensando", durationMs: 9_000 }),
      msg({ id: "tl1", kind: "tool", label: "Chefe", toolName: "read_file", text: "read_file" }),
    ];
    const doneSum = summarizeActivityGroup(doneMsgs, false, 10_000);
    expect(doneSum.title).toBe("Pensou por 9s · 1 passo");
    expect(doneSum.elapsedText).toBe("");
  });
});

describe("autoResizeTextarea", () => {
  it("ajusta a altura entre minPx e maxPx conforme scrollHeight", () => {
    const fakeEl = {
      scrollHeight: 68,
      style: { height: "", overflowY: "" },
    } as unknown as HTMLTextAreaElement;

    autoResizeTextarea(fakeEl, 22, 180);
    expect(fakeEl.style.height).toBe("68px");
    expect(fakeEl.style.overflowY).toBe("hidden");

    Object.defineProperty(fakeEl, "scrollHeight", { value: 320, configurable: true });
    autoResizeTextarea(fakeEl, 22, 180);
    expect(fakeEl.style.height).toBe("180px");
    expect(fakeEl.style.overflowY).toBe("auto");
  });
});
