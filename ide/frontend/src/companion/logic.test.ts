import { describe, expect, it } from "vitest";
import {
  channelFor,
  clampPosition,
  confirmTitle,
  countTurnFiles,
  dropNotice,
  latestNotice,
  moodFromSignal,
  noticeFromGitHubEvents,
  noticeFromSignal,
  pushNotice,
  turnTitle,
} from "./logic";
import { normalizeSettings } from "./settings";

describe("channelFor", () => {
  const base = { companion: true, osNotifications: true, doNotDisturb: false };

  it("em foco mostra só o balão", () => {
    expect(channelFor({ ...base, focused: true })).toBe("bubble");
  });

  it("fora de foco manda o aviso do sistema e não o balão", () => {
    expect(channelFor({ ...base, focused: false })).toBe("system");
  });

  it("não perturbe cala os dois canais", () => {
    expect(channelFor({ ...base, focused: true, doNotDisturb: true })).toBe("silent");
    expect(channelFor({ ...base, focused: false, doNotDisturb: true })).toBe("silent");
  });

  it("sem companion e em foco não duplica no sistema", () => {
    expect(channelFor({ ...base, companion: false, focused: true })).toBe("silent");
  });

  it("sem aviso do sistema e fora de foco guarda o balão", () => {
    expect(channelFor({ ...base, osNotifications: false, focused: false })).toBe("bubble");
  });
});

describe("noticeFromSignal", () => {
  it("plano pronto", () => {
    expect(noticeFromSignal({ laneId: "l1", kind: "plan_ready" })?.title).toBe("Plano pronto para aprovar");
  });

  it("gate jira", () => {
    expect(noticeFromSignal({ laneId: "l1", kind: "sdd_gate", gate: "jira" })?.title).toBe("Backlog pronto para aprovar");
  });

  it("dev com arquivos", () => {
    const n = noticeFromSignal({ laneId: "l1", kind: "finish", stage: "dev", fileCount: 3 });
    expect(n?.title).toBe("Dev terminou — 3 arquivos");
    expect(n?.mood).toBe("success");
  });

  it("um arquivo no singular", () => {
    expect(turnTitle("dev", 1)).toBe("Dev terminou — 1 arquivo");
  });

  it("erro no QA", () => {
    const n = noticeFromSignal({ laneId: "l1", kind: "error", stage: "qa", text: "teste falhou" });
    expect(n?.title).toBe("Erro no QA");
    expect(n?.mood).toBe("error");
  });

  it("confirma só shell, navegador e desktop", () => {
    expect(confirmTitle("run_shell")).toBe("Confirmar shell");
    expect(noticeFromSignal({ laneId: "l1", kind: "approval_required", toolName: "browser_open" })?.title).toBe("Confirmar navegador");
    expect(noticeFromSignal({ laneId: "l1", kind: "approval_required", toolName: "desktop_type" })?.title).toBe("Confirmar desktop");
    expect(noticeFromSignal({ laneId: "l1", kind: "approval_required", toolName: "write_file", gate: "budget" })).toBeNull();
  });

  it("rotina do residente e não o turno comum", () => {
    expect(noticeFromSignal({ laneId: "resident", kind: "finish", origin: "routine" })?.title).toBe("Rotina concluída");
    expect(noticeFromSignal({ laneId: "resident", kind: "finish", origin: "local", text: "pronto" })).toBeNull();
  });

  it("não repete o fim de turno quando o plano já avisou", () => {
    expect(noticeFromSignal({
      laneId: "l1",
      kind: "finish",
      text: "Plano pronto na sessão s1 — revise e aprove para iniciar o Squad.",
    })).toBeNull();
  });
});

describe("moodFromSignal", () => {
  it("pensa, trabalha e não muda num uso", () => {
    expect(moodFromSignal({ laneId: "l1", kind: "text", thought: true })).toBe("thinking");
    expect(moodFromSignal({ laneId: "l1", kind: "tool_call", toolName: "edit_file" })).toBe("working");
    expect(moodFromSignal({ laneId: "l1", kind: "usage" })).toBeNull();
  });
});

describe("fila", () => {
  it("guarda o mais novo e descarta por id", () => {
    let q = pushNotice([{ id: "a" }], { id: "b" });
    q = pushNotice(q, { id: "a" });
    expect(q.map((item) => item.id)).toEqual(["b", "a"]);
    expect(latestNotice(q)?.id).toBe("a");
    expect(dropNotice(q, "a").map((item) => item.id)).toEqual(["b"]);
  });

  it("limita o tamanho", () => {
    let q: { id: string }[] = [];
    for (let i = 0; i < 8; i++) q = pushNotice(q, { id: String(i) }, 6);
    expect(q.map((item) => item.id)).toEqual(["2", "3", "4", "5", "6", "7"]);
  });
});

describe("countTurnFiles", () => {
  it("conta caminhos únicos desde a última mensagem do usuário", () => {
    const n = countTurnFiles([
      { kind: "user", toolName: "write_file", toolDetail: "old.go" },
      { kind: "tool", toolName: "read_file", toolDetail: "a.go" },
      { kind: "tool", toolName: "edit_file", toolDetail: "a.go" },
      { kind: "tool", toolName: "write_file", toolDetail: "b.go" },
      { kind: "tool", toolName: "edit_file", toolDetail: "a.go" },
    ]);
    expect(n).toBe(2);
  });
});

describe("clampPosition", () => {
  it("prende o companion na janela", () => {
    expect(clampPosition(-20, 900, 80, 80, 400, 300)).toEqual({ x: 0, y: 220 });
  });
});

describe("normalizeSettings", () => {
  it("preenche o que faltar e ignora lixo", () => {
    const s = normalizeSettings({ enabled: false, sound: true, x: 12, y: "nope" });
    expect(s.enabled).toBe(false);
    expect(s.sound).toBe(true);
    expect(s.osNotifications).toBe(true);
    expect(s.doNotDisturb).toBe(false);
    expect(s.x).toBe(12);
    expect(s.y).toBeNull();
  });

  it("valor inválido volta ao padrão", () => {
    expect(normalizeSettings(null).enabled).toBe(true);
  });
});

describe("noticeFromGitHubEvents", () => {
  it("formata menção em PR com snippet", () => {
    const out = noticeFromGitHubEvents(
      [{ kind: "mention", prNumber: 42, title: "fix: token", author: "marcus", snippet: "@gilvan olha isso?" }],
      "hivemind"
    );
    expect(out?.title).toBe("[hivemind] @marcus marcou você no PR #42");
    expect(out?.body).toContain("fix: token");
    expect(out?.mood).toBe("attention");
  });

  it("formata PR pronto para merge como success", () => {
    const out = noticeFromGitHubEvents([
      { kind: "my_pr_approved", prNumber: 99, title: "feat: login", author: "joao", isMine: true },
    ]);
    expect(out?.title).toBe("PR #99 pronto para merge");
    expect(out?.mood).toBe("success");
  });
});
