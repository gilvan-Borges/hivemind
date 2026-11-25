import { icons } from "./icons";
import { api, askConfirm, beginBusyOp, endBusyOp, setStatus, state, toast, toastError } from "./state";
import type { WorktreeCheckpoint, WorktreeInfo } from "./types";
import { escapeHtml, renderSkeletonCardsHtml } from "./utils";

export function formatBytesCompact(bytes: number): string {
  const b = Number(bytes || 0);
  if (b <= 0) return "0 B";
  if (b >= 1024 * 1024 * 1024) return `${(b / (1024 * 1024 * 1024)).toFixed(1)} GB`;
  if (b >= 1024 * 1024) return `${(b / (1024 * 1024)).toFixed(1)} MB`;
  if (b >= 1024) return `${(b / 1024).toFixed(1)} KB`;
  return `${b} B`;
}

export async function refreshWorktreesPanel(onRender: () => void) {
  const backend = api();
  const ws = state.project?.root || "";
  if (!backend?.ListWorktrees || !ws) return;
  state.worktrees.loading = true;
  state.worktrees.error = "";
  beginBusyOp("worktrees", "Carregando worktrees…");
  onRender();
  try {
    const [list, cps] = await Promise.all([
      backend.ListWorktrees(ws),
      backend.ListWorktreeCheckpoints ? backend.ListWorktreeCheckpoints(ws) : Promise.resolve([] as WorktreeCheckpoint[]),
    ]);
    state.worktrees.list = Array.isArray(list) ? list : [];
    state.worktrees.checkpoints = Array.isArray(cps) ? cps : [];
    state.worktrees.loadedOnce = true;
  } catch (err: any) {
    state.worktrees.error = String(err?.message || err || "Falha ao listar worktrees");
  } finally {
    state.worktrees.loading = false;
    endBusyOp("worktrees");
    onRender();
  }
}

export function renderWorktreesTabHtml(): string {
  const wt = state.worktrees;
  const progress = wt.progress;
  const list: WorktreeInfo[] = wt.list || [];
  const checkpoints: WorktreeCheckpoint[] = wt.checkpoints || [];

  const createBoxHtml = `
    <div class="wt-create-box">
      <div class="wt-create-row">
        <input type="text" id="wt-new-agent" class="wt-input" placeholder="agente (ex.: dev_1)" value="${escapeHtml(wt.newAgent)}" />
        <input type="text" id="wt-new-branch" class="wt-input" placeholder="branch (ex.: feat/ajuste)" value="${escapeHtml(wt.newBranch)}" />
        <button type="button" class="mini primary" id="btn-wt-create" ${wt.creating ? "disabled" : ""}>
          ${wt.creating ? icons.spinner : icons.plus}
          <span>Criar</span>
        </button>
      </div>
      ${
        progress
          ? `<div class="wt-progress">
               <div class="wt-progress-top">
                 <span>${escapeHtml(progress.step)}</span>
                 <span class="wt-num">${progress.pct}%</span>
               </div>
               <div class="wt-progress-track"><div class="wt-progress-bar" style="width:${Math.min(100, Math.max(0, progress.pct))}%"></div></div>
             </div>`
          : ""
      }
      ${
        wt.createError
          ? `<div class="wt-err-bar">
               <span>${escapeHtml(wt.createError)}</span>
               <button type="button" class="mini" id="btn-wt-retry-create">Tentar novamente</button>
             </div>`
          : ""
      }
    </div>
  `;

  let bodyHtml = "";
  if (wt.loading && !wt.loadedOnce) {
    bodyHtml = renderSkeletonCardsHtml(2, "Carregando worktrees e checkpoints…");
  } else if (wt.error) {
    bodyHtml = `
      <div class="wt-err-bar" style="margin-top:8px">
        <span>${escapeHtml(wt.error)}</span>
        <button type="button" class="mini" id="btn-wt-retry-load">Tentar novamente</button>
      </div>
    `;
  } else if (list.length === 0) {
    bodyHtml = `<div class="dim" style="padding:12px">Nenhuma worktree encontrada. Crie uma acima ou inicie uma tarefa do Squad.</div>`;
  } else {
    bodyHtml = `
      <div class="wt-list">
        ${list
          .map((item) => {
            const title = item.isMain ? "Repositório principal" : item.agent || item.path.split(/[\\/]/).pop() || "worktree";
            const badge = item.orphan
              ? `<span class="wt-badge warn">Órfã</span>`
              : item.prunable
              ? `<span class="wt-badge warn">Podável</span>`
              : item.isMain
              ? `<span class="wt-badge">Main</span>`
              : `<span class="wt-badge ok">Ativa</span>`;
            return `
              <div class="wt-card">
                <div class="wt-card-head">
                  ${icons.gitBranch}
                  <strong class="wt-card-title">${escapeHtml(title)}</strong>
                  ${badge}
                  <span class="wt-num dim">${formatBytesCompact(item.sizeBytes)}</span>
                </div>
                <div class="wt-card-meta dim">
                  <span>${escapeHtml(item.branch || (item.detached ? "HEAD destacado" : "-"))}</span>
                  <span title="${escapeHtml(item.path)}">${escapeHtml(item.path)}</span>
                </div>
                ${
                  !item.isMain
                    ? `<div class="wt-card-actions">
                         <button type="button" class="mini danger" data-wt-archive="${escapeHtml(item.path)}" title="Remover worktree gerenciada">${icons.trash}<span>Arquivar</span></button>
                       </div>`
                    : ""
                }
              </div>
            `;
          })
          .join("")}
      </div>
    `;
  }

  const cpHtml =
    checkpoints.length > 0
      ? `
        <div class="wt-section">
          <div class="git-section-head"><span>Checkpoints de Turno (${checkpoints.length})</span></div>
          <div class="wt-list">
            ${checkpoints
              .slice(0, 8)
              .map(
                (cp) => `
                  <div class="wt-card">
                    <div class="wt-card-head">
                      ${icons.sessions}
                      <strong class="wt-card-title">${escapeHtml(cp.label || cp.id)}</strong>
                      <span class="wt-num dim">${escapeHtml(cp.sha.slice(0, 7))}</span>
                    </div>
                    <div class="wt-card-actions">
                      <button type="button" class="mini" data-wt-rollback="${escapeHtml(cp.id)}">${icons.undo}<span>Rollback</span></button>
                    </div>
                  </div>
                `
              )
              .join("")}
          </div>
        </div>
      `
      : "";

  return `
    <div class="git-section wt-panel" style="padding:10px">
      <div class="git-section-head">
        <span>Worktrees (${list.length})</span>
        <div style="display:flex;gap:5px">
          <button type="button" class="mini" id="btn-wt-prune" title="Limpar refs antigas e diretórios órfãos">${icons.trash}<span>Prune</span></button>
          <button type="button" class="mini" id="btn-wt-refresh" ${wt.loading ? "disabled" : ""}>${wt.loading ? icons.spinner : icons.refresh}<span>Atualizar</span></button>
        </div>
      </div>
      ${createBoxHtml}
      ${bodyHtml}
      ${cpHtml}
    </div>
  `;
}

export function bindWorktreesTabEvents(onRender: () => void) {
  const ws = state.project?.root || "";
  const backend = api();

  document.getElementById("btn-wt-refresh")?.addEventListener("click", () => void refreshWorktreesPanel(onRender));
  document.getElementById("btn-wt-retry-load")?.addEventListener("click", () => void refreshWorktreesPanel(onRender));

  const runCreate = async () => {
    if (!backend?.CreateWorktreeAsync || !ws) return;
    const agentInput = (document.getElementById("wt-new-agent") as HTMLInputElement | null)?.value.trim() || "dev_1";
    const branchInput = (document.getElementById("wt-new-branch") as HTMLInputElement | null)?.value.trim() || "";
    if (!branchInput) {
      toast("Informe o nome da branch para criar a worktree.", "info");
      return;
    }
    state.worktrees.newAgent = agentInput;
    state.worktrees.newBranch = branchInput;
    state.worktrees.creating = true;
    state.worktrees.createError = "";
    state.worktrees.progress = { step: "iniciando", pct: 5 };
    onRender();
    try {
      await backend.CreateWorktreeAsync(ws, agentInput, branchInput);
      toast(`Worktree "${agentInput}" criada na branch ${branchInput}.`, "success");
      state.worktrees.newBranch = "";
      state.worktrees.progress = null;
      await refreshWorktreesPanel(onRender);
    } catch (err: any) {
      state.worktrees.createError = String(err?.message || err || "Erro ao criar worktree");
    } finally {
      state.worktrees.creating = false;
      onRender();
    }
  };

  document.getElementById("btn-wt-create")?.addEventListener("click", () => void runCreate());
  document.getElementById("btn-wt-retry-create")?.addEventListener("click", () => void runCreate());

  document.getElementById("btn-wt-prune")?.addEventListener("click", async () => {
    if (!backend?.PruneWorktrees || !ws) return;
    try {
      const res = await backend.PruneWorktrees(ws);
      toast(`Prune concluído (${res.prunedRefs || 0} refs, ${res.removedOrphans?.length || 0} órfãs).`, "success");
      await refreshWorktreesPanel(onRender);
    } catch (err: any) {
      toastError("podar worktrees", err);
    }
  });

  document.querySelectorAll<HTMLButtonElement>("[data-wt-archive]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const targetPath = btn.dataset.wtArchive || "";
      if (!targetPath || !backend?.ArchiveWorktree || !ws) return;
      askConfirm({
        title: "Arquivar Worktree",
        body: `Remover permanentemente a worktree gerenciada em "${targetPath}"?`,
        confirmLabel: "Arquivar",
        danger: true,
        onConfirm: async () => {
          try {
            await backend.ArchiveWorktree!(ws, targetPath);
            toast("Worktree arquivada.", "success");
            await refreshWorktreesPanel(onRender);
          } catch (err: any) {
            toastError("arquivar worktree", err);
          }
        },
      });
    });
  });

  document.querySelectorAll<HTMLButtonElement>("[data-wt-rollback]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const cpId = btn.dataset.wtRollback || "";
      if (!cpId || !backend?.RollbackWorktreeCheckpoint || !ws) return;
      askConfirm({
        title: "Rollback de Checkpoint",
        body: `Restaurar o estado salvo pelo checkpoint "${cpId}"? Alterações posteriores não commitadas serão substituídas.`,
        confirmLabel: "Restaurar",
        danger: true,
        onConfirm: async () => {
          try {
            await backend.RollbackWorktreeCheckpoint!(ws, "", cpId);
            setStatus(`Checkpoint ${cpId} restaurado.`);
            toast("Rollback concluído.", "success");
            await refreshWorktreesPanel(onRender);
          } catch (err: any) {
            toastError("restaurar checkpoint", err);
          }
        },
      });
    });
  });
}
