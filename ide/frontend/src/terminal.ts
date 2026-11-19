import { icons } from "./icons";
import type { TerminalEntry } from "./types";
import { escapeHtml } from "./utils";
import { refreshDiffs } from "./git";
import { api, setStatus, state } from "./state";

export function toggleTerminal() {
  state.terminal.open = !state.terminal.open;
  renderTerminalDrawer();
  if (state.terminal.open) {
    (document.getElementById("term-cmd-input") as HTMLInputElement | null)?.focus();
  }
}

export function renderTerminalDrawer() {
  const drawer = document.getElementById("terminal-drawer");
  if (!drawer) return;
  drawer.classList.toggle("hidden", !state.terminal.open);
  if (!state.terminal.open) return;

  const hint = state.project?.stack?.testHint || "";
  drawer.innerHTML = `
    <div class="terminal-header">
      <span class="eyebrow">${icons.terminal}<span>Terminal</span></span>
      ${hint ? `<button class="mini success" id="term-run-hint" ${state.terminal.running ? "disabled" : ""}>${icons.play}<span>${escapeHtml(hint)}</span></button>` : ""}
      <button class="mini" id="term-run-status" ${state.terminal.running ? "disabled" : ""}>git status</button>
      ${state.terminal.running ? `<button class="mini danger" id="term-cancel-cmd" title="Interromper comando em execução">${icons.stop}<span>Parar Comando</span></button>` : ""}
      <div class="spacer"></div>
      <button class="mini ghost" id="term-clear">Limpar</button>
      <button class="icon-btn" id="term-close" title="Fechar Terminal (Ctrl+J)">${icons.closeSmall}</button>
    </div>
    <div class="terminal-output" id="terminal-output">
      ${state.terminal.entries.length === 0
        ? `<div class="dim">Digite um comando abaixo ou clique no atalho da stack (${escapeHtml(hint || "ex: git status")}).</div>`
        : state.terminal.entries.map((en) => `
          <div class="term-entry">
            <div class="term-cmd-line">
              <span>$ ${escapeHtml(en.command)}</span>
              ${en.running
                ? icons.spinner
                : `<span class="dim" style="color:${en.exitCode === 0 ? "var(--green)" : "var(--red)"}">[exit ${en.exitCode} · ${en.durationMs}ms]</span>`
              }
            </div>
            <div data-term-out="${escapeHtml(en.id)}">${escapeHtml(en.output)}</div>
          </div>
        `).join("")
      }
    </div>
    <div class="term-input-bar">
      <span class="dim">$</span>
      <input id="term-cmd-input" placeholder="Executar comando no projeto (Enter)…" value="${escapeHtml(state.terminal.cmdInput)}" ${state.terminal.running ? "disabled" : ""} />
      ${state.terminal.running
        ? `<button class="danger mini" id="term-cancel-bottom">${icons.stop}<span>Parar</span></button>`
        : `<button class="primary mini" id="term-send">Executar</button>`
      }
    </div>
  `;

  const outBox = document.getElementById("terminal-output");
  if (outBox) outBox.scrollTop = outBox.scrollHeight;

  const cancelRunningTerm = async () => {
    if (!state.project) return;
    await api()?.CancelCommand(state.project.root);
    setStatus("Comando do terminal interrompido.");
  };
  document.getElementById("term-cancel-cmd")?.addEventListener("click", cancelRunningTerm);
  document.getElementById("term-cancel-bottom")?.addEventListener("click", cancelRunningTerm);

  document.getElementById("term-close")?.addEventListener("click", () => toggleTerminal());
  document.getElementById("term-clear")?.addEventListener("click", () => {
    state.terminal.entries = [];
    renderTerminalDrawer();
  });
  document.getElementById("term-run-hint")?.addEventListener("click", () => {
    if (hint) runTerminalCommand(hint);
  });
  document.getElementById("term-run-status")?.addEventListener("click", () => runTerminalCommand("git status"));

  const cmdIn = document.getElementById("term-cmd-input") as HTMLInputElement | null;
  cmdIn?.addEventListener("input", () => {
    state.terminal.cmdInput = cmdIn.value;
  });
  cmdIn?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      runTerminalCommand(state.terminal.cmdInput);
    }
  });
  document.getElementById("term-send")?.addEventListener("click", () => runTerminalCommand(state.terminal.cmdInput));
}

export async function runTerminalCommand(rawCmd: string) {
  const cmd = rawCmd.trim();
  const backend = api();
  if (!cmd || !backend || !state.project || state.terminal.running) return;
  const entry: TerminalEntry = {
    id: `cmd-${Date.now()}`,
    command: cmd,
    output: "Executando…",
    exitCode: null,
    durationMs: 0,
    running: true,
  };
  state.terminal.entries.push(entry);
  state.terminal.cmdInput = "";
  state.terminal.running = true;
  renderTerminalDrawer();

  try {
    const res = await backend.RunCommand(state.project.root, cmd);
    entry.output = res.output || "(sem saída)";
    entry.exitCode = res.exitCode;
    entry.durationMs = res.durationMs;
  } catch (err: any) {
    entry.output = err?.message || String(err);
    entry.exitCode = -1;
  } finally {
    entry.running = false;
    state.terminal.running = false;
    renderTerminalDrawer();
    (document.getElementById("term-cmd-input") as HTMLInputElement | null)?.focus();
    refreshDiffs();
  }
}
