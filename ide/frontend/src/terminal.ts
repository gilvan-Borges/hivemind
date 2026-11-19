import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { icons } from "./icons";
import type { PTYEvent } from "./types";
import { escapeHtml } from "./utils";
import { openFileInEditor } from "./editor";
import { countProblems, getAllProblems, runProblemsCheck } from "./lsp";
import { updateStatusBar } from "./shell";
import { api, normalizePathKey, setStatus, state } from "./state";

interface XtermInstance {
  termKey: string;
  workspace: string;
  term: Terminal;
  fit: FitAddon;
  container: HTMLDivElement;
  started: boolean;
  starting: boolean;
  pendingWrites: string[];
}

const xtermSessions = new Map<string, XtermInstance>();
let resizeObserver: ResizeObserver | null = null;
let activeObservedKey = "";

function activeTermKey(): string {
  return normalizePathKey(state.project?.root || "");
}

function getOrCreateXtermSession(workspace: string): XtermInstance {
  const key = normalizePathKey(workspace);
  const existing = xtermSessions.get(key);
  if (existing) return existing;

  const container = document.createElement("div");
  container.className = "xterm-host-inner";

  const term = new Terminal({
    cursorBlink: true,
    cursorStyle: "bar",
    fontFamily: "var(--mono), 'Cascadia Code', 'JetBrains Mono', Consolas, monospace",
    fontSize: 12.5,
    lineHeight: 1.35,
    scrollback: 5000,
    allowProposedApi: false,
    theme: {
      background: "#0d0f13",
      foreground: "#d4d8e1",
      cursor: "#4c8dff",
      cursorAccent: "#0d0f13",
      selectionBackground: "rgba(76, 141, 255, 0.30)",
      black: "#181b22",
      red: "#f87171",
      green: "#34d399",
      yellow: "#fbbf24",
      blue: "#60a5fa",
      magenta: "#93c5fd",
      cyan: "#38bdf8",
      white: "#d4d8e1",
      brightBlack: "#5c6475",
      brightRed: "#fca5a5",
      brightGreen: "#6ee7b7",
      brightYellow: "#fcd34d",
      brightBlue: "#93c5fd",
      brightMagenta: "#bfdbfe",
      brightCyan: "#7dd3fc",
      brightWhite: "#f3f5f9",
    },
  });

  const fit = new FitAddon();
  term.loadAddon(fit);
  term.open(container);

  const inst: XtermInstance = {
    termKey: key,
    workspace,
    term,
    fit,
    container,
    started: false,
    starting: false,
    pendingWrites: [],
  };

  term.onData((data) => {
    const backend = api();
    if (!backend?.WritePTY) return;
    if (!inst.started) {
      inst.pendingWrites.push(data);
      void ensurePTYStarted(inst);
      return;
    }
    void backend.WritePTY(inst.termKey, data).catch(() => {});
  });

  xtermSessions.set(key, inst);
  return inst;
}

async function ensurePTYStarted(inst: XtermInstance): Promise<void> {
  const backend = api();
  if (!backend?.StartPTY || inst.started || inst.starting) return;
  inst.starting = true;
  try {
    try {
      inst.fit.fit();
    } catch {
      // ignore if hidden
    }
    const cols = Math.max(40, inst.term.cols || 100);
    const rows = Math.max(8, inst.term.rows || 18);
    const scrollback = await backend.StartPTY(inst.termKey, inst.workspace, cols, rows);
    inst.started = true;
    if (scrollback) {
      inst.term.write(scrollback);
    }
    if (inst.pendingWrites.length > 0) {
      const queued = inst.pendingWrites.splice(0);
      for (const chunk of queued) {
        await backend.WritePTY?.(inst.termKey, chunk);
      }
    }
  } catch (err: any) {
    inst.term.writeln(`\r\n\x1b[31m[HiveMind PTY] Falha ao iniciar terminal interativo: ${err?.message || String(err)}\x1b[0m`);
  } finally {
    inst.starting = false;
  }
}

export function handlePTYOutputEvent(ev: PTYEvent): void {
  const key = normalizePathKey(ev?.termId || ev?.workspace || "");
  if (!key) return;
  const inst = xtermSessions.get(key);
  if (!inst) return;
  if (ev.data) {
    inst.term.write(ev.data);
  }
  if (ev.exited) {
    inst.started = false;
    inst.term.writeln(`\r\n\x1b[90m[Shell encerrado (exit ${ev.exitCode ?? 0}) — pressione Enter ou clique em Reiniciar]\x1b[0m`);
  }
}

export function toggleTerminal(): void {
  if (state.terminal.open && state.bottomTab === "terminal") {
    state.terminal.open = false;
    renderTerminalDrawer();
    return;
  }
  state.terminal.open = true;
  state.bottomTab = "terminal";
  renderTerminalDrawer();
}

export function openProblemsPanel(): void {
  if (state.terminal.open && state.bottomTab === "problems") {
    state.terminal.open = false;
    renderTerminalDrawer();
    return;
  }
  state.terminal.open = true;
  state.bottomTab = "problems";
  renderTerminalDrawer();
}

export async function runTerminalCommand(rawCmd: string): Promise<void> {
  const cmd = rawCmd.trim();
  const backend = api();
  if (!cmd || !backend || !state.project) return;
  state.terminal.open = true;
  state.bottomTab = "terminal";
  renderTerminalDrawer();

  const inst = getOrCreateXtermSession(state.project.root);
  if (backend.StartPTY && backend.WritePTY) {
    await ensurePTYStarted(inst);
    await backend.WritePTY(inst.termKey, `${cmd}\r`);
    inst.term.focus();
    setStatus(`Comando enviado ao terminal: ${cmd}`);
    return;
  }

  // Fallback para modo preview / ambiente sem PTY
  inst.term.writeln(`\x1b[36m$ ${cmd}\x1b[0m`);
  try {
    const res = await backend.RunCommand(state.project.root, cmd);
    inst.term.writeln((res.output || "(sem saída)").replace(/\r?\n/g, "\r\n"));
  } catch (err: any) {
    inst.term.writeln(`\x1b[31m${err?.message || String(err)}\x1b[0m`);
  }
}

function fitActiveXterm(): void {
  const key = activeTermKey();
  const inst = xtermSessions.get(key);
  if (!inst || !state.terminal.open || state.bottomTab !== "terminal") return;
  try {
    inst.fit.fit();
    const cols = Math.max(40, inst.term.cols || 100);
    const rows = Math.max(8, inst.term.rows || 18);
    if (inst.started) {
      void api()?.ResizePTY?.(inst.termKey, cols, rows).catch(() => {});
    }
  } catch {
    // ignore layout race
  }
}

export function renderTerminalDrawer(): void {
  const drawer = document.getElementById("terminal-drawer");
  if (!drawer) return;
  drawer.classList.toggle("hidden", !state.terminal.open);
  if (!state.terminal.open) {
    if (resizeObserver) {
      resizeObserver.disconnect();
      activeObservedKey = "";
    }
    return;
  }

  const hint = state.project?.stack?.testHint || "";
  const counts = countProblems();
  const isTerm = state.bottomTab === "terminal";

  // Detach existing xterm host node before rewriting drawer innerHTML so it survives tab switches!
  const existingXterm = drawer.querySelector(".xterm-host-inner");
  if (existingXterm && existingXterm.parentElement) {
    existingXterm.parentElement.removeChild(existingXterm);
  }

  const probBadgeClass = counts.errors > 0 ? "err" : counts.warnings > 0 ? "warn" : "";

  drawer.innerHTML = `
    <div class="terminal-header">
      <div class="drawer-tabs">
        <button type="button" class="drawer-tab ${isTerm ? "active" : ""}" id="tab-bottom-terminal">
          ${icons.terminal}
          <span>Terminal</span>
        </button>
        <button type="button" class="drawer-tab ${!isTerm ? "active" : ""}" id="tab-bottom-problems">
          <span>Problemas</span>
          <span class="drawer-count-pill ${probBadgeClass}">${counts.total}</span>
        </button>
      </div>
      ${isTerm ? `
        ${hint ? `<button type="button" class="mini success" id="term-run-hint" title="Executar no terminal interativo">${icons.play}<span>${escapeHtml(hint)}</span></button>` : ""}
        <button type="button" class="mini" id="term-run-status">git status</button>
        <div class="spacer"></div>
        <button type="button" class="mini ghost" id="term-check-problems" title="Executar verificação e preencher o painel Problemas">${icons.check}<span>Verificar</span></button>
        <button type="button" class="mini ghost" id="term-restart" title="Reiniciar shell PowerShell (ConPTY)">${icons.refresh}<span>Reiniciar</span></button>
        <button type="button" class="mini ghost" id="term-clear">Limpar</button>
      ` : `
        <div class="problems-filter-pills">
          <button type="button" class="mini ${state.problemsFilter === "all" ? "primary" : "ghost"}" data-prob-filter="all">Todos (${counts.total})</button>
          <button type="button" class="mini ${state.problemsFilter === "error" ? "primary" : "ghost"}" data-prob-filter="error">Erros (${counts.errors})</button>
          <button type="button" class="mini ${state.problemsFilter === "warning" ? "primary" : "ghost"}" data-prob-filter="warning">Avisos (${counts.warnings})</button>
        </div>
        <div class="spacer"></div>
        <button type="button" class="mini primary" id="btn-run-problems-check" ${state.problemsChecking ? "disabled" : ""}>
          ${state.problemsChecking ? icons.spinner : icons.play}
          <span>${state.problemsChecking ? "Verificando…" : "Verificar Projeto"}</span>
        </button>
        ${state.buildProblems.length > 0 ? `<button type="button" class="mini ghost" id="btn-clear-build-problems">Limpar Build</button>` : ""}
      `}
      <button type="button" class="icon-btn" id="term-close" title="Fechar Painel Inferior (Ctrl+J)">${icons.closeSmall}</button>
    </div>
    ${isTerm
      ? `<div class="xterm-stage" id="xterm-stage"></div>`
      : renderProblemsBodyHtml()
    }
  `;

  document.getElementById("tab-bottom-terminal")?.addEventListener("click", () => {
    state.bottomTab = "terminal";
    renderTerminalDrawer();
  });
  document.getElementById("tab-bottom-problems")?.addEventListener("click", () => {
    state.bottomTab = "problems";
    renderTerminalDrawer();
  });
  document.getElementById("term-close")?.addEventListener("click", () => {
    state.terminal.open = false;
    renderTerminalDrawer();
  });

  if (isTerm) {
    const stage = document.getElementById("xterm-stage");
    if (stage && state.project) {
      const inst = getOrCreateXtermSession(state.project.root);
      stage.appendChild(inst.container);

      if (!resizeObserver) {
        resizeObserver = new ResizeObserver(() => fitActiveXterm());
      }
      if (activeObservedKey !== inst.termKey) {
        resizeObserver.disconnect();
        resizeObserver.observe(stage);
        activeObservedKey = inst.termKey;
      }

      requestAnimationFrame(() => {
        fitActiveXterm();
        void ensurePTYStarted(inst).then(() => {
          fitActiveXterm();
          inst.term.focus();
        });
      });

      document.getElementById("term-run-hint")?.addEventListener("click", () => {
        if (hint) void runTerminalCommand(hint);
      });
      document.getElementById("term-run-status")?.addEventListener("click", () => {
        void runTerminalCommand("git status");
      });
      document.getElementById("term-check-problems")?.addEventListener("click", () => {
        void runProblemsCheck();
      });
      document.getElementById("term-clear")?.addEventListener("click", () => {
        inst.term.clear();
        inst.term.focus();
      });
      document.getElementById("term-restart")?.addEventListener("click", async () => {
        const backend = api();
        if (!backend?.RestartPTY || !state.project) return;
        inst.term.reset();
        inst.started = false;
        const cols = Math.max(40, inst.term.cols || 100);
        const rows = Math.max(8, inst.term.rows || 18);
        try {
          await backend.RestartPTY(inst.termKey, inst.workspace, cols, rows);
          inst.started = true;
          inst.term.focus();
          setStatus("Shell ConPTY reiniciado.");
        } catch (err: any) {
          inst.term.writeln(`\x1b[31mErro ao reiniciar shell: ${err?.message || String(err)}\x1b[0m`);
        }
      });
    }
    return;
  }

  // Problems tab bindings
  drawer.querySelectorAll("[data-prob-filter]").forEach((btn) => {
    btn.addEventListener("click", () => {
      state.problemsFilter = ((btn as HTMLElement).dataset.probFilter || "all") as "all" | "error" | "warning";
      renderTerminalDrawer();
    });
  });
  document.getElementById("btn-run-problems-check")?.addEventListener("click", () => {
    void runProblemsCheck();
  });
  document.getElementById("btn-clear-build-problems")?.addEventListener("click", () => {
    state.buildProblems = [];
    updateStatusBar();
    renderTerminalDrawer();
  });
  drawer.querySelectorAll("[data-problem-idx]").forEach((row) => {
    row.addEventListener("click", () => {
      const idx = parseInt((row as HTMLElement).dataset.problemIdx || "-1", 10);
      const list = filteredProblems();
      const p = list[idx];
      if (!p) return;
      const name = p.file.split("/").pop() || p.file;
      void openFileInEditor(p.file, name, p.line, p.col);
    });
  });
}

function filteredProblems() {
  const all = getAllProblems();
  if (state.problemsFilter === "error") return all.filter((p) => p.severity === "error");
  if (state.problemsFilter === "warning") return all.filter((p) => p.severity === "warning");
  return all;
}

function renderProblemsBodyHtml(): string {
  const list = filteredProblems();
  const lspHint = state.lspStatus
    ? state.lspStatus.available
      ? `LSP ${state.lspStatus.server}: ${state.lspStatus.running ? "ativo" : "pronto (abra um arquivo .go)"}`
      : state.lspStatus.error || "gopls indisponível"
    : "";

  if (state.problemsChecking) {
    return `<div class="problems-body"><div class="dim" style="padding:14px">${icons.spinner} Executando verificação do projeto…</div></div>`;
  }
  if (list.length === 0) {
    return `
      <div class="problems-body">
        <div class="problems-empty">
          <span>Nenhum problema detectado${state.problemsLastCmd ? ` (${escapeHtml(state.problemsLastCmd)})` : ""}.</span>
          ${lspHint ? `<span class="dim" style="font-size:11px">${escapeHtml(lspHint)}</span>` : ""}
        </div>
      </div>
    `;
  }
  return `
    <div class="problems-body">
      ${list.map((p, idx) => {
        const sevCls = p.severity === "warning" ? "warn" : p.severity === "info" ? "info" : "err";
        const sevMark = p.severity === "warning" ? "▲" : p.severity === "info" ? "i" : "✕";
        return `
          <button type="button" class="problem-row" data-problem-idx="${idx}" title="${escapeHtml(`${p.file}:${p.line}:${p.col} — ${p.message}`)}">
            <span class="problem-sev ${sevCls}">${sevMark}</span>
            <span class="problem-msg">${p.code ? `<span class="problem-code">${escapeHtml(p.code)}</span>` : ""}${escapeHtml(p.message)}</span>
            <span class="problem-loc">${escapeHtml(p.file)}:${p.line}:${p.col}</span>
            <span class="problem-src">${escapeHtml(p.source || "lsp")}</span>
          </button>
        `;
      }).join("")}
    </div>
  `;
}
