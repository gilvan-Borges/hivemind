import "./hivestriker.css";

export type HiveStrikerState = "idle" | "notify" | "working" | "standby" | "error";
export type HiveStrikerPalette = "obsidian" | "stealth" | "amber";

export function mapMoodToStrikerState(mood: string, pendingCount: number): HiveStrikerState {
  switch (mood) {
    case "error":
      return "error";
    case "working":
    case "thinking":
      return "working";
    case "attention":
    case "success":
      return "notify";
    case "sleep":
    case "standby":
      return "standby";
    default:
      return pendingCount > 0 ? "notify" : "idle";
  }
}

export function hiveStrikerSVG(): string {
  return `
    <div class="mascot-physics-wrap">
      <svg class="mascot-svg" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 240 265" role="img" aria-label="HiveStriker — Abelha Atlética">
        <defs>
          <filter id="hsShadow" x="-30%" y="-30%" width="160%" height="160%">
            <feDropShadow dx="0" dy="4" stdDeviation="3" flood-color="#020307" flood-opacity="0.52" />
          </filter>
          <filter id="hsBlur" x="-40%" y="-40%" width="180%" height="180%">
            <feGaussianBlur stdDeviation="3.2" />
          </filter>
          <filter id="hsGoldGlow" x="-60%" y="-60%" width="220%" height="220%">
            <feGaussianBlur stdDeviation="2.8" result="b" />
            <feMerge>
              <feMergeNode in="b" />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
          <filter id="hsStrongGlow" x="-80%" y="-80%" width="260%" height="260%">
            <feGaussianBlur stdDeviation="4.5" result="b" />
            <feMerge>
              <feMergeNode in="b" />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
          <radialGradient id="hsArmor" cx="35%" cy="22%" r="78%" fx="32%" fy="18%">
            <stop offset="0%" style="stop-color: var(--armor-hi)" />
            <stop offset="48%" style="stop-color: var(--armor-mid)" />
            <stop offset="88%" style="stop-color: var(--armor-deep)" />
            <stop offset="100%" style="stop-color: var(--armor-rim)" />
          </radialGradient>
          <linearGradient id="hsSuit" x1="20%" y1="0%" x2="82%" y2="100%">
            <stop offset="0%" style="stop-color: var(--suit-hi)" />
            <stop offset="52%" style="stop-color: var(--suit-mid)" />
            <stop offset="100%" style="stop-color: var(--suit-deep)" />
          </linearGradient>
          <linearGradient id="hsGold" x1="12%" y1="0%" x2="88%" y2="100%">
            <stop offset="0%" style="stop-color: var(--gold-hi)" />
            <stop offset="46%" style="stop-color: var(--gold-mid)" />
            <stop offset="100%" style="stop-color: var(--gold-deep)" />
          </linearGradient>
          <radialGradient id="hsEnergy" cx="50%" cy="50%" r="55%">
            <stop offset="0%" style="stop-color: var(--energy-core)" />
            <stop offset="55%" style="stop-color: var(--energy)" />
            <stop offset="100%" style="stop-color: var(--gold-deep)" stop-opacity="0" />
          </radialGradient>
          <radialGradient id="hsSkin" cx="46%" cy="34%" r="68%">
            <stop offset="0%" style="stop-color: var(--skin-hi)" />
            <stop offset="60%" style="stop-color: var(--skin-mid)" />
            <stop offset="100%" style="stop-color: var(--skin-deep)" />
          </radialGradient>
          <linearGradient id="hsBrowShade" x1="0%" y1="0%" x2="0%" y2="100%">
            <stop offset="0%" stop-color="#05070c" stop-opacity="0.52" />
            <stop offset="42%" stop-color="#05070c" stop-opacity="0.06" />
            <stop offset="100%" stop-color="#05070c" stop-opacity="0" />
          </linearGradient>
          <linearGradient id="hsWing" x1="0%" y1="0%" x2="100%" y2="100%">
            <stop offset="0%" style="stop-color: var(--wing-a)" />
            <stop offset="100%" style="stop-color: var(--wing-b)" />
          </linearGradient>
          <linearGradient id="hsVisorGlass" x1="10%" y1="0%" x2="90%" y2="100%">
            <stop offset="0%" stop-color="#272e3f" stop-opacity="0.92" />
            <stop offset="45%" style="stop-color: var(--visor-tint)" />
            <stop offset="100%" stop-color="#05070c" stop-opacity="0.95" />
          </linearGradient>
        </defs>

        <ellipse class="hm-ground-shadow" cx="120" cy="244" rx="48" ry="10.5" fill="#03050a" opacity="0.56" filter="url(#hsBlur)" />

        <g class="fx-radar-waves" fill="none" stroke="var(--energy)" stroke-width="2.2" stroke-linejoin="round">
          <polygon class="fx-radar-1" points="120,86 160,109 160,155 120,178 80,155 80,109" />
          <polygon class="fx-radar-2" points="120,86 160,109 160,155 120,178 80,155 80,109" />
        </g>

        <polygon class="fx-flex-burst" style="transform-origin: 120px 132px;"
                 points="120,76 168,104 168,160 120,188 72,160 72,104"
                 fill="none" stroke="var(--energy-core)" stroke-width="3.2" stroke-linejoin="round" filter="url(#hsStrongGlow)" />

        <g class="fx-working-orbit" filter="url(#hsGoldGlow)">
          <g class="fx-orbit-cw">
            <polygon points="212,118 218,121.5 218,128.5 212,132 206,128.5 206,121.5" fill="var(--energy-core)" stroke="var(--gold-mid)" stroke-width="1.2" />
            <polygon points="28,130 33,133 33,139 28,142 23,139 23,133" fill="var(--energy)" />
          </g>
          <g class="fx-orbit-ccw">
            <polygon points="120,18 125,21 125,27 120,30 115,27 115,21" fill="var(--energy-core)" />
            <polygon points="194,186 199,189 199,195 194,198 189,195 189,189" fill="var(--energy)" />
          </g>
        </g>

        <g class="hm-character">
          <g class="hm-wing-l" opacity="0.94">
            <path d="M 92 124 L 40 84 L 18 98 L 28 122 L 86 136 Z"
                  fill="url(#hsWing)" stroke="var(--wing-stroke)" stroke-width="1.6" stroke-linejoin="round" />
            <path d="M 40 84 L 54 114 L 86 130 M 26 106 L 54 114 L 44 126" fill="none" stroke="var(--gold-hi)" stroke-width="1" opacity="0.55" />
            <path d="M 88 138 L 36 134 L 26 150 L 46 158 L 86 146 Z"
                  fill="url(#hsWing)" stroke="var(--wing-stroke)" stroke-width="1.3" stroke-linejoin="round" opacity="0.82" />
          </g>
          <g class="hm-wing-r" opacity="0.94">
            <path d="M 148 124 L 200 84 L 222 98 L 212 122 L 154 136 Z"
                  fill="url(#hsWing)" stroke="var(--wing-stroke)" stroke-width="1.6" stroke-linejoin="round" />
            <path d="M 200 84 L 186 114 L 154 130 M 214 106 L 186 114 L 196 126" fill="none" stroke="var(--gold-hi)" stroke-width="1" opacity="0.55" />
            <path d="M 152 138 L 204 134 L 214 150 L 194 158 L 154 146 Z"
                  fill="url(#hsWing)" stroke="var(--wing-stroke)" stroke-width="1.3" stroke-linejoin="round" opacity="0.82" />
          </g>

          <g class="hm-leg-l" filter="url(#hsShadow)">
            <path d="M 92 178 C 86 192 86 206 90 214 L 113 214 C 115 202 115 190 113 178 Z" fill="url(#hsSuit)" />
            <polygon points="101,197 111,202 109,212 101,215 92,211 91,202" fill="url(#hsArmor)" stroke="var(--gold-mid)" stroke-width="1.1" stroke-opacity="0.7" />
            <path d="M 88 212 L 113 212 L 114 232 C 114 237 109 239 101 239 L 83 239 C 78 239 77 233 82 228 L 88 220 Z"
                  fill="url(#hsArmor)" stroke="var(--armor-hi)" stroke-width="1" />
            <path d="M 83 235 L 112 235" stroke="url(#hsGold)" stroke-width="2" stroke-linecap="round" />
          </g>

          <g class="hm-leg-r" filter="url(#hsShadow)">
            <path d="M 127 178 C 125 190 125 202 127 214 L 150 214 C 154 206 154 192 148 178 Z" fill="url(#hsSuit)" />
            <polygon points="139,197 149,202 148,211 139,215 131,212 129,202" fill="url(#hsArmor)" stroke="var(--gold-mid)" stroke-width="1.1" stroke-opacity="0.7" />
            <path d="M 127 212 L 152 212 L 152 220 L 158 228 C 163 233 162 239 157 239 L 139 239 C 131 239 126 237 126 232 Z"
                  fill="url(#hsArmor)" stroke="var(--armor-hi)" stroke-width="1" />
            <path d="M 128 235 L 157 235" stroke="url(#hsGold)" stroke-width="2" stroke-linecap="round" />
          </g>

          <g class="hm-upper-body">
            <g class="hm-arm-l" filter="url(#hsShadow)">
              <g>
                <path d="M 79 124 C 62 127 52 138 52 152 C 53 160 64 162 72 156 L 82 142 Z" fill="url(#hsSuit)" />
                <path d="M 60 137 Q 58 146 62 152" fill="none" stroke="#ffffff" stroke-width="1.8" stroke-linecap="round" opacity="0.18" />
                <path d="M 52 150 C 46 161 48 175 56 181 L 70 177 C 74 169 74 159 70 151 Z"
                      fill="url(#hsArmor)" stroke="var(--gold-mid)" stroke-width="1.2" stroke-opacity="0.75" />
                <path d="M 52 164 L 68 161" stroke="var(--energy)" stroke-width="2" stroke-linecap="round" />
                <path d="M 53 177 C 50 185 55 193 64 192 C 71 191 74 184 71 177 Z" fill="url(#hsArmor)" stroke="var(--armor-hi)" stroke-width="1.2" />
                <path d="M 56 185 L 67 184" stroke="var(--gold-mid)" stroke-width="1.6" stroke-linecap="round" />
                <path d="M 84 114 C 67 110 56 122 57 136 C 64 139 76 138 83 132 Z" fill="url(#hsArmor)" stroke="url(#hsGold)" stroke-width="1.4" />
              </g>
            </g>

            <g class="hm-arm-r" filter="url(#hsShadow)">
              <g>
                <circle class="fx-fist-energy" cx="180" cy="182" r="22" fill="url(#hsEnergy)" filter="url(#hsStrongGlow)" />
                <path d="M 161 124 C 178 127 188 138 188 152 C 187 160 176 162 168 156 L 158 142 Z" fill="url(#hsSuit)" />
                <path d="M 180 137 Q 182 146 178 152" fill="none" stroke="#ffffff" stroke-width="1.8" stroke-linecap="round" opacity="0.18" />
                <path d="M 188 150 C 194 161 192 175 184 181 L 170 177 C 166 169 166 159 170 151 Z"
                      fill="url(#hsArmor)" stroke="var(--gold-mid)" stroke-width="1.2" stroke-opacity="0.75" />
                <path d="M 188 164 L 172 161" stroke="var(--energy)" stroke-width="2" stroke-linecap="round" />
                <path d="M 187 177 C 190 185 185 193 176 192 C 169 191 166 184 169 177 Z" fill="url(#hsArmor)" stroke="var(--armor-hi)" stroke-width="1.2" />
                <path d="M 184 185 L 173 184" stroke="var(--gold-mid)" stroke-width="1.6" stroke-linecap="round" />
                <path d="M 156 114 C 173 110 184 122 183 136 C 176 139 164 138 157 132 Z" fill="url(#hsArmor)" stroke="url(#hsGold)" stroke-width="1.4" />
              </g>
            </g>

            <g filter="url(#hsShadow)">
              <path d="M 76 112 C 92 104 148 104 164 112 L 154 146 L 146 178 C 132 184 108 184 94 178 L 86 146 Z"
                    fill="url(#hsSuit)" stroke="var(--armor-rim)" stroke-width="1.1" stroke-opacity="0.55" />
              <path d="M 80 116 C 92 110 112 112 118 122 L 117 141 C 102 143 87 139 81 131 Z"
                    fill="url(#hsArmor)" stroke="var(--armor-hi)" stroke-width="1" stroke-opacity="0.55" />
              <path d="M 160 116 C 148 110 128 112 122 122 L 123 141 C 138 143 153 139 159 131 Z"
                    fill="url(#hsArmor)" stroke="var(--armor-hi)" stroke-width="1" stroke-opacity="0.55" />
              <path d="M 86 118 Q 100 114 112 119" fill="none" stroke="#ffffff" stroke-width="1.6" stroke-linecap="round" opacity="0.22" />
              <path d="M 154 118 Q 140 114 128 119" fill="none" stroke="#ffffff" stroke-width="1.6" stroke-linecap="round" opacity="0.22" />
              <path d="M 92 146 Q 120 151 148 146 L 146 153 Q 120 158 94 153 Z" fill="url(#hsGold)" opacity="0.92" />
              <path d="M 95 159 Q 120 163 145 159 L 144 165 Q 120 169 96 165 Z" fill="url(#hsGold)" opacity="0.82" />
              <line x1="120" y1="143" x2="120" y2="173" stroke="#06080d" stroke-width="2" opacity="0.65" />
              <path d="M 92 172 Q 120 178 148 172 L 147 183 Q 120 189 93 183 Z" fill="url(#hsArmor)" stroke="var(--gold-mid)" stroke-width="1.1" stroke-opacity="0.65" />
              <polygon points="120,173 128,177 128,184 120,188 112,184 112,177" fill="url(#hsGold)" stroke="#11131a" stroke-width="1.2" />
              <g class="hm-chest-core" filter="url(#hsGoldGlow)">
                <polygon points="120,118 132,125 132,139 120,146 108,139 108,125"
                         fill="#0b0d13" stroke="url(#hsGold)" stroke-width="2.2" stroke-linejoin="round" />
                <g stroke="var(--energy)" stroke-width="1.1" opacity="0.95">
                  <line x1="115" y1="128" x2="125" y2="128" />
                  <line x1="120" y1="124" x2="120" y2="140" />
                  <line x1="115" y1="128" x2="120" y2="140" />
                  <line x1="125" y1="128" x2="120" y2="140" />
                </g>
                <circle cx="115" cy="128" r="1.6" fill="var(--energy-core)" />
                <circle cx="125" cy="128" r="1.6" fill="var(--energy-core)" />
                <circle cx="120" cy="132" r="2.2" fill="#ffffff" />
                <circle cx="120" cy="140" r="1.6" fill="var(--energy-core)" />
              </g>
            </g>

            <g class="hm-head-group">
              <path d="M 104 102 L 136 102 L 139 114 L 101 114 Z" fill="url(#hsSuit)" />
              <path d="M 96 106 Q 120 116 144 106" fill="none" stroke="url(#hsGold)" stroke-width="2" stroke-linecap="round" opacity="0.75" />

              <g filter="url(#hsShadow)">
                <path d="M 80 68 C 80 42 98 33 120 33 C 142 33 160 42 160 68 L 157 94 C 154 103 144 109 132 111 L 108 111 C 96 109 86 103 83 94 Z"
                      fill="url(#hsArmor)" stroke="var(--armor-hi)" stroke-width="1.2" />
                <ellipse cx="106" cy="46" rx="20" ry="7" transform="rotate(-12 106 46)" fill="#ffffff" opacity="0.16" filter="url(#hsBlur)" />
                <path d="M 82 70 L 90 74 L 93 96 L 85 92 Z" fill="url(#hsSuit)" stroke="var(--gold-mid)" stroke-width="0.9" stroke-opacity="0.55" />
                <path d="M 158 70 L 150 74 L 147 96 L 155 92 Z" fill="url(#hsSuit)" stroke="var(--gold-mid)" stroke-width="0.9" stroke-opacity="0.55" />
              </g>

              <g filter="url(#hsGoldGlow)">
                <path d="M 97 46 Q 82 28 65 28" fill="none" stroke="url(#hsGold)" stroke-width="3.2" stroke-linecap="round" />
                <circle cx="64" cy="28" r="3.4" fill="var(--energy-core)" stroke="var(--gold-mid)" stroke-width="1.3" />
                <path d="M 143 46 Q 158 28 175 28" fill="none" stroke="url(#hsGold)" stroke-width="3.2" stroke-linecap="round" />
                <circle cx="176" cy="28" r="3.4" fill="var(--energy-core)" stroke="var(--gold-mid)" stroke-width="1.3" />
              </g>
              <path d="M 120 35 L 120 56" stroke="url(#hsGold)" stroke-width="2" stroke-linecap="round" opacity="0.72" />

              <g class="hm-face-track">
                <path d="M 91 67 C 102 62 138 62 149 67 L 147 91 C 145 99 135 105 120 106 C 105 105 95 99 93 91 Z"
                      fill="url(#hsSkin)" />
                <path d="M 91 67 C 102 62 138 62 149 67 L 148 80 C 132 76 108 76 92 80 Z"
                      fill="url(#hsBrowShade)" />
                <path d="M 118 82 L 120 87 L 122 86" fill="none" stroke="#8a5e3f" stroke-width="1.4" stroke-linecap="round" opacity="0.6" />
                <path d="M 114 101 Q 120 103 126 101" fill="none" stroke="#8a5e3f" stroke-width="1.3" stroke-linecap="round" opacity="0.45" />

                <g class="hm-eyes-group">
                  <g class="hm-pupils-track">
                    <path d="M 96 76 L 114 79 L 112 85 L 98 84 Z" fill="#141114" />
                    <path d="M 144 76 L 126 79 L 128 85 L 142 84 Z" fill="#141114" />
                    <path d="M 99 78 L 112 80 L 110 84 L 100 83 Z" fill="var(--eye-glow)" filter="url(#hsGoldGlow)" />
                    <path d="M 141 78 L 128 80 L 130 84 L 140 83 Z" fill="var(--eye-glow)" filter="url(#hsGoldGlow)" />
                    <circle cx="106" cy="81" r="1.8" fill="var(--eye-core)" />
                    <circle cx="134" cy="81" r="1.8" fill="var(--eye-core)" />
                  </g>
                </g>

                <g class="expr-brows-normal" fill="none" stroke="#181210" stroke-width="3.4" stroke-linecap="round">
                  <path d="M 95 72 L 114 76.5" />
                  <path d="M 145 72 L 126 76.5" />
                </g>
                <g class="expr-brows-error" fill="none" stroke="#181210" stroke-width="3.8" stroke-linecap="round">
                  <path d="M 95 70 L 115 78.5" />
                  <path d="M 145 70 L 125 78.5" />
                </g>

                <path d="M 111 94.5 Q 121 97.5 131 92.2" fill="none" stroke="#362218" stroke-width="2.5" stroke-linecap="round" />

                <g class="hm-visor-layer">
                  <path d="M 89 72 Q 120 67 151 72 L 148 87 Q 134 90 124 85 L 120 83 L 116 85 Q 106 90 92 87 Z"
                        fill="url(#hsVisorGlass)" stroke="var(--visor-rim)" stroke-width="1.6" stroke-linejoin="round" />
                  <path d="M 101 72 L 111 71 L 103 86 L 95 86 Z" fill="#ffffff" opacity="0.22" />
                  <path d="M 97 80 L 112 81.5" stroke="var(--energy-core)" stroke-width="2" stroke-linecap="round" filter="url(#hsGoldGlow)" opacity="0.92" />
                  <path d="M 143 80 L 128 81.5" stroke="var(--energy-core)" stroke-width="2" stroke-linecap="round" filter="url(#hsGoldGlow)" opacity="0.92" />
                </g>
              </g>
            </g>
          </g>
        </g>

        <g class="fx-notify-badge">
          <polygon class="fx-notify-ping" points="194,28 208,36 208,52 194,60 180,52 180,36"
                   fill="none" stroke="var(--energy)" stroke-width="2.4" stroke-linejoin="round" />
          <polygon points="194,29 207,36.5 207,51.5 194,59 181,51.5 181,36.5"
                   fill="var(--badge-bg)" stroke="var(--gold-mid)" stroke-width="2.4" stroke-linejoin="round" filter="url(#hsGoldGlow)" />
          <path d="M 194 36.5 L 194 46.5" stroke="var(--energy-core)" stroke-width="3" stroke-linecap="round" />
          <circle cx="194" cy="52" r="2" fill="var(--energy-core)" />
        </g>

        <g class="fx-error-badge">
          <polygon class="fx-error-ping" points="194,28 208,36 208,52 194,60 180,52 180,36"
                   fill="none" stroke="#ff5c4d" stroke-width="2.6" stroke-linejoin="round" />
          <polygon points="194,29 207,36.5 207,51.5 194,59 181,51.5 181,36.5"
                   fill="#1d0b0d" stroke="#ff5c4d" stroke-width="2.4" stroke-linejoin="round" filter="url(#hsGoldGlow)" />
          <path d="M 194 36.5 L 194 46.5" stroke="#ffd2cc" stroke-width="3" stroke-linecap="round" />
          <circle cx="194" cy="52" r="2" fill="#ffd2cc" />
        </g>
      </svg>
    </div>
  `;
}

export interface StrikerLifeController {
  fistNow(): void;
  flexNow(): void;
  destroy(): void;
}

export function attachStrikerLife(el: HTMLElement): StrikerLifeController {
  const pupils = el.querySelector<SVGGElement>(".hm-pupils-track");
  const face = el.querySelector<SVGGElement>(".hm-face-track");
  let alive = true;
  let blinkTimer = 0;
  let rafId = 0;
  const pointer = { x: window.innerWidth / 2, y: window.innerHeight / 2, on: false };
  const cur = { px: 0, py: 0, fx: 0, fy: 0 };

  const onMove = (e: MouseEvent) => {
    pointer.x = e.clientX;
    pointer.y = e.clientY;
    pointer.on = true;
  };
  const onLeave = () => {
    pointer.on = false;
  };
  window.addEventListener("mousemove", onMove);
  document.addEventListener("mouseleave", onLeave);

  const scheduleBlink = () => {
    if (!alive) return;
    blinkTimer = window.setTimeout(() => {
      if (!alive) return;
      if (el.getAttribute("data-state") !== "standby") {
        el.classList.add("is-blinking");
        window.setTimeout(() => el.classList.remove("is-blinking"), 120);
      }
      scheduleBlink();
    }, 2800 + Math.random() * 3400);
  };
  scheduleBlink();

  const tick = () => {
    if (!alive) return;
    const r = el.getBoundingClientRect();
    let tx = 0;
    let ty = 0;
    if (pointer.on && el.getAttribute("data-state") !== "standby" && r.width > 0) {
      const dx = pointer.x - (r.left + r.width / 2);
      const dy = pointer.y - (r.top + r.height * 0.34);
      const d = Math.hypot(dx, dy) || 1;
      const s = Math.min(1, d / 200);
      tx = (dx / d) * s;
      ty = (dy / d) * s;
    }
    cur.px += (tx * 3.2 - cur.px) * 0.14;
    cur.py += (ty * 2.1 - cur.py) * 0.14;
    cur.fx += (tx * 2.1 - cur.fx) * 0.1;
    cur.fy += (ty * 1.4 - cur.fy) * 0.1;
    if (pupils) pupils.setAttribute("transform", `translate(${cur.px.toFixed(2)},${cur.py.toFixed(2)})`);
    if (face) face.setAttribute("transform", `translate(${cur.fx.toFixed(2)},${cur.fy.toFixed(2)})`);
    rafId = requestAnimationFrame(tick);
  };
  rafId = requestAnimationFrame(tick);

  return {
    fistNow() {
      el.classList.remove("is-fist-action");
      void el.offsetWidth;
      el.classList.add("is-fist-action");
      window.setTimeout(() => el.classList.remove("is-fist-action"), 880);
    },
    flexNow() {
      el.classList.remove("is-flexing");
      void el.offsetWidth;
      el.classList.add("is-flexing");
      window.setTimeout(() => el.classList.remove("is-flexing"), 940);
      const colors = ["#fff2b2", "#f5c842", "#ffaa24", "#ffffff"];
      for (let i = 0; i < 12; i++) {
        const s = document.createElement("div");
        s.className = "surge-particle";
        const a = (Math.PI * 2 * i) / 12;
        const dist = 42 + Math.random() * 34;
        s.style.setProperty("--tx", `${(Math.cos(a) * dist).toFixed(1)}px`);
        s.style.setProperty("--ty", `${(Math.sin(a) * dist - 6).toFixed(1)}px`);
        s.style.setProperty("--rot", `${Math.round((Math.random() - 0.5) * 300)}deg`);
        s.innerHTML = `<svg width="8" height="8" viewBox="0 0 12 12"><polygon points="6,0.8 11,3.5 11,8.5 6,11.2 1,8.5 1,3.5" fill="${colors[i % colors.length]}"/></svg>`;
        el.appendChild(s);
        window.setTimeout(() => s.remove(), 860);
      }
    },
    destroy() {
      alive = false;
      if (blinkTimer) window.clearTimeout(blinkTimer);
      if (rafId) cancelAnimationFrame(rafId);
      window.removeEventListener("mousemove", onMove);
      document.removeEventListener("mouseleave", onLeave);
    },
  };
}
