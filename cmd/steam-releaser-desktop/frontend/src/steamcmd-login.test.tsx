import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { App } from "@/app";
import { mockDesktop } from "@/test/desktop-mock";

vi.mock("sonner", () => ({
  Toaster: () => null,
  toast: {
    success: vi.fn<typeof toast.success>(),
    error: vi.fn<typeof toast.error>(),
    warning: vi.fn<typeof toast.warning>(),
  },
}));

const consent =
  /Allow it to read SteamCMD's saved account names to reuse your existing login\?/;

describe("SteamCMD saved login", () => {
  let desktop: ReturnType<typeof mockDesktop>;
  beforeEach(() => {
    desktop = mockDesktop();
    desktop.api.SteamCMDLocation.mockResolvedValue("/usr/bin/steamcmd");
    desktop.api.SteamCMDCachedAccounts.mockResolvedValue(["builder_one"]);
  });

  async function openSignIn() {
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", {
        name: "Steam account cached_builder",
      }),
    );
    return user;
  }

  it("reads account names only after Allow and signs in with the chosen account", async () => {
    render(<App />);
    const user = await openSignIn();

    expect(await screen.findByText(consent)).toHaveTextContent(
      "/usr/bin/steamcmd",
    );
    expect(desktop.api.SteamCMDCachedAccounts).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Allow" }));
    expect(desktop.preferences["steam-releaser:steamcmd-accounts"]).toBe(
      "allow",
    );
    expect(
      await screen.findByText("SteamCMD is already signed in as builder_one."),
    ).toBeInTheDocument();
    expect(desktop.api.SteamCMDCachedAccounts).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(consent)).not.toBeInTheDocument();

    await user.click(
      screen.getByRole("button", { name: "Continue as builder_one" }),
    );
    expect(desktop.api.StartOperation).toHaveBeenCalledWith(
      expect.objectContaining({ action: "login", username: "builder_one" }),
    );
  });

  it("remembers Not now without reading SteamCMD files", async () => {
    const view = render(<App />);
    const user = await openSignIn();
    await user.click(await screen.findByRole("button", { name: "Not now" }));
    await waitFor(() =>
      expect(desktop.preferences["steam-releaser:steamcmd-accounts"]).toBe(
        "deny",
      ),
    );
    expect(screen.queryByText(consent)).not.toBeInTheDocument();

    view.unmount();
    render(<App />);
    await openSignIn();
    expect(
      screen.getByRole("button", { name: "Continue with SteamCMD" }),
    ).toBeInTheDocument();
    expect(screen.queryByText(consent)).not.toBeInTheDocument();
    expect(desktop.api.SteamCMDCachedAccounts).not.toHaveBeenCalled();
  });

  it("can be allowed later from Client settings", async () => {
    desktop.preferences["steam-releaser:steamcmd-accounts"] = "deny";
    const user = userEvent.setup();
    render(<App />);
    await user.click(screen.getByRole("button", { name: "Client settings" }));
    const reuse = await screen.findByRole("checkbox", {
      name: "Reuse SteamCMD's saved login",
    });
    await waitFor(() => expect(reuse).toBeEnabled());
    expect(reuse).not.toBeChecked();
    expect(desktop.api.SteamCMDCachedAccounts).not.toHaveBeenCalled();
    await user.click(reuse);
    await waitFor(() =>
      expect(desktop.api.SteamCMDCachedAccounts).toHaveBeenCalledTimes(1),
    );
    expect(desktop.preferences["steam-releaser:steamcmd-accounts"]).toBe(
      "allow",
    );
  });
});
