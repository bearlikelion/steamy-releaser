import { FolderOpen, Settings, X } from "lucide-react";
import { Dialog } from "radix-ui";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import type { ReleaseConsole } from "@/hooks/use-release-console";

export function ClientSettings({
  console: state,
}: {
  console: ReleaseConsole;
}) {
  return (
    <Dialog.Root>
      <Dialog.Trigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="[--wails-draggable:no-drag]"
          aria-label="Client settings"
          title="Client settings"
        >
          <Settings aria-hidden="true" />
        </Button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/70 [--wails-draggable:no-drag]" />
        <Dialog.Content className="fixed top-1/2 left-1/2 z-50 max-h-[calc(100svh-2rem)] w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 space-y-6 overflow-y-auto rounded-xl border bg-card p-6 text-card-foreground shadow-2xl [--wails-draggable:no-drag] focus:outline-none">
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-1">
              <Dialog.Title className="text-lg font-semibold">
                Client settings
              </Dialog.Title>
              <Dialog.Description className="text-sm text-muted-foreground">
                Preferences for Steam Releaser on this device.
              </Dialog.Description>
            </div>
            <Dialog.Close asChild>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Close client settings"
              >
                <X aria-hidden="true" />
              </Button>
            </Dialog.Close>
          </div>
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <Checkbox
                id="background-notifications"
                checked={state.notifications}
                disabled={state.locked || state.configuringNotifications}
                aria-describedby="background-notifications-description"
                onCheckedChange={(checked) =>
                  void state.configureNotifications(checked === true)
                }
              />
              <Label htmlFor="background-notifications">
                Background notifications
              </Label>
            </div>
            <p
              id="background-notifications-description"
              className="text-xs text-muted-foreground"
            >
              Get system alerts for completed builds, release failures, and
              Steam sign-in requests.
            </p>
          </div>
          <div className="space-y-2 border-t pt-5">
            <div className="flex items-center gap-2">
              <Checkbox
                id="steamcmd-saved-login"
                checked={state.steamcmdAccess === "allow"}
                disabled={state.locked}
                aria-describedby="steamcmd-saved-login-description"
                onCheckedChange={(checked) =>
                  void state.setSteamcmdAccess(
                    checked === true ? "allow" : "deny",
                  )
                }
              />
              <Label htmlFor="steamcmd-saved-login">
                Reuse SteamCMD's saved login
              </Label>
            </div>
            <p
              id="steamcmd-saved-login-description"
              className="text-xs text-muted-foreground"
            >
              Read only the account names SteamCMD has signed in with. Passwords
              and tokens stay in SteamCMD.
            </p>
          </div>
          <div className="space-y-3 border-t pt-5">
            <div className="flex items-center gap-2">
              <Checkbox
                id="use-steam-credential-file"
                checked={state.credentialFile.enabled}
                disabled={state.locked || state.configuringCredentialFile}
                aria-describedby="use-steam-credential-file-description"
                onCheckedChange={(checked) =>
                  void state.configureCredentialFile(checked === true)
                }
              />
              <Label htmlFor="use-steam-credential-file">
                Use an existing Steam credential file
              </Label>
            </div>
            <p
              id="use-steam-credential-file-description"
              className="text-xs text-muted-foreground"
            >
              Choose an existing JSON file with your Steam username and
              password. The file is unencrypted and should be kept in a private
              location. Steam Releaser only reads it.
            </p>
            {state.credentialFile.enabled && (
              <div className="space-y-2">
                <Label htmlFor="steam-credential-file">Credential file</Label>
                <div className="flex gap-2">
                  <Input
                    id="steam-credential-file"
                    value={state.credentialFile.path}
                    readOnly
                    title={state.credentialFile.path}
                  />
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    aria-label="Browse credential file"
                    title="Browse credential file"
                    disabled={state.locked || state.configuringCredentialFile}
                    onClick={() =>
                      void state.configureCredentialFile(true, true)
                    }
                  >
                    <FolderOpen aria-hidden="true" />
                  </Button>
                </div>
                {state.credentialFile.problem ? (
                  <p role="alert" className="text-xs text-destructive">
                    {state.credentialFile.problem}
                  </p>
                ) : (
                  <p className="text-xs text-muted-foreground">
                    {state.credentialFile.has_credentials
                      ? `File account: ${state.credentialFile.username}`
                      : "No credentials are available from this file."}
                  </p>
                )}
              </div>
            )}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
