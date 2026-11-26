import { describe, expect, it } from "vitest";
import { formatPlanAnnotationsFeedback, splitPlanBlocks } from "./plan-annotator";

describe("plan-annotator", () => {
  it("extrai blocos numerados ignorando linhas em branco", () => {
    const raw = [
      "# Plano de Entrega",
      "",
      "- Criar endpoint /api/itens",
      "Validar contrato DTO",
    ].join("\n");
    const blocks = splitPlanBlocks(raw);
    expect(blocks).toEqual([
      { line: 1, text: "Plano de Entrega", isHeading: true },
      { line: 3, text: "Criar endpoint /api/itens", isHeading: false },
      { line: 4, text: "Validar contrato DTO", isHeading: false },
    ]);
  });

  it("formata anotações ordenadas por linha para o Chefe", () => {
    expect(formatPlanAnnotationsFeedback([])).toBe("");
    const out = formatPlanAnnotationsFeedback([
      { id: "2", line: 8, quote: "Usar polling de 5s", comment: "Prefiro evento reativo" },
      { id: "1", line: 2, quote: "Criar tabela nova", comment: "Reaproveitar a tabela existente" },
    ]);
    expect(out).toContain("Ajuste o plano considerando as seguintes anotações");
    expect(out.indexOf("[L2]")).toBeLessThan(out.indexOf("[L8]"));
    expect(out).toContain('[L2] "Criar tabela nova" -> Reaproveitar a tabela existente');
    expect(out).toContain('[L8] "Usar polling de 5s" -> Prefiro evento reativo');
  });
});
