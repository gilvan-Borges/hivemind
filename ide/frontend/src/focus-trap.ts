// Focus trap — Spec 07 §3.2.3: modals must trap Tab/Shift+Tab inside the
// dialog, support Escape for dismissible dialogs and restore focus to the
// element that opened them when they close.

export interface FocusableLike {
  focus(): void;
  hidden?: boolean;
  disabled?: boolean;
}

const FOCUSABLE_SELECTOR =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/** nextFocusTarget returns where focus should move from `current` inside
 * `candidates` for the given tab direction, cycling at the edges. Pure. */
export function nextFocusTarget(
  candidates: FocusableLike[],
  currentIndex: number,
  backwards: boolean,
): { index: number; target: FocusableLike | null } {
  if (candidates.length === 0) return { index: -1, target: null };
  if (candidates.length === 1) return { index: 0, target: candidates[0] };
  // Foco fora do trap: entra pela borda correspondente à direção do Tab.
  if (currentIndex < 0) {
    const entry = backwards ? candidates.length - 1 : 0;
    return { index: entry, target: candidates[entry] };
  }
  const step = backwards ? -1 : 1;
  let idx = currentIndex;
  for (let hops = 0; hops < candidates.length; hops++) {
    idx = (idx + step + candidates.length) % candidates.length;
    if (idx !== currentIndex) return { index: idx, target: candidates[idx] };
  }
  return { index: currentIndex, target: candidates[currentIndex] ?? null };
}

/** collectFocusables returns the list of focusable candidates inside a
 * container element, in DOM order, skipping hidden/disabled ones. */
export function collectFocusables(container: HTMLElement): HTMLElement[] {
  if (!container || typeof container.querySelectorAll !== "function") return [];
  return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter((el) => {
    if (el.hasAttribute("disabled")) return false;
    const style = typeof window !== "undefined" ? window.getComputedStyle?.(el) : undefined;
    if (style && (style.display === "none" || style.visibility === "hidden")) return false;
    return true;
  });
}

export interface FocusTrapHandle {
  release(): void;
}

/**
 * trapFocusIn activates a focus trap on `layer` (the element containing the
 * modal). It stores the previously focused element, moves focus into the
 * dialog, cycles Tab/Shift+Tab inside it, invokes `onEscape` for dismissible
 * dialogs and restores the previous focus on release().
 */
export function trapFocusIn(layer: HTMLElement, opts: { onEscape?: () => void } = {}): FocusTrapHandle {
  if (!layer) {
    return { release: () => {} };
  }
  const previous =
    typeof document !== "undefined" && document.activeElement && layer.contains(document.activeElement)
      ? null
      : (document.activeElement as HTMLElement | null);

  const candidates = () => collectFocusables(layer);
  // Only move focus when nothing inside the modal already owns it: several
  // modal branches focus a specific input/button with caret positioning.
  const current = typeof document !== "undefined" ? (document.activeElement as HTMLElement | null) : null;
  if (!(current && layer.contains(current))) {
    const list = candidates();
    const initial =
      list.find((el) => el.dataset.autofocus === "1") ||
      list.find((el) => /button|input|select|textarea/i.test(el.tagName)) ||
      layer;
    initial.focus?.();
  }

  const onKeyDown = (ev: KeyboardEvent) => {
    if (ev.key === "Escape" && opts.onEscape) {
      ev.preventDefault();
      ev.stopPropagation();
      opts.onEscape();
      return;
    }
    if (ev.key !== "Tab") return;
    const current = document.activeElement as HTMLElement | null;
    const items = candidates();
    if (items.length === 0) {
      ev.preventDefault();
      layer.focus?.();
      return;
    }
    const idx = current && items.indexOf(current) >= 0 ? items.indexOf(current) : -1;
    const { target } = nextFocusTarget(items, idx, ev.shiftKey);
    ev.preventDefault();
    target?.focus?.();
  };

  layer.addEventListener("keydown", onKeyDown);
  return {
    release() {
      layer.removeEventListener("keydown", onKeyDown);
      if (previous && typeof previous.focus === "function" && !previous.isConnected) return;
      previous?.focus?.();
    },
  };
}
