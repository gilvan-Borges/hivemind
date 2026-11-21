import { describe, expect, it } from "vitest";
import { getVisibleSplitLanes, toggleSplitLanes } from "./lanes";
import type { TeamLane } from "./types";

function mockLane(id: string, teamName = "principal"): TeamLane {
  return {
    id,
    teamName,
    sessionId: `sess-${id}`,
    running: false,
    stage: "",
    doneStages: [],
    autoScroll: true,
    turnInTokens: 0,
    turnOutTokens: 0,
    turnTotalTokens: 0,
    sessionInTokens: 0,
    sessionOutTokens: 0,
    sessionTotalTokens: 0,
    turnCostMicroUsd: 0,
    sessionCostMicroUsd: 0,
    costByRole: {},
    viewMode: "chat",
    agents: [],
    messages: [],
  };
}

describe("split panes & lane Chat/PTY viewMode (W6-06)", () => {
  it("alterna split com 2 lanes válidas e fecha quando já ativo", () => {
    const l1 = mockLane("lane-1", "frontend");
    const l2 = mockLane("lane-2", "backend");
    const l3 = mockLane("lane-3", "qa");

    const opened = toggleSplitLanes([l1, l2, l3], "lane-2", []);
    expect(opened).toEqual(["lane-2", "lane-1"]);

    const visible = getVisibleSplitLanes([l1, l2, l3], opened);
    expect(visible.map((x) => x.id)).toEqual(["lane-2", "lane-1"]);

    const closed = toggleSplitLanes([l1, l2, l3], "lane-2", opened);
    expect(closed).toEqual([]);
    expect(getVisibleSplitLanes([l1, l2, l3], closed)).toEqual([]);
  });

  it("não ativa split se houver apenas 1 lane ou IDs inválidos", () => {
    const l1 = mockLane("lane-1");
    expect(toggleSplitLanes([l1], "lane-1", [])).toEqual([]);
    expect(getVisibleSplitLanes([l1], ["lane-1", "missing"])).toEqual([]);
  });
});
