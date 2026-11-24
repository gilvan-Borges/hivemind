export const sensitiveWords = [
  "submit",
  "enviar",
  "comprar",
  "buy now",
  "buy",
  "pay",
  "pagar",
  "checkout",
  "purchase",
  "place order",
  "assinar",
  "subscribe",
  "transferir",
  "sacar",
  "withdraw",
  "confirmar compra",
  "excluir conta",
  "delete account",
];

export function isSensitiveLabel(text: string): boolean {
  const hay = text.toLowerCase();
  return sensitiveWords.some((word) => hay.includes(word));
}

export function redactSecrets(text: string): string {
  return text
    .replace(/bearer\s+\S+/gi, "Bearer [redigido]")
    .replace(/(token|password|passwd|secret|api[_-]?key)=([^&\s]+)/gi, "$1=[redigido]")
    .replace(/\b(ghp_|github_pat_|sk-|xoxb-|ya29\.|AIza)[A-Za-z0-9._-]+/g, "[redigido]");
}

export function hostOf(raw: string): string | null {
  try {
    const url = new URL(raw);
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    if (url.username || url.password) return null;
    const host = url.hostname.toLowerCase().replace(/\.$/, "");
    return host || null;
  } catch {
    return null;
  }
}

export function isLoopbackWebSocket(raw: string): boolean {
  try {
    const url = new URL(raw);
    if (url.protocol !== "ws:") return false;
    const host = url.hostname.toLowerCase();
    return host === "127.0.0.1" || host === "localhost" || host === "::1";
  } catch {
    return false;
  }
}

export function safeURL(raw: string): string {
  try {
    const url = new URL(raw);
    const secretKeys: string[] = [];
    url.searchParams.forEach((_value, key) => {
      if (/token|password|passwd|secret|key|auth/i.test(key)) secretKeys.push(key);
    });
    for (const key of secretKeys) url.searchParams.set(key, "[redigido]");
    return url.toString().slice(0, 300);
  } catch {
    return redactSecrets(raw).slice(0, 300);
  }
}

export interface AxNode {
  role?: { value?: string };
  name?: { value?: string };
  backendDOMNodeId?: number;
}

export interface PageRef {
  backendNodeId: number;
  role: string;
  name: string;
}

const skipRoles = new Set(["none", "generic", "InlineTextBox", "LineBreak", "Ignored"]);

export function readPage(nodes: AxNode[]): { text: string; refs: Record<string, PageRef> } {
  const refs: Record<string, PageRef> = {};
  const lines: string[] = [];
  for (const node of nodes) {
    const role = node.role?.value ?? "";
    if (!role || skipRoles.has(role)) continue;
    const name = (node.name?.value ?? "").replace(/\s+/g, " ").trim();
    if (!name && (role === "StaticText" || role === "image")) continue;
    const ref = "e" + (lines.length + 1);
    if (node.backendDOMNodeId) {
      refs[ref] = { backendNodeId: node.backendDOMNodeId, role, name };
    }
    lines.push(`- ${ref} ${role}${name ? ` "${name.slice(0, 160)}"` : ""}`);
    if (lines.length >= 700) break;
  }
  return { text: lines.join("\n"), refs };
}

export function findRefs(tree: string, query: string): string {
  const q = query.trim().toLowerCase();
  if (!q) return "";
  return tree
    .split("\n")
    .filter((line) => line.toLowerCase().includes(q))
    .slice(0, 40)
    .join("\n");
}
