// Render guard — Spec 07 §3.2.2: background/async events (PR polling, provider
// bootstrap, status refreshes) must never destroy the DOM of a region while the
// user is typing in it. Guards wrap the *async* call sites of
// renderLeftSidebar()/renderModalLayer() and defer the render until the region
// is safe again, so commit messages, searches and pasted tokens survive.

export type GuardedRegion = "left-sidebar" | "modal-layer";

export interface GuardedElement {
  value?: string;
  defaultValue?: string;
  tagName?: string;
}

export interface RegionProbe {
  /** Currently focused element (document.activeElement in the real DOM). */
  activeElement(): GuardedElement | null;
  /** Returns true when `el` lives inside the given region container. */
  owns(region: GuardedRegion, el: GuardedElement): boolean;
  /** All input-like elements inside the region. */
  inputsIn(region: GuardedRegion): GuardedElement[];
  /** True when the element holds unsynchronized user input. */
  isDirty(el: GuardedElement): boolean;
}

export interface RenderGuard {
  isRegionBusy(region: GuardedRegion): boolean;
  guardedRender(region: GuardedRegion, render: () => void): void;
  notifyRegionRendered(region: GuardedRegion): void;
  flushPendingNow(region: GuardedRegion): void;
}

function isInputish(el: GuardedElement): boolean {
  const tag = String(el.tagName || "").toLowerCase();
  return tag === "input" || tag === "textarea";
}

/**
 * createRenderGuard builds a guard over a region probe. Pure logic, fully
 * unit-testable without a DOM.
 *
 * Busy policy:
 *  - "left-sidebar":  an input/textarea is FOCUSED inside the sidebar.
 *  - "modal-layer":   an input/textarea is focused OR holds dirty (unsaved)
 *                     text inside the modal, because pasted tokens survive
 *                     only in the DOM until the user submits them.
 */
export function createRenderGuard(probe: RegionProbe): RenderGuard {
  const pending = new Map<GuardedRegion, () => void>();

  const focusedInputIn = (region: GuardedRegion): boolean => {
    const active = probe.activeElement();
    return Boolean(active && isInputish(active) && probe.owns(region, active));
  };

  const dirtyInputIn = (region: GuardedRegion): boolean => {
    for (const el of probe.inputsIn(region)) {
      if (isInputish(el) && probe.isDirty(el)) return true;
    }
    return false;
  };

  return {
    isRegionBusy(region: GuardedRegion): boolean {
      if (focusedInputIn(region)) return true;
      if (region === "modal-layer") return dirtyInputIn(region);
      return false;
    },

    guardedRender(region: GuardedRegion, render: () => void): void {
      if (this.isRegionBusy(region)) {
        pending.set(region, render);
        return;
      }
      render();
    },

    notifyRegionRendered(region: GuardedRegion): void {
      pending.delete(region);
    },

    flushPendingNow(region: GuardedRegion): void {
      const render = pending.get(region);
      if (!render) return;
      pending.delete(region);
      if (!this.isRegionBusy(region)) {
        render();
      } else {
        pending.set(region, render);
      }
    },
  };
}

function regionContainerId(region: GuardedRegion): string {
  return region === "left-sidebar" ? "left-sidebar" : "modal-layer";
}

/** Real DOM probe (test builds run in node without a DOM, hence the guards). */
export const domProbe: RegionProbe = {
  activeElement(): GuardedElement | null {
    if (typeof document === "undefined") return null;
    return document.activeElement as GuardedElement | null;
  },
  owns(region: GuardedRegion, el: GuardedElement): boolean {
    if (typeof document === "undefined") return false;
    const container = document.getElementById(regionContainerId(region));
    return Boolean(container && el && container.contains(el as Node));
  },
  inputsIn(region: GuardedRegion): GuardedElement[] {
    if (typeof document === "undefined") return [];
    const container = document.getElementById(regionContainerId(region));
    if (!container) return [];
    const found = container.querySelectorAll("input, textarea");
    return Array.from(found).map((el) => el as unknown as GuardedElement);
  },
  isDirty(el: GuardedElement): boolean {
    const value = el.value;
    if (value === undefined) return false;
    return value !== (el.defaultValue ?? "");
  },
};

export const renderGuard: RenderGuard = createRenderGuard(domProbe);

/** Notify the guard that a region was re-rendered (cancels pending flushes). */
export function notifyRegionRendered(region: GuardedRegion): void {
  renderGuard.notifyRegionRendered(region);
}

/**
 * Installs the global focus-out flush: when the user leaves an input of a
 * region, any render deferred by guardedRender runs immediately (after the
 * focus transition settles; flushPendingNow re-checks busyness).
 */
export function installRenderGuardFlushHooks(): () => void {
  if (typeof document === "undefined") return () => {};
  let flushTimer = 0;
  const onFocusOut = (ev: Event) => {
    const target = ev.target as GuardedElement | null;
    if (!target || !isInputish(target)) return;
    if (flushTimer) window.clearTimeout(flushTimer);
    flushTimer = window.setTimeout(() => {
      flushTimer = 0;
      renderGuard.flushPendingNow("left-sidebar");
      renderGuard.flushPendingNow("modal-layer");
    }, 180);
  };
  document.addEventListener("focusout", onFocusOut);
  return () => {
    document.removeEventListener("focusout", onFocusOut);
    if (flushTimer) window.clearTimeout(flushTimer);
  };
}
