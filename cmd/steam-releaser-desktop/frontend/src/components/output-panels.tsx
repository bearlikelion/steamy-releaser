import {
  CircleCheck,
  CircleDashed,
  CircleX,
  LoaderCircle,
  MinusCircle,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import type { ReleaseConsole } from "@/hooks/use-release-console";
import type { ProgressRow, ProgressStatus } from "@/lib/release-progress";
import { cn } from "@/lib/utils";
import { DoctorResults } from "@/components/doctor-results";
import { PlanResults } from "@/components/plan-results";
import { retryLabel } from "@/components/history-panel";
import { retryAction, retryUnavailableReason } from "@/lib/release-retry";

const statusLabels: Record<ProgressStatus, string> = {
  pending: "Waiting",
  running: "Building",
  complete: "Complete",
  failed: "Failed",
  skipped: "Skipped",
  cancelled: "Cancelled",
};

function ProgressIcon({ status }: { status: ProgressStatus }) {
  if (status === "complete")
    return (
      <CircleCheck className="size-4 text-emerald-400" aria-hidden="true" />
    );
  if (status === "failed")
    return <CircleX className="size-4 text-red-400" aria-hidden="true" />;
  if (status === "running")
    return (
      <LoaderCircle
        className="size-4 animate-spin text-amber-400"
        aria-hidden="true"
      />
    );
  if (status === "cancelled" || status === "skipped")
    return (
      <MinusCircle
        className="size-4 text-muted-foreground"
        aria-hidden="true"
      />
    );
  return (
    <CircleDashed className="size-4 text-muted-foreground" aria-hidden="true" />
  );
}

function ProgressRowView({
  row,
  steam = false,
}: {
  row: ProgressRow;
  steam?: boolean;
}) {
  const indeterminate = steam && row.status === "running" && row.progress === 0;
  const label =
    steam && row.status === "running"
      ? row.name === "Steam upload"
        ? "Uploading"
        : row.name === "Godot imports"
          ? "Preparing"
          : "Connecting"
      : statusLabels[row.status];
  return (
    <fieldset className="min-w-0 rounded-lg border bg-muted/15 p-3">
      <legend className="sr-only">{row.name} progress</legend>
      <div className="flex min-w-0 items-center gap-2">
        <ProgressIcon status={row.status} />
        <span className="min-w-0 flex-1 truncate text-sm font-medium">
          {row.name}
        </span>
        {row.platform &&
          row.platform.toLowerCase() !== row.name.toLowerCase() && (
            <span className="text-xs text-muted-foreground capitalize">
              {row.platform}
            </span>
          )}
        <span className="text-xs text-muted-foreground">{label}</span>
        {!indeterminate && (
          <span className="w-9 text-right font-mono text-xs tabular-nums">
            {row.progress}%
          </span>
        )}
      </div>
      <progress
        aria-label={`${row.name} ${label.toLowerCase()}`}
        max={100}
        value={indeterminate ? undefined : row.progress}
        className={cn(
          "mt-2 h-2 w-full overflow-hidden rounded-full accent-primary",
          row.status === "failed" && "accent-red-400",
          row.status === "complete" && "accent-emerald-400",
        )}
      />
      {row.status !== "pending" && row.detail && (
        <p
          className={cn(
            "mt-2 truncate text-xs text-muted-foreground",
            row.status === "failed" && "text-red-300",
          )}
          title={row.detail}
        >
          {row.detail}
        </p>
      )}
      {row.output.length > 0 && (
        <details className="mt-2 text-xs">
          <summary className="cursor-pointer text-muted-foreground hover:text-foreground">
            Recent output
          </summary>
          <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded border bg-background p-2 font-mono text-xs">
            {row.output.join("\n")}
          </pre>
        </details>
      )}
    </fieldset>
  );
}

export function OutputPanels({ console: state }: { console: ReleaseConsole }) {
  const progress = state.progress;
  const diagnostics =
    progress.action === "doctor" || progress.action === "plan";
  const showResults = diagnostics && (state.result !== null || state.busy);
  const showProgress =
    progress.action !== null && !diagnostics && progress.status !== "idle";

  if (!showResults && !showProgress) return null;

  return (
    <div
      className={cn(
        "grid min-h-0 flex-1 gap-4",
        showResults && showProgress && "xl:grid-cols-2",
      )}
    >
      {showResults && (
        <Card className="min-h-64 min-w-0 overflow-hidden md:min-h-0">
          <CardHeader className="border-b">
            <CardDescription>Structured output</CardDescription>
            <CardTitle>
              <h2 id="result-heading">
                {diagnostics ? "Plan & diagnostics" : "Operation result"}
              </h2>
            </CardTitle>
            <CardAction>
              <Button
                variant="ghost"
                size="sm"
                onClick={state.clearResult}
                aria-label="Clear results"
              >
                Clear
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent
            className="min-h-0 flex-1 overflow-auto"
            aria-labelledby="result-heading"
          >
            {state.result === null ? (
              <p className="text-sm leading-relaxed text-muted-foreground">
                Running {progress.action === "doctor" ? "Doctor" : "Plan"}…
              </p>
            ) : state.doctorReport ? (
              <div className="space-y-4">
                <DoctorResults report={state.doctorReport} />
                <details>
                  <summary className="cursor-pointer text-xs text-muted-foreground">
                    Raw diagnostics
                  </summary>
                  <pre className="mt-2 font-mono text-xs whitespace-pre-wrap break-all">
                    {state.result}
                  </pre>
                </details>
              </div>
            ) : progress.action === "plan" && progress.status === "complete" ? (
              <PlanResults result={state.result} />
            ) : (
              <pre
                role={
                  diagnostics && progress.status === "failed"
                    ? "alert"
                    : undefined
                }
                className="font-mono text-xs leading-relaxed whitespace-pre-wrap break-all"
              >
                {state.result}
              </pre>
            )}
          </CardContent>
        </Card>
      )}
      {showProgress && (
        <Card className="min-h-80 min-w-0 overflow-hidden md:min-h-0">
          <CardHeader className="border-b">
            <CardDescription>{progress.stage}</CardDescription>
            <CardTitle>
              <h2 id="progress-heading">
                {progress.action === "login" || progress.action === "logout"
                  ? "Steam progress"
                  : "Release progress"}
              </h2>
            </CardTitle>
            <CardAction>
              <Button
                variant="ghost"
                size="sm"
                disabled={state.busy}
                onClick={state.clearProgress}
                aria-label="Clear progress"
              >
                Clear
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent
            className="min-h-0 flex-1 space-y-3 overflow-auto"
            aria-labelledby="progress-heading"
          >
            {progress.status === "complete" && progress.publication && (
              <div
                aria-live="polite"
                className="rounded-lg border border-emerald-400/30 bg-emerald-400/10 p-4"
              >
                <p className="font-semibold text-emerald-300">
                  {progress.publication.preview
                    ? "Steam preview completed"
                    : "Steam upload completed"}
                </p>
                <p className="mt-1 text-sm">
                  App ID {progress.publication.appId} · Profile{" "}
                  {progress.publication.profile}
                  {" · Targets: "}
                  {progress.publication.targets.join(", ")}
                </p>
                {(progress.publication.preview ||
                  progress.publication.setLive) && (
                  <p className="mt-1 text-sm text-muted-foreground">
                    {progress.publication.preview
                      ? "Preview only; no content was uploaded."
                      : `Set live on beta branch ${progress.publication.setLive}.`}
                  </p>
                )}
              </div>
            )}
            {state.retry &&
              (progress.status === "failed" ||
                progress.status === "cancelled") && (
                <div className="space-y-2 rounded-lg border p-3">
                  <p className="text-sm">
                    {retryUnavailableReason(state.retry) ||
                      (retryAction(state.retry) === "upload"
                        ? "Retry the upload after verifying the recorded artifacts."
                        : "Retry the original selection, reusing verified targets and rebuilding the rest.")}
                  </p>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={
                      state.locked ||
                      !state.configurationReady ||
                      !!retryUnavailableReason(state.retry)
                    }
                    onClick={() => void state.retryRelease(state.retry!)}
                  >
                    {retryLabel(state.retry)}
                  </Button>
                </div>
              )}
            {progress.targets.length === 0 &&
            !progress.preparation &&
            !progress.steam &&
            !progress.message ? (
              <p className="text-sm leading-relaxed text-muted-foreground">
                Build progress will appear here for each selected platform.
              </p>
            ) : (
              <>
                {progress.preparation && (
                  <ProgressRowView row={progress.preparation} steam />
                )}
                {progress.targets.map((row) => (
                  <ProgressRowView key={row.name} row={row} />
                ))}
                {progress.steam && (
                  <ProgressRowView row={progress.steam} steam />
                )}
                {progress.message && (
                  <p
                    role={progress.status === "failed" ? "alert" : "status"}
                    className={cn(
                      "rounded-lg border p-3 text-sm",
                      progress.status === "failed"
                        ? "border-red-400/30 text-red-300"
                        : "text-muted-foreground",
                    )}
                  >
                    {progress.message}
                  </p>
                )}
              </>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
