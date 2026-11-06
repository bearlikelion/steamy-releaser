import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { App } from "@/app";
import { config, mockDesktop } from "@/test/desktop-mock";
import type { ConfigSummary } from "@/lib/types";

vi.mock("sonner", () => ({
  Toaster: () => null,
  toast: {
    success: vi.fn<typeof toast.success>(),
    warning: vi.fn<typeof toast.warning>(),
    error: vi.fn<typeof toast.error>(),
  },
}));

const created: ConfigSummary = {
  ...config,
  path: "/games/space/steam-release.yaml",
  project: "Space Game",
  engine: "godot",
  default_profile: "release",
  profiles: [
    {
      name: "release",
      app_id: 480,
      targets: [
        {
          name: "windows",
          platform: "windows",
          architecture: "",
          depot_id: 481,
          output: "/games/space/builds/windows/Space.exe",
        },
      ],
    },
  ],
};

describe("configuration generator", () => {
  let desktop: ReturnType<typeof mockDesktop>;
  beforeEach(() => {
    desktop = mockDesktop();
  });

  it("fills targets from a detected Godot project and creates a ready configuration", async () => {
    desktop.api.OpenProjectDialog.mockResolvedValueOnce("/games/space");
    desktop.api.DetectProject.mockResolvedValueOnce({
      engine: "godot",
      name: "Space Game",
      presets: [
        {
          name: "Windows Desktop",
          platform: "windows",
          output: "builds/windows/Space.exe",
        },
        { name: "Linux", platform: "linux", output: "../outside/space" },
      ],
      profiles: [],
      existing: "",
    });
    desktop.api.PreviewConfig.mockResolvedValue("app_id: 480\n");
    desktop.api.CreateConfig.mockResolvedValueOnce(created);
    render(<App />);
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Browse game folder" }),
    );
    expect(desktop.api.DetectProject).toHaveBeenCalledWith("/games/space");
    expect(screen.getByRole("textbox", { name: "Project name" })).toHaveValue(
      "Space Game",
    );
    expect(screen.getByRole("combobox", { name: "Engine" })).toHaveValue(
      "godot",
    );
    expect(
      screen.getByRole("combobox", { name: "Target 1 export preset" }),
    ).toHaveValue("Windows Desktop");
    expect(
      screen.getByRole("textbox", { name: "Target 2 build output" }),
    ).toHaveValue("builds/linux/Game.x86_64");
    await user.click(screen.getByRole("button", { name: "Remove target 3" }));
    await user.type(
      screen.getByRole("spinbutton", { name: "Steam App ID" }),
      "480",
    );
    expect(
      screen.getByRole("spinbutton", { name: "Target 2 depot ID" }),
    ).toHaveAttribute("placeholder", "482");
    expect(await screen.findByLabelText("Generated YAML")).toHaveTextContent(
      "app_id: 480",
    );

    await user.click(
      screen.getByRole("button", { name: "Create configuration" }),
    );
    expect(desktop.api.CreateConfig).toHaveBeenCalledExactlyOnceWith({
      path: "/games/space/steam-release.yaml",
      project_root: "/games/space",
      name: "Space Game",
      engine: "godot",
      app_id: 480,
      targets: [
        {
          name: "windows",
          platform: "windows",
          depot_id: 481,
          path: "builds/windows/Space.exe",
          build_setting: "Windows Desktop",
          mappings: undefined,
          exclusions: undefined,
        },
        {
          name: "linux",
          platform: "linux",
          depot_id: 482,
          path: "builds/linux/Game.x86_64",
          build_setting: "Linux",
          mappings: undefined,
          exclusions: undefined,
        },
      ],
    });
    await screen.findByRole("heading", { name: "Space Game / release" });
    expect(
      screen.getByRole("button", { name: "Build & Deploy" }),
    ).toBeEnabled();
  });

  it("imports depots, mappings, and exclusions from a SteamPipe VDF", async () => {
    desktop.api.ImportVDF.mockResolvedValueOnce({
      app_id: 480,
      description: "Nightly",
      content_root: "/sdk/content",
      depots: [
        {
          depot_id: 481,
          platform: "windows",
          content: "/sdk/content/windows",
          mappings: [],
          exclusions: ["*.pdb"],
        },
        {
          depot_id: 483,
          platform: "",
          content: "/sdk/content",
          mappings: [{ local_path: "shared/*", depot_path: "." }],
          exclusions: [],
        },
      ],
    });
    render(<App />);
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Import SteamPipe VDF" }),
    );
    expect(desktop.api.ImportVDF).toHaveBeenCalledWith(
      "/games/demo/scripts/app_build_480.vdf",
    );
    expect(
      screen.getByText("Imported from /games/demo/scripts/app_build_480.vdf"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("spinbutton", { name: "Steam App ID" }),
    ).toHaveValue(480);
    expect(screen.getByRole("textbox", { name: "Game folder" })).toHaveValue(
      "/sdk",
    );
    expect(screen.getByRole("textbox", { name: "Project name" })).toHaveValue(
      "sdk",
    );
    expect(screen.getByRole("textbox", { name: "Target 2 name" })).toHaveValue(
      "depot_483",
    );
    expect(
      screen.getByText("Keeps 1 file mapping from the VDF."),
    ).toBeInTheDocument();

    await user.click(
      screen.getByRole("button", { name: "Create configuration" }),
    );
    expect(desktop.api.CreateConfig).toHaveBeenCalledWith(
      expect.objectContaining({
        path: "/sdk/steam-release.yaml",
        engine: "prebuilt",
        app_id: 480,
        targets: [
          expect.objectContaining({
            name: "windows",
            depot_id: 481,
            path: "/sdk/content/windows",
            exclusions: ["*.pdb"],
          }),
          expect.objectContaining({
            name: "depot_483",
            depot_id: 483,
            mappings: [{ local_path: "shared/*", depot_path: "." }],
          }),
        ],
      }),
    );
  });

  it("shows validation errors from the preview and offers an existing configuration", async () => {
    desktop.api.DetectProject.mockResolvedValueOnce({
      engine: "prebuilt",
      name: "demo",
      presets: [],
      profiles: [],
      existing: config.path,
    });
    desktop.api.PreviewConfig.mockRejectedValue(
      new Error('target "windows": depot_id is required'),
    );
    render(<App />);
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Browse game folder" }),
    );
    await user.type(
      screen.getByRole("spinbutton", { name: "Steam App ID" }),
      "1",
    );
    const preview = screen.getByText("YAML preview").closest("details")!;
    expect(await within(preview).findByRole("alert")).toHaveTextContent(
      "depot_id is required",
    );

    await user.click(screen.getByRole("button", { name: "Open it instead" }));
    expect(desktop.api.LoadConfig).toHaveBeenCalledWith(config.path, "");
    await screen.findByRole("heading", { name: "Demo Game / full" });
  });

  it("finds SteamPipe scripts in a typed folder and matches depots to Godot presets", async () => {
    desktop.api.DetectProject.mockResolvedValueOnce({
      engine: "godot",
      name: "SurfsUp",
      presets: [
        {
          name: "Windows Release",
          platform: "windows",
          output: "Build/Windows/SurfsUp.exe",
        },
        {
          name: "Linux Release",
          platform: "linux",
          output: "Build/Linux/SurfsUp.x86_64",
        },
      ],
      profiles: [],
      existing: "",
    });
    const depot = (id: number, folder: string) => ({
      depot_id: id,
      platform: folder.toLowerCase(),
      content: `/games/surfsup/Build/${folder}`,
      mappings: [],
      exclusions: ["*.pdb"],
    });
    desktop.api.DiscoverBuildScripts.mockResolvedValueOnce([
      {
        path: "/games/surfsup/Build/scripts/app_3454830.vdf",
        build: {
          app_id: 3454830,
          description: "",
          content_root: "/games/surfsup/Build/scripts",
          depots: [depot(3454831, "Windows"), depot(3454832, "Linux")],
        },
      },
      {
        path: "/games/surfsup/Build/scripts/app_3688390.vdf",
        build: {
          app_id: 3688390,
          description: "",
          content_root: "/games/surfsup/Build/scripts",
          depots: [depot(3688391, "Windows")],
        },
      },
    ]);
    desktop.api.SteamAppName.mockImplementation(async (appId) =>
      appId === 3454830 ? "SurfsUp" : "",
    );
    render(<App />);
    const user = userEvent.setup();

    await user.type(
      await screen.findByRole("textbox", { name: "Game folder" }),
      "/games/surfsup{Enter}",
    );
    expect(desktop.api.DiscoverBuildScripts).toHaveBeenCalledWith(
      "/games/surfsup",
    );
    const found = await screen.findByRole("region", {
      name: "SteamPipe scripts in this folder",
    });
    expect(
      await within(found).findByRole("button", { name: /^SurfsUp/ }),
    ).toHaveTextContent(
      "App 3454830 · 2 depots · Build/scripts/app_3454830.vdf",
    );
    expect(
      within(found).getByRole("button", { name: /^App 3688390/ }),
    ).toHaveTextContent("1 depot");

    await user.click(within(found).getByRole("button", { name: /^SurfsUp/ }));
    expect(screen.getByRole("combobox", { name: "Engine" })).toHaveValue(
      "godot",
    );
    expect(
      screen.getByRole("spinbutton", { name: "Target 2 depot ID" }),
    ).toHaveValue(3454832);
    expect(
      screen.getByRole("combobox", { name: "Target 2 export preset" }),
    ).toHaveValue("Linux Release");
    expect(
      screen.getByRole("textbox", { name: "Target 2 build output" }),
    ).toHaveValue("Build/Linux/SurfsUp.x86_64");
    expect(screen.getByRole("textbox", { name: "Project name" })).toHaveValue(
      "SurfsUp",
    );
  });
});
