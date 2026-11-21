import { describe, expect, it } from "vitest";
import { state } from "./state";
import { formatDesignModeContext, renderDesignModeChipHtml, renderDesignModePopoverHtml } from "./design-mode";

describe("design mode CDP", () => {
  it("formata contexto de elemento selecionado para o composer", () => {
    const out = formatDesignModeContext({
      url: "http://localhost:5173/",
      selector: "#hero-cta",
      tag: "button",
      id: "hero-cta",
      classes: ["btn", "primary"],
      computedStyles: { color: "rgb(9, 9, 9)", backgroundColor: "rgb(243, 242, 240)" },
      boundingBox: { width: 140, height: 40 },
      outerHtml: "<button id='hero-cta'>Enviar</button>",
    });
    expect(out).toContain("[Contexto Design Mode (CDP)]");
    expect(out).toContain("Seletor: #hero-cta");
    expect(out).toContain("color: rgb(9, 9, 9)");
    expect(out).toContain("140×40px");
  });

  it("renderiza os estados do popover e chip de contexto", () => {
    state.designMode = { open: true, loadingPages: true, picking: false, error: "", pages: [], pageIdx: 0, selectorInput: "", selected: null };
    expect(renderDesignModePopoverHtml()).toContain("Conectando ao Chrome DevTools MCP");

    state.designMode.loadingPages = false;
    state.designMode.error = "CDP offline";
    const errHtml = renderDesignModePopoverHtml();
    expect(errHtml).toContain("CDP offline");
    expect(errHtml).toContain("Tentar novamente");

    state.designMode.error = "";
    state.designMode.pages = [{ index: 0, title: "App Preview", url: "http://localhost:5173/" }];
    expect(renderDesignModePopoverHtml()).toContain("App Preview");

    state.designMode.selected = { url: "http://localhost:5173/", selector: ".nav-item" };
    expect(renderDesignModeChipHtml()).toContain("CDP: <code>.nav-item</code>");
  });
});
