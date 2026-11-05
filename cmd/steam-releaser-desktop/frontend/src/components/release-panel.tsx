import { FilePenLine, RotateCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import {
  NativeSelect,
  NativeSelectOption,
} from "@/components/ui/native-select";
import type { ReleaseConsole } from "@/hooks/use-release-console";

export function ReleasePanel({ console: state }: { console: ReleaseConsole }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Release</h2>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="profile">Profile</Label>
          <NativeSelect
            id="profile"
            className="w-full"
            value={state.profile?.name ?? ""}
            disabled={state.locked || !state.summary}
            onChange={(event) => state.selectProfile(event.target.value)}
          >
            {!state.summary && (
              <NativeSelectOption value="">
                Load a project first
              </NativeSelectOption>
            )}
            {state.summary?.profiles.map((profile) => (
              <NativeSelectOption key={profile.name} value={profile.name}>
                {profile.name} · App {profile.app_id}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <fieldset
          className="space-y-2"
          disabled={state.locked || !state.profile}
        >
          <legend className="sr-only">Targets</legend>
          <div className="flex items-center justify-between gap-2">
            <span className="text-sm font-medium">
              Targets · {state.targets.length} selected
            </span>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={state.locked || !state.profile?.targets.length}
              onClick={state.toggleTargets}
            >
              {state.profile &&
              state.targets.length === state.profile.targets.length
                ? "Clear all"
                : "Select all"}
            </Button>
          </div>
          {state.profile?.targets.map((target, index) => {
            const id = `target-${index}`;
            return (
              <div
                key={target.name}
                className="flex items-center gap-3 rounded-lg border bg-muted/20 p-3"
              >
                <Checkbox
                  id={id}
                  checked={state.targets.includes(target.name)}
                  disabled={state.locked}
                  onCheckedChange={(checked) =>
                    state.toggleTarget(target.name, checked === true)
                  }
                  aria-describedby={`${id}-details`}
                />
                <div className="min-w-0 flex-1 space-y-1">
                  <Label htmlFor={id} className="break-all">
                    {target.name}
                  </Label>
                  <p
                    id={`${id}-details`}
                    className="text-xs text-muted-foreground break-words"
                  >
                    {[target.platform, target.architecture]
                      .filter(Boolean)
                      .join(" / ")}
                    {(target.platform || target.architecture) && " · "}Depot{" "}
                    {target.depot_id}
                  </p>
                  {target.build_setting && (
                    <p className="break-words text-xs text-muted-foreground">
                      {target.build_setting}
                    </p>
                  )}
                  {target.build_args?.length ||
                  Object.keys(target.build_env ?? {}).length ? (
                    <p className="text-xs text-muted-foreground">
                      {target.build_args?.length ?? 0} extra arguments ·{" "}
                      {Object.keys(target.build_env ?? {}).length} environment
                      overrides
                    </p>
                  ) : null}
                </div>
              </div>
            );
          })}
          {!state.profile?.targets.length && (
            <p className="py-2 text-sm text-muted-foreground">
              No targets available
            </p>
          )}
        </fieldset>
        <div className="space-y-3 border-t pt-4">
          <p
            aria-label="Configuration file"
            className="break-all font-mono text-xs text-muted-foreground"
          >
            {state.summary?.path}
          </p>
          <Button
            type="button"
            variant="outline"
            className="w-full"
            disabled={state.locked || !state.summary}
            onClick={() => void state.editConfiguration()}
          >
            <FilePenLine aria-hidden="true" />
            Edit YAML
          </Button>
          <Button
            type="button"
            variant="secondary"
            className="w-full"
            disabled={state.locked || !state.summary}
            onClick={() => void state.reloadConfiguration()}
          >
            <RotateCw aria-hidden="true" />
            Reload and validate
          </Button>
          {state.configurationError && (
            <p role="alert" className="break-words text-sm text-red-300">
              {state.configurationError}
            </p>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
