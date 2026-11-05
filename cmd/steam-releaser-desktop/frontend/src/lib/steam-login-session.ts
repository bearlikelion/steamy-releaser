// App-session memory only. Never serialize this object or store it in preferences.
export class SteamLoginSession {
  private enabled = false;
  private username = "";
  private password = "";
  private pendingPassword = "";
  private triedRememberedPassword = false;

  remember(enabled: boolean) {
    this.enabled = enabled;
    if (!enabled) this.forget();
  }

  selectAccount(username: string) {
    if (username !== this.username) this.forget();
    this.username = username;
  }

  begin(username: string) {
    this.selectAccount(username);
    this.pendingPassword = "";
    this.triedRememberedPassword = false;
  }

  passwordForPrompt() {
    // A repeated password prompt rejects the previous response. Do not loop.
    this.pendingPassword = "";
    if (this.triedRememberedPassword) this.forget();
    if (!this.enabled || !this.password) return null;
    this.triedRememberedPassword = true;
    return this.password;
  }

  submitPassword(password: string) {
    if (this.enabled) this.pendingPassword = password;
  }

  discardPendingPassword() {
    this.pendingPassword = "";
  }

  finish(ok: boolean, authenticationFailed: boolean) {
    if (authenticationFailed) this.forget();
    else if (ok && this.enabled && this.pendingPassword)
      this.password = this.pendingPassword;
    this.pendingPassword = "";
  }

  forget() {
    this.password = "";
    this.pendingPassword = "";
  }
}
