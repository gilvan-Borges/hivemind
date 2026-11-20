import { describe, expect, it } from "vitest";
import { truncateNotifyBody } from "./notify";

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
