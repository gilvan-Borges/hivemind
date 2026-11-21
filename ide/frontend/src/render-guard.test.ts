import { describe, expect, it } from "vitest";
import { createRenderGuard, type GuardedElement, type RegionProbe } from "./render-guard";

interface FakeInput extends GuardedElement {
  value: string;
  defaultValue: string;
  tagName: string;
  region: string | null;
}

function makeProbe(): { probe: RegionProbe; inputs: FakeInput[]; focus: (el: FakeInput | null) => void } {
  const inputs: FakeInput[] = [];
  let active: FakeInput | null = null;
  const probe: RegionProbe = {
    activeElement: () => active,
    owns: (region, el) => (el as FakeInput).region === region,
    inputsIn: (region) => inputs.filter((i) => i.region === region),
    isDirty: (el) => el.value !== el.defaultValue,
  };
  return {
    probe,
    inputs,
    focus: (el) => {
      active = el;
    },
  };
}

function input(region: string, value = "", defaultValue = ""): FakeInput {
  return { tagName: "input", region, value, defaultValue };
}

describe("createRenderGuard", () => {
  it("renderiza imediatamente quando a região está livre", () => {
    const { probe } = makeProbe();
    const guard = createRenderGuard(probe);
    let renders = 0;
    guard.guardedRender("left-sidebar", () => {
      renders++;
    });
    expect(renders).toBe(1);
  });

  it("bloqueia o re-render enquanto um input da sidebar está focado e recupera após o focusout", () => {
    const { probe, inputs, focus } = makeProbe();
    const commitInput = input("left-sidebar", "feat: orb", "feat: orb");
    inputs.push(commitInput);
    focus(commitInput);
    const guard = createRenderGuard(probe);

    let renders = 0;
    guard.guardedRender("left-sidebar", () => {
      renders++;
    });
    expect(renders).toBe(0);
    expect(guard.isRegionBusy("left-sidebar")).toBe(true);

    // Async frames keep arriving while the user types — all deferred.
    guard.guardedRender("left-sidebar", () => {
      renders++;
    });
    expect(renders).toBe(0);

    // User clicks elsewhere: focus leaves the input, pending render flushes.
    focus(null);
    guard.flushPendingNow("left-sidebar");
    expect(renders).toBe(1);
    expect(guard.isRegionBusy("left-sidebar")).toBe(false);
  });

  it("mensagem de commit digitada não bloqueia para sempre: sujo SEM foco não é busy na sidebar", () => {
    const { probe, inputs, focus } = makeProbe();
    const commitInput = input("left-sidebar", "feat: msg sendo digitada", "");
    inputs.push(commitInput);
    focus(commitInput);
    const guard = createRenderGuard(probe);

    guard.guardedRender("left-sidebar", () => {});
    expect(guard.isRegionBusy("left-sidebar")).toBe(true);

    // Foco saiu mas o texto ficou sujo: o render async volta a fluir porque o
    // valor digitado já vive no estado controlado e sobrevive ao re-render.
    focus(null);
    expect(guard.isRegionBusy("left-sidebar")).toBe(false);
    let renders = 0;
    guard.guardedRender("left-sidebar", () => {
      renders++;
    });
    expect(renders).toBe(1);
  });

  it("modal protege tokens colados mesmo sem foco (sujo = busy no modal-layer)", () => {
    const { probe, inputs, focus } = makeProbe();
    const tokenInput = input("modal-layer", "ghp_colado", "");
    inputs.push(tokenInput);
    focus(null); // usuário colou e clicou fora antes do refresh async chegar
    const guard = createRenderGuard(probe);

    expect(guard.isRegionBusy("modal-layer")).toBe(true);
    let renders = 0;
    guard.guardedRender("modal-layer", () => {
      renders++;
    });
    expect(renders).toBe(0);

    // Fechar/relimpar o modal cancela a pendência (notifyRegionRendered).
    guard.notifyRegionRendered("modal-layer");
    tokenInput.value = "";
    tokenInput.defaultValue = "";
    guard.guardedRender("modal-layer", () => {
      renders++;
    });
    expect(renders).toBe(1);
  });

  it("render direto de interação cancela flush pendente sem duplicar", () => {
    const { probe, inputs, focus } = makeProbe();
    const searchInput = input("left-sidebar", "busca", "");
    inputs.push(searchInput);
    focus(searchInput);
    const guard = createRenderGuard(probe);

    let renders = 0;
    guard.guardedRender("left-sidebar", () => {
      renders++;
    }); // bloqueado (polling async)
    expect(renders).toBe(0);

    // O usuário clica numa aba da sidebar → render direto roda e notifica.
    focus(null);
    renders++;
    guard.notifyRegionRendered("left-sidebar");
    guard.flushPendingNow("left-sidebar");
    expect(renders).toBe(1);
  });

  it("foco em input de OUTRA região não bloqueia a sidebar", () => {
    const { probe, inputs, focus } = makeProbe();
    const modalToken = input("modal-layer", "tok", "");
    inputs.push(modalToken);
    focus(modalToken);
    const guard = createRenderGuard(probe);
    expect(guard.isRegionBusy("left-sidebar")).toBe(false);
    let renders = 0;
    guard.guardedRender("left-sidebar", () => {
      renders++;
    });
    expect(renders).toBe(1);
  });
});
