const KEYWORDS = new Set([
  "package", "import", "func", "return", "if", "else", "for", "range", "switch", "case", "default",
  "type", "struct", "interface", "var", "const", "go", "defer", "select", "chan", "map",
  "public", "private", "protected", "class", "extends", "implements", "new", "void", "static", "final",
  "try", "catch", "finally", "throw", "throws", "async", "await", "export", "from", "let", "true", "false", "null", "nil",
  "def", "in", "not", "and", "or", "with", "as", "yield", "fn", "pub", "impl", "match", "use", "mod"
]);

const TYPES = new Set([
  "string", "int", "int32", "int64", "bool", "error", "byte", "any", "float64",
  "String", "Integer", "Boolean", "List", "Map", "Optional", "Path", "Promise", "Record", "number", "boolean"
]);

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

export function highlightCode(code: string, filename: string): string {
  const ext = filename.split(".").pop()?.toLowerCase() ?? "";
  const escaped = escapeHtml(code);

  if (ext === "json" || ext === "yaml" || ext === "yml") {
    return escaped
      .replace(/(&quot;|")([^"\n]*)(\1)\s*:/g, '<span class="tok-kw">$1$2$3</span>:')
      .replace(/^([a-zA-Z0-9_.-]+):/gm, '<span class="tok-kw">$1</span>:')
      .replace(/(".*?")/g, '<span class="tok-str">$1</span>')
      .replace(/\b(\d+(?:\.\d+)?|true|false|null)\b/g, '<span class="tok-num">$1</span>')
      .replace(/(#.*)$/gm, '<span class="tok-com">$1</span>');
  }

  return escaped.replace(
    /(\/\/.*$|\/\*[\s\S]*?\*\/|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`[^`]*`|\b\d+(?:\.\d+)?\b|\b[A-Za-z_][A-Za-z0-9_]*\b)/gm,
    (match) => {
      if (match.startsWith("//") || match.startsWith("/*")) {
        return `<span class="tok-com">${match}</span>`;
      }
      if (match.startsWith('"') || match.startsWith("'") || match.startsWith("`")) {
        return `<span class="tok-str">${match}</span>`;
      }
      if (/^\d/.test(match)) {
        return `<span class="tok-num">${match}</span>`;
      }
      if (KEYWORDS.has(match)) {
        return `<span class="tok-kw">${match}</span>`;
      }
      if (TYPES.has(match)) {
        return `<span class="tok-type">${match}</span>`;
      }
      return match;
    }
  );
}
