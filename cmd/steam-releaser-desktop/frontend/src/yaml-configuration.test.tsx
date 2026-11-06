import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { App } from "@/app";
import { config, mockDesktop, openExistingConfig } from "@/test/desktop-mock";

vi.mock("sonner", () => ({
  Toaster: () => null,
  toast: {
    success: vi.fn<typeof toast.success>(),
    warning: vi.fn<typeof toast.warning>(),
    error: vi.fn<typeof toast.error>(),
  },
}));

async function openProject() {
  const user = userEvent.setup();
  await openExistingConfig();
  await user.click(
    screen.getByRole("button", { name: "Browse configuration" }),
  );
  await user.click(screen.getByRole("button", { name: "Load configuration" }));
  await screen.findByRole("heading", { name: "Demo Game / full" });
  return user;
}

describe("YAML configuration workflow", () => {
  let desktop: ReturnType<typeof mockDesktop>;
  beforeEach(() => {
    desktop = mockDesktop();
  });

  it("opens YAML and reloads externally added profiles and targets while preserving selection", async () => {
    render(<App />);
    const user = await openProject();
    expect(screen.queryByRole("button", { name: "Add profile" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Add target" })).toBeNull();
    expect(
      screen.queryByRole("button", { name: /Edit build options/ }),
    ).toBeNull();
    await user.click(screen.getByRole("checkbox", { name: "linux" }));
    await user.click(screen.getByRole("button", { name: "Edit YAML" }));
    expect(desktop.api.EditConfig).toHaveBeenCalledExactlyOnceWith(config.path);
    expect(
      screen.getByRole("button", { name: "Build & Deploy" }),
    ).toBeDisabled();
    expect(
      screen.queryByRole("status", { name: "Configuration editing status" }),
    ).toBeNull();

    const beta = {
      name: "beta",
      app_id: 30,
      targets: [
        {
          name: "macos",
          platform: "macos",
          architecture: "",
          depot_id: 31,
          output: "/build/beta/Game.app",
        },
      ],
    };
    desktop.api.LoadConfig.mockResolvedValue({
      ...config,
      config_hash: "edited-config",
      profiles: [...config.profiles, beta],
    });
    await user.click(
      screen.getByRole("button", { name: "Reload and validate" }),
    );
    expect(
      screen.getByRole("button", { name: "Build & Deploy" }),
    ).toBeEnabled();
    expect(screen.getByRole("checkbox", { name: "windows" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "linux" })).not.toBeChecked();
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Profile" }),
      "beta",
    );
    expect(
      screen.getByRole("heading", { name: "Demo Game / beta" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "macos" })).toBeChecked();
  });

  it("keeps repair and reload available after invalid edits and restores release actions after a fix", async () => {
    render(<App />);
    const user = await openProject();
    desktop.api.LoadConfig.mockRejectedValueOnce(
      new Error("yaml: line 12: unknown field argz"),
    );
    await user.click(
      screen.getByRole("button", { name: "Reload and validate" }),
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "line 12: unknown field argz",
    );
    expect(
      screen.getByRole("button", { name: "Build & Deploy" }),
    ).toBeDisabled();
    expect(screen.getByRole("button", { name: "Edit YAML" })).toBeEnabled();
    expect(
      screen.getByRole("button", { name: "Reload and validate" }),
    ).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Edit YAML" }));
    expect(desktop.api.EditConfig).toHaveBeenCalledWith(config.path);
    await user.click(
      screen.getByRole("button", { name: "Reload and validate" }),
    );
    expect(screen.queryByRole("alert")).toBeNull();
    expect(
      screen.getByRole("button", { name: "Build & Deploy" }),
    ).toBeEnabled();
  });

  it("refreshes an out-of-band edit before running and requires review of the new destination", async () => {
    render(<App />);
    const user = await openProject();
    const changed = {
      ...config,
      config_hash: "new-destination",
      profiles: config.profiles.map((profile) => ({
        ...profile,
        app_id: profile.app_id + 100,
      })),
    };
    desktop.api.LoadConfig.mockResolvedValue(changed);
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    expect(desktop.api.StartOperation).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Publishing destination")).toHaveTextContent(
      "App 120",
    );
    expect(toast.warning).toHaveBeenCalledWith(
      expect.stringContaining("Review the updated profile"),
    );
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    await waitFor(() =>
      expect(desktop.api.StartOperation).toHaveBeenCalledExactlyOnceWith(
        expect.objectContaining({
          profile: "full",
          targets: ["windows", "linux"],
        }),
      ),
    );
    act(() => desktop.emit("operation:finished", { action: "ship", ok: true }));
    expect(
      screen.getByText("App ID 120 · Profile full · Targets: windows, linux"),
    ).toBeInTheDocument();
  });

  it("blocks a release when external YAML has become invalid", async () => {
    render(<App />);
    const user = await openProject();
    desktop.api.LoadConfig.mockRejectedValueOnce(new Error("invalid app_id"));
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    expect(desktop.api.StartOperation).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("invalid app_id");
    expect(
      screen.getByRole("button", { name: "Build & Deploy" }),
    ).toBeDisabled();
    expect(screen.getByRole("button", { name: "Edit YAML" })).toBeEnabled();
  });

  it("can open a selected invalid configuration before it has ever loaded", async () => {
    desktop.api.LoadConfig.mockRejectedValueOnce(new Error("broken YAML"));
    render(<App />);
    const user = userEvent.setup();
    await openExistingConfig();
    await user.click(
      screen.getByRole("button", { name: "Browse configuration" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Load configuration" }),
    );
    expect(screen.getByRole("alert")).toHaveTextContent("broken YAML");
    await user.click(screen.getByRole("button", { name: "Edit YAML" }));
    expect(desktop.api.EditConfig).toHaveBeenCalledExactlyOnceWith(config.path);
  });

  it("reports an unavailable editor without blocking the valid loaded configuration", async () => {
    render(<App />);
    const user = await openProject();
    desktop.api.EditConfig.mockRejectedValueOnce(
      new Error("No associated editor"),
    );
    await user.click(screen.getByRole("button", { name: "Edit YAML" }));
    expect(toast.error).toHaveBeenCalledWith("No associated editor");
    expect(
      screen.getByRole("button", { name: "Build & Deploy" }),
    ).toBeEnabled();
    expect(screen.getByLabelText("Configuration file")).toHaveTextContent(
      config.path,
    );
  });
});
