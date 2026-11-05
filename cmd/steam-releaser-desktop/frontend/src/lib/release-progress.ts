import type {
  OperationAction,
  ProfileSummary,
  ReleaseEvent,
} from "@/lib/types";

export type ProgressStatus =
  "pending" | "running" | "complete" | "failed" | "skipped" | "cancelled";

export interface ProgressRow {
  name: string;
  platform: string;
  status: ProgressStatus;
  progress: number;
  detail: string;
  output: string[];
}

export interface ReleaseProgress {
  action: OperationAction | null;
  stage: string;
  status: "idle" | "running" | "complete" | "failed" | "cancelled";
  publication: {
    appId: number;
    profile: string;
    targets: string[];
    preview: boolean;
    setLive: string;
  } | null;
  preparation: ProgressRow | null;
  targets: ProgressRow[];
  steam: ProgressRow | null;
  message: string;
}

const outputLimit = 24;
const percentPattern = /(?:^|[^\d])(100|[1-9]?\d)%/g;

function row(name: string, platform = ""): ProgressRow {
  return {
    name,
    platform,
    status: "pending",
    progress: 0,
    detail: "Waiting",
    output: [],
  };
}

export const emptyProgress: ReleaseProgress = {
  action: null,
  stage: "Ready",
  status: "idle",
  publication: null,
  preparation: null,
  targets: [],
  steam: null,
  message: "",
};

export function startProgress(
  action: OperationAction,
  profile: ProfileSummary,
  selectedTargets: string[],
  publish: { preview: boolean; setLive: string },
): ReleaseProgress {
  const targets =
    action === "build" || action === "ship"
      ? profile.targets
          .filter((target) => selectedTargets.includes(target.name))
          .map((target) => row(target.name, target.platform))
      : [];
  const steam =
    action === "ship" || action === "upload"
      ? row("Steam upload")
      : action === "login"
        ? row("Steam sign-in")
        : action === "logout"
          ? row("Steam sign-out")
          : null;
  return {
    action,
    stage:
      action === "build" || action === "ship"
        ? "Preparing builds"
        : action === "upload"
          ? "Preparing upload"
          : action === "login"
            ? "Connecting to Steam"
            : action === "logout"
              ? "Signing out of Steam"
              : "Checking release",
    status: "running",
    publication:
      action === "ship" || action === "upload"
        ? {
            appId: profile.app_id,
            profile: profile.name,
            targets: [...selectedTargets],
            preview: publish.preview,
            setLive: publish.setLive,
          }
        : null,
    preparation: null,
    targets,
    steam,
    message: "",
  };
}

function percentage(message: string): number | null {
  let match: RegExpExecArray | null;
  let result: number | null = null;
  percentPattern.lastIndex = 0;
  while ((match = percentPattern.exec(message))) result = Number(match[1]);
  return result;
}

function addOutput(current: string[], message: string): string[] {
  const lines = message.split(/\r?\n/).filter(Boolean);
  return [...current, ...lines].slice(-outputLimit);
}

function updateTarget(target: ProgressRow, event: ReleaseEvent): ProgressRow {
  if (event.kind === "build-start") {
    return { ...target, status: "running", progress: 2, detail: "Building" };
  }
  if (event.kind === "build-prebuilt" || event.kind === "build-reused") {
    return {
      ...target,
      status: "complete",
      progress: 100,
      detail:
        event.kind === "build-reused"
          ? "Reused completed build"
          : "Prebuilt artifact",
    };
  }
  if (event.kind === "build-complete") {
    return {
      ...target,
      status: "complete",
      progress: 100,
      detail: "Build complete",
    };
  }
  if (event.kind === "build-failed") {
    return {
      ...target,
      status: "failed",
      detail: event.message,
      output: addOutput(target.output, event.message),
    };
  }
  if (target.status === "complete" || target.status === "failed") {
    return {
      ...target,
      detail: event.message.trim() || target.detail,
      output: addOutput(target.output, event.message),
    };
  }
  const reported = percentage(event.message);
  const progress =
    reported === null
      ? Math.min(90, target.progress + 1)
      : Math.max(target.progress, Math.min(90, reported));
  return {
    ...target,
    status: "running",
    progress,
    detail: event.message.trim() || target.detail,
    output: addOutput(target.output, event.message),
  };
}

function updateSteam(target: ProgressRow, event: ReleaseEvent): ProgressRow {
  const reported = percentage(event.message);
  return {
    ...target,
    status: "running",
    progress:
      reported === null
        ? target.progress
        : Math.max(target.progress, Math.min(99, reported)),
    detail: event.message.trim() || target.detail,
    output: addOutput(target.output, event.message),
  };
}

export function progressEvent(
  current: ReleaseProgress,
  event: ReleaseEvent,
): ReleaseProgress {
  if (current.status !== "running") return current;
  const index = current.targets.findIndex(
    (target) => target.name === event.target,
  );
  if (index >= 0) {
    const targets = [...current.targets];
    targets[index] = updateTarget(targets[index], event);
    return {
      ...current,
      preparation: current.preparation && {
        ...current.preparation,
        status: "complete",
        progress: 100,
        detail: "Imports complete",
      },
      targets,
      stage:
        event.kind === "build-complete" &&
        targets.every((target) => target.status === "complete")
          ? "Validating builds"
          : "Building platforms",
    };
  }
  if (event.target?.startsWith("SteamCMD") && current.steam) {
    return {
      ...current,
      stage:
        current.action === "login"
          ? "Signing in to Steam"
          : current.action === "logout"
            ? "Signing out of Steam"
            : "Uploading to Steam",
      steam: updateSteam(current.steam, event),
    };
  }
  if (event.target === "Godot import") {
    return {
      ...current,
      stage: "Preparing Godot imports",
      preparation: updateSteam(
        current.preparation ?? row("Godot imports"),
        event,
      ),
    };
  }
  if (event.level === "success" || event.level === "error") {
    return { ...current, message: event.message };
  }
  return current;
}

export function finishProgress(
  current: ReleaseProgress,
  ok: boolean,
  error = "",
  cancelled = false,
): ReleaseProgress {
  const status = ok ? "complete" : cancelled ? "cancelled" : "failed";
  const preparation: ProgressRow | null = current.preparation && {
    ...current.preparation,
    status: current.preparation.status === "complete" ? "complete" : status,
    progress: ok ? 100 : current.preparation.progress,
    detail:
      current.preparation.status === "complete"
        ? current.preparation.detail
        : ok
          ? "Imports complete"
          : error,
  };
  const targets = current.targets.map((target): ProgressRow => {
    if (target.status === "complete" || target.status === "failed")
      return target;
    if (ok)
      return {
        ...target,
        status: "complete",
        progress: 100,
        detail: "Build complete",
      };
    return {
      ...target,
      status:
        target.status === "running"
          ? cancelled
            ? "cancelled"
            : "failed"
          : "skipped",
      detail: target.status === "running" ? error : "Not started",
    };
  });
  const steam: ProgressRow | null = current.steam && {
    ...current.steam,
    status: (current.steam.status === "pending" && !ok
      ? "skipped"
      : status) as ProgressStatus,
    progress: ok ? 100 : current.steam.progress,
    detail: ok
      ? "Complete"
      : current.steam.status === "pending"
        ? "Not started"
        : error,
  };
  return {
    ...current,
    preparation,
    targets,
    steam,
    status,
    stage: ok ? "Completed" : cancelled ? "Cancelled" : "Failed",
    message: error || current.message,
  };
}
