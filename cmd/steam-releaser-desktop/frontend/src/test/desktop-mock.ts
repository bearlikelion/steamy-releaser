import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, vi } from "vitest";
import type {
  ConfigSummary,
  DesktopBackend,
  DesktopEvents,
  DesktopRuntime,
} from "@/lib/types";

export const config: ConfigSummary = {
  path: "/games/demo/steam-release.yaml",
  config_hash: "original-config",
  project: "Demo Game",
  root: "/games/demo",
  engine: "godot",
  default_profile: "full",
  username: "cached_builder",
  profiles: [
    {
      name: "demo",
      app_id: 10,
      targets: [
        {
          name: "demo-linux",
          platform: "linux",
          architecture: "x64",
          depot_id: 11,
          output: "/build/demo",
        },
      ],
    },
    {
      name: "full",
      app_id: 20,
      targets: [
        {
          name: "windows",
          platform: "windows",
          architecture: "x64",
          depot_id: 21,
          output: "/build/windows",
        },
        {
          name: "linux",
          platform: "linux",
          architecture: "x64",
          depot_id: 22,
          output: "/build/linux",
        },
      ],
    },
  ],
};

export function mockDesktop() {
  const listeners = new Map<keyof DesktopEvents, Set<(value: never) => void>>();
  const preferences: Record<string, string> = {};
  let credentialFile = {
    enabled: false,
    path: "",
    username: "",
    has_credentials: false,
  };
  const api = {
    GetCredentialFileSettings: vi
      .fn<DesktopBackend["GetCredentialFileSettings"]>()
      .mockImplementation(async () => ({ ...credentialFile })),
    ChooseCredentialFile: vi
      .fn<DesktopBackend["ChooseCredentialFile"]>()
      .mockResolvedValue("/private/steam-credentials.json"),
    ConfigureCredentialFile: vi
      .fn<DesktopBackend["ConfigureCredentialFile"]>()
      .mockImplementation(async (enabled, path) => {
        credentialFile = {
          enabled,
          path,
          username: enabled ? config.username : "",
          has_credentials: enabled,
        };
        return { ...credentialFile };
      }),
    SubmitSteamPassword: vi
      .fn<DesktopBackend["SubmitSteamPassword"]>()
      .mockResolvedValue(undefined),
    SubmitSavedSteamPassword: vi
      .fn<DesktopBackend["SubmitSavedSteamPassword"]>()
      .mockResolvedValue(false),
    GetPreferences: vi
      .fn<DesktopBackend["GetPreferences"]>()
      .mockImplementation(async (legacy) => {
        for (const [key, value] of Object.entries(legacy))
          if (!(key in preferences)) preferences[key] = value;
        return { ...preferences };
      }),
    SavePreferences: vi
      .fn<DesktopBackend["SavePreferences"]>()
      .mockImplementation(async (values) => {
        Object.assign(preferences, values);
      }),
    OpenConfigDialog: vi
      .fn<DesktopBackend["OpenConfigDialog"]>()
      .mockResolvedValue(config.path),
    OpenProjectDialog: vi
      .fn<DesktopBackend["OpenProjectDialog"]>()
      .mockResolvedValue("/games/demo"),
    SaveConfigDialog: vi
      .fn<DesktopBackend["SaveConfigDialog"]>()
      .mockResolvedValue("/games/new/steam-release.yaml"),
    LoadConfig: vi.fn<DesktopBackend["LoadConfig"]>().mockResolvedValue(config),
    CreateConfig: vi
      .fn<DesktopBackend["CreateConfig"]>()
      .mockResolvedValue(config),
    PreviewConfig: vi
      .fn<DesktopBackend["PreviewConfig"]>()
      .mockResolvedValue("app_id: 480\n"),
    DetectProject: vi.fn<DesktopBackend["DetectProject"]>().mockResolvedValue({
      engine: "prebuilt",
      name: "demo",
      presets: [],
      profiles: [],
      existing: "",
    }),
    OpenVDFDialog: vi
      .fn<DesktopBackend["OpenVDFDialog"]>()
      .mockResolvedValue("/games/demo/scripts/app_build_480.vdf"),
    DiscoverBuildScripts: vi
      .fn<DesktopBackend["DiscoverBuildScripts"]>()
      .mockResolvedValue([]),
    SteamAppName: vi.fn<DesktopBackend["SteamAppName"]>().mockResolvedValue(""),
    ImportVDF: vi.fn<DesktopBackend["ImportVDF"]>().mockResolvedValue({
      app_id: 480,
      description: "",
      content_root: "/games/demo/content",
      depots: [],
    }),
    EditConfig: vi
      .fn<DesktopBackend["EditConfig"]>()
      .mockResolvedValue(undefined),
    CachedUsername: vi
      .fn<DesktopBackend["CachedUsername"]>()
      .mockResolvedValue(config.username),
    SteamCMDLocation: vi
      .fn<DesktopBackend["SteamCMDLocation"]>()
      .mockResolvedValue(""),
    SteamCMDCachedAccounts: vi
      .fn<DesktopBackend["SteamCMDCachedAccounts"]>()
      .mockResolvedValue([]),
    StartOperation: vi
      .fn<DesktopBackend["StartOperation"]>()
      .mockResolvedValue(undefined),
    SubmitSteamInput: vi
      .fn<DesktopBackend["SubmitSteamInput"]>()
      .mockResolvedValue(undefined),
    CancelOperation: vi
      .fn<DesktopBackend["CancelOperation"]>()
      .mockResolvedValue(undefined),
    CheckReadiness: vi
      .fn<DesktopBackend["CheckReadiness"]>()
      .mockImplementation(async (request) => ({
        project: config.project,
        profile: request.profile,
        issues: [],
      })),
    GetReleaseHistory: vi
      .fn<DesktopBackend["GetReleaseHistory"]>()
      .mockResolvedValue([]),
    ClearReleaseHistory: vi
      .fn<DesktopBackend["ClearReleaseHistory"]>()
      .mockResolvedValue(undefined),
    ConfigureNotifications: vi
      .fn<DesktopBackend["ConfigureNotifications"]>()
      .mockImplementation(async (enabled) => enabled),
    NotifySteamInput: vi
      .fn<DesktopBackend["NotifySteamInput"]>()
      .mockResolvedValue(undefined),
  } satisfies DesktopBackend;
  const EventsOn: DesktopRuntime["EventsOn"] = (name, callback) => {
    const handlers = listeners.get(name) ?? new Set();
    handlers.add(callback);
    listeners.set(name, handlers);
    return () => {
      handlers.delete(callback);
    };
  };
  window.go = { main: { DesktopApp: api } };
  window.runtime = { EventsOn: vi.fn<DesktopRuntime["EventsOn"]>(EventsOn) };
  return {
    api,
    preferences,
    listeners,
    emit<K extends keyof DesktopEvents>(name: K, value: DesktopEvents[K]) {
      listeners.get(name)?.forEach((callback) => callback(value as never));
    },
  };
}

export async function openExistingConfig() {
  const button = await screen.findByRole("button", {
    name: "Open existing config",
  });
  await waitFor(() => expect(button).toBeEnabled());
  await userEvent.click(button);
}
