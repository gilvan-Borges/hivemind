const NODES: Array<[number, number, boolean]> = [
  [38, 48, false],
  [50, 40, true],
  [62, 38, false],
  [74, 44, true],
  [80, 56, false],
  [34, 60, false],
  [46, 54, false],
  [58, 54, false],
  [70, 58, false],
  [42, 68, false],
  [54, 68, false],
  [68, 72, false],
  [52, 80, false],
  [46, 90, false],
];

const LINKS: Array<[number, number]> = [
  [0, 1], [1, 2], [2, 3], [3, 4], [0, 6], [0, 5], [5, 9], [6, 1], [6, 7],
  [7, 2], [7, 8], [8, 3], [8, 4], [9, 6], [9, 10], [10, 7], [10, 11], [11, 8],
  [9, 12], [10, 12], [11, 12], [12, 13],
];

function line(a: number, b: number): string {
  const [x1, y1] = NODES[a];
  const [x2, y2] = NODES[b];
  return `<line x1="${x1}" y1="${y1}" x2="${x2}" y2="${y2}"/>`;
}

/** Mascote vetorial: hexágono dourado e cérebro de nós. A cor vem de currentColor. */
export function mascotSVG(): string {
  const nodes = NODES.map(([x, y, eye], i) =>
    `<circle class="hm-node n${i}${eye ? " hm-eye" : ""}" cx="${x}" cy="${y}" r="${eye ? 3 : 2.4}"/>`
  ).join("");
  return `
    <svg class="hm-svg" viewBox="0 0 120 120" aria-hidden="true">
      <circle class="hm-glow" cx="60" cy="60" r="40"/>
      <polygon class="hm-hex" points="106,60 83,99.8 37,99.8 14,60 37,20.2 83,20.2"/>
      <g class="hm-brain">
        <g class="hm-links">${LINKS.map(([a, b]) => line(a, b)).join("")}</g>
        <g class="hm-nodes">${nodes}</g>
      </g>
    </svg>
  `;
}
