import { useState } from "react";
import { ArrowLeft, Info, Package } from "lucide-react";
import { CommandBar } from "@/components/command-bar";
import { ClientSettings } from "@/components/client-settings";
import { OutputPanels } from "@/components/output-panels";
import { ProjectPanel } from "@/components/project-panel";
import { PublishPanel } from "@/components/publish-panel";
import { ReleasePanel } from "@/components/release-panel";
import { SteamAuth } from "@/components/steam-auth";
import { HistoryPanel } from "@/components/history-panel";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Toaster } from "@/components/ui/sonner";
import { useReleaseConsole } from "@/hooks/use-release-console";
import { version } from "../package.json";

export function App() {
  const state = useReleaseConsole();
  const [selectingProject, setSelectingProject] = useState(true);
  return (
    <div className="min-h-svh bg-background text-foreground">
      <header className="flex min-h-20 flex-wrap items-center justify-between gap-3 border-b px-4 py-4 sm:px-6 [--wails-draggable:drag]">
        <div className="flex items-center gap-3">
          <div className="rounded-lg border bg-primary/10 p-2 text-primary">
            <Package className="size-6" aria-hidden="true" />
          </div>
          <div>
            <h1 className="text-lg font-semibold tracking-tight">
              Steam Releaser
            </h1>
            <p className="text-xs text-muted-foreground">
              v{version}
              {import.meta.env.VITE_COMMIT_SHA &&
                ` (${import.meta.env.VITE_COMMIT_SHA})`}
            </p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-4">
          {selectingProject && state.summary ? (
            <Button
              variant="outline"
              size="sm"
              className="[--wails-draggable:no-drag]"
              disabled={state.locked}
              onClick={() => {
                state.restoreLoadedSelection();
                setSelectingProject(false);
              }}
            >
              <ArrowLeft aria-hidden="true" />
              Back to release
            </Button>
          ) : !selectingProject ? (
            <Button
              variant="outline"
              size="sm"
              className="[--wails-draggable:no-drag]"
              disabled={state.locked}
              onClick={() => setSelectingProject(true)}
            >
              Change project
            </Button>
          ) : null}
          <SteamAuth console={state} />
          <ClientSettings console={state} />
        </div>
      </header>
      {selectingProject ? (
        <main
          aria-label="Project selector"
          className="mx-auto w-full max-w-2xl space-y-6 px-4 py-8 sm:px-6 sm:py-12"
        >
          {!state.available && (
            <Alert>
              <Info aria-hidden="true" />
              <AlertTitle>Desktop connection required</AlertTitle>
              <AlertDescription>
                Open the desktop application to load projects and run releases.
              </AlertDescription>
            </Alert>
          )}
          <ProjectPanel
            console={state}
            onSelected={() => setSelectingProject(false)}
          />
        </main>
      ) : (
        <main className="grid min-h-0 md:h-[calc(100svh-5rem)] md:grid-cols-[20rem_minmax(0,1fr)]">
          <aside
            aria-label="Release configuration"
            className="space-y-4 border-b bg-muted/10 p-4 md:overflow-y-auto md:border-r md:border-b-0"
          >
            <ReleasePanel console={state} />
            <PublishPanel console={state} />
          </aside>
          <section
            aria-label="Release console"
            className="flex min-h-0 min-w-0 flex-col gap-4 p-4"
          >
            {!state.available && (
              <Alert>
                <Info aria-hidden="true" />
                <AlertTitle>Desktop connection required</AlertTitle>
                <AlertDescription>
                  Open the desktop application to load projects and run
                  releases.
                </AlertDescription>
              </Alert>
            )}
            <CommandBar console={state} />
            <OutputPanels console={state} />
            <HistoryPanel console={state} />
          </section>
        </main>
      )}
      <Toaster theme="dark" richColors closeButton />
    </div>
  );
}
