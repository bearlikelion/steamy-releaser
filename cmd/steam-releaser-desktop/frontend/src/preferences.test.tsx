import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { App } from "@/app";
import {
  initializePreferences,
  readLastConfig,
  readNotifications,
  rememberNotifications,
} from "@/lib/desktop";
import { config, mockDesktop } from "@/test/desktop-mock";

vi.mock("sonner", () => ({
  Toaster: () => null,
  toast: {
    success: vi.fn<typeof toast.success>(),
    error: vi.fn<typeof toast.error>(),
    warning: vi.fn<typeof toast.warning>(),
  },
}));

describe("SQLite desktop preferences", () => {
  let desktop: ReturnType<typeof mockDesktop>;
  beforeEach(() => {
    desktop = mockDesktop();
  });

  it("defaults notifications on, saves changes before loading a project, and restores focus on close", async () => {
    const user = userEvent.setup();
    render(<App />);

    expect(
      screen.queryByRole("checkbox", { name: "Background notifications" }),
    ).not.toBeInTheDocument();
    const trigger = screen.getByRole("button", { name: "Client settings" });
    await user.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "Client settings" });
    const notifications = within(dialog).getByRole("checkbox", {
      name: "Background notifications",
    });
    expect(notifications).toHaveAccessibleDescription(
      "Get system alerts for completed builds, release failures, and Steam sign-in requests.",
    );
    expect(notifications).toBeChecked();
    await user.click(notifications);
    await waitFor(() =>
      expect(desktop.preferences["steam-releaser:notifications"]).toBe("false"),
    );
    expect(desktop.api.LoadConfig).not.toHaveBeenCalled();

    await user.keyboard("{Escape}");
    expect(
      screen.queryByRole("dialog", { name: "Client settings" }),
    ).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(
      screen.getByRole("checkbox", { name: "Background notifications" }),
    ).not.toBeChecked();
    await user.click(
      screen.getByRole("button", { name: "Close client settings" }),
    );
    expect(
      screen.getByRole("main", { name: "Project selector" }),
    ).toBeInTheDocument();
  });

  it("imports browser preferences once and keeps SQLite values when both exist", async () => {
    localStorage.setItem("steam-releaser:last-config", config.path);
    localStorage.setItem("steam-releaser:notifications", "true");
    localStorage.setItem("unrelated-app", "keep me");
    desktop.preferences["steam-releaser:notifications"] = "false";

    await initializePreferences();
    await initializePreferences();

    expect(readLastConfig()).toBe(config.path);
    expect(readNotifications()).toBe(false);
    expect(desktop.api.GetPreferences).toHaveBeenCalledTimes(1);
    expect(localStorage.getItem("steam-releaser:last-config")).toBeNull();
    expect(localStorage.getItem("steam-releaser:notifications")).toBeNull();
    expect(localStorage.getItem("unrelated-app")).toBe("keep me");

    render(<App />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Client settings" }));
    expect(
      screen.getByRole("checkbox", { name: "Background notifications" }),
    ).not.toBeChecked();
    expect(desktop.api.ConfigureNotifications).not.toHaveBeenCalled();
  });

  it("enables a credential file through Browse, changes its location, and disables it without clearing the path", async () => {
    const user = userEvent.setup();
    render(<App />);
    await user.click(screen.getByRole("button", { name: "Client settings" }));
    const enable = screen.getByRole("checkbox", {
      name: "Use an existing Steam credential file",
    });
    expect(enable).toHaveAccessibleDescription(/existing JSON file/);
    expect(enable).toHaveAccessibleDescription(/only reads it/);
    await user.click(enable);
    expect(desktop.api.ChooseCredentialFile).toHaveBeenCalledTimes(1);
    expect(desktop.api.ConfigureCredentialFile).toHaveBeenCalledWith(
      true,
      "/private/steam-credentials.json",
    );
    expect(
      screen.getByRole("textbox", { name: "Credential file" }),
    ).toHaveValue("/private/steam-credentials.json");
    expect(
      screen.getByText(`File account: ${config.username}`),
    ).toBeInTheDocument();

    desktop.api.ChooseCredentialFile.mockResolvedValue("/private/other.json");
    await user.click(
      screen.getByRole("button", { name: "Browse credential file" }),
    );
    expect(
      screen.getByRole("textbox", { name: "Credential file" }),
    ).toHaveValue("/private/other.json");
    await user.click(enable);
    expect(desktop.api.ConfigureCredentialFile).toHaveBeenLastCalledWith(
      false,
      "/private/other.json",
    );
    expect(enable).not.toBeChecked();
    expect(
      screen.queryByRole("textbox", { name: "Credential file" }),
    ).not.toBeInTheDocument();
    await user.click(enable);
    expect(desktop.api.ChooseCredentialFile).toHaveBeenCalledTimes(3);
    expect(
      screen.getByRole("textbox", { name: "Credential file" }),
    ).toHaveValue("/private/other.json");
  });

  it("keeps credential file use off when Browse is cancelled or the file is invalid", async () => {
    const user = userEvent.setup();
    desktop.api.ChooseCredentialFile.mockResolvedValueOnce("");
    render(<App />);
    await user.click(screen.getByRole("button", { name: "Client settings" }));
    const enable = screen.getByRole("checkbox", {
      name: "Use an existing Steam credential file",
    });
    await user.click(enable);
    expect(enable).not.toBeChecked();
    expect(desktop.api.ConfigureCredentialFile).not.toHaveBeenCalled();
    desktop.api.ConfigureCredentialFile.mockRejectedValueOnce(
      new Error("Invalid credential file"),
    );
    await user.click(enable);
    expect(enable).not.toBeChecked();
    expect(toast.warning).toHaveBeenCalledWith(
      "Credential file could not be configured: Invalid credential file",
    );
  });

  it("keeps an imported account when the startup account lookup finishes late", async () => {
    const user = userEvent.setup();
    let resolveAccount!: (username: string) => void;
    desktop.api.CachedUsername.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolveAccount = done;
        }),
    );
    desktop.api.ConfigureCredentialFile.mockResolvedValueOnce({
      enabled: true,
      path: "/private/steam-credentials.json",
      username: "file_builder",
      has_credentials: true,
    });
    render(<App />);
    await user.click(screen.getByRole("button", { name: "Client settings" }));
    await user.click(
      screen.getByRole("checkbox", {
        name: "Use an existing Steam credential file",
      }),
    );
    await user.click(
      screen.getByRole("button", { name: "Close client settings" }),
    );
    await act(async () => resolveAccount("previous_builder"));
    expect(
      screen.getByRole("button", { name: "Steam account file_builder" }),
    ).toBeInTheDocument();
  });

  it("restores credential file settings after restart and allows disabling an unavailable file", async () => {
    const user = userEvent.setup();
    desktop.api.GetCredentialFileSettings.mockResolvedValueOnce({
      enabled: true,
      path: "/private/missing.json",
      username: "",
      has_credentials: false,
      problem: "Credential file is missing",
    });
    render(<App />);
    await user.click(screen.getByRole("button", { name: "Client settings" }));
    const enable = screen.getByRole("checkbox", {
      name: "Use an existing Steam credential file",
    });
    expect(enable).toBeChecked();
    expect(
      screen.getByRole("textbox", { name: "Credential file" }),
    ).toHaveValue("/private/missing.json");
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Credential file is missing",
    );
    await user.click(enable);
    expect(enable).not.toBeChecked();
    expect(desktop.api.ConfigureCredentialFile).toHaveBeenCalledWith(
      false,
      "/private/missing.json",
    );
  });

  it("preserves browser data if SQLite import fails and allows loading a project", async () => {
    localStorage.setItem("steam-releaser:last-config", config.path);
    desktop.api.GetPreferences.mockRejectedValueOnce(
      new Error("Database unavailable"),
    );
    render(<App />);

    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Load configuration" }),
      ).toBeEnabled(),
    );
    expect(screen.getByRole("textbox", { name: "Configuration" })).toHaveValue(
      config.path,
    );
    expect(localStorage.getItem("steam-releaser:last-config")).toBe(
      config.path,
    );
    expect(toast.warning).toHaveBeenCalledWith(
      "Local preferences could not be loaded: Database unavailable",
    );
  });

  it("waits for saved preferences before enabling project controls", async () => {
    let resolve!: (values: Record<string, string>) => void;
    desktop.api.GetPreferences.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    render(<App />);
    expect(
      screen.getByRole("button", { name: "Browse configuration" }),
    ).toBeDisabled();
    resolve({ "steam-releaser:last-config": config.path });
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Browse configuration" }),
      ).toBeEnabled(),
    );
    expect(screen.getByRole("textbox", { name: "Configuration" })).toHaveValue(
      config.path,
    );
    await waitFor(() =>
      expect(desktop.api.SavePreferences).toHaveBeenCalledExactlyOnceWith({
        "steam-releaser:notifications": "true",
      }),
    );
  });

  it("saves rapid changes in order without writing new browser preferences", async () => {
    await initializePreferences();
    rememberNotifications(true);
    rememberNotifications(false);
    await waitFor(() =>
      expect(desktop.api.SavePreferences).toHaveBeenCalledTimes(2),
    );
    expect(desktop.preferences["steam-releaser:notifications"]).toBe("false");
    expect(localStorage.getItem("steam-releaser:notifications")).toBeNull();
  });
});
