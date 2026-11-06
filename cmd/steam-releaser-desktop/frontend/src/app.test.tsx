import { StrictMode } from "react";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { App } from "@/app";
import { config, mockDesktop, openExistingConfig } from "@/test/desktop-mock";

vi.mock("sonner", () => ({
  Toaster: () => null,
  toast: {
    success: vi.fn<typeof toast.success>(),
    error: vi.fn<typeof toast.error>(),
    warning: vi.fn<typeof toast.warning>(),
  },
}));

async function loadProject() {
  const user = userEvent.setup();
  expect(
    screen.getByRole("main", { name: "Project selector" }),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Build" }),
  ).not.toBeInTheDocument();
  await openExistingConfig();
  await user.click(
    screen.getByRole("button", { name: "Browse configuration" }),
  );
  expect(screen.getByRole("textbox", { name: "Configuration" })).toHaveValue(
    config.path,
  );
  await user.click(screen.getByRole("button", { name: "Load configuration" }));
  await screen.findByRole("heading", { name: "Demo Game / full" });
  expect(
    screen.queryByRole("main", { name: "Project selector" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("heading", { name: "Plan & diagnostics" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("heading", { name: "Release progress" }),
  ).not.toBeInTheDocument();
  return user;
}

async function chooseReleaseAction(
  user: ReturnType<typeof userEvent.setup>,
  action: "Build & Deploy" | "Doctor" | "Plan" | "Build" | "Upload",
) {
  await user.click(
    screen.getByRole("button", { name: "More release actions" }),
  );
  await user.click(screen.getByRole("menuitem", { name: action }));
}

async function runReleaseAction(
  user: ReturnType<typeof userEvent.setup>,
  action: "Doctor" | "Plan" | "Build" | "Upload",
) {
  await chooseReleaseAction(user, action);
  await user.click(screen.getByRole("button", { name: action }));
}

describe("release console", () => {
  let desktop: ReturnType<typeof mockDesktop>;
  beforeEach(() => {
    desktop = mockDesktop();
  });

  it("selects a release action without running it until the main button is pressed", async () => {
    render(<App />);
    const user = await loadProject();

    await chooseReleaseAction(user, "Doctor");
    expect(desktop.api.StartOperation).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Doctor" })).toBeEnabled();

    await chooseReleaseAction(user, "Plan");
    expect(desktop.api.StartOperation).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Plan" }));
    expect(desktop.api.StartOperation).toHaveBeenCalledWith(
      expect.objectContaining({ action: "plan" }),
    );

    act(() => desktop.emit("operation:finished", { action: "plan", ok: true }));
    await chooseReleaseAction(user, "Build & Deploy");
    expect(desktop.api.StartOperation).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    expect(desktop.api.StartOperation).toHaveBeenLastCalledWith(
      expect.objectContaining({ action: "ship" }),
    );
  });

  it("loads a browsed config, selects its default profile and resets targets when switching profiles", async () => {
    render(<App />);
    const user = await loadProject();
    expect(desktop.api.LoadConfig).toHaveBeenCalledWith(config.path, "");
    expect(desktop.preferences["steam-releaser:last-config"]).toBe(config.path);
    expect(
      screen.getByRole("button", { name: "Steam account cached_builder" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("status", {
        name: "Saved Steam account; login will be checked when needed",
      }),
    ).toHaveClass("bg-muted-foreground");
    expect(screen.queryByText("Idle")).not.toBeInTheDocument();
    expect(screen.queryByText("GODOT")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Build & Deploy" }),
    ).toBeEnabled();
    expect(
      screen.queryByRole("menuitem", { name: "Doctor" }),
    ).not.toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "More release actions" }),
    );
    for (const action of ["Doctor", "Plan", "Build", "Upload"]) {
      expect(
        screen.getByRole("menuitem", { name: action }),
      ).toBeInTheDocument();
    }
    await user.keyboard("{Escape}");
    expect(screen.getByRole("checkbox", { name: "windows" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "linux" })).toBeChecked();
    await user.click(screen.getByRole("checkbox", { name: "windows" }));
    await user.click(screen.getByRole("button", { name: "Select all" }));
    expect(screen.getByRole("checkbox", { name: "windows" })).toBeChecked();
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Profile" }),
      "demo",
    );
    expect(
      screen.queryByRole("checkbox", { name: "windows" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "demo-linux" })).toBeChecked();
  });

  it("keeps a machine-local project folder out of the shared YAML", async () => {
    desktop.api.OpenProjectDialog.mockResolvedValueOnce("/custom/game");
    render(<App />);
    const user = userEvent.setup();
    await openExistingConfig();
    await user.click(
      screen.getByRole("button", { name: "Browse configuration" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Browse game project folder" }),
    );
    expect(
      screen.getByRole("textbox", { name: "Game project folder" }),
    ).toHaveValue("/custom/game");
    await user.click(
      screen.getByRole("button", { name: "Load configuration" }),
    );
    await waitFor(() =>
      expect(desktop.api.LoadConfig).toHaveBeenCalledWith(
        config.path,
        "/custom/game",
      ),
    );
    expect(
      desktop.preferences[`steam-releaser:project-root:${config.path}`],
    ).toBe("/custom/game");
    await screen.findByRole("heading", { name: "Demo Game / full" });
    await runReleaseAction(user, "Plan");
    expect(
      screen.getByRole("heading", { name: "Plan & diagnostics" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Release progress" }),
    ).not.toBeInTheDocument();
    expect(desktop.api.StartOperation).toHaveBeenCalledWith(
      expect.objectContaining({ project_root: "/custom/game" }),
    );
  });

  it("shows only the output needed by the selected action and hides it when cleared", async () => {
    render(<App />);
    const user = await loadProject();
    await runReleaseAction(user, "Plan");
    expect(
      screen.getByRole("heading", { name: "Plan & diagnostics" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Release progress" }),
    ).not.toBeInTheDocument();
    act(() =>
      desktop.emit("operation:finished", {
        action: "plan",
        ok: true,
        data: { ready: true },
      }),
    );
    expect(screen.getByText(/"ready": true/)).toBeInTheDocument();

    await runReleaseAction(user, "Build");
    expect(
      screen.queryByRole("heading", { name: "Plan & diagnostics" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Release progress" }),
    ).toBeInTheDocument();
    act(() =>
      desktop.emit("operation:finished", { action: "build", ok: true }),
    );
    await user.click(screen.getByRole("button", { name: "Clear progress" }));
    expect(
      screen.queryByRole("heading", { name: "Release progress" }),
    ).not.toBeInTheDocument();
  });

  it("switches projects from the release screen and can return to the loaded project", async () => {
    render(<App />);
    const user = await loadProject();
    await user.type(
      screen.getByRole("textbox", { name: /Set live on beta branch/ }),
      "first-game-beta",
    );
    expect(screen.getByLabelText("Publishing destination")).toHaveTextContent(
      "App 20 · Beta branch: first-game-beta",
    );
    await user.click(screen.getByRole("checkbox", { name: "Preview only" }));
    expect(screen.getByLabelText("Publishing destination")).toHaveTextContent(
      "App 20 · Preview only",
    );
    await user.click(screen.getByRole("button", { name: "Change project" }));
    expect(
      screen.getByRole("main", { name: "Project selector" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Build" }),
    ).not.toBeInTheDocument();

    await openExistingConfig();
    await user.clear(screen.getByRole("textbox", { name: "Configuration" }));
    await user.type(
      screen.getByRole("textbox", { name: "Configuration" }),
      "/games/other/steam-release.yaml",
    );
    await user.click(screen.getByRole("button", { name: "Back to release" }));
    expect(
      screen.getByRole("heading", { name: "Demo Game / full" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: "Preview only" }),
    ).toBeChecked();
    expect(
      screen.getByRole("textbox", { name: /Set live on beta branch/ }),
    ).toHaveValue("first-game-beta");

    await user.click(screen.getByRole("button", { name: "Change project" }));
    expect(screen.getByRole("textbox", { name: "Configuration" })).toHaveValue(
      config.path,
    );
    const other = {
      ...config,
      path: "/games/other/steam-release.yaml",
      project: "Other Game",
      profiles: config.profiles.map((profile) => ({
        ...profile,
        app_id: profile.app_id + 100,
      })),
    };
    desktop.api.LoadConfig.mockResolvedValueOnce(other);
    await user.clear(screen.getByRole("textbox", { name: "Configuration" }));
    await user.type(
      screen.getByRole("textbox", { name: "Configuration" }),
      other.path,
    );
    await user.click(
      screen.getByRole("button", { name: "Load configuration" }),
    );
    await screen.findByRole("heading", { name: "Other Game / full" });
    expect(
      screen.queryByRole("textbox", { name: "Configuration" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: "Preview only" }),
    ).not.toBeChecked();
    expect(
      screen.getByRole("textbox", { name: /Set live on beta branch/ }),
    ).toHaveValue("");
    expect(screen.getByLabelText("Publishing destination")).toHaveTextContent(
      "App 120 · No beta branch selected",
    );
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    expect(desktop.api.StartOperation).toHaveBeenCalledWith(
      expect.objectContaining({
        config_path: other.path,
        preview: false,
        set_live: "",
      }),
    );
  });

  it("resets publishing settings when switching profiles", async () => {
    render(<App />);
    const user = await loadProject();
    await user.type(
      screen.getByRole("textbox", { name: /Set live on beta branch/ }),
      "full-game-beta",
    );
    await user.click(screen.getByRole("checkbox", { name: "Preview only" }));
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Profile" }),
      "demo",
    );
    expect(
      screen.getByRole("checkbox", { name: "Preview only" }),
    ).not.toBeChecked();
    expect(
      screen.getByRole("textbox", { name: /Set live on beta branch/ }),
    ).toHaveValue("");
    expect(screen.getByLabelText("Publishing destination")).toHaveTextContent(
      "App 10 · No beta branch selected",
    );
    await runReleaseAction(user, "Upload");
    expect(desktop.api.StartOperation).toHaveBeenCalledWith(
      expect.objectContaining({
        profile: "demo",
        preview: false,
        set_live: "",
      }),
    );
  });

  it("quick opens recent projects after restart with their saved game folders", async () => {
    const other = {
      ...config,
      path: "/games/other/steam-release.yaml",
      project: "Other Game",
    };
    const user = userEvent.setup();
    const firstSession = render(<App />);
    await openExistingConfig();
    await user.click(
      screen.getByRole("button", { name: "Browse configuration" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Browse game project folder" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Load configuration" }),
    );
    await screen.findByRole("heading", { name: "Demo Game / full" });
    await user.click(screen.getByRole("button", { name: "Change project" }));
    desktop.api.LoadConfig.mockResolvedValueOnce(other);
    await user.clear(screen.getByRole("textbox", { name: "Configuration" }));
    await user.type(
      screen.getByRole("textbox", { name: "Configuration" }),
      other.path,
    );
    await user.click(
      screen.getByRole("button", { name: "Load configuration" }),
    );
    await screen.findByRole("heading", { name: "Other Game / full" });
    firstSession.unmount();

    render(<App />);
    const recent = screen.getByRole("region", { name: "Recently opened" });
    expect(
      within(recent)
        .getAllByRole("button")
        .map((button) => button.textContent),
    ).toEqual([
      expect.stringContaining("Other Game"),
      expect.stringContaining("Demo Game"),
    ]);
    await user.click(
      within(recent).getByRole("button", {
        name: `Open Demo Game: ${config.path}`,
      }),
    );
    await screen.findByRole("heading", { name: "Demo Game / full" });
    expect(desktop.api.LoadConfig).toHaveBeenLastCalledWith(
      config.path,
      "/games/demo",
    );
    expect(
      JSON.parse(desktop.preferences["steam-releaser:recent-configs"]!),
    ).toEqual([
      { path: config.path, project: "Demo Game" },
      { path: other.path, project: "Other Game" },
    ]);
  });

  it("sends selected targets and publishing options, then stays locked until the finish event", async () => {
    render(<App />);
    const user = await loadProject();
    await user.click(screen.getByRole("checkbox", { name: "linux" }));
    await user.type(
      screen.getByRole("textbox", { name: /Set live on beta branch/ }),
      " beta ",
    );
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    expect(
      screen.getByRole("heading", { name: "Release progress" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Plan & diagnostics" }),
    ).not.toBeInTheDocument();
    expect(desktop.api.StartOperation).toHaveBeenCalledExactlyOnceWith({
      action: "ship",
      config_path: config.path,
      project_root: "",
      profile: "full",
      targets: ["windows"],
      username: "cached_builder",
      preview: false,
      set_live: "beta",
      notifications: true,
    });
    expect(
      screen.getByRole("button", { name: "Build & Deploy" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "More release actions" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Change project" }),
    ).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Profile" })).toBeDisabled();
    act(() => desktop.emit("operation:started", "ship"));
    act(() =>
      desktop.emit("release:event", {
        level: "info",
        target: "windows",
        kind: "build-start",
        message: "Building",
      }),
    );
    act(() =>
      desktop.emit("release:event", {
        level: "info",
        target: "windows",
        message: "Uploading build",
      }),
    );
    expect(
      within(
        screen.getByRole("group", { name: "windows progress" }),
      ).getByTitle("Uploading build"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(desktop.api.CancelOperation).toHaveBeenCalledOnce();
    expect(screen.getByRole("button", { name: "Cancelling…" })).toBeDisabled();
    act(() =>
      desktop.emit("operation:finished", {
        action: "ship",
        ok: true,
        data: {
          username: "cached_builder",
          build_script: "/games/demo/.steam-releaser/app_build.vdf",
          scripts: { app_path: "/games/demo/.steam-releaser/app_build.vdf" },
        },
      }),
    );
    expect(screen.getByText("Steam upload completed")).toBeInTheDocument();
    expect(
      screen.getByText("App ID 20 · Profile full · Targets: windows"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Set live on beta branch beta."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/app_build\.vdf/)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Operation result" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Build & Deploy" }),
    ).toBeEnabled();
    expect(
      screen.queryByRole("button", { name: "Cancel" }),
    ).not.toBeInTheDocument();
  });

  it("labels a successful preview without calling it an upload", async () => {
    render(<App />);
    const user = await loadProject();
    await user.click(screen.getByRole("checkbox", { name: "Preview only" }));
    await runReleaseAction(user, "Upload");
    act(() =>
      desktop.emit("operation:finished", {
        action: "upload",
        ok: true,
        data: { build_script: "/games/demo/.steam-releaser/app_build.vdf" },
      }),
    );
    expect(screen.getByText("Steam preview completed")).toBeInTheDocument();
    expect(
      screen.getByText("Preview only; no content was uploaded."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("App ID 20 · Profile full · Targets: windows, linux"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/app_build\.vdf/)).not.toBeInTheDocument();
  });

  it("rejects an empty target selection but allows clearing the cached account", async () => {
    render(<App />);
    const user = await loadProject();
    await user.click(screen.getByRole("button", { name: "Clear all" }));
    await runReleaseAction(user, "Plan");
    expect(desktop.api.StartOperation).not.toHaveBeenCalled();
    expect(toast.warning).toHaveBeenCalledWith("Select at least one target.");
    await user.click(
      screen.getByRole("button", { name: "Steam account cached_builder" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Clear Steam Credentials" }),
    );
    expect(desktop.api.StartOperation).toHaveBeenCalledWith(
      expect.objectContaining({ action: "logout", targets: [] }),
    );
    expect(
      screen.queryByRole("dialog", { name: "Steam sign-in" }),
    ).not.toBeInTheDocument();
    act(() =>
      desktop.emit("operation:finished", { action: "logout", ok: true }),
    );
    expect(
      screen.getByRole("button", { name: "Sign in to Steam" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("status", { name: "Steam sign-in required" }),
    ).toHaveClass("bg-muted-foreground");
  });

  it("signs in to Steam before any project is loaded", async () => {
    render(<App />);
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", {
        name: "Steam account cached_builder",
      }),
    );
    const dialog = screen.getByRole("dialog", { name: "Steam sign-in" });
    await user.click(
      within(dialog).getByRole("button", { name: "Continue with SteamCMD" }),
    );
    expect(desktop.api.StartOperation).toHaveBeenCalledWith(
      expect.objectContaining({
        action: "login",
        username: "cached_builder",
        config_path: "",
        project_root: "",
      }),
    );
  });

  it("tries the saved Steam login before requesting a password, then handles Steam Guard", async () => {
    render(<App />);
    const user = await loadProject();
    await user.click(screen.getByRole("button", { name: "Clear all" }));
    await user.click(
      screen.getByRole("button", { name: "Steam account cached_builder" }),
    );
    const dialog = screen.getByRole("dialog", { name: "Steam sign-in" });
    expect(
      within(dialog).getByRole("textbox", { name: "Steam account" }),
    ).toHaveValue("cached_builder");
    await user.clear(
      within(dialog).getByRole("textbox", { name: "Steam account" }),
    );
    expect(desktop.api.StartOperation).not.toHaveBeenCalled();
    await user.type(
      within(dialog).getByRole("textbox", { name: "Steam account" }),
      "builder",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Continue with SteamCMD" }),
    );
    expect(desktop.api.StartOperation).toHaveBeenCalledWith(
      expect.objectContaining({
        action: "login",
        username: "builder",
        targets: [],
      }),
    );
    expect(desktop.api.SubmitSteamInput).not.toHaveBeenCalled();
    act(() =>
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD login",
        message: "Password: ",
      }),
    );
    const password = within(dialog).getByLabelText("Steam password");
    await user.type(password, "private-password");
    await user.click(
      within(dialog).getByRole("button", { name: "Send response" }),
    );
    expect(desktop.api.SubmitSteamInput).toHaveBeenCalledExactlyOnceWith(
      "private-password",
    );
    act(() =>
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD login",
        message: "Steam Guard code: ",
      }),
    );
    const response = within(dialog).getByLabelText("Steam Guard response");
    expect(response).toHaveAttribute("type", "password");
    await user.type(response, "guard-code");
    await user.click(
      within(dialog).getByRole("button", { name: "Send response" }),
    );
    expect(desktop.api.SubmitSteamInput).toHaveBeenLastCalledWith("guard-code");
    await waitFor(() =>
      expect(
        within(dialog).queryByLabelText("Steam Guard response"),
      ).not.toBeInTheDocument(),
    );
    expect(screen.queryByText("private-password")).not.toBeInTheDocument();
    act(() =>
      desktop.emit("operation:finished", {
        action: "login",
        ok: true,
        data: { username: "builder" },
      }),
    );
    expect(
      screen.queryByRole("dialog", { name: "Steam sign-in" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Steam account builder" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("status", { name: "Steam login verified" }),
    ).toHaveClass("bg-emerald-400");
    await user.click(
      screen.getByRole("button", { name: "Steam account builder" }),
    );
    expect(screen.getByRole("textbox", { name: "Steam account" })).toHaveValue(
      "builder",
    );
    expect(screen.queryByLabelText("Steam password")).not.toBeInTheDocument();
  });

  it("turns the Steam indicator grey after expired credentials and allows reauthentication", async () => {
    render(<App />);
    const user = await loadProject();
    await user.click(
      screen.getByRole("button", { name: "Steam account cached_builder" }),
    );
    await user.click(
      within(screen.getByRole("dialog", { name: "Steam sign-in" })).getByRole(
        "button",
        { name: "Continue with SteamCMD" },
      ),
    );
    act(() =>
      desktop.emit("operation:finished", {
        action: "login",
        ok: true,
        data: { username: "cached_builder" },
      }),
    );
    expect(
      screen.getByRole("status", { name: "Steam login verified" }),
    ).toHaveClass("bg-emerald-400");

    await runReleaseAction(user, "Upload");
    act(() =>
      desktop.emit("operation:finished", {
        action: "upload",
        ok: false,
        error: "Steam credentials expired",
      }),
    );
    expect(
      screen.getByRole("status", { name: "Steam sign-in required" }),
    ).toHaveClass("bg-muted-foreground");
    expect(
      screen.queryByRole("dialog", { name: "Steam sign-in" }),
    ).not.toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Steam account cached_builder" }),
    );
    expect(screen.getByRole("textbox", { name: "Steam account" })).toHaveValue(
      "cached_builder",
    );
    expect(screen.queryByLabelText("Steam password")).not.toBeInTheDocument();
    await user.click(
      within(screen.getByRole("dialog", { name: "Steam sign-in" })).getByRole(
        "button",
        { name: "Continue with SteamCMD" },
      ),
    );
    act(() =>
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD login",
        message: "Password: ",
      }),
    );
    await user.type(screen.getByLabelText("Steam password"), "fresh-password");
    await user.click(screen.getByRole("button", { name: "Send response" }));
    expect(desktop.api.SubmitSteamInput).toHaveBeenLastCalledWith(
      "fresh-password",
    );
    act(() =>
      desktop.emit("operation:finished", {
        action: "login",
        ok: true,
        data: { username: "cached_builder" },
      }),
    );
    expect(
      screen.getByRole("status", { name: "Steam login verified" }),
    ).toHaveClass("bg-emerald-400");
  });

  it("uses the saved Steam login during upload without asking for a password", async () => {
    render(<App />);
    const user = await loadProject();
    await runReleaseAction(user, "Upload");
    expect(desktop.api.StartOperation).toHaveBeenCalledWith(
      expect.objectContaining({ action: "upload", username: "cached_builder" }),
    );
    act(() =>
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD upload",
        message: "Logging in using cached credentials.",
      }),
    );
    expect(screen.queryByRole("dialog", { name: "Steam sign-in" })).toBeNull();
    act(() =>
      desktop.emit("operation:finished", { action: "upload", ok: true }),
    );
    expect(
      screen.getByRole("status", { name: "Steam login verified" }),
    ).toHaveClass("bg-emerald-400");
  });

  it("accepts SteamCMD authentication during upload when cached credentials are missing", async () => {
    render(<App />);
    const user = await loadProject();
    await runReleaseAction(user, "Upload");
    expect(desktop.api.StartOperation).toHaveBeenCalledWith(
      expect.objectContaining({ action: "upload", username: "cached_builder" }),
    );
    expect(screen.queryByLabelText("Steam password")).not.toBeInTheDocument();
    act(() =>
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD upload",
        message: "Password: ",
      }),
    );
    await user.type(screen.getByLabelText("Steam password"), "secret");
    await user.click(screen.getByRole("button", { name: "Send response" }));
    expect(desktop.api.SubmitSteamInput).toHaveBeenCalledWith("secret");
  });

  it.each(["login", "upload", "ship"] as const)(
    "can cancel %s from its authentication prompt and retry a failed cancellation",
    async (action) => {
      render(<App />);
      const user = await loadProject();
      if (action === "login") {
        await user.click(
          screen.getByRole("button", { name: "Steam account cached_builder" }),
        );
        await user.click(
          screen.getByRole("button", { name: "Continue with SteamCMD" }),
        );
      } else if (action === "ship") {
        await user.click(
          screen.getByRole("button", { name: "Build & Deploy" }),
        );
      } else {
        await runReleaseAction(user, "Upload");
      }
      act(() => {
        desktop.emit("operation:started", action);
        desktop.emit("release:event", {
          level: "info",
          target: action === "login" ? "SteamCMD login" : "SteamCMD upload",
          message: action === "ship" ? "Steam Guard code: " : "Password: ",
        });
      });
      const dialog = screen.getByRole("dialog", { name: "Steam sign-in" });
      const response = within(dialog).getByLabelText(
        action === "ship" ? "Steam Guard response" : "Steam password",
      );
      await user.type(response, "unused-secret");
      const cancelName =
        action === "login"
          ? "Cancel sign-in"
          : action === "ship"
            ? "Cancel release"
            : "Cancel upload";
      desktop.api.CancelOperation.mockRejectedValueOnce(
        new Error("Cancellation unavailable"),
      );
      await user.click(
        within(dialog).getByRole("button", { name: cancelName }),
      );
      expect(toast.error).toHaveBeenCalledWith("Cancellation unavailable");
      expect(
        within(dialog).getByRole("button", { name: cancelName }),
      ).toBeEnabled();
      expect(response).toHaveValue("");
      await user.click(
        within(dialog).getByRole("button", { name: cancelName }),
      );
      const cancelling = within(dialog).getByRole("button", {
        name: "Cancelling…",
      });
      expect(cancelling).toBeDisabled();
      expect(response).toBeDisabled();
      await user.click(cancelling);
      expect(desktop.api.CancelOperation).toHaveBeenCalledTimes(2);
      expect(desktop.api.SubmitSteamInput).not.toHaveBeenCalled();
      act(() =>
        desktop.emit("operation:finished", {
          action,
          ok: false,
          error: "Operation cancelled",
        }),
      );
      expect(screen.queryByRole("dialog", { name: "Steam sign-in" })).toBe(
        action === "login" ? dialog : null,
      );
      expect(
        screen.getByRole("button", {
          name:
            action === "login"
              ? "Continue with SteamCMD"
              : action === "upload"
                ? "Upload"
                : "Build & Deploy",
        }),
      ).toBeEnabled();
    },
  );

  it("recovers from load, start and cancellation failures and shows failed diagnostics", async () => {
    desktop.api.LoadConfig.mockRejectedValueOnce(new Error("Invalid YAML"));
    render(<App />);
    const user = userEvent.setup();
    await openExistingConfig();
    await user.click(
      screen.getByRole("button", { name: "Browse configuration" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Load configuration" }),
    );
    expect(toast.error).toHaveBeenCalledWith("Invalid YAML");
    expect(
      screen.getByRole("main", { name: "Project selector" }),
    ).toBeInTheDocument();
    await loadProject();
    desktop.api.StartOperation.mockRejectedValueOnce(
      new Error("Already running"),
    );
    await runReleaseAction(user, "Build");
    expect(toast.error).toHaveBeenCalledWith("Already running");
    expect(
      screen.getByRole("button", { name: "More release actions" }),
    ).toBeEnabled();
    await runReleaseAction(user, "Doctor");
    act(() => desktop.emit("operation:started", "doctor"));
    desktop.api.CancelOperation.mockRejectedValueOnce(
      new Error("Cancel failed"),
    );
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(toast.error).toHaveBeenCalledWith("Cancel failed");
    expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled();
    act(() =>
      desktop.emit("operation:finished", {
        action: "doctor",
        ok: false,
        error: "Blocking issues",
        data: { ok: false },
      }),
    );
    expect(screen.getByText(/"ok": false/)).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("Blocking issues");
    await user.click(screen.getByRole("button", { name: "Clear results" }));
    expect(screen.queryByText(/"ok": false/)).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Plan & diagnostics" }),
    ).not.toBeInTheDocument();
  });

  it("offers the saved config on the selector and cleans up event subscriptions under StrictMode", async () => {
    localStorage.setItem("steam-releaser:last-config", config.path);
    const { unmount } = render(
      <StrictMode>
        <App />
      </StrictMode>,
    );
    await waitFor(() =>
      expect(
        screen.getByRole("textbox", { name: "Configuration" }),
      ).toHaveValue(config.path),
    );
    expect(desktop.api.LoadConfig).not.toHaveBeenCalled();
    await userEvent.setup().click(
      screen.getByRole("button", {
        name: `Open configuration: ${config.path}`,
      }),
    );
    await screen.findByRole("heading", { name: "Demo Game / full" });
    desktop.listeners.forEach((callbacks) => expect(callbacks.size).toBe(1));
    act(() => desktop.emit("operation:started", "build"));
    expect(desktop.listeners.get("release:event")?.size).toBe(1);
    unmount();
    desktop.listeners.forEach((callbacks) => expect(callbacks.size).toBe(0));
  });

  it("tracks each platform separately, limits recent output, and renders output as text", async () => {
    render(<App />);
    const user = await loadProject();
    await runReleaseAction(user, "Build");
    act(() => {
      desktop.emit("release:event", {
        level: "info",
        target: "windows",
        kind: "build-start",
        message: "Building",
      });
      desktop.emit("release:event", {
        level: "info",
        target: "linux",
        kind: "build-start",
        message: "Building",
      });
      for (let i = 0; i < 1001; i++)
        desktop.emit("release:event", {
          level: "info",
          target: "windows",
          message: `entry-${i}`,
        });
      desktop.emit("release:event", {
        level: "error",
        target: "linux",
        message: "<script>alert('unsafe')</script>",
      });
    });
    const windows = screen.getByRole("group", { name: "windows progress" });
    const linux = screen.getByRole("group", { name: "linux progress" });
    expect(within(windows).getByRole("progressbar")).toHaveAttribute(
      "value",
      "90",
    );
    expect(within(linux).getByRole("progressbar")).toHaveAttribute(
      "value",
      "3",
    );
    expect(within(windows).getByText("entry-1000")).toBeInTheDocument();
    expect(within(windows).queryByText("entry-0")).not.toBeInTheDocument();
    expect(
      windows.querySelector("pre")?.textContent?.split("\n").length,
    ).toBeLessThanOrEqual(24);
    expect(linux.querySelector("script")).toBeNull();
    expect(
      within(linux).getByTitle("<script>alert('unsafe')</script>"),
    ).toBeInTheDocument();
  });

  it("shows build completion, prebuilt artifacts, and Steam upload progress", async () => {
    render(<App />);
    const user = await loadProject();
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    act(() =>
      desktop.emit("release:event", {
        level: "info",
        target: "Godot import",
        message: "Importing game assets",
      }),
    );
    expect(
      screen.getByRole("group", { name: "Godot imports progress" }),
    ).toHaveTextContent("Importing game assets");
    act(() => {
      desktop.emit("release:event", {
        level: "info",
        target: "windows",
        kind: "build-start",
        message: "Building",
      });
      desktop.emit("release:event", {
        level: "info",
        target: "windows",
        message: "Exporting 43%",
      });
      desktop.emit("release:event", {
        level: "success",
        target: "linux",
        kind: "build-prebuilt",
        message: "Using prebuilt artifact",
      });
    });
    const windows = screen.getByRole("group", { name: "windows progress" });
    const linux = screen.getByRole("group", { name: "linux progress" });
    expect(within(windows).getByRole("progressbar")).toHaveAttribute(
      "value",
      "43",
    );
    expect(within(linux).getByRole("progressbar")).toHaveAttribute(
      "value",
      "100",
    );
    expect(within(linux).getByText("Prebuilt artifact")).toBeInTheDocument();
    act(() => {
      desktop.emit("release:event", {
        level: "success",
        target: "windows",
        kind: "build-complete",
        message: "Build completed",
      });
      desktop.emit("release:event", {
        level: "info",
        target: "windows",
        message: "Copied launcher into depot",
      });
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD upload",
        message: "Uploading 67%",
      });
    });
    expect(screen.getByText("Uploading to Steam")).toBeInTheDocument();
    expect(within(windows).getByRole("progressbar")).toHaveAttribute(
      "value",
      "100",
    );
    expect(
      within(
        screen.getByRole("group", { name: "Steam upload progress" }),
      ).getByRole("progressbar"),
    ).toHaveAttribute("value", "67");
    act(() => desktop.emit("operation:finished", { action: "ship", ok: true }));
    expect(
      within(
        screen.getByRole("group", { name: "Steam upload progress" }),
      ).getByRole("progressbar"),
    ).toHaveAttribute("value", "100");
  });

  it("keeps an actual build failure distinct from cancellation of sibling workers", async () => {
    render(<App />);
    const user = await loadProject();
    await runReleaseAction(user, "Build");
    act(() => {
      desktop.emit("release:event", {
        level: "info",
        target: "windows",
        kind: "build-start",
        message: "Building",
      });
      desktop.emit("release:event", {
        level: "error",
        target: "windows",
        kind: "build-failed",
        message: "compiler failed",
      });
      desktop.emit("operation:finished", {
        action: "build",
        ok: false,
        error: "compiler failed; sibling cancelled",
      });
    });
    expect(
      screen.getByRole("group", { name: "windows progress" }),
    ).toHaveTextContent("Failed");
    expect(
      within(screen.getByRole("group", { name: "windows progress" })).getByRole(
        "progressbar",
      ),
    ).toHaveAttribute("value", "2");
    expect(
      screen.getByRole("group", { name: "linux progress" }),
    ).toHaveTextContent("Skipped");
  });

  it("handles browser preview and unavailable local storage without a backend", () => {
    delete window.go;
    delete window.runtime;
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("Storage disabled");
    });
    render(<App />);
    expect(screen.getByText("Desktop connection required")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Open existing config" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Set up a new game" }),
    ).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: "Build" }),
    ).not.toBeInTheDocument();
  });

  it("loads a typed path with Enter, tolerates a cancelled picker and storage write failures", async () => {
    desktop.api.OpenConfigDialog.mockResolvedValueOnce("");
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("Storage disabled");
    });
    render(<App />);
    const user = userEvent.setup();
    await openExistingConfig();
    await user.click(
      screen.getByRole("button", { name: "Browse configuration" }),
    );
    expect(desktop.api.LoadConfig).not.toHaveBeenCalled();
    await user.type(
      screen.getByRole("textbox", { name: "Configuration" }),
      ` ${config.path} {Enter}`,
    );
    await waitFor(() =>
      expect(desktop.api.LoadConfig).toHaveBeenCalledWith(config.path, ""),
    );
    await screen.findByRole("heading", { name: "Demo Game / full" });
    expect(toast.error).not.toHaveBeenCalled();
  });
});
