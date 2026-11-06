import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { App } from "@/app";
import type { ReleaseRecord } from "@/lib/types";
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
  await screen.findByRole("heading", { name: /Demo Game \/ (full|demo)/ });
  return user;
}

function attempt(overrides: Partial<ReleaseRecord> = {}): ReleaseRecord {
  return {
    id: "attempt-1",
    project: config.project,
    root: config.root,
    app_id: 20,
    started_at: "2026-10-07T18:00:00Z",
    finished_at: "2026-10-07T18:00:30Z",
    duration_ms: 30000,
    status: "failed",
    config_hash: "hash",
    commit: "abcdef1234567890",
    version: "v1.0",
    dirty: false,
    completed_targets: ["linux", "windows"],
    build_ready: true,
    upload_fingerprint: "a".repeat(64),
    logs: "Uploaded depot files\nNetwork failed",
    error: "Network failed",
    request: {
      action: "ship",
      config_path: config.path,
      project_root: config.root,
      profile: "full",
      targets: ["windows", "linux"],
      username: "cached_builder",
      preview: false,
      set_live: "qa",
    },
    ...overrides,
  };
}

describe("release convenience features", () => {
  let desktop: ReturnType<typeof mockDesktop>;
  beforeEach(() => {
    desktop = mockDesktop();
  });

  it("shows readable diagnostics only when Doctor is requested", async () => {
    render(<App />);
    const user = await openProject();
    expect(
      screen.queryByRole("heading", { name: "Readiness checks" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("list", { name: "Diagnostic checklist" }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "windows" }));
    expect(desktop.api.CheckReadiness).not.toHaveBeenCalled();
    await user.click(
      screen.getByRole("button", { name: "More release actions" }),
    );
    await user.click(screen.getByRole("menuitem", { name: "Doctor" }));
    await user.click(screen.getByRole("button", { name: "Doctor" }));
    act(() =>
      desktop.emit("operation:finished", {
        action: "doctor",
        ok: false,
        error: "Blocking issues",
        data: {
          project: config.project,
          issues: [
            { severity: "error", code: "tool.godot", message: "Godot missing" },
          ],
        },
      }),
    );
    expect(screen.getByText("Godot missing")).toBeInTheDocument();
    expect(
      screen.getByText(
        "Select an installed Godot executable using GODOT_BIN or project.engine.executable.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("Raw diagnostics")).toBeInTheDocument();
  });

  it("remembers each profile's targets and publishing settings across restarts", async () => {
    const first = render(<App />);
    let user = await openProject();
    await user.click(screen.getByRole("checkbox", { name: "windows" }));
    await user.type(
      screen.getByRole("textbox", { name: /Set live on beta branch/ }),
      "qa",
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Profile" }),
      "demo",
    );
    await user.click(screen.getByRole("checkbox", { name: "Preview only" }));
    first.unmount();
    render(<App />);
    user = await openProject();
    expect(screen.getByRole("combobox", { name: "Profile" })).toHaveValue(
      "demo",
    );
    expect(
      screen.getByRole("checkbox", { name: "Preview only" }),
    ).toBeChecked();
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Profile" }),
      "full",
    );
    expect(screen.getByRole("checkbox", { name: "windows" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "linux" })).toBeChecked();
    expect(
      screen.getByRole("textbox", { name: /Set live on beta branch/ }),
    ).toHaveValue("qa");
    expect(screen.getByLabelText("Publishing destination")).toHaveTextContent(
      "Beta branch: qa",
    );
  });

  it("clears a remembered branch when a profile's App ID changes", async () => {
    const first = render(<App />);
    const user = await openProject();
    await user.type(
      screen.getByRole("textbox", { name: /Set live on beta branch/ }),
      "old-app-beta",
    );
    first.unmount();
    desktop.api.LoadConfig.mockResolvedValue({
      ...config,
      profiles: config.profiles.map((profile) => ({
        ...profile,
        app_id: profile.app_id + 100,
      })),
    });
    render(<App />);
    await openProject();
    expect(
      screen.getByRole("textbox", { name: /Set live on beta branch/ }),
    ).toHaveValue("");
  });

  it("retries a failed upload with its original targets and branch", async () => {
    render(<App />);
    const user = await openProject();
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    act(() =>
      desktop.emit("operation:finished", {
        action: "ship",
        ok: false,
        error: "Network failed",
        history: attempt(),
      }),
    );
    await user.click(screen.getByRole("checkbox", { name: "windows" }));
    await user.click(screen.getByRole("button", { name: "Retry upload" }));
    expect(desktop.api.StartOperation).toHaveBeenLastCalledWith(
      expect.objectContaining({
        action: "upload",
        retry_id: "attempt-1",
        targets: ["windows", "linux"],
        set_live: "qa",
        profile: "full",
      }),
    );
    expect(screen.getByLabelText("Publishing destination")).toHaveTextContent(
      "Beta branch: qa",
    );
  });

  it("requires a new release when a failed ship has no upload checkpoint", async () => {
    render(<App />);
    const user = await openProject();
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    act(() =>
      desktop.emit("operation:finished", {
        action: "ship",
        ok: false,
        error: "Network failed",
        history: attempt({ upload_fingerprint: undefined }),
      }),
    );
    expect(
      screen.getByRole("button", { name: "Retry build & deploy" }),
    ).toBeDisabled();
    expect(
      screen.getAllByText(/Start a new build and deploy/),
    ).not.toHaveLength(0);
  });

  it("explains why a legacy upload without a checkpoint cannot be retried", async () => {
    const original = attempt();
    desktop.api.GetReleaseHistory.mockResolvedValue([
      attempt({
        upload_fingerprint: undefined,
        request: { ...original.request, action: "upload" },
      }),
    ]);
    render(<App />);
    const user = await openProject();
    await user.click(await screen.findByText("Local Release History (1)"));
    const entry = screen.getByRole("article", { name: "Release attempt-1" });
    expect(
      within(entry).getByText(/No artifact checkpoint was saved/),
    ).toBeInTheDocument();
    expect(
      within(entry).getByRole("button", { name: "Retry release attempt-1" }),
    ).toBeDisabled();
  });

  it("disables a partial ship retry for a custom Steam script", async () => {
    desktop.api.GetReleaseHistory.mockResolvedValue([
      attempt({ custom_upload_script: true, build_ready: false }),
    ]);
    render(<App />);
    const user = await openProject();
    await user.click(await screen.findByText("Local Release History (1)"));
    const entry = screen.getByRole("article", { name: "Release attempt-1" });
    expect(
      within(entry).getByText(/Custom Steam scripts cannot be verified/),
    ).toBeInTheDocument();
    expect(
      within(entry).getByRole("button", { name: "Retry release attempt-1" }),
    ).toBeDisabled();
  });

  it("loads persistent history with build IDs and logs and offers partial-build recovery", async () => {
    const record = attempt({
      id: "older",
      status: "complete",
      build_id: "12345",
      error: undefined,
    });
    desktop.api.GetReleaseHistory.mockResolvedValue([record]);
    render(<App />);
    const user = await openProject();
    await user.click(await screen.findByText("Local Release History (1)"));
    const entry = screen.getByRole("article", { name: "Release older" });
    expect(
      within(entry).getByText("Steam build ID: 12345"),
    ).toBeInTheDocument();
    expect(within(entry).getByText(/v1.0 · abcdef123456/)).toBeInTheDocument();
    await user.click(within(entry).getByText("Saved logs"));
    expect(within(entry).getByText(/Uploaded depot files/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    act(() =>
      desktop.emit("operation:finished", {
        action: "ship",
        ok: false,
        error: "Windows failed",
        history: attempt({ build_ready: false, completed_targets: ["linux"] }),
      }),
    );
    await user.click(
      screen.getByRole("button", {
        name: "Rebuild unfinished targets & deploy",
      }),
    );
    expect(desktop.api.StartOperation).toHaveBeenLastCalledWith(
      expect.objectContaining({
        action: "ship",
        retry_id: "attempt-1",
        targets: ["windows", "linux"],
      }),
    );
  });

  it("clears the displayed history across reopening and shows future releases", async () => {
    desktop.api.GetReleaseHistory.mockResolvedValue([attempt()]);
    desktop.api.ClearReleaseHistory.mockImplementation(async () => {
      desktop.api.GetReleaseHistory.mockResolvedValue([]);
    });
    const view = render(<App />);
    const user = await openProject();
    await user.click(await screen.findByText("Local Release History (1)"));
    const clear = screen.getByRole("button", {
      name: "Clear local release history",
    });
    expect(clear).toHaveAccessibleDescription(
      /Saved records remain stored locally/,
    );
    await user.click(clear);
    expect(desktop.api.ClearReleaseHistory).toHaveBeenCalledWith(
      config.path,
      "",
      ["attempt-1"],
    );
    await screen.findByText("Local Release History (0)");
    expect(
      screen.queryByRole("article", { name: "Release attempt-1" }),
    ).not.toBeInTheDocument();
    expect(clear).toBeDisabled();
    view.unmount();
    render(<App />);
    await openProject();
    await screen.findByText("Local Release History (0)");
    act(() =>
      desktop.emit("operation:finished", {
        action: "build",
        ok: true,
        history: attempt({ id: "new-release", status: "complete" }),
      }),
    );
    await user.click(await screen.findByText("Local Release History (1)"));
    expect(
      screen.getByRole("article", { name: "Release new-release" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("article", { name: "Release attempt-1" }),
    ).not.toBeInTheDocument();
  });

  it("preserves displayed history if clearing fails", async () => {
    desktop.api.GetReleaseHistory.mockResolvedValue([attempt()]);
    desktop.api.ClearReleaseHistory.mockRejectedValueOnce(
      new Error("Database unavailable"),
    );
    render(<App />);
    const user = await openProject();
    await user.click(await screen.findByText("Local Release History (1)"));
    const clear = screen.getByRole("button", {
      name: "Clear local release history",
    });
    await user.click(clear);
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        "Release history could not be cleared: Database unavailable",
      ),
    );
    expect(
      screen.getByRole("article", { name: "Release attempt-1" }),
    ).toBeInTheDocument();
    expect(clear).toBeEnabled();
  });

  it("clears older entries during a release and preserves a release that finishes while clearing", async () => {
    desktop.api.GetReleaseHistory.mockResolvedValue([attempt()]);
    let resolveClear: () => void = () => {};
    desktop.api.ClearReleaseHistory.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveClear = resolve;
      }),
    );
    render(<App />);
    const user = await openProject();
    await user.click(await screen.findByText("Local Release History (1)"));
    const clear = screen.getByRole("button", {
      name: "Clear local release history",
    });
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    expect(clear).toBeEnabled();
    await user.click(clear);
    expect(desktop.api.ClearReleaseHistory).toHaveBeenCalledExactlyOnceWith(
      config.path,
      "",
      ["attempt-1"],
    );
    expect(clear).toBeDisabled();
    expect(clear).toHaveTextContent("Clearing…");
    act(() =>
      desktop.emit("operation:finished", {
        action: "ship",
        ok: false,
        error: "Upload interrupted",
        history: attempt({ id: "new-release" }),
      }),
    );
    await act(async () => resolveClear());
    expect(screen.getByText("Local Release History (1)")).toBeInTheDocument();
    expect(
      screen.queryByRole("article", { name: "Release attempt-1" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("article", { name: "Release new-release" }),
    ).toBeInTheDocument();
    expect(clear).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Retry upload" }));
    expect(desktop.api.StartOperation).toHaveBeenLastCalledWith(
      expect.objectContaining({ retry_id: "new-release" }),
    );
  });

  it("keeps a cleared running entry hidden when its completion arrives", async () => {
    desktop.api.GetReleaseHistory.mockResolvedValue([
      attempt({ status: "running" }),
    ]);
    render(<App />);
    const user = await openProject();
    await user.click(await screen.findByText("Local Release History (1)"));
    act(() => desktop.emit("operation:started", "ship"));
    await user.click(
      screen.getByRole("button", { name: "Clear local release history" }),
    );
    await screen.findByText("Local Release History (0)");
    act(() =>
      desktop.emit("operation:finished", {
        action: "ship",
        ok: true,
        history: attempt({ status: "complete" }),
      }),
    );
    expect(screen.getByText("Local Release History (0)")).toBeInTheDocument();
    expect(
      screen.queryByRole("article", { name: "Release attempt-1" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Build & Deploy" }),
    ).toBeEnabled();
  });

  it("ignores an old history load that resolves after Clear", async () => {
    let resolveHistory: (records: ReleaseRecord[]) => void = () => {};
    desktop.api.GetReleaseHistory.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveHistory = resolve;
      }),
    );
    render(<App />);
    const user = await openProject();
    act(() =>
      desktop.emit("operation:finished", {
        action: "ship",
        ok: false,
        history: attempt(),
      }),
    );
    await user.click(await screen.findByText("Local Release History (1)"));
    await user.click(
      screen.getByRole("button", { name: "Clear local release history" }),
    );
    await screen.findByText("Local Release History (0)");
    await act(async () => resolveHistory([attempt()]));
    expect(screen.getByText("Local Release History (0)")).toBeInTheDocument();
    expect(
      screen.queryByRole("article", { name: "Release attempt-1" }),
    ).not.toBeInTheDocument();
  });

  it("enables native notifications by default, deduplicates Steam prompts, and saves the preference", async () => {
    render(<App />);
    const user = await openProject();
    expect(
      screen.queryByRole("checkbox", { name: "Background notifications" }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Client settings" }));
    expect(
      screen.getByRole("checkbox", { name: "Background notifications" }),
    ).toBeChecked();
    expect(desktop.api.ConfigureNotifications).toHaveBeenCalledExactlyOnceWith(
      true,
    );
    expect(desktop.preferences["steam-releaser:notifications"]).toBe("true");
    await user.click(
      screen.getByRole("button", { name: "Close client settings" }),
    );
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    expect(desktop.api.StartOperation).toHaveBeenLastCalledWith(
      expect.objectContaining({ notifications: true }),
    );
    act(() => {
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD upload",
        message: "Waiting for mobile approval in Steam",
      });
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD upload",
        message: "Waiting for mobile approval in Steam",
      });
    });
    expect(desktop.api.NotifySteamInput).toHaveBeenCalledTimes(1);
    act(() => desktop.emit("operation:finished", { action: "ship", ok: true }));
    await user.click(screen.getByRole("button", { name: "Client settings" }));
    await user.click(
      screen.getByRole("checkbox", { name: "Background notifications" }),
    );
    expect(desktop.api.ConfigureNotifications).toHaveBeenLastCalledWith(false);
    expect(desktop.preferences["steam-releaser:notifications"]).toBe("false");
  });

  it("keeps notifications disabled if system authorization fails", async () => {
    desktop.api.ConfigureNotifications.mockRejectedValueOnce(
      new Error("Permission denied"),
    );
    render(<App />);
    const user = await openProject();
    await user.click(screen.getByRole("button", { name: "Client settings" }));
    expect(
      screen.getByRole("checkbox", { name: "Background notifications" }),
    ).not.toBeChecked();
    expect(desktop.api.ConfigureNotifications).toHaveBeenCalledExactlyOnceWith(
      true,
    );
    expect(desktop.preferences["steam-releaser:notifications"]).toBe("false");
    await user.click(
      screen.getByRole("button", { name: "Close client settings" }),
    );
    expect(
      screen.getByRole("button", { name: "Build & Deploy" }),
    ).toBeEnabled();
  });

  it("alerts again when Steam progresses from a password prompt to Guard", async () => {
    render(<App />);
    const user = await openProject();
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    act(() => {
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD upload",
        message: "Enter your password:",
      });
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD upload",
        message: "Enter your password:",
      });
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD upload",
        message: "Enter Steam Guard code:",
      });
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD upload",
        message: "Enter Steam Guard code:",
      });
    });
    expect(desktop.api.NotifySteamInput).toHaveBeenCalledTimes(2);
  });
});
