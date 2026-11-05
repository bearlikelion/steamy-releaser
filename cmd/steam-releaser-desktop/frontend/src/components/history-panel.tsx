import { RotateCw } from "lucide-react";
import type { ReleaseRecord } from "@/lib/types";
import { operationLabels } from "@/lib/types";
import { retryAction, retryUnavailableReason } from "@/lib/release-retry";
import type { ReleaseConsole } from "@/hooks/use-release-console";
import { Button } from "@/components/ui/button";

export function retryLabel(record: ReleaseRecord) {
  if (retryAction(record) === "upload") return "Retry upload";
  if (record.request.action === "ship" && record.build_ready)
    return "Retry build & deploy";
  return record.request.action === "ship"
    ? "Rebuild unfinished targets & deploy"
    : "Rebuild unfinished targets";
}

export function HistoryPanel({ console: state }: { console: ReleaseConsole }) {
  return (
    <details className="shrink-0 rounded-xl border bg-card p-4">
      <summary className="cursor-pointer text-sm font-semibold">
        Local Release History ({state.history.length})
      </summary>
      <div className="mt-3 max-h-80 space-y-3 overflow-auto">
        <div className="flex items-center justify-between gap-3">
          <p id="history-clear-help" className="sr-only">
            Clear hides this project's entries. Saved records remain stored
            locally.
          </p>
          {!state.history.length && (
            <p className="text-sm text-muted-foreground">No releases yet.</p>
          )}
          <Button
            variant="outline"
            size="sm"
            className="ml-auto"
            aria-label="Clear local release history"
            aria-describedby="history-clear-help"
            title="Hide entries; saved records stay local."
            disabled={!state.canClearHistory}
            onClick={() => void state.clearHistory()}
          >
            {state.clearingHistory ? "Clearing…" : "Clear"}
          </Button>
        </div>
        {state.historyError && (
          <p role="alert" className="text-sm text-amber-400">
            History could not be saved or loaded: {state.historyError}
          </p>
        )}
        {state.history.map((record) => (
          <article
            key={record.id}
            aria-label={`Release ${record.id}`}
            className="space-y-2 rounded-lg border p-3 text-sm"
          >
            <div className="flex flex-wrap justify-between gap-2">
              <strong>
                {operationLabels[record.request.action]} ·{" "}
                {record.request.profile}
              </strong>
              <span
                className={
                  record.status === "complete"
                    ? "text-emerald-400"
                    : record.status === "failed"
                      ? "text-red-400"
                      : "text-muted-foreground"
                }
              >
                {record.status === "running"
                  ? "Interrupted or still running"
                  : record.status}
              </span>
            </div>
            <p className="text-xs text-muted-foreground">
              {new Date(record.started_at).toLocaleString()} ·{" "}
              {record.finished_at
                ? `${(record.duration_ms / 1000).toFixed(1)}s`
                : "Duration unavailable"}{" "}
              · App {record.app_id}
            </p>
            <p>
              Targets: {record.request.targets.join(", ")}
              {record.request.action === "build"
                ? " · Build only"
                : record.request.preview
                  ? " · Preview only"
                  : record.request.set_live
                    ? ` · Beta branch: ${record.request.set_live}`
                    : " · Upload only; no branch activation"}
            </p>
            {(record.version || record.commit) && (
              <p className="font-mono text-xs">
                {record.version || "Untagged"}
                {record.commit ? ` · ${record.commit.slice(0, 12)}` : ""}
                {record.dirty ? " · Local changes" : ""}
              </p>
            )}
            {record.build_id && <p>Steam build ID: {record.build_id}</p>}
            {record.error && (
              <p className="break-words text-red-300">{record.error}</p>
            )}
            {retryUnavailableReason(record) && (
              <p className="text-xs text-muted-foreground">
                {retryUnavailableReason(record)}
              </p>
            )}
            {(record.status === "failed" || record.status === "cancelled") && (
              <Button
                variant="outline"
                size="sm"
                aria-label={`Retry release ${record.id}`}
                disabled={
                  state.locked ||
                  !state.configurationReady ||
                  !!retryUnavailableReason(record)
                }
                onClick={() => void state.retryRelease(record)}
              >
                <RotateCw aria-hidden="true" />
                {retryLabel(record)}
              </Button>
            )}
            {record.logs && (
              <details>
                <summary className="cursor-pointer text-xs text-muted-foreground">
                  Saved logs
                </summary>
                <pre className="mt-2 max-h-48 overflow-auto rounded border p-2 font-mono text-xs whitespace-pre-wrap break-all">
                  {record.logs}
                </pre>
              </details>
            )}
          </article>
        ))}
      </div>
    </details>
  );
}
