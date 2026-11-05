import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { ReleaseConsole } from "@/hooks/use-release-console";

export function PublishPanel({ console: state }: { console: ReleaseConsole }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Publish</h2>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-center gap-2">
          <Checkbox
            id="preview"
            checked={state.preview}
            disabled={state.locked}
            onCheckedChange={(checked) => state.setPreview(checked === true)}
          />
          <Label htmlFor="preview">Preview only</Label>
        </div>
        <div className="space-y-2">
          <Label htmlFor="set-live">
            Set live on beta branch{" "}
            <span className="text-xs text-muted-foreground">optional</span>
          </Label>
          <Input
            id="set-live"
            placeholder="beta"
            disabled={state.locked}
            value={state.setLive}
            onChange={(event) => state.setSetLive(event.target.value)}
          />
          {state.setLive.trim() && (
            <p className="text-xs text-muted-foreground">
              This beta branch will be activated after upload. The selection is
              remembered for this profile.
            </p>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
