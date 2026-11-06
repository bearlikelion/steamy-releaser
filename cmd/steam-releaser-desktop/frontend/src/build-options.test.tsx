import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { App } from "@/app";
import { config, mockDesktop, openExistingConfig } from "@/test/desktop-mock";
import type { ReleasePlan } from "@/lib/types";

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

describe("engine build options", () => {
  let desktop: ReturnType<typeof mockDesktop>;
  beforeEach(() => {
    desktop = mockDesktop();
  });

  it("shows resolved target options separately from shared preparation and retains raw plan output", async () => {
    render(<App />);
    const user = await openProject();
    await user.click(
      screen.getByRole("button", { name: "More release actions" }),
    );
    await user.click(screen.getByRole("menuitem", { name: "Plan" }));
    await user.click(screen.getByRole("button", { name: "Plan" }));
    const plan: ReleasePlan = {
      project: config.project,
      profile: "full",
      engine: "godot",
      parallelism: 1,
      prepare: [
        {
          label: "Godot import",
          program: "godot",
          args: ["--import"],
          working_dir: config.root,
        },
      ],
      targets: [
        {
          name: "windows",
          depot_id: 21,
          output: "/build/windows/Game.exe",
          content: "/build/windows",
          command: {
            label: "windows",
            program: "godot",
            args: ["--export-release", "Windows Demo", "", "path with spaces"],
            working_dir: config.root,
            environment: { EDITION: "demo", EMPTY: "" },
            warnings: ["Managed engine flag overlap"],
          },
        },
      ],
      upload: {
        app_id: 20,
        depot_ids: [21],
        build_script: "/generated/app_build.vdf",
        preview: true,
        set_live: "",
      },
    };
    act(() =>
      desktop.emit("operation:finished", {
        action: "plan",
        ok: true,
        data: plan,
      }),
    );
    const preparation = screen.getByRole("region", {
      name: "Preparation: Godot import",
    });
    expect(
      within(preparation).getByText("No environment overrides"),
    ).toBeInTheDocument();
    const target = screen.getByRole("region", {
      name: "Build plan for windows",
    });
    expect(
      within(target).getByRole("list", { name: "windows arguments" }),
    ).toHaveTextContent('"Windows Demo"');
    expect(within(target).getByText('"path with spaces"')).toBeInTheDocument();
    expect(
      within(target).getByLabelText("windows environment overrides"),
    ).toHaveTextContent("EDITION");
    expect(
      within(target).getByText("Managed engine flag overlap"),
    ).toBeInTheDocument();
    await user.click(screen.getByText("Raw plan"));
    expect(screen.getByText(/"environment":/)).toBeInTheDocument();
  });
});
