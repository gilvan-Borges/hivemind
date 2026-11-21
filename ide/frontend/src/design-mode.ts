import { icons } from "./icons";
import { api, setStatus, state } from "./state";
import type { DesignModeElement, DesignModePage } from "./types";
import { escapeHtml } from "./utils";

export function formatDesignModeContext(el: DesignModeElement): string {
  const parts = ["[Contexto Design Mode (CDP)]"];
  if (el.url) parts.push(`URL: ${el.url}`);
  parts.push(`Seletor: ${el.selector}`);
  if (el.tag) {
    const cls = Array.isArray(el.classes) && el.classes.length ? ` .${el.classes.join(".")}` : "";
    parts.push(`Elemento: <${el.tag}>${el.id ? ` #${el.id}` : ""}${cls}`);
  }
  const styles = el.computedStyles || {};
  const stylePairs = Object.entries(styles)
    .filter(([, v]) => String(v || "").trim() !== "")
    .map(([k, v]) => `${k}: ${v}`);
  if (stylePairs.length > 0) {
    parts.push(`Estilos: ${stylePairs.join("; ")}`);
  }
  if (el.boundingBox && (el.boundingBox.width || el.boundingBox.height)) {
    parts.push(`Dimensões: ${Math.round(el.boundingBox.width || 0)}×${Math.round(el.boundingBox.height || 0)}px`);
  }
  if (el.outerHtml) {
    parts.push(`HTML:\n\`\`\`html\n${el.outerHtml}\n\`\`\``);
  }
  return parts.join("\n");
}

export async function loadDesignModePages(onRender: () => void): Promise<void> {
  const backend = api();
  const ws = state.project?.root || ".";
  if (!backend?.DesignModeListPages) {
    state.designMode.error = "Backend CDP indisponível no modo preview.";
    onRender();
    return;
  }
  state.designMode.loadingPages = true;
  state.designMode.error = "";
  onRender();
  try {
    const pages = await backend.DesignModeListPages(ws);
    state.designMode.pages = Array.isArray(pages) ? pages : [];
    if (state.designMode.pages.length > 0 && state.designMode.pageIdx < 0) {
      state.designMode.pageIdx = state.designMode.pages[0].index;
    }
  } catch (err: any) {
    state.designMode.error = String(err?.message || err || "Falha ao listar abas do Chrome DevTools MCP.");
  } finally {
    state.designMode.loadingPages = false;
    onRender();
  }
}

export async function pickDesignModeElement(selector: string, onRender: () => void): Promise<void> {
  const backend = api();
  const ws = state.project?.root || ".";
  if (!backend?.DesignModePickElement) return;
  state.designMode.picking = true;
  state.designMode.error = "";
  if (!selector.trim()) {
    setStatus("Clique no elemento desejado na aba aberta do Chrome…");
  }
  onRender();
  try {
    const el = await backend.DesignModePickElement(ws, state.designMode.pageIdx, selector.trim());
    state.designMode.selected = el;
    state.designMode.open = false;
    setStatus(`Elemento selecionado via CDP: ${el.selector}`);
  } catch (err: any) {
    state.designMode.error = String(err?.message || err || "Falha ao inspecionar elemento no Chrome.");
  } finally {
    state.designMode.picking = false;
    onRender();
  }
}

export function renderDesignModeChipHtml(): string {
  const sel = state.designMode.selected;
  if (!sel) return "";
  return `
    <div class="dm-chip" title="${escapeHtml(sel.url || sel.selector)}">
      ${icons.target}
      <span>CDP: <code>${escapeHtml(sel.selector)}</code></span>
      ${sel.tag ? `<span class="dim">&lt;${escapeHtml(sel.tag)}&gt;</span>` : ""}
      <button type="button" class="icon-btn mini" id="btn-dm-clear-chip" title="Remover contexto Design Mode">${icons.closeSmall}</button>
    </div>
  `;
}

export function renderDesignModePopoverHtml(): string {
  const dm = state.designMode;
  if (!dm.open) return "";
  const pages: DesignModePage[] = dm.pages || [];

  let contentHtml = "";
  if (dm.loadingPages) {
    contentHtml = `<div class="dim" style="padding:10px">${icons.spinner} Conectando ao Chrome DevTools MCP…</div>`;
  } else if (dm.error) {
    contentHtml = `
      <div class="wt-err-bar">
        <span>${escapeHtml(dm.error)}</span>
        <button type="button" class="mini" id="btn-dm-retry">Tentar novamente</button>
      </div>
    `;
  } else if (pages.length === 0) {
    contentHtml = `<div class="dim" style="padding:8px">Nenhuma página aberta encontrada no Chrome DevTools MCP. Ative <code>chrome-devtools</code> no painel MCP e abra uma aba no navegador.</div>`;
  } else {
    contentHtml = `
      <label class="dim" style="font-size:11px">Aba do Chrome:</label>
      <select id="dm-page-select" class="wt-input">
        ${pages
          .map(
            (p) => `<option value="${p.index}" ${p.index === dm.pageIdx ? "selected" : ""}>${escapeHtml(p.title || p.url || `Aba ${p.index}`)}</option>`
          )
          .join("")}
      </select>
      <div class="wt-create-row" style="margin-top:6px">
        <input type="text" id="dm-selector-input" class="wt-input" placeholder="seletor CSS (opcional, vazio = clique interativo)" value="${escapeHtml(dm.selectorInput)}" />
        <button type="button" class="mini primary" id="btn-dm-pick" ${dm.picking ? "disabled" : ""}>
          ${dm.picking ? icons.spinner : icons.target}
          <span>${dm.picking ? "Selecionando…" : dm.selectorInput.trim() ? "Capturar" : "Selecionar no Browser"}</span>
        </button>
      </div>
    `;
  }

  return `
    <div class="dm-popover" id="design-mode-popover" role="dialog" aria-label="Design Mode via CDP">
      <div class="dm-popover-head">
        <strong>Design Mode (Chrome DevTools CDP)</strong>
        <div class="spacer"></div>
        <button type="button" class="icon-btn" id="btn-dm-refresh" title="Recarregar abas">${icons.refresh}</button>
        <button type="button" class="icon-btn" id="btn-dm-close" title="Fechar">${icons.closeSmall}</button>
      </div>
      ${contentHtml}
    </div>
  `;
}

export function bindDesignModeEvents(onRender: () => void): void {
  document.getElementById("btn-dm-clear-chip")?.addEventListener("click", () => {
    state.designMode.selected = null;
    onRender();
  });
  document.getElementById("btn-dm-close")?.addEventListener("click", () => {
    state.designMode.open = false;
    onRender();
  });
  document.getElementById("btn-dm-refresh")?.addEventListener("click", () => {
    void loadDesignModePages(onRender);
  });
  document.getElementById("btn-dm-retry")?.addEventListener("click", () => {
    void loadDesignModePages(onRender);
  });
  const selEl = document.getElementById("dm-page-select") as HTMLSelectElement | null;
  selEl?.addEventListener("change", () => {
    state.designMode.pageIdx = parseInt(selEl.value || "0", 10);
  });
  const inputEl = document.getElementById("dm-selector-input") as HTMLInputElement | null;
  inputEl?.addEventListener("input", () => {
    state.designMode.selectorInput = inputEl.value;
  });
  document.getElementById("btn-dm-pick")?.addEventListener("click", () => {
    void pickDesignModeElement(state.designMode.selectorInput, onRender);
  });
}
