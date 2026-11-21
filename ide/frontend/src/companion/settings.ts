export interface CompanionSettings {
  enabled: boolean;
  osNotifications: boolean;
  sound: boolean;
  doNotDisturb: boolean;
  minimized: boolean;
  x: number | null;
  y: number | null;
}

const KEY = "hivemind.companion.v1";

export const defaultSettings: CompanionSettings = {
  enabled: true,
  osNotifications: true,
  sound: false,
  doNotDisturb: false,
  minimized: false,
  x: null,
  y: null,
};

const listeners = new Set<() => void>();

export function normalizeSettings(raw: unknown): CompanionSettings {
  const source = raw && typeof raw === "object" ? raw as Record<string, unknown> : {};
  const flag = (name: keyof CompanionSettings, fallback: boolean): boolean =>
    typeof source[name] === "boolean" ? source[name] as boolean : fallback;
  const coord = (name: "x" | "y"): number | null =>
    typeof source[name] === "number" && Number.isFinite(source[name] as number) ? source[name] as number : null;
  return {
    enabled: flag("enabled", defaultSettings.enabled),
    osNotifications: flag("osNotifications", defaultSettings.osNotifications),
    sound: flag("sound", defaultSettings.sound),
    doNotDisturb: flag("doNotDisturb", defaultSettings.doNotDisturb),
    minimized: flag("minimized", defaultSettings.minimized),
    x: coord("x"),
    y: coord("y"),
  };
}

export function loadSettings(): CompanionSettings {
  if (typeof localStorage === "undefined") return { ...defaultSettings };
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return { ...defaultSettings };
    return normalizeSettings(JSON.parse(raw));
  } catch {
    return { ...defaultSettings };
  }
}

export function saveSettings(next: CompanionSettings): void {
  if (typeof localStorage === "undefined") return;
  localStorage.setItem(KEY, JSON.stringify(next));
  listeners.forEach((fn) => fn());
}

export function updateSettings(patch: Partial<CompanionSettings>): CompanionSettings {
  const next = { ...loadSettings(), ...patch };
  saveSettings(next);
  return next;
}

export function toggleCompanionEnabled(): boolean {
  const next = !loadSettings().enabled;
  updateSettings({ enabled: next });
  return next;
}

export function subscribeSettings(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}
