export interface PlanAnnotation {
  id: string;
  line: number;
  quote: string;
  comment: string;
}

export interface PlanBlock {
  line: number;
  text: string;
  isHeading: boolean;
}

/**
 * Quebra o markdown do plano em blocos/linhas não vazios numerados (1-based)
 * para permitir anotação pontual sem perder a estrutura visual.
 */
export function splitPlanBlocks(raw: string): PlanBlock[] {
  const lines = String(raw ?? "").replace(/\r\n/g, "\n").split("\n");
  const blocks: PlanBlock[] = [];
  for (let i = 0; i < lines.length; i++) {
    const trimmed = lines[i].trim();
    if (!trimmed) continue;
    const isHeading = /^#{1,4}\s+/.test(trimmed);
    const cleaned = trimmed
      .replace(/^#{1,4}\s+/, "")
      .replace(/^[-*]\s+/, "")
      .trim();
    blocks.push({
      line: i + 1,
      text: cleaned || trimmed,
      isHeading,
    });
  }
  return blocks;
}

/**
 * Formata as anotações em um pedido estruturado para o Chefe ajustar o plano
 * antes de aprovar a execução.
 */
export function formatPlanAnnotationsFeedback(annotations: PlanAnnotation[]): string {
  if (!annotations || annotations.length === 0) return "";
  const sorted = [...annotations].sort((a, b) => a.line - b.line);
  const lines = sorted.map((a) => {
    const snippet = a.quote.length > 90 ? `${a.quote.slice(0, 87)}...` : a.quote;
    return `- [L${a.line}] "${snippet}" -> ${a.comment.trim()}`;
  });
  return [
    "Ajuste o plano considerando as seguintes anotações antes de prosseguir:",
    "",
    ...lines,
  ].join("\n");
}
