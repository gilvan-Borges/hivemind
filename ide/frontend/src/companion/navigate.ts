import { rebuildChatMessagesFull, renderTokenBar, syncComposerButtons } from "../chat";
import { clearGitHubUnread } from "../git";
import { renderLaneBarOnly, renderPipelineBar } from "../lanes";
import { openResidentTab, residentState } from "../resident";
import { openSidebarView, syncLayoutVisibility, switchProjectTab, updateStatusBar } from "../shell";
import { state } from "../state";
import { updateMissionBoardIfOpen } from "../teamconfig";
import type { NoticeTarget } from "./logic";

export function openNoticeTarget(target: NoticeTarget): void {
  if (!target) return;
  if (target.surface === "resident") {
    residentState.subTab = "chat";
    openResidentTab();
    pulse(target.messageId);
    return;
  }
  if (target.surface === "github") {
    if (target.projectId && target.projectId !== state.activeProjectId) {
      switchProjectTab(target.projectId);
    }
    state.gitTab = "flow";
    clearGitHubUnread();
    openSidebarView("git");
    return;
  }
  if (target.projectId && target.projectId !== state.activeProjectId) {
    switchProjectTab(target.projectId);
  }
  state.showRightAI = true;
  syncLayoutVisibility();
  if (target.laneId) state.activeLaneId = target.laneId;
  renderLaneBarOnly();
  renderPipelineBar();
  renderTokenBar();
  rebuildChatMessagesFull();
  syncComposerButtons();
  updateStatusBar();
  updateMissionBoardIfOpen();
  pulse(target.messageId);
}

function pulse(id: string): void {
  if (!id || typeof document === "undefined") return;
  const mark = () => {
    const el = document.querySelector<HTMLElement>(`[data-msg-id="${CSS.escape(id)}"]`);
    if (!el) return;
    el.scrollIntoView({ block: "nearest" });
    el.classList.add("hm-target");
    window.setTimeout(() => el.classList.remove("hm-target"), 2400);
  };
  requestAnimationFrame(mark);
  window.setTimeout(mark, 360);
}
