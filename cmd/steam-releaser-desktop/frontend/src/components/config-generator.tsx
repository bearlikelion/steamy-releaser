import { useEffect, useState } from "react";
import { FileInput, FolderOpen, Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  NativeSelect,
  NativeSelectOption,
} from "@/components/ui/native-select";
import type { ReleaseConsole } from "@/hooks/use-release-console";
import { backend, errorMessage } from "@/lib/desktop";
import type {
  CreateConfigRequest,
  DiscoveredBuild,
  FileMapping,
  ImportedBuild,
  ProjectDetection,
} from "@/lib/types";

interface TargetRow {
  id: number;
  name: string;
  platform: string;
  depot: string;
  path: string;
  setting: string;
  mappings?: FileMapping[];
  exclusions?: string[];
}

const platforms = ["windows", "linux", "macos"] as const;
const emptyDetection: ProjectDetection = {
  engine: "",
  name: "",
  presets: [],
  profiles: [],
  existing: "",
};
let nextRowId = 1;

function row(values: Omit<TargetRow, "id" | "depot">): TargetRow {
  return { id: nextRowId++, depot: "", ...values };
}

function starterRows(engine: string, detection: ProjectDetection) {
  return platforms.map((platform) => {
    if (engine === "godot") {
      const preset = detection.presets.find(
        (item) => item.platform === platform,
      );
      const output =
        preset?.output &&
        !preset.output.startsWith("..") &&
        !/^([a-z]:)?[\\/]/i.test(preset.output)
          ? preset.output
          : `builds/${platform}/${{ windows: "Game.exe", linux: "Game.x86_64", macos: "Game.zip" }[platform]}`;
      return row({
        name: platform,
        platform,
        path: output,
        setting: preset?.name ?? "",
      });
    }
    if (engine === "unity") {
      const profile =
        detection.profiles.find((item) =>
          item.toLowerCase().includes(platform === "macos" ? "mac" : platform),
        ) ?? "";
      return row({
        name: platform,
        platform,
        path: `Builds/${platform}/${{ windows: "Game.exe", linux: "Game", macos: "Game.app" }[platform]}`,
        setting: profile,
      });
    }
    if (engine === "custom")
      return row({
        name: platform,
        platform,
        path: `dist/${platform}/Game`,
        setting: "",
      });
    return row({
      name: platform,
      platform,
      path: `builds/${platform}`,
      setting: "",
    });
  });
}

function joinPath(folder: string, file: string) {
  const separator = folder.includes("\\") && !folder.includes("/") ? "\\" : "/";
  return folder.replace(/[\\/]+$/, "") + separator + file;
}

function parentFolder(path: string) {
  const trimmed = path.replace(/[\\/]+$/, "");
  return /[\\/]/.test(trimmed) ? trimmed.replace(/[\\/][^\\/]*$/, "") : "";
}

function relativeTo(folder: string, path: string) {
  const base = folder.replace(/[\\/]+$/, "");
  return path.startsWith(base) ? path.slice(base.length + 1) : path;
}

function counted(count = 0, noun: string) {
  return count ? `${count} ${noun}${count === 1 ? "" : "s"}` : "";
}

function samePath(a: string, b: string) {
  const normal = (path: string) =>
    path
      .replace(/\\/g, "/")
      .replace(/\/\.(?=\/|$)/g, "")
      .replace(/\/+$/, "");
  return normal(a) === normal(b);
}

const settingLabels: Record<string, string> = {
  godot: "Export preset",
  unity: "Build profile",
  custom: "Build command",
};

export function ConfigGenerator({
  console: state,
  onCreated,
}: {
  console: ReleaseConsole;
  onCreated: () => void;
}) {
  const [gameFolder, setGameFolder] = useState("");
  const [configPath, setConfigPath] = useState("");
  const [name, setName] = useState("");
  const [engine, setEngine] = useState("prebuilt");
  const [appId, setAppId] = useState("");
  const [detection, setDetection] = useState(emptyDetection);
  const [rows, setRows] = useState(() =>
    starterRows("prebuilt", emptyDetection),
  );
  const [importedFrom, setImportedFrom] = useState("");
  const [analyzedFolder, setAnalyzedFolder] = useState("");
  const [discovered, setDiscovered] = useState<
    (DiscoveredBuild & { name: string })[]
  >([]);
  const [result, setResult] = useState({ key: "", yaml: "", error: "" });
  const [actionError, setActionError] = useState("");

  const suggestedDepot = (index: number) =>
    Number(appId) > 0 ? String(Number(appId) + index + 1) : "";
  const request: CreateConfigRequest = {
    path:
      configPath ||
      (gameFolder ? joinPath(gameFolder, "steam-release.yaml") : ""),
    project_root: gameFolder,
    name: name.trim(),
    engine,
    app_id: Number(appId) || 0,
    targets: rows.map((target, index) => ({
      name: target.name.trim(),
      platform: target.platform,
      depot_id: Number(target.depot || suggestedDepot(index)) || 0,
      path: target.path.trim(),
      build_setting: engine === "prebuilt" ? "" : target.setting.trim(),
      mappings: target.mappings,
      exclusions: target.exclusions,
    })),
  };
  const requestKey = JSON.stringify(request);

  const ready = Boolean(
    state.available && request.path && request.name && request.app_id,
  );
  const shown = ready && result.key === requestKey ? result : null;
  const previewError = actionError || shown?.error;

  useEffect(() => {
    if (!ready) return;
    const pending = { cancelled: false };
    const timer = setTimeout(() => {
      backend()
        .PreviewConfig(JSON.parse(requestKey) as CreateConfigRequest)
        .then((yaml) => {
          if (!pending.cancelled)
            setResult({ key: requestKey, yaml, error: "" });
        })
        .catch((error: unknown) => {
          if (!pending.cancelled)
            setResult({
              key: requestKey,
              yaml: "",
              error: errorMessage(error),
            });
        });
    }, 250);
    return () => {
      pending.cancelled = true;
      clearTimeout(timer);
    };
  }, [requestKey, ready]);

  async function analyzeFolder(folder: string) {
    folder = folder.trim();
    if (!folder) return;
    setAnalyzedFolder(folder);
    try {
      setActionError("");
      const [detected, builds] = await Promise.all([
        backend().DetectProject(folder),
        backend().DiscoverBuildScripts(folder),
      ]);
      setGameFolder(folder);
      setDetection(detected);
      setDiscovered(builds.map((build) => ({ ...build, name: "" })));
      if (!name.trim()) setName(detected.name);
      if (!importedFrom) {
        setEngine(detected.engine);
        setRows(starterRows(detected.engine, detected));
      }
      const names = await Promise.all(
        builds.map((build) =>
          backend()
            .SteamAppName(build.build.app_id)
            .catch(() => ""),
        ),
      );
      setDiscovered(
        builds.map((build, index) => ({ ...build, name: names[index] })),
      );
    } catch (error) {
      setActionError(errorMessage(error));
    }
  }

  async function chooseGameFolder() {
    try {
      const folder = await backend().OpenProjectDialog();
      if (folder) await analyzeFolder(folder);
    } catch (error) {
      setActionError(errorMessage(error));
    }
  }

  function applyBuild(
    imported: ImportedBuild,
    path: string,
    folder: string,
    appName = "",
  ) {
    const presetFor = (content: string) =>
      detection.presets.find(
        (preset) =>
          preset.output &&
          samePath(
            joinPath(folder, parentFolder(preset.output) || "."),
            content,
          ),
      );
    const godot =
      detection.engine === "godot" &&
      imported.depots.every((depot) => presetFor(depot.content));
    const used = new Set<string>();
    setImportedFrom(path);
    setAppId(String(imported.app_id));
    setEngine(godot ? "godot" : "prebuilt");
    setGameFolder(folder);
    if (appName) setName(appName);
    else if (!name.trim()) setName(folder.split(/[\\/]/).pop() ?? "");
    setRows(
      imported.depots.map((depot) => {
        const preset = godot ? presetFor(depot.content) : undefined;
        let targetName = depot.platform || `depot_${depot.depot_id}`;
        if (used.has(targetName)) targetName += `_${depot.depot_id}`;
        used.add(targetName);
        return {
          ...row({
            name: targetName,
            platform: depot.platform || preset?.platform || "",
            path: preset?.output ?? depot.content,
            setting: preset?.name ?? "",
            mappings:
              !preset && depot.mappings.length ? depot.mappings : undefined,
            exclusions: depot.exclusions.length ? depot.exclusions : undefined,
          }),
          depot: String(depot.depot_id),
        };
      }),
    );
  }

  async function importVDF() {
    try {
      const path = await backend().OpenVDFDialog();
      if (!path) return;
      setActionError("");
      const imported = await backend().ImportVDF(path);
      applyBuild(
        imported,
        path,
        gameFolder || parentFolder(imported.content_root),
      );
    } catch (error) {
      setActionError(errorMessage(error));
    }
  }

  function changeEngine(next: string) {
    setEngine(next);
    setImportedFrom("");
    setRows(starterRows(next, detection));
  }

  function updateRow(id: number, values: Partial<TargetRow>) {
    setRows((current) =>
      current.map((item) => (item.id === id ? { ...item, ...values } : item)),
    );
  }

  const disabled = state.locked;
  return (
    <form
      className="space-y-4"
      onSubmit={async (event) => {
        event.preventDefault();
        if (await state.createConfiguration(request)) onCreated();
      }}
    >
      <div className="space-y-1">
        <Label htmlFor="generator-folder">Game folder</Label>
        <div className="flex gap-2">
          <Input
            id="generator-folder"
            className="min-w-0 font-mono text-xs"
            value={gameFolder}
            onChange={(event) => setGameFolder(event.target.value)}
            onBlur={(event) => {
              if (event.target.value.trim() !== analyzedFolder)
                void analyzeFolder(event.target.value);
            }}
            onKeyDown={(event) => {
              if (event.key !== "Enter") return;
              event.preventDefault();
              void analyzeFolder(event.currentTarget.value);
            }}
            placeholder="Choose the folder with your game project or builds"
            spellCheck={false}
            disabled={disabled}
          />
          <Button
            type="button"
            variant="outline"
            size="icon"
            aria-label="Browse game folder"
            disabled={disabled}
            onClick={() => void chooseGameFolder()}
          >
            <FolderOpen aria-hidden="true" />
          </Button>
        </div>
        {detection.existing && (
          <p className="text-xs text-muted-foreground">
            This folder already has a configuration.{" "}
            <button
              type="button"
              className="text-primary underline underline-offset-2"
              disabled={disabled}
              onClick={async () => {
                if (await state.openRecentConfig(detection.existing))
                  onCreated();
              }}
            >
              Open it instead
            </button>
          </p>
        )}
      </div>
      {discovered.length > 0 && (
        <section aria-labelledby="discovered-title" className="space-y-2">
          <h3 id="discovered-title" className="text-sm font-medium">
            SteamPipe scripts in this folder
          </h3>
          {discovered.map((found) => (
            <Button
              key={found.path}
              type="button"
              variant={importedFrom === found.path ? "secondary" : "outline"}
              className="h-auto w-full justify-start gap-3 px-3 py-2 text-left whitespace-normal"
              aria-pressed={importedFrom === found.path}
              disabled={disabled}
              onClick={() =>
                applyBuild(found.build, found.path, gameFolder, found.name)
              }
            >
              <FileInput className="text-muted-foreground" aria-hidden="true" />
              <span className="min-w-0 flex-1">
                <span className="block font-medium">
                  {found.name || `App ${found.build.app_id}`}
                </span>
                <span className="block truncate text-xs font-normal text-muted-foreground">
                  App {found.build.app_id} · {found.build.depots.length}{" "}
                  {found.build.depots.length === 1 ? "depot" : "depots"} ·{" "}
                  {relativeTo(gameFolder, found.path)}
                </span>
              </span>
            </Button>
          ))}
        </section>
      )}
      <div className="flex flex-wrap items-center gap-2 rounded-lg border border-dashed p-3 text-xs text-muted-foreground">
        <span className="min-w-0 flex-1">
          {importedFrom
            ? `Imported from ${importedFrom}`
            : "Already upload with a SteamPipe app_build VDF? Import it to fill in your App ID, depots, and content folders."}
        </span>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={disabled}
          onClick={() => void importVDF()}
        >
          <FileInput aria-hidden="true" />
          Import SteamPipe VDF
        </Button>
      </div>
      <div className="grid gap-3 sm:grid-cols-[1fr_9rem_10rem]">
        <div className="space-y-1">
          <Label htmlFor="new-project-name">Project name</Label>
          <Input
            id="new-project-name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            disabled={disabled}
            required
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="new-engine">Engine</Label>
          <NativeSelect
            id="new-engine"
            className="w-full"
            value={engine}
            onChange={(event) => changeEngine(event.target.value)}
            disabled={disabled}
          >
            <NativeSelectOption value="prebuilt">Prebuilt</NativeSelectOption>
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
            disabled={disabled}
            required
          />
        </div>
      </div>
      <fieldset className="space-y-2" disabled={disabled}>
        <legend className="text-sm font-medium">Targets</legend>
        <p className="text-xs text-muted-foreground">
          One target per Steam depot. Empty depot IDs use App ID + 1, 2, 3,
          Steam&apos;s default numbering; check them in Steamworks.
        </p>
        {rows.map((target, index) => {
          const label = `Target ${index + 1}`;
          const presetOptions = engine === "godot" ? detection.presets : [];
          return (
            <div
              key={target.id}
              className="space-y-2 rounded-lg border bg-muted/20 p-3"
            >
              <div className="grid grid-cols-[1fr_7rem_8rem_auto] gap-2">
                <Input
                  aria-label={`${label} name`}
                  value={target.name}
                  onChange={(event) =>
                    updateRow(target.id, { name: event.target.value })
                  }
                  required
                />
                <NativeSelect
                  aria-label={`${label} platform`}
                  className="w-full"
                  value={target.platform}
                  onChange={(event) =>
                    updateRow(target.id, { platform: event.target.value })
                  }
                >
                  <NativeSelectOption value="">Any OS</NativeSelectOption>
                  {platforms.map((platform) => (
                    <NativeSelectOption key={platform} value={platform}>
                      {platform}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <Input
                  aria-label={`${label} depot ID`}
                  type="number"
                  min="1"
                  value={target.depot}
                  placeholder={suggestedDepot(index) || "Depot ID"}
                  onChange={(event) =>
                    updateRow(target.id, { depot: event.target.value })
                  }
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={`Remove ${label.toLowerCase()}`}
                  disabled={rows.length === 1}
                  onClick={() =>
                    setRows((current) =>
                      current.filter((item) => item.id !== target.id),
                    )
                  }
                >
                  <X aria-hidden="true" />
                </Button>
              </div>
              <Input
                aria-label={`${label} ${engine === "prebuilt" ? "content folder" : "build output"}`}
                className="font-mono text-xs"
                value={target.path}
                placeholder={
                  engine === "prebuilt" ? "Content folder" : "Build output"
                }
                onChange={(event) =>
                  updateRow(target.id, { path: event.target.value })
                }
                spellCheck={false}
                required
              />
              {engine !== "prebuilt" &&
                (presetOptions.length > 0 ? (
                  <NativeSelect
                    aria-label={`${label} ${settingLabels[engine].toLowerCase()}`}
                    className="w-full"
                    value={target.setting}
                    onChange={(event) =>
                      updateRow(target.id, { setting: event.target.value })
                    }
                  >
                    {!presetOptions.some(
                      (preset) => preset.name === target.setting,
                    ) && (
                      <NativeSelectOption value={target.setting}>
                        {target.setting || "Choose an export preset"}
                      </NativeSelectOption>
                    )}
                    {presetOptions.map((preset) => (
                      <NativeSelectOption key={preset.name} value={preset.name}>
                        {preset.name}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                ) : (
                  <Input
                    aria-label={`${label} ${settingLabels[engine].toLowerCase()}`}
                    className="font-mono text-xs"
                    value={target.setting}
                    placeholder={settingLabels[engine]}
                    onChange={(event) =>
                      updateRow(target.id, { setting: event.target.value })
                    }
                    spellCheck={false}
                  />
                ))}
              {Boolean(
                target.mappings?.length || target.exclusions?.length,
              ) && (
                <p className="text-xs text-muted-foreground">
                  Keeps{" "}
                  {[
                    counted(target.mappings?.length, "file mapping"),
                    counted(target.exclusions?.length, "exclusion"),
                  ]
                    .filter(Boolean)
                    .join(" and ")}{" "}
                  from the VDF.
                </p>
              )}
            </div>
          );
        })}
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() =>
            setRows((current) => [
              ...current,
              row({
                name: `target${current.length + 1}`,
                platform: "",
                path: "",
                setting: "",
              }),
            ])
          }
        >
          <Plus aria-hidden="true" />
          Add target
        </Button>
      </fieldset>
      <div className="space-y-1">
        <Label htmlFor="new-config-path">Save configuration as</Label>
        <div className="flex gap-2">
          <Input
            id="new-config-path"
            className="min-w-0 font-mono text-xs"
            value={request.path}
            onChange={(event) => setConfigPath(event.target.value)}
            placeholder="/path/to/steam-release.yaml"
            spellCheck={false}
            disabled={disabled}
            required
          />
          <Button
            type="button"
            size="icon"
            variant="outline"
            aria-label="Choose new configuration path"
            disabled={disabled}
            onClick={async () => {
              const chosen = await state.chooseNewConfigPath();
              if (chosen) setConfigPath(chosen);
            }}
          >
            <FolderOpen aria-hidden="true" />
          </Button>
        </div>
      </div>
      <details className="rounded-lg border bg-muted/20" open>
        <summary className="cursor-pointer px-3 py-2 text-sm font-medium">
          YAML preview
        </summary>
        {previewError ? (
          <p
            role="alert"
            className="px-3 pb-3 text-sm break-words text-red-300"
          >
            {previewError}
          </p>
        ) : shown?.yaml ? (
          <pre
            aria-label="Generated YAML"
            className="max-h-80 overflow-auto px-3 pb-3 font-mono text-xs whitespace-pre"
          >
            {shown.yaml}
          </pre>
        ) : (
          <p className="px-3 pb-3 text-xs text-muted-foreground">
            Fill in the game folder, project name, and App ID to see the file.
          </p>
        )}
      </details>
      <Button type="submit" className="w-full" disabled={disabled}>
        Create configuration
      </Button>
    </form>
  );
}
