import { useState } from "react";
import {
  ArrowRight,
  CircleCheck,
  Clock3,
  FilePenLine,
  FolderOpen,
  LoaderCircle,
  Sparkles,
} from "lucide-react";
import { ConfigGenerator } from "@/components/config-generator";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { ReleaseConsole } from "@/hooks/use-release-console";

export function ProjectPanel({
  console: state,
  onSelected,
}: {
  console: ReleaseConsole;
  onSelected: () => void;
}) {
  const [chosenMode, setMode] = useState<"create" | "open" | null>(null);
  const mode =
    chosenMode ??
    (!state.preferencesReady ||
    state.recentConfigs.length > 0 ||
    state.configPath
      ? "open"
      : "create");
  const signedIn = Boolean(state.username);

  return (
    <>
      {state.recentConfigs.length === 0 && (
        <section aria-labelledby="getting-started-title" className="space-y-3">
          <h2
            id="getting-started-title"
            className="text-xl font-semibold tracking-tight"
          >
            Ship your game to Steam
          </h2>
          <ol className="space-y-2 text-sm text-muted-foreground">
            <li className="flex gap-3">
              <StepNumber value={1} />
              <span>
                <span className="font-medium text-foreground">
                  Set up your game.
                </span>{" "}
                Create a release config from your Steam App ID, or open one you
                already have.
              </span>
            </li>
            <li className="flex gap-3">
              <StepNumber value={2} done={signedIn} />
              <span>
                <span className="font-medium text-foreground">
                  Sign in to Steam.
                </span>{" "}
                {signedIn
                  ? `Using ${state.username}.`
                  : "Use the button at the top right with an account that can upload builds."}
              </span>
            </li>
            <li className="flex gap-3">
              <StepNumber value={3} />
              <span>
                <span className="font-medium text-foreground">Ship.</span> Pick
                platforms, check readiness, then build and upload.
              </span>
            </li>
          </ol>
        </section>
      )}
      {state.recentConfigs.length > 0 && (
        <section aria-labelledby="recent-projects-title" className="space-y-3">
          <h3 id="recent-projects-title" className="text-sm font-medium">
            Recently opened
          </h3>
          <div className="space-y-2">
            {state.recentConfigs.map((recent) => (
              <Button
                key={recent.path}
                type="button"
                variant="outline"
                className="h-auto w-full justify-start gap-3 px-4 py-3 text-left whitespace-normal"
                aria-label={`Open ${recent.project || "configuration"}: ${recent.path}`}
                disabled={state.locked}
                onClick={async () => {
                  if (await state.openRecentConfig(recent.path)) onSelected();
                }}
              >
                <Clock3 className="text-muted-foreground" aria-hidden="true" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">
                    {recent.project || "Configuration"}
                  </span>
                  <span className="block truncate text-xs font-normal text-muted-foreground">
                    {recent.path}
                  </span>
                </span>
                <ArrowRight
                  className="text-muted-foreground"
                  aria-hidden="true"
                />
              </Button>
            ))}
          </div>
        </section>
      )}
      <Card>
        <CardContent className="space-y-4">
          <fieldset className="grid grid-cols-2 gap-1 rounded-lg border bg-muted/30 p-1">
            <legend className="sr-only">Project setup</legend>
            <Button
              type="button"
              variant={mode === "create" ? "secondary" : "ghost"}
              aria-pressed={mode === "create"}
              disabled={state.locked}
              onClick={() => setMode("create")}
            >
              <Sparkles aria-hidden="true" />
              Set up a new game
            </Button>
            <Button
              type="button"
              variant={mode === "open" ? "secondary" : "ghost"}
              aria-pressed={mode === "open"}
              disabled={state.locked}
              onClick={() => setMode("open")}
            >
              <FolderOpen aria-hidden="true" />
              Open existing config
            </Button>
          </fieldset>
          {mode === "open" && (
            <form
              className="space-y-3"
              onSubmit={async (event) => {
                event.preventDefault();
                if (await state.loadConfiguration()) onSelected();
              }}
            >
              <Label htmlFor="config-path">Configuration</Label>
              <div className="flex gap-2">
                <Input
                  id="config-path"
                  className="min-w-0 font-mono text-xs"
                  value={state.configPath}
                  onChange={(event) => state.setConfigPath(event.target.value)}
                  disabled={state.locked}
                  spellCheck={false}
                  placeholder="/path/to/steam-release.yaml"
                />
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  aria-label="Browse configuration"
                  disabled={state.locked}
                  onClick={() => void state.chooseConfigPath()}
                >
                  <FolderOpen aria-hidden="true" />
                </Button>
              </div>
              <Label htmlFor="project-root">Game project folder</Label>
              <div className="flex gap-2">
                <Input
                  id="project-root"
                  className="min-w-0 font-mono text-xs"
                  value={state.projectRoot}
                  onChange={(event) => state.setProjectRoot(event.target.value)}
                  disabled={state.locked}
                  spellCheck={false}
                  placeholder="Choose the game checkout on this computer"
                />
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  aria-label="Browse game project folder"
                  disabled={state.locked}
                  onClick={() => void state.chooseProjectRoot()}
                >
                  <FolderOpen aria-hidden="true" />
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                Saved on this computer, outside the shared YAML. Leave blank
                when the YAML is in the game folder.
              </p>
              <Button
                type="submit"
                variant="secondary"
                className="w-full"
                disabled={state.locked}
              >
                {state.loading && (
                  <LoaderCircle
                    className="motion-safe:animate-spin"
                    aria-hidden="true"
                  />
                )}
                {state.loading
                  ? "Loading configuration…"
                  : "Load configuration"}
              </Button>
              <Button
                type="button"
                variant="outline"
                className="w-full"
                disabled={state.locked || !state.configPath.trim()}
                onClick={() => void state.editConfiguration()}
              >
                <FilePenLine aria-hidden="true" />
                Edit YAML
              </Button>
              {state.configurationError && (
                <p role="alert" className="break-words text-sm text-red-300">
                  {state.configurationError}
                </p>
              )}
            </form>
          )}
          {mode === "create" && (
            <ConfigGenerator console={state} onCreated={onSelected} />
          )}
        </CardContent>
      </Card>
    </>
  );
}

function StepNumber({
  value,
  done = false,
}: {
  value: number;
  done?: boolean;
}) {
  if (done)
    return (
      <CircleCheck
        className="mt-0.5 size-5 shrink-0 text-primary"
        aria-label="Done"
      />
    );
  return (
    <span
      aria-hidden="true"
      className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full border text-xs font-medium text-foreground"
    >
      {value}
    </span>
  );
}
