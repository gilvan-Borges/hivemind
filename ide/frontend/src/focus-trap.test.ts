import { describe, expect, it } from "vitest";
import { nextFocusTarget, type FocusableLike } from "./focus-trap";

function fakeFocusables(n: number): FocusableLike[] {
  return Array.from({ length: n }, (_, i) => ({ focus: () => {}, id: i }));
}

describe("nextFocusTarget (ciclo de foco do focus trap)", () => {
  it("avança em ciclo para frente", () => {
    const items = fakeFocusables(4);
    expect(nextFocusTarget(items, 0, false).index).toBe(1);
    expect(nextFocusTarget(items, 3, false).index).toBe(0); // wrap
  });

  it("recua em ciclo com Shift+Tab", () => {
    const items = fakeFocusables(4);
    expect(nextFocusTarget(items, 2, true).index).toBe(1);
    expect(nextFocusTarget(items, 0, true).index).toBe(3); // wrap para trás
  });

  it("entra no primeiro elemento quando o foco está fora do trap", () => {
    const items = fakeFocusables(3);
    expect(nextFocusTarget(items, -1, false).index).toBe(0);
    expect(nextFocusTarget(items, -1, true).index).toBe(items.length - 1);
  });

  it("com um único candidato devolve sempre ele", () => {
    const items = fakeFocusables(1);
    expect(nextFocusTarget(items, 0, false).target).toBe(items[0]);
  });

  it("sem candidatos devolve null", () => {
    expect(nextFocusTarget([], 0, false).target).toBeNull();
  });
});
