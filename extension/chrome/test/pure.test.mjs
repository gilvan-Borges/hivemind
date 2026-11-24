import assert from "node:assert/strict";
import test from "node:test";
import { findRefs, hostOf, isLoopbackWebSocket, isSensitiveLabel, readPage, redactSecrets, safeURL } from "../dist/pure.js";

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

test("árvore vira refs", () => {
  const page = readPage([
    { role: { value: "generic" } },
    { role: { value: "textbox" }, name: { value: "E-mail" }, backendDOMNodeId: 12 },
    { role: { value: "button" }, name: { value: "Salvar" }, backendDOMNodeId: 13 },
  ]);
  assert.match(page.text, /e1 textbox "E-mail"/);
  assert.equal(page.refs.e2.backendNodeId, 13);
  assert.match(findRefs(page.text, "e-mail"), /e1/);
});

test("query string secreta é filtrada", () => {
  assert.equal(safeURL("https://example.com/cb?token=abc&ok=1").includes("abc"), false);
});
