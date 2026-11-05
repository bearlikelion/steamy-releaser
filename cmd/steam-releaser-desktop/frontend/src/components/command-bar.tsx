import { useState } from "react";
import {
  ChevronDown,
  Hammer,
  ListChecks,
  Rocket,
  Stethoscope,
  Upload,
  X,
} from "lucide-react";
import { DropdownMenu } from "radix-ui";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import type { ReleaseConsole } from "@/hooks/use-release-console";
import { operationLabels } from "@/lib/types";

const actions = [
  { action: "ship", icon: Rocket },
  { action: "doctor", icon: Stethoscope },
  { action: "plan", icon: ListChecks },
  { action: "build", icon: Hammer },
  { action: "upload", icon: Upload },
] as const;

export function CommandBar({ console: state }: { console: ReleaseConsole }) {
  const [selectedAction, setSelectedAction] =
    useState<(typeof actions)[number]["action"]>("ship");
  const SelectedIcon = actions.find(
    ({ action }) => action === selectedAction,
  )!.icon;

  return (
    <Card>
      <CardContent className="flex flex-wrap items-center justify-between gap-4">
        <div className="min-w-0">
          <p className="text-xs font-medium tracking-widest text-muted-foreground uppercase">
            Release pipeline
          </p>
          <h2 className="mt-1 text-lg font-semibold break-words">
            {state.summary && state.profile
              ? `${state.summary.project} / ${state.profile.name}`
              : "Select a project to begin"}
          </h2>
          {state.profile && (
            <p
              aria-label="Publishing destination"
              className="mt-1 text-xs text-muted-foreground break-words"
            >
              App {state.profile.app_id} ·{" "}
              {state.preview
                ? "Preview only"
                : state.setLive.trim()
                  ? `Beta branch: ${state.setLive.trim()}`
                  : "No beta branch selected"}
            </p>
          )}
        </div>
        <fieldset className="flex flex-wrap gap-2">
          <legend className="sr-only">Release actions</legend>
          <div className="inline-flex">
            <Button
              type="button"
              size="sm"
              className="rounded-r-none"
              disabled={
                state.locked || !state.profile || !state.configurationReady
              }
              onClick={() => void state.startOperation(selectedAction)}
            >
              <SelectedIcon aria-hidden="true" />
              {operationLabels[selectedAction]}
            </Button>
            <DropdownMenu.Root>
              <DropdownMenu.Trigger asChild>
                <Button
                  type="button"
                  size="icon-sm"
                  className="rounded-l-none border-l border-primary-foreground/30"
                  aria-label="More release actions"
                  disabled={
                    state.locked || !state.profile || !state.configurationReady
                  }
                >
                  <ChevronDown aria-hidden="true" />
                </Button>
              </DropdownMenu.Trigger>
              <DropdownMenu.Portal>
                <DropdownMenu.Content
                  align="end"
                  sideOffset={6}
                  className="z-50 min-w-40 rounded-lg border bg-popover p-1 text-popover-foreground shadow-lg"
                >
                  {actions
                    .filter(({ action }) => action !== selectedAction)
                    .map(({ action, icon: Icon }) => (
                      <DropdownMenu.Item
                        key={action}
                        className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-none select-none data-[highlighted]:bg-accent data-[highlighted]:text-accent-foreground"
                        onSelect={() => setSelectedAction(action)}
                      >
                        <Icon className="size-4" aria-hidden="true" />
                        {operationLabels[action]}
                      </DropdownMenu.Item>
                    ))}
                </DropdownMenu.Content>
              </DropdownMenu.Portal>
            </DropdownMenu.Root>
          </div>
          {state.busy && (
            <Button
              variant="destructive"
              size="sm"
              disabled={state.operation?.phase !== "running"}
              onClick={() => void state.cancelOperation()}
            >
              <X aria-hidden="true" />
              {state.operation?.phase === "cancelling"
                ? "Cancelling…"
                : "Cancel"}
            </Button>
          )}
        </fieldset>
      </CardContent>
    </Card>
  );
}
