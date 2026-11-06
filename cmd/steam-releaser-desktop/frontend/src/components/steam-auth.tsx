import { useState } from "react";
import type { FormEvent } from "react";
import { LoaderCircle, X } from "lucide-react";
import { Dialog } from "radix-ui";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { ReleaseConsole } from "@/hooks/use-release-console";

export function SteamAuth({ console: state }: { console: ReleaseConsole }) {
  const [manualOpen, setManualOpen] = useState(false);
  const [account, setAccount] = useState(
    state.username || (state.steamcmdAccounts[0] ?? ""),
  );
  const [response, setResponse] = useState("");
  const [sending, setSending] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [loginCountAtOpen, setLoginCountAtOpen] = useState(
    state.steamLoginCount,
  );
  const loginRunning = state.operation?.action === "login";
  const steamOperation =
    loginRunning ||
    state.operation?.action === "upload" ||
    state.operation?.action === "ship";

  const open =
    (manualOpen && !(attempted && state.steamLoginCount > loginCountAtOpen)) ||
    Boolean(state.steamPrompt && steamOperation);

  function changeOpen(next: boolean) {
    if (!next && (loginRunning || state.steamPrompt)) return;
    if (next) {
      setAccount(state.username || (state.steamcmdAccounts[0] ?? ""));
      setResponse("");
      setAttempted(false);
      setLoginCountAtOpen(state.steamLoginCount);
    } else {
      setResponse("");
    }
    setManualOpen(next);
  }

  async function signIn(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (state.locked || !account.trim()) return;
    setAttempted(true);
    await state.signIn(account);
  }

  async function signInAs(name: string) {
    if (state.locked) return;
    setAccount(name);
    setAttempted(true);
    await state.signIn(name);
  }

  async function sendResponse(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!response || sending) return;
    const answer = response;
    setSending(true);
    try {
      if (await state.submitSteamInput(answer)) setResponse("");
    } finally {
      setSending(false);
    }
  }

  return (
    <Dialog.Root open={open} onOpenChange={changeOpen}>
      <Dialog.Trigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="max-w-48 [--wails-draggable:no-drag]"
          disabled={!state.available || (state.busy && !steamOperation)}
          aria-label={
            state.username
              ? `Steam account ${state.username}`
              : "Sign in to Steam"
          }
        >
          <output
            aria-label={
              state.steamAuthStatus === "verified"
                ? "Steam login verified"
                : state.steamAuthStatus === "cached"
                  ? "Saved Steam account; login will be checked when needed"
                  : "Steam sign-in required"
            }
            className={`block size-2 shrink-0 rounded-full ${state.steamAuthStatus === "verified" ? "bg-emerald-400" : "bg-muted-foreground"}`}
          />
          <span className="truncate">
            {state.username || "Sign in to Steam"}
          </span>
        </Button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/70 [--wails-draggable:no-drag]" />
        <Dialog.Content className="fixed top-1/2 left-1/2 z-50 w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 space-y-5 rounded-xl border bg-card p-6 text-card-foreground shadow-2xl [--wails-draggable:no-drag] focus:outline-none">
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-1">
              <Dialog.Title className="text-lg font-semibold">
                Steam sign-in
              </Dialog.Title>
              <Dialog.Description className="text-sm text-muted-foreground">
                {steamOperation
                  ? "Complete any password or Steam Guard prompt from SteamCMD."
                  : "SteamCMD will try its saved login first and ask for a password only if needed."}
              </Dialog.Description>
            </div>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label="Close Steam sign-in"
              disabled={loginRunning || Boolean(state.steamPrompt)}
              onClick={() => changeOpen(false)}
            >
              <X aria-hidden="true" />
            </Button>
          </div>
          {state.credentialFile.enabled ? (
            <p className="text-xs text-muted-foreground">
              The credential file in Client settings saves your password after a
              successful sign-in. Steam Guard responses are never saved.
            </p>
          ) : (
            <div className="space-y-2">
              <div className="flex items-center gap-2">
                <Checkbox
                  id="steam-remember-me"
                  checked={state.rememberSteamLogin}
                  onCheckedChange={(checked) =>
                    state.setRememberSteamLogin(checked === true)
                  }
                  disabled={sending || state.operation?.phase === "cancelling"}
                  aria-describedby="steam-remember-me-description"
                />
                <Label htmlFor="steam-remember-me">Remember Me</Label>
              </div>
              <p
                id="steam-remember-me-description"
                className="text-xs text-muted-foreground"
              >
                Keep my password in memory until I close the app. Steam Guard
                may still ask for approval.
              </p>
            </div>
          )}
          {!steamOperation && state.steamcmdPath && !state.steamcmdAccess && (
            <div className="space-y-3 rounded-lg border p-3">
              <p className="text-sm">
                Steam Releaser found SteamCMD at{" "}
                <span className="font-mono text-xs break-all">
                  {state.steamcmdPath}
                </span>
                . Allow it to read SteamCMD's saved account names to reuse your
                existing login? Only the account name is read; passwords and
                tokens stay in SteamCMD.
              </p>
              <div className="flex gap-2">
                <Button
                  type="button"
                  size="sm"
                  onClick={() => void state.setSteamcmdAccess("allow")}
                >
                  Allow
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => void state.setSteamcmdAccess("deny")}
                >
                  Not now
                </Button>
              </div>
            </div>
          )}
          {!steamOperation && state.steamcmdAccounts.length > 0 && (
            <div className="space-y-2">
              <p className="text-sm text-muted-foreground">
                SteamCMD is already signed in as{" "}
                {state.steamcmdAccounts.join(", ")}.
              </p>
              {state.steamcmdAccounts.map((name) => (
                <Button
                  key={name}
                  type="button"
                  variant="outline"
                  className="w-full"
                  disabled={state.locked}
                  onClick={() => void signInAs(name)}
                >
                  Continue as {name}
                </Button>
              ))}
            </div>
          )}
          {!steamOperation && (
            <form
              className="space-y-4"
              onSubmit={(event) => void signIn(event)}
            >
              <div className="space-y-2">
                <Label htmlFor="steam-account">Steam account</Label>
                <Input
                  id="steam-account"
                  autoComplete="username"
                  value={account}
                  onChange={(event) => setAccount(event.target.value)}
                  required
                  maxLength={64}
                />
              </div>
              <Button type="submit" className="w-full" disabled={state.locked}>
                Continue with SteamCMD
              </Button>
            </form>
          )}
          {steamOperation && (
            <div className="space-y-4">
              <output className="flex items-center gap-2 text-sm text-muted-foreground">
                <LoaderCircle
                  className="size-4 motion-safe:animate-spin"
                  aria-hidden="true"
                />
                {state.steamPrompt
                  ? "SteamCMD needs a response."
                  : "Waiting for SteamCMD…"}
              </output>
              {state.steamPrompt && (
                <form
                  className="space-y-3"
                  onSubmit={(event) => void sendResponse(event)}
                >
                  <div className="space-y-2">
                    <Label htmlFor="steam-response">
                      {state.steamPrompt === "password"
                        ? "Steam password"
                        : "Steam Guard response"}
                    </Label>
                    <Input
                      id="steam-response"
                      type="password"
                      autoComplete={
                        state.steamPrompt === "password"
                          ? "current-password"
                          : "off"
                      }
                      value={response}
                      onChange={(event) => setResponse(event.target.value)}
                      disabled={
                        state.operation?.phase === "cancelling" || sending
                      }
                      maxLength={1024}
                    />
                  </div>
                  {state.steamPrompt === "response" && (
                    <p className="text-xs text-muted-foreground">
                      For mobile approval, approve the request in Steam and wait
                      here.
                    </p>
                  )}
                  <Button
                    type="submit"
                    size="sm"
                    disabled={
                      !response ||
                      sending ||
                      state.operation?.phase === "cancelling"
                    }
                  >
                    Send response
                  </Button>
                </form>
              )}
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={state.operation?.phase === "cancelling"}
                onClick={() => {
                  setResponse("");
                  void state.cancelOperation();
                }}
              >
                {state.operation?.phase === "cancelling"
                  ? "Cancelling…"
                  : loginRunning
                    ? "Cancel sign-in"
                    : state.operation?.action === "ship"
                      ? "Cancel release"
                      : "Cancel upload"}
              </Button>
            </div>
          )}
          {!state.busy && state.username && (
            <Button
              type="button"
              variant="destructive"
              size="sm"
              className="w-full bg-destructive text-white hover:bg-destructive/85 dark:bg-destructive dark:hover:bg-destructive/85"
              disabled={state.locked}
              onClick={() => {
                void state.startOperation("logout");
                changeOpen(false);
              }}
            >
              Clear Steam Credentials
            </Button>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
