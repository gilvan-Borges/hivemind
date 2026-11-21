import "./hivebee.css";

export type HiveBeeState = "idle" | "notify" | "working" | "sleep" | "error";
export type HiveBeeTheme = "honey" | "gold";

export function mapMoodToBeeState(mood: string, pendingCount: number): HiveBeeState {
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
      return "sleep";
    default:
      return pendingCount > 0 ? "notify" : "idle";
  }
}

export function hiveBeeSVG(): string {
  return `
    <div class="mascot-physics-wrap">
      <svg class="mascot-svg" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 220 240" role="img" aria-label="HiveMind Mascote Chibi 3D">
        <defs>
          <filter id="plushShadow" x="-30%" y="-30%" width="160%" height="160%">
            <feDropShadow dx="0" dy="4" stdDeviation="3.2" flood-color="#0a0502" flood-opacity="0.34" />
          </filter>
          <filter id="softBlur" x="-40%" y="-40%" width="180%" height="180%">
            <feGaussianBlur stdDeviation="3.4" />
          </filter>
          <filter id="goldGlow" x="-50%" y="-50%" width="200%" height="200%">
            <feGaussianBlur stdDeviation="2.6" result="b" />
            <feMerge>
              <feMergeNode in="b" />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
          <filter id="badgeGlow" x="-60%" y="-60%" width="220%" height="220%">
            <feGaussianBlur stdDeviation="3.2" result="b" />
            <feMerge>
              <feMergeNode in="b" />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
          <radialGradient id="gHood" cx="35%" cy="24%" r="76%" fx="30%" fy="20%">
            <stop offset="0%" style="stop-color: var(--hood-hi)" />
            <stop offset="46%" style="stop-color: var(--hood-mid)" />
            <stop offset="86%" style="stop-color: var(--hood-deep)" />
            <stop offset="100%" style="stop-color: var(--hood-rim)" />
          </radialGradient>
          <radialGradient id="gSuit" cx="38%" cy="28%" r="74%" fx="34%" fy="22%">
            <stop offset="0%" style="stop-color: var(--suit-hi)" />
            <stop offset="48%" style="stop-color: var(--suit-mid)" />
            <stop offset="88%" style="stop-color: var(--suit-deep)" />
            <stop offset="100%" style="stop-color: var(--suit-rim)" />
          </radialGradient>
          <radialGradient id="gTuft" cx="32%" cy="24%" r="75%">
            <stop offset="0%" style="stop-color: var(--tuft-hi)" />
            <stop offset="52%" style="stop-color: var(--tuft-mid)" />
            <stop offset="100%" style="stop-color: var(--tuft-deep)" />
          </radialGradient>
          <linearGradient id="gStripe" x1="15%" y1="0%" x2="88%" y2="100%">
            <stop offset="0%" style="stop-color: var(--stripe-hi)" />
            <stop offset="50%" style="stop-color: var(--stripe-mid)" />
            <stop offset="100%" style="stop-color: var(--stripe-deep)" />
          </linearGradient>
          <radialGradient id="gDimple" cx="55%" cy="62%" r="68%">
            <stop offset="0%" style="stop-color: var(--dimple-hi)" />
            <stop offset="70%" style="stop-color: var(--dimple-deep)" />
            <stop offset="100%" style="stop-color: var(--dimple-deep)" />
          </radialGradient>
          <radialGradient id="gSkin" cx="42%" cy="32%" r="72%">
            <stop offset="0%" style="stop-color: var(--skin-hi)" />
            <stop offset="62%" style="stop-color: var(--skin-mid)" />
            <stop offset="100%" style="stop-color: var(--skin-deep)" />
          </radialGradient>
          <linearGradient id="gFaceRecess" x1="0%" y1="0%" x2="0%" y2="100%">
            <stop offset="0%" stop-color="#472308" stop-opacity="0.28" />
            <stop offset="22%" stop-color="#472308" stop-opacity="0.03" />
            <stop offset="82%" stop-color="#ffffff" stop-opacity="0" />
            <stop offset="100%" stop-color="#8c4c1c" stop-opacity="0.16" />
          </linearGradient>
          <radialGradient id="gEye" cx="38%" cy="32%" r="68%">
            <stop offset="0%" stop-color="#3a2f2a" />
            <stop offset="55%" stop-color="#17110e" />
            <stop offset="100%" stop-color="#070504" />
          </radialGradient>
          <radialGradient id="gBlush" cx="50%" cy="50%" r="50%">
            <stop offset="0%" style="stop-color: var(--blush-color)" stop-opacity="0.58" />
            <stop offset="65%" style="stop-color: var(--blush-color)" stop-opacity="0.22" />
            <stop offset="100%" style="stop-color: var(--blush-color)" stop-opacity="0" />
          </radialGradient>
          <radialGradient id="gWing" cx="35%" cy="30%" r="75%">
            <stop offset="0%" stop-color="#ffffff" stop-opacity="0.88" />
            <stop offset="50%" stop-color="#e8f3ff" stop-opacity="0.54" />
            <stop offset="100%" stop-color="#ffd97d" stop-opacity="0.28" />
          </radialGradient>
          <radialGradient id="gSweat" cx="35%" cy="30%" r="70%">
            <stop offset="0%" stop-color="#e0f7ff" />
            <stop offset="55%" stop-color="#56c7ff" />
            <stop offset="100%" stop-color="#1e78c8" />
          </radialGradient>
          <clipPath id="torsoClip">
            <path d="M 80 144 C 72 158 70 182 77 195 C 84 205 136 205 143 195 C 150 182 148 158 140 144 Z" />
          </clipPath>
        </defs>

        <ellipse class="mc-ground-shadow" cx="110" cy="218" rx="44" ry="10.5" fill="#05060a" opacity="0.52" filter="url(#softBlur)" />

        <g class="fx-orbit-bees">
          <g class="fx-orbit-ring">
            <circle cx="182" cy="88" r="4.2" fill="#ffe27a" filter="url(#goldGlow)" />
            <circle cx="181" cy="87" r="1.6" fill="#ffffff" />
            <circle cx="38" cy="112" r="3.4" fill="#f5c842" filter="url(#goldGlow)" />
          </g>
          <g class="fx-orbit-ring-rev">
            <polygon points="110,20 114,22.5 114,27 110,29.5 106,27 106,22.5" fill="#fff2b2" filter="url(#goldGlow)" />
          </g>
        </g>

        <g class="mc-character">
          <g class="mc-wing-l" opacity="0.95">
            <path d="M 78 156 C 50 132 24 142 30 162 C 35 178 62 174 79 164 Z"
                  fill="url(#gWing)" stroke="#fff6d6" stroke-width="1.6" stroke-opacity="0.72" />
            <path d="M 72 158 C 56 148 42 152 38 159" fill="none" stroke="#ffffff" stroke-width="1.3" stroke-linecap="round" opacity="0.65" />
          </g>
          <g class="mc-wing-r" opacity="0.95">
            <path d="M 142 156 C 170 132 196 142 190 162 C 185 178 158 174 141 164 Z"
                  fill="url(#gWing)" stroke="#fff6d6" stroke-width="1.6" stroke-opacity="0.72" />
            <path d="M 148 158 C 164 148 178 152 182 159" fill="none" stroke="#ffffff" stroke-width="1.3" stroke-linecap="round" opacity="0.65" />
          </g>

          <g class="mc-leg-l" filter="url(#plushShadow)">
            <path d="M 84 188 C 80 202 82 214 95 214 C 107 214 108 202 106 189 Z" fill="url(#gSuit)" />
            <ellipse cx="91" cy="204" rx="5" ry="3" fill="#ffffff" opacity="0.16" />
          </g>
          <g class="mc-leg-r" filter="url(#plushShadow)">
            <path d="M 114 189 C 112 202 113 214 125 214 C 138 214 140 202 136 188 Z" fill="url(#gSuit)" />
            <ellipse cx="123" cy="204" rx="5" ry="3" fill="#ffffff" opacity="0.16" />
          </g>

          <g class="mc-arm-l" filter="url(#plushShadow)">
            <path d="M 80 152 C 63 157 52 169 57 180 C 61 189 75 182 84 171 Z" fill="url(#gSuit)" />
            <circle cx="56" cy="180" r="11.5" fill="url(#gSkin)" />
            <circle cx="64" cy="174" r="5.2" fill="url(#gSkin)" />
            <ellipse cx="53" cy="176" rx="4.2" ry="2.8" transform="rotate(-24 53 176)" fill="#ffffff" opacity="0.45" />
          </g>

          <g class="mc-arm-r" filter="url(#plushShadow)">
            <path d="M 140 152 C 156 146 168 138 173 147 C 178 156 163 172 141 171 Z" fill="url(#gSuit)" />
            <circle cx="174" cy="142" r="12.2" fill="url(#gSkin)" />
            <circle cx="165" cy="138" r="5.4" fill="url(#gSkin)" />
            <ellipse cx="171" cy="137" rx="4.4" ry="2.8" transform="rotate(-22 171 137)" fill="#ffffff" opacity="0.48" />
          </g>

          <g filter="url(#plushShadow)">
            <path d="M 80 144 C 72 158 70 182 77 195 C 84 205 136 205 143 195 C 150 182 148 158 140 144 Z" fill="url(#gSuit)" />
            <ellipse cx="97" cy="164" rx="14" ry="18" transform="rotate(-12 97 164)" fill="#ffffff" opacity="0.14" filter="url(#softBlur)" />
            <g clip-path="url(#torsoClip)">
              <path d="M 66 164 Q 110 173 154 164 L 154 176 Q 110 185 66 176 Z" fill="url(#gStripe)" />
              <path d="M 66 184 Q 110 192 154 184 L 154 196 Q 110 203 66 196 Z" fill="url(#gStripe)" />
            </g>
            <g transform="translate(110 168)" filter="url(#goldGlow)">
              <polygon points="0,-10 9,-5 9,5 0,10 -9,5 -9,-5" fill="var(--emblem-fill)" stroke="var(--emblem-stroke)" stroke-width="1.8" stroke-linejoin="round" />
              <g stroke="var(--emblem-glow)" stroke-width="0.9" opacity="0.9">
                <line x1="-3.5" y1="-3" x2="3.5" y2="-3" />
                <line x1="-3.5" y1="-3" x2="0" y2="4" />
                <line x1="3.5" y1="-3" x2="0" y2="4" />
              </g>
              <circle cx="-3.5" cy="-3" r="1.4" fill="var(--emblem-glow)" />
              <circle cx="3.5" cy="-3" r="1.4" fill="var(--emblem-glow)" />
              <circle cx="0" cy="4" r="1.5" fill="#ffffff" />
            </g>
          </g>

          <g class="mc-head-group">
            <g filter="url(#plushShadow)">
              <rect x="44" y="54" width="132" height="106" rx="48" ry="48" fill="url(#gHood)" />
              <ellipse cx="86" cy="70" rx="34" ry="11" transform="rotate(-8 86 70)" fill="#ffffff" opacity="0.22" filter="url(#softBlur)" />
              <path d="M 74 154 Q 110 162 146 154" fill="none" stroke="var(--hood-rim)" stroke-width="2.5" stroke-linecap="round" opacity="0.45" />
            </g>

            <g class="mc-tuft-group" filter="url(#plushShadow)">
              <path d="M 65 65 C 57 49 87 30 116 32 C 130 33 132 45 117 55 C 102 65 73 74 65 65 Z" fill="url(#gTuft)" />
              <ellipse cx="94" cy="44" rx="16" ry="5" transform="rotate(-16 94 44)" fill="#ffffff" opacity="0.24" filter="url(#softBlur)" />
              <path d="M 73 75 C 65 64 90 36 121 31 C 135 29 138 41 124 55 C 108 71 81 84 73 75 Z" fill="url(#gTuft)" />
              <path d="M 85 59 C 96 47 110 39 122 37" fill="none" stroke="#fff7d1" stroke-width="3.2" stroke-linecap="round" opacity="0.46" filter="url(#softBlur)" />
            </g>

            <g class="mc-face-track">
              <rect x="64" y="75" width="92" height="72" rx="31" ry="31" fill="url(#gSkin)" />
              <rect x="64" y="75" width="92" height="72" rx="31" ry="31" fill="url(#gFaceRecess)" />
              <ellipse cx="78" cy="123" rx="11.5" ry="7.2" fill="url(#gBlush)" />
              <ellipse cx="142" cy="123" rx="11.5" ry="7.2" fill="url(#gBlush)" />

              <g class="expr-brows-normal" fill="none" stroke="#362216" stroke-width="3" stroke-linecap="round">
                <path d="M 81 92 Q 87 87 93 90" />
                <path d="M 127 90 Q 133 87 139 92" />
              </g>
              <g class="expr-brows-work" fill="none" stroke="#362216" stroke-width="3.2" stroke-linecap="round">
                <path d="M 81 89 L 95 94" />
                <path d="M 139 89 L 125 94" />
              </g>
              <g class="expr-brows-error" fill="none" stroke="#362216" stroke-width="3.1" stroke-linecap="round">
                <path d="M 80 95 Q 87 92 94 87" />
                <path d="M 140 95 Q 133 92 126 87" />
              </g>

              <g class="mc-eyes-open">
                <g class="mc-pupils-track">
                  <ellipse cx="89" cy="110" rx="8.4" ry="11.4" fill="url(#gEye)" />
                  <circle cx="85.8" cy="105.2" r="3.1" fill="#ffffff" />
                  <circle cx="92.2" cy="115.2" r="1.4" fill="#ffffff" opacity="0.72" />
                  <ellipse cx="131" cy="110" rx="8.4" ry="11.4" fill="url(#gEye)" />
                  <circle cx="127.8" cy="105.2" r="3.1" fill="#ffffff" />
                  <circle cx="134.2" cy="115.2" r="1.4" fill="#ffffff" opacity="0.72" />
                </g>
              </g>

              <g class="expr-sleep-eyes" fill="none" stroke="#2a1910" stroke-width="3.4" stroke-linecap="round">
                <path d="M 80 112 Q 89 119 98 112" />
                <path d="M 122 112 Q 131 119 140 112" />
              </g>

              <path class="expr-mouth-normal" d="M 103 122 Q 110 129.5 117 122" fill="none" stroke="#2d1a10" stroke-width="3.2" stroke-linecap="round" />
              <g class="expr-mouth-open">
                <path d="M 101 120 Q 110 135 119 120 Z" fill="#5e211b" stroke="#2d1a10" stroke-width="2.4" stroke-linejoin="round" />
                <ellipse cx="110" cy="127.5" rx="4.8" ry="2.6" fill="#ff7d7d" />
              </g>
              <path class="expr-mouth-work" d="M 105 124 Q 110 126.5 115 123.5" fill="none" stroke="#2d1a10" stroke-width="3" stroke-linecap="round" />
              <ellipse class="expr-mouth-sleep" cx="110" cy="125" rx="3.2" ry="2.2" fill="#7a3e30" opacity="0.75" />
              <ellipse class="expr-mouth-error" cx="110" cy="125.5" rx="4.4" ry="5.2" fill="#4f1e1a" stroke="#2d1a10" stroke-width="2.2" />

              <g class="expr-sweat">
                <path d="M 149 84 C 149 84 155 93 155 97.5 C 155 101 152.3 103.5 149 103.5 C 145.7 103.5 143 101 143 97.5 C 143 93 149 84 149 84 Z"
                      fill="url(#gSweat)" stroke="#ffffff" stroke-width="0.9" stroke-opacity="0.7" />
                <circle cx="147.2" cy="96" r="1.5" fill="#ffffff" opacity="0.85" />
              </g>
            </g>
          </g>
        </g>

        <g class="fx-notify-badge">
          <polygon class="fx-notify-ring" points="170,28 184,36 184,52 170,60 156,52 156,36"
                   fill="none" stroke="#ffe066" stroke-width="2.6" stroke-linejoin="round" />
          <polygon points="170,29 183,36.5 183,51.5 170,59 157,51.5 157,36.5"
                   fill="var(--badge-bg)" stroke="#ffd64f" stroke-width="2.6" stroke-linejoin="round" filter="url(#badgeGlow)" />
          <path d="M 170 36.5 L 170 46.5" stroke="#fff4bd" stroke-width="3" stroke-linecap="round" />
          <circle cx="170" cy="51.8" r="2" fill="#fff4bd" />
        </g>

        <g class="fx-sparkles" fill="#ffe680" filter="url(#goldGlow)">
          <path class="fx-spark-1" style="transform-origin: 42px 46px;" d="M 42 38 L 44.2 43.8 L 50 46 L 44.2 48.2 L 42 54 L 39.8 48.2 L 34 46 L 39.8 43.8 Z" />
          <path class="fx-spark-2" style="transform-origin: 184px 82px;" d="M 184 76 L 185.7 80.3 L 190 82 L 185.7 83.7 L 184 88 L 182.3 83.7 L 178 82 L 182.3 80.3 Z" />
          <path class="fx-spark-3" style="transform-origin: 34px 118px;" d="M 34 113 L 35.4 116.6 L 39 118 L 35.4 119.4 L 34 123 L 32.6 119.4 L 29 118 L 32.6 116.6 Z" />
        </g>

        <g class="fx-working-particles" filter="url(#goldGlow)">
          <polygon class="fx-hex-p1" points="44,82 50,85.5 50,92.5 44,96 38,92.5 38,85.5" fill="rgba(255,224,102,0.28)" stroke="#ffe066" stroke-width="1.6" />
          <polygon class="fx-hex-p2" points="178,74 185,78 185,86 178,90 171,86 171,78" fill="rgba(255,224,102,0.28)" stroke="#ffe066" stroke-width="1.6" />
          <polygon class="fx-hex-p3" points="154,40 159,43 159,49 154,52 149,49 149,43" fill="rgba(255,224,102,0.34)" stroke="#fff3b0" stroke-width="1.5" />
        </g>

        <g class="fx-sleep-zzz" fill="#ffe27a" font-weight="800" font-family="Inter, sans-serif" filter="url(#goldGlow)">
          <text class="fx-z1" x="150" y="66" font-size="14">z</text>
          <text class="fx-z2" x="163" y="54" font-size="18">Z</text>
          <text class="fx-z3" x="176" y="42" font-size="13">z</text>
        </g>

        <g class="fx-error-badge">
          <polygon class="fx-error-ring" points="170,28 184,36 184,52 170,60 156,52 156,36"
                   fill="none" stroke="#ff6b57" stroke-width="2.6" stroke-linejoin="round" />
          <polygon points="170,29 183,36.5 183,51.5 170,59 157,51.5 157,36.5"
                   fill="#210b0b" stroke="#ff5c4d" stroke-width="2.6" stroke-linejoin="round" filter="url(#badgeGlow)" />
          <path d="M 170 36.5 L 170 46.5" stroke="#ffccc7" stroke-width="3.2" stroke-linecap="round" />
          <circle cx="170" cy="51.8" r="2.1" fill="#ffccc7" />
        </g>
      </svg>
    </div>
  `;
}

export interface BeeLifeController {
  waveNow(): void;
  celebrateNow(): void;
  destroy(): void;
}

export function attachBeeLife(el: HTMLElement): BeeLifeController {
  const pupils = el.querySelector<SVGGElement>(".mc-pupils-track");
  const face = el.querySelector<SVGGElement>(".mc-face-track");
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
      if (el.getAttribute("data-state") !== "sleep") {
        el.classList.add("is-blinking");
        window.setTimeout(() => el.classList.remove("is-blinking"), 140);
      }
      scheduleBlink();
    }, 2800 + Math.random() * 3200);
  };
  scheduleBlink();

  const tick = () => {
    if (!alive) return;
    const r = el.getBoundingClientRect();
    let tx = 0;
    let ty = 0;
    if (pointer.on && el.getAttribute("data-state") !== "sleep" && r.width > 0) {
      const dx = pointer.x - (r.left + r.width / 2);
      const dy = pointer.y - (r.top + r.height * 0.45);
      const d = Math.hypot(dx, dy) || 1;
      const s = Math.min(1, d / 200);
      tx = (dx / d) * s;
      ty = (dy / d) * s;
    }
    cur.px += (tx * 4.2 - cur.px) * 0.14;
    cur.py += (ty * 3.1 - cur.py) * 0.14;
    cur.fx += (tx * 2.4 - cur.fx) * 0.1;
    cur.fy += (ty * 1.6 - cur.fy) * 0.1;
    if (pupils) pupils.setAttribute("transform", `translate(${cur.px.toFixed(2)},${cur.py.toFixed(2)})`);
    if (face) face.setAttribute("transform", `translate(${cur.fx.toFixed(2)},${cur.fy.toFixed(2)})`);
    rafId = requestAnimationFrame(tick);
  };
  rafId = requestAnimationFrame(tick);

  return {
    waveNow() {
      el.classList.remove("is-waving", "click-bounce");
      void el.offsetWidth;
      el.classList.add("is-waving", "click-bounce");
      window.setTimeout(() => el.classList.remove("is-waving", "click-bounce"), 1700);
    },
    celebrateNow() {
      el.classList.remove("celebrate-spin");
      void el.offsetWidth;
      el.classList.add("celebrate-spin");
      window.setTimeout(() => el.classList.remove("celebrate-spin"), 850);
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
