import { describe, expect, it } from "vitest";
import {
  markdownStats,
  renderMarkdownLite,
  renderMarkdownStreamed,
  resetMarkdownStreams,
  splitMarkdownBlocks,
} from "./markdown";

const SAMPLE = `# Título
Parágrafo com **negrito** e \`code\`.

- item 1
- item 2

\`\`\`go
func main() {
    fmt.Println("olá")
}
\`\`\`

> citação
Texto final.`;

// Construído por código de caractere para imunizar o teste a qualquer
// transformação de aspas por editores/ferramentas no caminho (mesma razão do
// utils.test.ts). EVIL = <img src="x" onerror='alert(1)'> & "aspas" 'simples'
const DQ = String.fromCharCode(34); // "
const SQ = String.fromCharCode(39); // '
const EVIL = `<img src=${DQ}x${DQ} onerror=${SQ}alert(1)${SQ}> & ${DQ}aspas${DQ} ${SQ}simples${SQ}`;

describe("renderMarkdownLite (compatibilidade do parser completo)", () => {
  it("renderiza headings, bullets, code e citações", () => {
    const html = renderMarkdownLite(SAMPLE);
    expect(html).toContain('<div class="md-h1">Título</div>');
    expect(html).toContain("<strong>negrito</strong>");
    expect(html).toContain('<code class="md-inline">code</code>');
    expect(html).toContain('<div class="md-li"><span class="md-bullet">•</span><span>item 1</span></div>');
    expect(html).toContain('<span>go</span>');
    expect(html).toContain("fmt.Println(&quot;olá&quot;)");
    expect(html).toContain('<div class="md-quote">citação</div>');
  });

  it("escapa HTML perigosos antes de qualquer markup", () => {
    const html = renderMarkdownLite(EVIL);
    expect(html).toContain("&lt;img src=&quot;x&quot; onerror=&#39;alert(1)&#39;&gt;");
    expect(html).not.toContain("<img");
  });

  it("retorna vazio para entrada vazia", () => {
    expect(renderMarkdownLite("")).toBe("");
    expect(renderMarkdownLite(undefined as unknown as string)).toBe("");
  });

  it("preserva fence de código aberto como prose (semântica do stream)", () => {
    const html = renderMarkdownLite("antes\n```go\nlinha em stream");
    expect(html).not.toContain("md-code-wrap");
    expect(html).toContain("antes");
  });
});

describe("splitMarkdownBlocks", () => {
  it("fecha blocos de código e deixa só a última prose aberta", () => {
    const blocks = splitMarkdownBlocks("a\n```go\nx\n```\nb");
    expect(blocks.map((b) => b.type)).toEqual(["prose", "code", "prose"]);
    expect(blocks.map((b) => b.closed)).toEqual([true, true, false]);
    expect(blocks[1]).toMatchObject({ lang: "go", content: "x" });
    // A quebra de linha após o fence pertence ao prose seguinte (mesma
    // semântica de split da regex usada pelo parser completo).
    expect(blocks[2]).toMatchObject({ content: "\nb" });
  });

  it("sem fences há um único bloco prose aberto", () => {
    const blocks = splitMarkdownBlocks("texto puro");
    expect(blocks).toHaveLength(1);
    expect(blocks[0].closed).toBe(false);
  });
});

describe("renderMarkdownStreamed (parser incremental por blocos)", () => {
  it("produz saída idêntica ao renderMarkdownLite em cada estágio do stream", () => {
    resetMarkdownStreams();
    const words = SAMPLE.split(" ");
    let accumulated = "";
    for (const w of words) {
      accumulated = accumulated ? `${accumulated} ${w}` : w;
      expect(renderMarkdownStreamed("msg-1", accumulated)).toBe(renderMarkdownLite(accumulated));
    }
    expect(renderMarkdownStreamed("msg-1", SAMPLE)).toBe(renderMarkdownLite(SAMPLE));
  });

  it("congela blocos fechados: só a cauda é re-parseada a cada frame", () => {
    resetMarkdownStreams();
    // Prefixo estável longo com um bloco de código fechado no meio.
    const frozen = Array.from({ length: 200 }, (_, i) => `Linha estável ${i}`).join("\n");
    const withCode = `${frozen}\n\n\`\`\`go\ncódigo fechado\n\`\`\`\n\ntexto pós-código`;
    renderMarkdownStreamed("msg-2", withCode);
    const afterFirst = markdownStats();

    // Streaming da cauda: 60 frames acrescentando texto no fim.
    let tail = "";
    for (let i = 0; i < 60; i++) {
      tail += ` chunk ${i}`;
      renderMarkdownStreamed("msg-2", `${withCode}${tail}`);
    }
    const afterStream = markdownStats();
    const parsedDelta = afterStream.parsedBlocks - afterFirst.parsedBlocks;
    // O(N) e não O(N²): cada frame re-parseia APENAS o bloco de cauda aberto.
    expect(parsedDelta).toBe(60);
    expect(afterStream.cacheHits - afterFirst.cacheHits).toBeGreaterThanOrEqual(60);
  });

  it("invalida o cache quando o stream reinicia com texto diferente", () => {
    resetMarkdownStreams();
    renderMarkdownStreamed("msg-3", "resposta original A");
    renderMarkdownStreamed("msg-3", "resposta reescrita B");
    const s = markdownStats();
    expect(s.cacheHits).toBe(0);
    expect(s.parsedBlocks).toBe(2);
  });

  it("evicta o cache acima do limite de entradas", () => {
    resetMarkdownStreams();
    for (let i = 0; i < 70; i++) {
      renderMarkdownStreamed(`k-${i}`, "texto");
    }
    expect(markdownStats().cachedEntries).toBeLessThanOrEqual(64);
  });
});
