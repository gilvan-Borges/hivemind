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

const MAX_HIGHLIGHT_CHARS = 150000;

export function highlightCode(code: string, filename: string): string {
  const escaped = escapeHtml(code);
  if (code.length > MAX_HIGHLIGHT_CHARS) {
    return escaped;
  }
  const ext = filename.split(".").pop()?.toLowerCase() ?? "";

  if (ext === "json") {
    return escaped.replace(
      /("(?:\\.|[^"\\\n])*")(?=\s*:)|("(?:\\.|[^"\\\n])*")|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)\b/gm,
      (match, jsonKey, jsonStr, jsonKw, jsonNum) => {
        if (jsonKey !== undefined) {
          return `<span class="tok-kw">${jsonKey}</span>`;
        }
        if (jsonStr !== undefined) {
          return `<span class="tok-str">${jsonStr}</span>`;
        }
        if (jsonKw !== undefined) {
          return `<span class="tok-kw">${jsonKw}</span>`;
        }
        if (jsonNum !== undefined) {
          return `<span class="tok-num">${jsonNum}</span>`;
        }
        return match;
      }
    );
  }

  if (ext === "yaml" || ext === "yml") {
    return escaped.replace(
      /(#.*$)|("(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*')(?=\s*:)|(^[ \t-]*)([a-zA-Z0-9_.-]+)(?=\s*:)|("(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*')|\b(true|false|null)\b|\b(-?\d+(?:\.\d+)?)\b/gm,
      (match, comment, quotedKey, yamlIndent, yamlKey, strVal, boolKw, numVal) => {
        if (comment !== undefined) {
          return `<span class="tok-com">${comment}</span>`;
        }
        if (quotedKey !== undefined) {
          return `<span class="tok-kw">${quotedKey}</span>`;
        }
        if (yamlKey !== undefined) {
          return `${yamlIndent ?? ""}<span class="tok-kw">${yamlKey}</span>`;
        }
        if (strVal !== undefined) {
          return `<span class="tok-str">${strVal}</span>`;
        }
        if (boolKw !== undefined) {
          return `<span class="tok-kw">${boolKw}</span>`;
        }
        if (numVal !== undefined) {
          return `<span class="tok-num">${numVal}</span>`;
        }
        return match;
      }
    );
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
