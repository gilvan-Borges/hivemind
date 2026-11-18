function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function formatInline(escapedLine: string): string {
  return escapedLine
    .replace(/\[([^\]\n]+)\]\((https?:\/\/[^)\s]+)\)/g, '<a class="md-link" href="$2" target="_blank" rel="noopener noreferrer">$1</a>')
    .replace(/`([^`\n]+)`/g, '<code class="md-inline">$1</code>')
    .replace(/\*\*([^*\n]+)\*\*/g, "<strong>$1</strong>");
}

export function renderMarkdownLite(raw: string): string {
  if (!raw) return "";
  const escaped = escapeHtml(raw.replace(/\r\n/g, "\n"));
  const parts = escaped.split(/```([a-zA-Z0-9_+-]*)\n([\s\S]*?)```/g);
  const out: string[] = [];

  for (let i = 0; i < parts.length; i += 3) {
    const prose = parts[i] ?? "";
    if (prose) {
      const lines = prose.split("\n");
      const formattedLines = lines.map((line) => {
        const heading = line.match(/^(#{1,4})\s+(.*)$/);
        if (heading) {
          return `<div class="md-h${heading[1].length}">${formatInline(heading[2])}</div>`;
        }
        const quote = line.match(/^&gt;\s?(.*)$/);
        if (quote) {
          return `<div class="md-quote">${formatInline(quote[1])}</div>`;
        }
        if (/^\s*(---|___)\s*$/.test(line)) {
          return `<hr class="md-hr" />`;
        }
        const bullet = line.match(/^\s*[-*]\s+(.*)$/);
        if (bullet) {
          return `<div class="md-li"><span class="md-bullet">•</span><span>${formatInline(bullet[1])}</span></div>`;
        }
        return formatInline(line);
      });
      out.push(formattedLines.join("\n"));
    }
    if (i + 2 < parts.length) {
      const rawLang = parts[i + 1] || "code";
      const lang = rawLang.replace(/[^a-zA-Z0-9_+-]/g, "") || "code";
      const code = (parts[i + 2] ?? "").replace(/\n$/, "");
      out.push(
        `<div class="md-code-wrap"><div class="md-code-lang"><span>${lang}</span><button class="md-copy-btn" type="button" data-copy-code="1" title="Copiar código">Copiar</button></div><pre class="md-code"><code>${code}</code></pre></div>`
      );
    }
  }
  return out.join("");
}
