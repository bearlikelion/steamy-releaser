import { useState } from "react";
import {
  ArrowRight,
  Clock3,
  FilePenLine,
  FolderOpen,
  LoaderCircle,
  Plus,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  NativeSelect,
  NativeSelectOption,
} from "@/components/ui/native-select";
import type { ReleaseConsole } from "@/hooks/use-release-console";

export function ProjectPanel({
  console: state,
  onSelected,
}: {
  console: ReleaseConsole;
  onSelected: () => void;
}) {
  const [creating, setCreating] = useState(false);
  const [path, setPath] = useState("");
  const [name, setName] = useState("");
  const [engine, setEngine] = useState("prebuilt");
  const [appId, setAppId] = useState("");

  return (
    <>
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
        <CardHeader>
          <CardTitle>
            <h2>Project</h2>
          </CardTitle>
          <CardAction>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="text-muted-foreground hover:text-foreground"
              aria-label={
                creating ? "Cancel new configuration" : "New configuration"
              }
              title={
                creating ? "Cancel new configuration" : "New configuration"
              }
              aria-expanded={creating}
              disabled={state.locked}
              onClick={() => setCreating((current) => !current)}
            >
              {creating ? (
                <X aria-hidden="true" />
              ) : (
                <Plus aria-hidden="true" />
              )}
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="space-y-4">
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
              Saved on this computer, outside the shared YAML. Leave blank when
              the YAML is in the game folder.
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
              {state.loading ? "Loading configuration…" : "Load configuration"}
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
          {creating && (
            <form
              className="space-y-3 border-t pt-4"
              onSubmit={async (event) => {
                event.preventDefault();
                const created = await state.createConfiguration({
                  path: path.trim(),
                  name: name.trim(),
                  engine,
                  app_id: Number(appId),
                });
                if (created) {
                  setCreating(false);
                  onSelected();
                }
              }}
            >
              <h3 className="text-sm font-semibold">Create configuration</h3>
              <div className="space-y-1">
                <Label htmlFor="new-config-path">Configuration path</Label>
                <div className="flex gap-2">
                  <Input
                    id="new-config-path"
                    className="min-w-0 font-mono text-xs"
                    value={path}
                    onChange={(event) => setPath(event.target.value)}
                    placeholder="/path/to/steam-release.yaml"
                    required
                  />
                  <Button
                    type="button"
                    size="icon"
                    variant="outline"
                    aria-label="Choose new configuration path"
                    onClick={async () => {
                      const chosen = await state.chooseNewConfigPath();
                      if (chosen) setPath(chosen);
                    }}
                  >
                    <FolderOpen aria-hidden="true" />
                  </Button>
                </div>
              </div>
              <div className="space-y-1">
                <Label htmlFor="new-project-name">Project name</Label>
                <Input
                  id="new-project-name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  required
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="new-engine">Engine</Label>
                <NativeSelect
                  id="new-engine"
                  value={engine}
                  onChange={(event) => setEngine(event.target.value)}
                >
                  <NativeSelectOption value="prebuilt">
                    Prebuilt
                  </NativeSelectOption>
                  <NativeSelectOption value="godot">Godot</NativeSelectOption>
                  <NativeSelectOption value="unity">Unity</NativeSelectOption>
                  <NativeSelectOption value="custom">Custom</NativeSelectOption>
                </NativeSelect>
              </div>
              <div className="space-y-1">
                <Label htmlFor="new-app-id">Steam App ID</Label>
                <Input
                  id="new-app-id"
                  type="number"
                  min="1"
                  max={Number.MAX_SAFE_INTEGER}
                  step="1"
                  value={appId}
                  onChange={(event) => setAppId(event.target.value)}
                  required
                />
              </div>
              <p className="text-xs text-muted-foreground">
                Creates a release profile and Windows, Linux, and macOS targets.
                Edit the YAML to replace placeholders and enable commented
                options before publishing.
              </p>
              <Button type="submit" className="w-full" disabled={state.locked}>
                Create configuration
              </Button>
            </form>
          )}
        </CardContent>
      </Card>
    </>
  );
}
