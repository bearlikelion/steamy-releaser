import { toast } from "sonner";
import type { DesktopBackend } from "@/lib/types";

export function desktopAvailable() {
  return Boolean(window.go?.main?.DesktopApp && window.runtime);
}

export function backend() {
  const service = window.go?.main?.DesktopApp;
  if (!service) {
    throw new Error(
      "Run this UI through the desktop application to access project files and release operations.",
    );
  }
  return service;
}

export function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}

type PreferenceStore = {
  values: Map<string, string>;
  loading?: Promise<void>;
  pending: Promise<void>;
};

const stores = new WeakMap<DesktopBackend, PreferenceStore>();

function preferenceStore() {
  if (!desktopAvailable()) return undefined;
  const service = backend();
  let store = stores.get(service);
  if (!store) {
    store = { values: new Map(), pending: Promise.resolve() };
    stores.set(service, store);
  }
  return store;
}

function legacyPreferenceKey(key: string) {
  return (
    [
      "steam-releaser:last-config",
      "steam-releaser:recent-configs",
      "steam-releaser:notifications",
    ].includes(key) ||
    key.startsWith("steam-releaser:project-root:") ||
    key.startsWith("steam-releaser:selections:")
  );
}

export function initializePreferences(): Promise<void> {
  const store = preferenceStore();
  if (!store) return Promise.resolve();
  const service = backend();
  store.loading ??= (async () => {
    const legacy: Record<string, string> = {};
    try {
      for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i);
        if (key && legacyPreferenceKey(key)) {
          const value = localStorage.getItem(key);
          if (value !== null) legacy[key] = value;
        }
      }
    } catch {
      // SQLite preferences remain available when browser storage is disabled.
    }
    store.values = new Map(Object.entries(legacy));
    const values = await service.GetPreferences(legacy);
    store.values = new Map(Object.entries(values ?? {}));
    try {
      for (const key of Object.keys(legacy)) localStorage.removeItem(key);
    } catch {
      // Import is committed; leftover browser values cannot replace SQLite data.
    }
  })();
  return store.loading;
}

function readPreference(key: string) {
  return preferenceStore()?.values.get(key) ?? null;
}

function savePreferences(values: Record<string, string>) {
  const store = preferenceStore();
  if (!store) return;
  for (const [key, value] of Object.entries(values))
    store.values.set(key, value);
  const service = backend();
  // Keep writes in user-action order, including rapid selection changes.
  store.pending = store.pending
    .then(() => service.SavePreferences(values))
    .catch((error: unknown) => {
      toast.warning(
        `Local preferences could not be saved: ${errorMessage(error)}`,
      );
    });
}

const lastConfigKey = "steam-releaser:last-config";
const recentConfigsKey = "steam-releaser:recent-configs";
const projectRootKey = (path: string) => `steam-releaser:project-root:${path}`;
const recentConfigLimit = 5;

export type RecentConfig = { path: string; project: string };

export function readLastConfig() {
  try {
    return readPreference(lastConfigKey) ?? "";
  } catch {
    return "";
  }
}

export function readRecentConfigs(): RecentConfig[] {
  try {
    const stored = JSON.parse(readPreference(recentConfigsKey) ?? "null");
    if (Array.isArray(stored)) {
      const seen = new Set<string>();
      const recent: RecentConfig[] = [];
      for (const item of stored) {
        if (
          !item ||
          typeof item.path !== "string" ||
          !item.path.trim() ||
          typeof item.project !== "string" ||
          seen.has(item.path)
        )
          continue;
        seen.add(item.path);
        recent.push({ path: item.path, project: item.project });
        if (recent.length === recentConfigLimit) break;
      }
      if (recent.length) return recent;
    }
  } catch {
    // Fall back to the last configuration when recent history is unavailable.
  }
  const last = readLastConfig();
  return last ? [{ path: last, project: "" }] : [];
}

export function rememberConfig(path: string, project: string): RecentConfig[] {
  const recent = [
    { path, project },
    ...readRecentConfigs().filter((item) => item.path !== path),
  ].slice(0, recentConfigLimit);
  try {
    savePreferences({
      [lastConfigKey]: path,
      [recentConfigsKey]: JSON.stringify(recent),
    });
  } catch {
    // Preferences are optional; unavailable storage must not prevent releases.
  }
  return recent;
}

export function readProjectRoot(path: string) {
  if (!path) return "";
  try {
    return readPreference(projectRootKey(path)) ?? "";
  } catch {
    return "";
  }
}

export function rememberProjectRoot(path: string, root: string) {
  try {
    savePreferences({ [projectRootKey(path)]: root });
  } catch {
    // Preferences are optional; unavailable storage must not prevent releases.
  }
}

export type SavedSelection = {
  appId: number;
  targets: string[];
  preview: boolean;
  branch: string;
};

type ProjectSelections = {
  profile: string;
  profiles: Record<string, SavedSelection>;
};

const selectionsKey = (path: string, root: string) =>
  `steam-releaser:selections:${JSON.stringify([path, root])}`;

export function readSelections(path: string, root: string): ProjectSelections {
  try {
    const value: unknown = JSON.parse(
      readPreference(selectionsKey(path, root)) ?? "null",
    );
    if (
      value &&
      typeof value === "object" &&
      "profile" in value &&
      typeof value.profile === "string" &&
      "profiles" in value &&
      value.profiles &&
      typeof value.profiles === "object"
    ) {
      const profiles: Record<string, SavedSelection> = {};
      for (const [name, selection] of Object.entries(value.profiles)) {
        if (
          selection &&
          typeof selection === "object" &&
          typeof selection.appId === "number" &&
          Array.isArray(selection.targets) &&
          selection.targets.every(
            (target: unknown) => typeof target === "string",
          ) &&
          typeof selection.preview === "boolean" &&
          typeof selection.branch === "string"
        ) {
          profiles[name] = selection as SavedSelection;
        }
      }
      return { profile: value.profile, profiles };
    }
  } catch {
    /* Optional local preferences. */
  }
  return { profile: "", profiles: {} };
}

export function rememberSelection(
  path: string,
  root: string,
  profile: string,
  selection: SavedSelection,
) {
  const saved = readSelections(path, root);
  saved.profile = profile;
  saved.profiles[profile] = selection;
  try {
    savePreferences({ [selectionsKey(path, root)]: JSON.stringify(saved) });
  } catch {
    /* Optional local preferences. */
  }
}

export function readNotifications() {
  try {
    return (
      (readPreference("steam-releaser:notifications") ?? "true") === "true"
    );
  } catch {
    return true;
  }
}

export function rememberNotifications(enabled: boolean) {
  try {
    savePreferences({ "steam-releaser:notifications": String(enabled) });
  } catch {
    /* Optional local preferences. */
  }
}

export type SteamCMDAccess = "allow" | "deny" | "";

export function readSteamCMDAccess(): SteamCMDAccess {
  try {
    const value = readPreference("steam-releaser:steamcmd-accounts");
    return value === "allow" || value === "deny" ? value : "";
  } catch {
    return "";
  }
}

export async function rememberSteamCMDAccess(access: "allow" | "deny") {
  savePreferences({ "steam-releaser:steamcmd-accounts": access });
  await preferenceStore()?.pending;
}
