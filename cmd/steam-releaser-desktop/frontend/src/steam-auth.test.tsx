import { StrictMode } from "react";
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
    error: vi.fn<typeof toast.error>(),
    warning: vi.fn<typeof toast.warning>(),
  },
}));

describe("Steam account startup", () => {
  let desktop: ReturnType<typeof mockDesktop>;
  beforeEach(() => {
    desktop = mockDesktop();
  });

  it("shows the saved account before opening a recent project", async () => {
    localStorage.setItem(
      "steam-releaser:recent-configs",
      JSON.stringify([{ path: config.path, project: config.project }]),
    );
    render(
      <StrictMode>
        <App />
      </StrictMode>,
    );

    expect(
      await screen.findByRole("button", {
        name: "Steam account cached_builder",
      }),
    ).toBeEnabled();
    expect(
      screen.getByRole("status", {
        name: "Saved Steam account; login will be checked when needed",
      }),
    ).toHaveClass("bg-muted-foreground");
    expect(
      screen.getByRole("main", { name: "Project selector" }),
    ).toBeInTheDocument();
    expect(desktop.api.CachedUsername).toHaveBeenCalled();
    expect(desktop.api.LoadConfig).not.toHaveBeenCalled();
    expect(desktop.api.StartOperation).not.toHaveBeenCalled();

    await userEvent.setup().click(
      screen.getByRole("button", {
        name: `Open Demo Game: ${config.path}`,
      }),
    );
    await screen.findByRole("heading", { name: "Demo Game / full" });
    expect(
      screen.getByRole("button", { name: "Steam account cached_builder" }),
    ).toBeEnabled();
  });

  it.each(["missing", "unavailable"])(
    "keeps the project selector usable when the saved account is %s",
    async (reason) => {
      if (reason === "missing")
        desktop.api.CachedUsername.mockResolvedValue("");
      else
        desktop.api.CachedUsername.mockRejectedValue(
          new Error("Desktop connection unavailable"),
        );
      render(<App />);

      await waitFor(() =>
        expect(desktop.api.CachedUsername).toHaveBeenCalled(),
      );
      expect(
        screen.getByRole("button", { name: "Sign in to Steam" }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("status", { name: "Steam sign-in required" }),
      ).toBeInTheDocument();
      await openExistingConfig();
      expect(
        screen.getByRole("button", { name: "Browse configuration" }),
      ).toBeEnabled();
      expect(desktop.api.LoadConfig).not.toHaveBeenCalled();
    },
  );

  it("ignores a startup lookup that finishes after a project loads", async () => {
    let resolveUsername!: (username: string) => void;
    desktop.api.CachedUsername.mockReturnValue(
      new Promise<string>((resolve) => {
        resolveUsername = resolve;
      }),
    );
    localStorage.setItem("steam-releaser:last-config", config.path);
    render(<App />);
    await userEvent.setup().click(
      await screen.findByRole("button", {
        name: `Open configuration: ${config.path}`,
      }),
    );
    await screen.findByRole("heading", { name: "Demo Game / full" });

    await act(async () => resolveUsername("previous_builder"));
    expect(
      screen.getByRole("button", { name: "Steam account cached_builder" }),
    ).toBeInTheDocument();
  });
});

describe("Steam credential file", () => {
  let desktop: ReturnType<typeof mockDesktop>;
  beforeEach(() => {
    desktop = mockDesktop();
    desktop.api.GetCredentialFileSettings.mockResolvedValue({
      enabled: true,
      path: "/private/steam-credentials.json",
      username: config.username,
      has_credentials: true,
    });
  });

  async function startRelease() {
    const user = userEvent.setup();
    render(<App />);
    await openExistingConfig();
    await user.click(
      screen.getByRole("button", { name: "Browse configuration" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Load configuration" }),
    );
    await screen.findByRole("heading", { name: "Demo Game / full" });
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    return user;
  }

  function prompt(message: string) {
    act(() =>
      desktop.emit("release:event", {
        level: "info",
        target: "SteamCMD upload",
        message,
      }),
    );
  }

  it("uses the saved password in the backend and sends fresh Steam Guard responses separately", async () => {
    desktop.api.SubmitSavedSteamPassword.mockResolvedValue(true);
    const user = await startRelease();
    act(() =>
      desktop.emit("release:event", {
        kind: "steam-password-prompt",
        level: "info",
        target: "SteamCMD upload",
        message: "Steam password required.",
      }),
    );
    await waitFor(() =>
      expect(desktop.api.SubmitSavedSteamPassword).toHaveBeenCalledTimes(1),
    );
    expect(desktop.api.SubmitSteamInput).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("Steam password")).not.toBeInTheDocument();
    prompt("Steam Guard code: ");
    await user.type(
      screen.getByLabelText("Steam Guard response"),
      "fresh-guard",
    );
    await user.click(screen.getByRole("button", { name: "Send response" }));
    expect(desktop.api.SubmitSteamInput).toHaveBeenCalledExactlyOnceWith(
      "fresh-guard",
    );
    expect(desktop.api.SubmitSteamPassword).not.toHaveBeenCalled();
  });

  it("falls back to a manual password when the file password is rejected", async () => {
    desktop.api.SubmitSavedSteamPassword.mockResolvedValueOnce(
      true,
    ).mockResolvedValueOnce(false);
    const user = await startRelease();
    prompt("Password: ");
    await waitFor(() =>
      expect(desktop.api.SubmitSavedSteamPassword).toHaveBeenCalledTimes(1),
    );
    prompt("Password: ");
    await user.type(
      await screen.findByLabelText("Steam password"),
      "replacement-password",
    );
    expect(
      screen.queryByRole("checkbox", { name: "Remember Me" }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Send response" }));
    expect(desktop.api.SubmitSteamPassword).toHaveBeenCalledExactlyOnceWith(
      "replacement-password",
    );
    expect(desktop.api.SubmitSteamInput).not.toHaveBeenCalled();
    expect(JSON.stringify(desktop.preferences)).not.toContain(
      "replacement-password",
    );
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });

  it("keeps a newer Guard prompt when a saved-password lookup finishes late", async () => {
    let resolve!: (submitted: boolean) => void;
    desktop.api.SubmitSavedSteamPassword.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    await startRelease();
    prompt("Password: ");
    prompt("Steam Guard code: ");
    await act(async () => resolve(false));
    expect(screen.getByLabelText("Steam Guard response")).toBeInTheDocument();
    expect(screen.queryByLabelText("Steam password")).not.toBeInTheDocument();
  });

  it("keeps a new setting when a credential refresh from a finished release arrives late", async () => {
    const user = await startRelease();
    let resolveSettings!: (
      settings: Awaited<
        ReturnType<typeof desktop.api.GetCredentialFileSettings>
      >,
    ) => void;
    desktop.api.GetCredentialFileSettings.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolveSettings = done;
        }),
    );
    act(() => desktop.emit("operation:finished", { action: "ship", ok: true }));
    await user.click(screen.getByRole("button", { name: "Client settings" }));
    const enable = screen.getByRole("checkbox", {
      name: "Use an existing Steam credential file",
    });
    await user.click(enable);
    expect(enable).not.toBeChecked();
    await act(async () =>
      resolveSettings({
        enabled: true,
        path: "/private/steam-credentials.json",
        username: config.username,
        has_credentials: true,
      }),
    );
    expect(enable).not.toBeChecked();
  });
});

describe("Remember Me", () => {
  let desktop: ReturnType<typeof mockDesktop>;
  beforeEach(() => {
    desktop = mockDesktop();
  });

  async function loadProject() {
    const user = userEvent.setup();
    await openExistingConfig();
    await user.click(
      screen.getByRole("button", { name: "Browse configuration" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Load configuration" }),
    );
    await screen.findByRole("heading", { name: "Demo Game / full" });
    return user;
  }

  function prompt(message = "Password: ", target = "SteamCMD upload") {
    act(() => {
      desktop.emit("release:event", { level: "info", target, message });
    });
  }

  function finish(
    action: "login" | "upload" | "logout",
    ok = true,
    error = "",
  ) {
    act(() => desktop.emit("operation:finished", { action, ok, error }));
  }

  async function sendResponse(
    user: ReturnType<typeof userEvent.setup>,
    response: string,
    label = "Steam password",
  ) {
    await user.type(screen.getByLabelText(label), response);
    await user.click(screen.getByRole("button", { name: "Send response" }));
  }

  async function startUpload(user: ReturnType<typeof userEvent.setup>) {
    if (!screen.queryByRole("button", { name: "Upload" })) {
      await user.click(
        screen.getByRole("button", { name: "More release actions" }),
      );
      await user.click(screen.getByRole("menuitem", { name: "Upload" }));
    }
    await user.click(screen.getByRole("button", { name: "Upload" }));
  }

  async function enterLogin(remember = true, complete = true) {
    const user = await loadProject();
    await user.click(
      screen.getByRole("button", { name: "Steam account cached_builder" }),
    );
    const checkbox = screen.getByRole("checkbox", { name: "Remember Me" });
    expect(checkbox).not.toBeChecked();
    if (remember) await user.click(checkbox);
    await user.click(
      screen.getByRole("button", { name: "Continue with SteamCMD" }),
    );
    prompt("Password: ", "SteamCMD login");
    await sendResponse(user, "private-password");
    if (complete) finish("login");
    return user;
  }

  it("reuses a verified password for uploads and releases while requiring fresh Steam Guard responses", async () => {
    render(<App />);
    const user = await enterLogin(true, false);
    prompt("Steam Guard code: ", "SteamCMD login");
    await sendResponse(user, "first-guard-code", "Steam Guard response");
    finish("login");

    await startUpload(user);
    desktop.api.SubmitSteamInput.mockClear();
    prompt("No cached credentials. ");
    prompt("Pass");
    expect(desktop.api.SubmitSteamInput).not.toHaveBeenCalled();
    prompt("word: ");
    await waitFor(() =>
      expect(desktop.api.SubmitSteamInput).toHaveBeenCalledExactlyOnceWith(
        "private-password",
      ),
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    prompt("Steam Guard code: ");
    expect(screen.getByLabelText("Steam Guard response")).toHaveValue("");
    expect(desktop.api.SubmitSteamInput).toHaveBeenCalledTimes(1);
    await sendResponse(user, "fresh-guard-code", "Steam Guard response");
    finish("upload");

    await user.click(
      screen.getByRole("button", { name: "More release actions" }),
    );
    await user.click(screen.getByRole("menuitem", { name: "Build & Deploy" }));
    await user.click(screen.getByRole("button", { name: "Build & Deploy" }));
    desktop.api.SubmitSteamInput.mockClear();
    prompt();
    expect(desktop.api.SubmitSteamInput).toHaveBeenCalledExactlyOnceWith(
      "private-password",
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    for (const storage of [localStorage, sessionStorage]) {
      const values = Array.from({ length: storage.length }, (_, index) =>
        storage.getItem(storage.key(index)!),
      ).join(" ");
      expect(values).not.toMatch(/private-password|guard-code/);
    }
  });

  it("does not remember passwords unless the option is selected", async () => {
    render(<App />);
    const user = await enterLogin(false);
    await startUpload(user);
    desktop.api.SubmitSteamInput.mockClear();
    prompt();
    expect(screen.getByLabelText("Steam password")).toHaveValue("");
    expect(desktop.api.SubmitSteamInput).not.toHaveBeenCalled();
  }, 15_000);

  it("allows remembering a password from an upload's login prompt", async () => {
    render(<App />);
    const user = await loadProject();
    await startUpload(user);
    prompt();
    await user.click(screen.getByRole("checkbox", { name: "Remember Me" }));
    await sendResponse(user, "upload-password");
    finish("upload");
    await startUpload(user);
    desktop.api.SubmitSteamInput.mockClear();
    prompt();
    expect(desktop.api.SubmitSteamInput).toHaveBeenCalledExactlyOnceWith(
      "upload-password",
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("uses the remembered password when checking the same account again", async () => {
    render(<App />);
    const user = await enterLogin();
    await user.click(
      screen.getByRole("button", { name: "Steam account cached_builder" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Continue with SteamCMD" }),
    );
    desktop.api.SubmitSteamInput.mockClear();
    prompt("Password: ", "SteamCMD login");
    expect(desktop.api.SubmitSteamInput).toHaveBeenCalledExactlyOnceWith(
      "private-password",
    );
    expect(screen.queryByLabelText("Steam password")).not.toBeInTheDocument();
    finish("login");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("forgets immediately when Remember Me is turned off", async () => {
    render(<App />);
    const user = await enterLogin();
    await user.click(
      screen.getByRole("button", { name: "Steam account cached_builder" }),
    );
    const checkbox = screen.getByRole("checkbox", { name: "Remember Me" });
    expect(checkbox).toBeChecked();
    await user.click(checkbox);
    await user.click(checkbox);
    await user.click(
      screen.getByRole("button", { name: "Close Steam sign-in" }),
    );
    await startUpload(user);
    desktop.api.SubmitSteamInput.mockClear();
    prompt();
    expect(screen.getByLabelText("Steam password")).toHaveValue("");
    expect(desktop.api.SubmitSteamInput).not.toHaveBeenCalled();
  });

  it.each([true, false])(
    "forgets when clearing credentials (logout success: %s)",
    async (ok) => {
      render(<App />);
      const user = await enterLogin();
      await user.click(
        screen.getByRole("button", { name: "Steam account cached_builder" }),
      );
      await user.click(
        screen.getByRole("button", { name: "Clear Steam Credentials" }),
      );
      finish("logout", ok, ok ? "" : "SteamCMD was not found");
      await startUpload(user);
      desktop.api.SubmitSteamInput.mockClear();
      prompt();
      expect(screen.getByLabelText("Steam password")).toHaveValue("");
      expect(desktop.api.SubmitSteamInput).not.toHaveBeenCalled();
    },
  );

  it("forgets when switching accounts", async () => {
    render(<App />);
    const user = await enterLogin();
    await user.click(
      screen.getByRole("button", { name: "Steam account cached_builder" }),
    );
    await user.clear(screen.getByRole("textbox", { name: "Steam account" }));
    await user.type(
      screen.getByRole("textbox", { name: "Steam account" }),
      "other_builder",
    );
    await user.click(
      screen.getByRole("button", { name: "Continue with SteamCMD" }),
    );
    desktop.api.SubmitSteamInput.mockClear();
    prompt("Password: ", "SteamCMD login");
    expect(screen.getByLabelText("Steam password")).toHaveValue("");
    expect(desktop.api.SubmitSteamInput).not.toHaveBeenCalled();
  });

  it.each(["Steam credentials expired", "SteamCMD authentication failed"])(
    "forgets after %s",
    async (error) => {
      render(<App />);
      const user = await enterLogin();
      await startUpload(user);
      finish("upload", false, error);
      await startUpload(user);
      desktop.api.SubmitSteamInput.mockClear();
      prompt();
      expect(screen.getByLabelText("Steam password")).toHaveValue("");
      expect(desktop.api.SubmitSteamInput).not.toHaveBeenCalled();
    },
  );

  it("falls back to manual entry when SteamCMD rejects the remembered password", async () => {
    render(<App />);
    const user = await enterLogin();
    await startUpload(user);
    desktop.api.SubmitSteamInput.mockClear();
    prompt();
    expect(desktop.api.SubmitSteamInput).toHaveBeenCalledTimes(1);
    prompt();
    expect(screen.getByLabelText("Steam password")).toHaveValue("");
    expect(desktop.api.SubmitSteamInput).toHaveBeenCalledTimes(1);
    await sendResponse(user, "replacement-password");
    finish("upload");
    await startUpload(user);
    desktop.api.SubmitSteamInput.mockClear();
    prompt();
    expect(desktop.api.SubmitSteamInput).toHaveBeenCalledExactlyOnceWith(
      "replacement-password",
    );
  });

  it.each(["SteamCMD login cancelled", "SteamCMD authentication failed"])(
    "does not retain a password from %s",
    async (error) => {
      render(<App />);
      const user = await enterLogin(true, false);
      finish("login", false, error);
      await user.click(
        screen.getByRole("button", { name: "Continue with SteamCMD" }),
      );
      desktop.api.SubmitSteamInput.mockClear();
      prompt("Password: ", "SteamCMD login");
      expect(screen.getByLabelText("Steam password")).toHaveValue("");
      expect(desktop.api.SubmitSteamInput).not.toHaveBeenCalled();
    },
  );

  it("falls back to manual entry if the remembered response cannot be sent", async () => {
    render(<App />);
    const user = await enterLogin();
    await startUpload(user);
    desktop.api.SubmitSteamInput.mockRejectedValueOnce(
      new Error("Input unavailable"),
    );
    prompt();
    expect(await screen.findByLabelText("Steam password")).toHaveValue("");
    await sendResponse(user, "replacement-password");
    expect(desktop.api.SubmitSteamInput).toHaveBeenLastCalledWith(
      "replacement-password",
    );
  });

  it("preserves the password after an unrelated upload failure", async () => {
    render(<App />);
    const user = await enterLogin();
    await startUpload(user);
    finish("upload", false, "Content directory was not found");
    await startUpload(user);
    desktop.api.SubmitSteamInput.mockClear();
    prompt();
    expect(desktop.api.SubmitSteamInput).toHaveBeenCalledExactlyOnceWith(
      "private-password",
    );
  });

  it("does not reopen an obsolete prompt after an operation has finished", async () => {
    render(<App />);
    const user = await enterLogin();
    await startUpload(user);
    let rejectResponse!: (error: Error) => void;
    desktop.api.SubmitSteamInput.mockReturnValueOnce(
      new Promise<void>((_, reject) => {
        rejectResponse = reject;
      }),
    );
    prompt();
    finish("upload");
    await startUpload(user);
    await act(async () => rejectResponse(new Error("Late input failure")));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("starts with no remembered password when the app is reopened", async () => {
    const app = render(<App />);
    await enterLogin();
    app.unmount();
    render(<App />);
    const user = await loadProject();
    await startUpload(user);
    desktop.api.SubmitSteamInput.mockClear();
    prompt();
    expect(
      screen.getByRole("checkbox", { name: "Remember Me" }),
    ).not.toBeChecked();
    expect(screen.getByLabelText("Steam password")).toHaveValue("");
    expect(desktop.api.SubmitSteamInput).not.toHaveBeenCalled();
  });
});
