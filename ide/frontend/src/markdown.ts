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

export interface MarkdownBlock {
  type: "prose" | "code";
  lang: string;
  content: string;
  closed: boolean;
}

const CODE_FENCE_SPLIT_RE = /```([a-zA-Z0-9_+-]*)\n([\s\S]*?)```/g;

// splitMarkdownBlocks segments escaped markdown text into consecutive prose and
// closed code-fence blocks. Everything except the final prose block is frozen
// (a closed fence is immutable), which is what enables the incremental parser.
export function splitMarkdownBlocks(escaped: string): MarkdownBlock[] {
  const parts = escaped.split(CODE_FENCE_SPLIT_RE);
  const blocks: MarkdownBlock[] = [];
  for (let i = 0; i < parts.length; i += 3) {
    const prose = parts[i] ?? "";
    const isTail = i + 1 >= parts.length;
    blocks.push({ type: "prose", lang: "", content: prose, closed: !isTail });
    if (i + 2 < parts.length) {
      const rawLang = parts[i + 1] || "code";
      const lang = rawLang.replace(/[^a-zA-Z0-9_+-]/g, "") || "code";
      const code = (parts[i + 2] ?? "").replace(/\n$/, "");
      blocks.push({ type: "code", lang, content: code, closed: true });
    }
  }
  return blocks;
}

function renderProseBlock(prose: string): string {
  if (!prose) return "";
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
  return formattedLines.join("\n");
}

function renderCodeBlock(block: MarkdownBlock): string {
  return `<div class="md-code-wrap"><div class="md-code-lang"><span>${block.lang}</span><button class="md-copy-btn" type="button" data-copy-code="1" title="Copiar código">Copiar</button></div><pre class="md-code"><code>${block.content}</code></pre></div>`;
}

function renderBlock(block: MarkdownBlock): string {
  return block.type === "code" ? renderCodeBlock(block) : renderProseBlock(block.content);
}

export function renderMarkdownLite(raw: string): string {
  if (!raw) return "";
  const escaped = escapeHtml(raw.replace(/\r\n/g, "\n"));
  return splitMarkdownBlocks(escaped)
    .map(renderBlock)
    .join("");
}

// ---------------------------------------------------------------------------
// Incremental block renderer (Spec 07 — kills the O(N²) re-parse during
// streaming). Frozen blocks are memoized per message key; only the open tail
// block is re-parsed on each streaming frame.
// ---------------------------------------------------------------------------

interface StreamEntry {
  frozenSigs: string[];
  frozenHtml: string[];
}

const MAX_STREAM_ENTRIES = 64;

const streamCache = new Map<string, StreamEntry>();
const stats = { parsedBlocks: 0, cacheHits: 0, tailRenders: 0 };

function blockSignature(block: MarkdownBlock): string {
  return `${block.type}:${block.lang}:${block.content.length}:${block.content.length > 0 ? block.content.charCodeAt(block.content.length - 1) : 0}`;
}

// renderMarkdownStreamed renders `raw` for the streaming message identified by
// `key`, re-using the HTML of every block that did not change since the last
// frame and re-parsing only the changed/new blocks.
export function renderMarkdownStreamed(key: string, raw: string): string {
  if (!raw) return "";
  const escaped = escapeHtml(raw.replace(/\r\n/g, "\n"));
  const blocks = splitMarkdownBlocks(escaped);

  let entry = streamCache.get(key);
  if (!entry) {
    entry = { frozenSigs: [], frozenHtml: [] };
    if (streamCache.size >= MAX_STREAM_ENTRIES) {
      // Simple FIFO eviction: the oldest key loses its cache slot.
      const oldest = streamCache.keys().next().value;
      if (oldest !== undefined) streamCache.delete(oldest);
    }
    streamCache.set(key, entry);
  }

  let html = "";
  let frozen = 0;
  for (let i = 0; i < blocks.length; i++) {
    const block = blocks[i];
    if (!block.closed) {
      // Open tail block: always re-rendered (it is the streaming cauda).
      stats.tailRenders++;
      stats.parsedBlocks++;
      html += renderBlock(block);
      continue;
    }
    const sig = blockSignature(block);
    if (entry.frozenSigs[i] === sig && entry.frozenHtml[i] !== undefined) {
      stats.cacheHits++;
      html += entry.frozenHtml[i];
      frozen = i + 1;
      continue;
    }
    // Divergence detected (stream restart or edit): rebuild everything ahead.
    entry.frozenSigs.length = i;
    entry.frozenHtml.length = i;
    stats.parsedBlocks++;
    entry.frozenSigs[i] = sig;
    entry.frozenHtml[i] = renderBlock(block);
    html += entry.frozenHtml[i];
    frozen = i + 1;
  }
  // If the tail closed after this frame (e.g. text stopped at a fence boundary),
  // later calls will freeze it thanks to the signature comparison above.
  return html;
}

// markdownStats exposes counters used by unit tests to assert the parser is
// effectively incremental (cache hits grow linearly with frozen blocks).
export function markdownStats(): { parsedBlocks: number; cacheHits: number; tailRenders: number; cachedEntries: number } {
  return {
    parsedBlocks: stats.parsedBlocks,
    cacheHits: stats.cacheHits,
    tailRenders: stats.tailRenders,
    cachedEntries: streamCache.size,
  };
}

// resetMarkdownStreams clears caches and counters (test helper).
export function resetMarkdownStreams(): void {
  streamCache.clear();
  stats.parsedBlocks = 0;
  stats.cacheHits = 0;
  stats.tailRenders = 0;
}

// dropMarkdownStream frees the cache of one message (e.g. when a message is removed).
export function dropMarkdownStream(key: string): void {
  streamCache.delete(key);
}
