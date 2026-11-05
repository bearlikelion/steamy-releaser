import { CircleCheck, CircleX, TriangleAlert } from "lucide-react";
import type { DoctorReport } from "@/lib/types";

const fixes: Record<string, string> = {
  "build.args":
    "Edit build.args in YAML to remove overlapping arguments and use the engine's build settings instead.",
  "tool.steamcmd":
    "Install SteamCMD and add it to PATH, or set STEAMCMD_BIN to its executable.",
  "tool.godot":
    "Select an installed Godot executable using GODOT_BIN or project.engine.executable.",
  "tool.unity":
    "Install the project's Unity Editor version, or set UNITY_BIN to the Editor executable.",
  "tool.custom":
    "Install the build tool and check the executable configured in build.command.",
  "project.root":
    "Choose the correct Game project folder from Change project, then reload the configuration.",
  "project.godot": "Choose the folder containing project.godot.",
  "project.unity": "Choose the folder containing ProjectSettings and Assets.",
  "godot.preset":
    "Set build.preset to an existing name from export_presets.cfg.",
  "unity.profile":
    "Set build.profile to an existing Unity Build Profile asset.",
  "unity.module": "Install the platform's build support module in Unity Hub.",
  "artifact.missing":
    "Build this target before uploading, or correct its output and content paths.",
  "artifact.invalid":
    "Correct the content and output paths, and supply the required files before uploading.",
  "steam.depots":
    "Select every target sharing the same depot before uploading.",
  "steam.options":
    "Use a valid beta branch, and turn off Preview only when setting a build live.",
  "steam.script":
    "Check the existing VDF path and upload the full profile without Preview or Set live.",
  "steam.account": "Use Sign in to Steam to select your build account.",
};

export function DoctorResults({ report }: { report: DoctorReport }) {
  const issues = report.issues ?? [];
  const errors = issues.filter((issue) => issue.severity === "error").length;
  const warnings = issues.filter(
    (issue) => issue.severity === "warning",
  ).length;
  return (
    <div className="space-y-3">
      <output className="block text-sm font-medium">
        {errors
          ? `${errors} blocking issue${errors === 1 ? "" : "s"}`
          : "Local checks passed"}
        {warnings ? ` · ${warnings} warning${warnings === 1 ? "" : "s"}` : ""}
      </output>
      {issues.length === 0 && (
        <p className="text-xs text-muted-foreground">
          No local issues found. Steam will verify account permissions during
          upload.
        </p>
      )}
      <ul className="space-y-3" aria-label="Diagnostic checklist">
        {issues.map((issue, index) => {
          const Icon =
            issue.severity === "error"
              ? CircleX
              : issue.severity === "warning"
                ? TriangleAlert
                : CircleCheck;
          return (
            <li
              key={`${issue.code}-${issue.target ?? ""}-${index}`}
              className="flex gap-2 text-xs"
            >
              <Icon
                aria-hidden="true"
                className={`mt-0.5 size-4 shrink-0 ${issue.severity === "error" ? "text-red-400" : issue.severity === "warning" ? "text-amber-400" : "text-emerald-400"}`}
              />
              <div className="min-w-0 space-y-1 break-words">
                <p>
                  <span className="sr-only">{issue.severity}: </span>
                  {issue.target && <strong>{issue.target}: </strong>}
                  {issue.message}
                </p>
                {issue.severity !== "info" && fixes[issue.code] && (
                  <p className="text-muted-foreground">{fixes[issue.code]}</p>
                )}
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
