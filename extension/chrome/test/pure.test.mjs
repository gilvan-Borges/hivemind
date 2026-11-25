import assert from "node:assert/strict";
import test from "node:test";
import { diffPage, filterLines, findRefs, hostOf, isLoopbackWebSocket, isSensitiveLabel, pageText, readPage, redactSecrets, safeURL, tokenBench } from "../dist/pure.js";

test("rótulos sensíveis pedem aprovação", () => {
  assert.equal(isSensitiveLabel("Comprar agora"), true);
  assert.equal(isSensitiveLabel("Salvar rascunho"), false);
  assert.equal(isSensitiveLabel("Place order"), true);
});

test("segredos saem do texto", () => {
  const got = redactSecrets("Bearer sk-test-fake-0000 token=abc123");
  assert.equal(got.includes("sk-test"), false);
  assert.equal(got.includes("abc123"), false);
});

test("só http(s) sem usuário", () => {
  assert.equal(hostOf("https://Example.com/a"), "example.com");
  assert.equal(hostOf("file:///tmp/x"), null);
  assert.equal(hostOf("https://user:pw@example.com"), null);
  assert.equal(isLoopbackWebSocket("ws://127.0.0.1:47891/browser"), true);
  assert.equal(isLoopbackWebSocket("ws://10.0.0.8:47891/browser"), false);
  assert.equal(isLoopbackWebSocket("wss://127.0.0.1/browser"), false);
});

test("árvore vira refs curtas e estáveis", () => {
  const nodes = [
    { role: { value: "generic" } },
    { nodeId: "a", role: { value: "textbox" }, name: { value: "E-mail" }, backendDOMNodeId: 12 },
    { nodeId: "b", role: { value: "button" }, name: { value: "Salvar" }, backendDOMNodeId: 13 },
    { nodeId: "c", role: { value: "link" }, name: { value: "Ad" }, backendDOMNodeId: 14 },
    { nodeId: "d", role: { value: "link" }, name: { value: "Ad" }, backendDOMNodeId: 15 },
  ];
  const page = readPage(nodes);
  assert.match(page.text, /e1 textbox "E-mail"/);
  assert.equal(page.refs.e2.backendNodeId, 13);
  assert.match(page.text, /×2/);
  assert.match(findRefs(page.text, "e-mail"), /e1/);
  assert.equal(findRefs(page.text, "e-mail", "button"), "");
  const again = readPage(nodes, { keep: page.refs });
  assert.equal(again.refs.e2.backendNodeId, 13);
  assert.equal(diffPage(page.text, again.text), "igual");
  const changed = readPage([...nodes, { nodeId: "e", role: { value: "button" }, name: { value: "Ok" }, backendDOMNodeId: 16 }], { keep: page.refs });
  assert.match(diffPage(page.text, changed.text), /Ok/);
});

test("texto principal e filtros ficam curtos", () => {
  const text = pageText([
    { nodeId: "nav", role: { value: "navigation" }, name: { value: "menu" } },
    { nodeId: "item", parentId: "nav", role: { value: "StaticText" }, name: { value: "Home Home Home" } },
    { nodeId: "p", role: { value: "paragraph" }, name: { value: "O artigo fala de um assunto longo o bastante." } },
  ]);
  assert.match(text, /artigo/);
  assert.equal(text.includes("Home"), false);
  const logs = ["error: quebrou", "log: ok", "warning: atenção"];
  assert.match(filterLines(logs, { keep: (line) => /^(error|warning)/.test(line) }), /quebrou/);
  assert.equal(filterLines(logs, { keep: (line) => /^(error|warning)/.test(line) }).includes("log:"), false);
});

test("benchmark de tokens cabe na metade", () => {
  const rows = tokenBench();
  for (const row of rows) {
    assert.ok(row.after * 2 < row.before, `${row.name} ${row.after} vs ${row.before}`);
  }
  console.log(JSON.stringify(rows));
});

test("query string secreta é filtrada", () => {
  assert.equal(safeURL("https://example.com/cb?token=abc&ok=1").includes("abc"), false);
});

test("botões profundos (depth > 20) e com prefixo de ícone continuam visíveis", () => {
  const nodes = [{ nodeId: "n0", role: { value: "generic" } }];
  for (let i = 1; i <= 32; i++) {
    nodes.push({ nodeId: "n" + i, parentId: "n" + (i - 1), role: { value: "generic" } });
  }
  nodes.push({
    nodeId: "btn",
    parentId: "n32",
    role: { value: "button" },
    name: { value: "Novo projeto" },
    backendDOMNodeId: 901,
  });
  nodes.push({
    nodeId: "p1",
    parentId: "n32",
    role: { value: "paragraph" },
    name: { value: "Estúdio criativo para vídeos com IA." },
  });
  const got = readPage(nodes, { scope: "interactive", visible: ["add novo projeto"] });
  assert.match(got.text, /e1 button "Novo projeto"/);
  assert.match(pageText(nodes), /Estúdio criativo/);
});
