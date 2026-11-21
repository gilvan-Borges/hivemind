import { describe, expect, it } from "vitest";
import { formatGitHubEventNotification, truncateNotifyBody } from "./notify";

describe("truncateNotifyBody", () => {
  it("mantém texto curto intacto", () => {
    expect(truncateNotifyBody("ok")).toBe("ok");
  });

  it("corta texto longo com reticências", () => {
    const long = "a".repeat(200);
    const out = truncateNotifyBody(long, 40);
    expect(out.length).toBe(40);
    expect(out.endsWith("…")).toBe(true);
  });

  it("colapsa espaços", () => {
    expect(truncateNotifyBody("  hello   world  ")).toBe("hello world");
  });
});

describe("formatGitHubEventNotification", () => {
  it("formata menção em PR de terceiro com snippet", () => {
    const out = formatGitHubEventNotification({
      kind: "mention",
      prNumber: 42,
      title: "fix: valida token",
      author: "marcus",
      snippet: "@gilvan-Borges consegue revisar?",
    });
    expect(out.title).toBe("@marcus marcou você no PR #42");
    expect(out.body).toContain("fix: valida token");
    expect(out.body).toContain("@gilvan-Borges consegue revisar?");
    expect(out.kind).toBe("attention");
  });

  it("formata comentário no meu PR", () => {
    const out = formatGitHubEventNotification({
      kind: "my_pr_comment",
      prNumber: 12,
      title: "feat: auto sync",
      author: "ana",
      snippet: "Ficou muito bom!",
      isMine: true,
    });
    expect(out.title).toBe("@ana comentou no seu PR #12");
    expect(out.kind).toBe("attention");
  });

  it("formata PR pronto para merge como success", () => {
    const out = formatGitHubEventNotification({
      kind: "my_pr_approved",
      prNumber: 99,
      title: "feat: login",
      author: "joao",
      isMine: true,
    });
    expect(out.title).toBe("PR #99 pronto para merge");
    expect(out.kind).toBe("success");
  });
});
