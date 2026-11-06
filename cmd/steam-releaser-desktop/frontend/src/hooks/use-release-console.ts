import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { retryAction, retryUnavailableReason } from "@/lib/release-retry";
import {
  backend,
  desktopAvailable,
  errorMessage,
  initializePreferences,
  readLastConfig,
  readRecentConfigs,
  readProjectRoot,
  rememberConfig,
  rememberProjectRoot,
  readSelections,
  rememberSelection,
  readNotifications,
  rememberNotifications,
  readSteamCMDAccess,
  rememberSteamCMDAccess,
} from "@/lib/desktop";
import type { SteamCMDAccess } from "@/lib/desktop";
import { operationLabels } from "@/lib/types";
import { SteamLoginSession } from "@/lib/steam-login-session";
import {
  emptyProgress,
  finishProgress,
  progressEvent,
  startProgress,
} from "@/lib/release-progress";
import type {
  ConfigSummary,
  CreateConfigRequest,
  OperationAction,
  DoctorReport,
  ReleaseRecord,
  OperationRequest,
  CredentialFileSettings,
} from "@/lib/types";

type Operation = {
  action: OperationAction;
  phase: "starting" | "running" | "cancelling";
};

type SteamAuthStatus = "cached" | "verified" | "required";

export function useReleaseConsole() {
  const [initialPath] = useState(readLastConfig);
  const [initialRoot] = useState(() => readProjectRoot(initialPath));
  const available = desktopAvailable();
  const [preferencesReady, setPreferencesReady] = useState(!available);
  const [credentialFileReady, setCredentialFileReady] = useState(!available);
  const [credentialFile, setCredentialFile] = useState<CredentialFileSettings>({
    enabled: false,
    path: "",
    username: "",
    has_credentials: false,
  });
  const [configuringCredentialFile, setConfiguringCredentialFile] =
    useState(false);
  const credentialFileRef = useRef(credentialFile);
  const credentialFileVersionRef = useRef(0);
  const configuringCredentialFileRef = useRef(false);
  const [configPath, setConfigPath] = useState(initialPath);
  const [recentConfigs, setRecentConfigs] = useState(readRecentConfigs);
  const [projectRoot, setProjectRoot] = useState(initialRoot);
  const [loadedProjectRoot, setLoadedProjectRoot] = useState(initialRoot);
  const [summary, setSummary] = useState<ConfigSummary | null>(null);
  const [configurationError, setConfigurationError] = useState("");
  const [configurationNeedsReload, setConfigurationNeedsReload] =
    useState(false);
  const [profileName, setProfileName] = useState("");
  const [targets, setTargets] = useState<string[]>([]);
  const [username, setUsername] = useState("");
  const [steamAuthStatus, setSteamAuthStatus] =
    useState<SteamAuthStatus>("required");
  const [steamLoginCount, setSteamLoginCount] = useState(0);
  const [rememberSteamLogin, setRememberSteamLogin] = useState(false);
  const [steamcmdAccess, setSteamcmdAccess] =
    useState<SteamCMDAccess>(readSteamCMDAccess);
  const [steamcmdPath, setSteamcmdPath] = useState("");
  const [steamcmdAccounts, setSteamcmdAccounts] = useState<string[]>([]);
  const [steamPrompt, setSteamPrompt] = useState<
    "password" | "response" | null
  >(null);
  const [preview, setPreview] = useState(false);
  const [setLive, setSetLive] = useState("");
  const [loading, setLoading] = useState(false);
  const [operation, setOperation] = useState<Operation | null>(null);
  const [steamInputReady, setSteamInputReady] = useState(false);
  const [progress, setProgress] = useState(emptyProgress);
  const [result, setResult] = useState<string | null>(null);
  const [doctorReport, setDoctorReport] = useState<DoctorReport | null>(null);
  const [history, setHistory] = useState<ReleaseRecord[]>([]);
  const [historyError, setHistoryError] = useState("");
  const [clearingHistory, setClearingHistory] = useState(false);
  const clearingHistoryRef = useRef(false);
  const clearedHistoryIdsRef = useRef(new Set<string>());
  const historyVersionRef = useRef(0);
  const [retry, setRetry] = useState<ReleaseRecord | null>(null);
  const [notifications, setNotifications] = useState(readNotifications);
  const [configuringNotifications, setConfiguringNotifications] =
    useState(false);
  // Refs close the gap before React renders disabled controls after an action.
  const loadingRef = useRef(loading);
  const operationRef = useRef<Operation | null>(null);
  const steamOutputTailRef = useRef("");
  const steamPromptSequenceRef = useRef(0);
  const steamLoginSessionRef = useRef(new SteamLoginSession());
  const steamNotificationRef = useRef<string | null>(null);

  const profile =
    summary?.profiles.find((item) => item.name === profileName) ?? null;
  const busy = operation !== null;
  const configurationReady =
    !!summary && !configurationError && !configurationNeedsReload;
  const canClearHistory =
    !!summary &&
    available &&
    !loading &&
    !clearingHistory &&
    history.length > 0;
  const locked =
    busy ||
    loading ||
    clearingHistory ||
    !available ||
    !preferencesReady ||
    !credentialFileReady ||
    configuringCredentialFile;
  const applyConfig = useCallback(
    (config: ConfigSummary, preferred?: string, rootOverride = "") => {
      historyVersionRef.current++;
      clearedHistoryIdsRef.current.clear();
      const saved = readSelections(config.path, config.root);
      const selected =
        config.profiles.find(
          (item) => item.name === (preferred ?? saved.profile),
        ) ??
        config.profiles.find((item) => item.name === config.default_profile) ??
        config.profiles[0];
      const selection = selected && saved.profiles[selected.name];
      const restore =
        selection?.appId === selected?.app_id ? selection : undefined;
      setPreview(restore?.preview ?? false);
      setSetLive(restore?.branch ?? "");
      setSummary(config);
      setConfigurationError("");
      setConfigurationNeedsReload(false);
      setConfigPath(config.path);
      setProjectRoot(rootOverride);
      setLoadedProjectRoot(rootOverride);
      setProfileName(selected?.name ?? "");
      const names = selected?.targets.map((target) => target.name) ?? [];
      setTargets(
        preferred
          ? names
          : restore
            ? restore.targets.filter((name) => names.includes(name))
            : names,
      );
      setUsername(config.username);
      steamLoginSessionRef.current.selectAccount(config.username);
      setSteamAuthStatus(config.username ? "cached" : "required");
      setSteamPrompt(null);
      setResult(null);
      setDoctorReport(null);
      setRetry(null);
      setHistory([]);
      setHistoryError("");
      setProgress(emptyProgress);
      setRecentConfigs(rememberConfig(config.path, config.project));
      rememberProjectRoot(config.path, rootOverride);
    },
    [],
  );

  useEffect(() => {
    if (!available) return;
    const lifetime = new AbortController();
    void backend()
      .GetCredentialFileSettings()
      .then((settings) => {
        if (lifetime.signal.aborted) return;
        credentialFileRef.current = settings;
        setCredentialFile(settings);
        if (settings.problem)
          toast.warning(
            `Credential file could not be loaded: ${settings.problem}`,
          );
      })
      .catch((error: unknown) => {
        if (!lifetime.signal.aborted)
          toast.warning(
            `Credential file settings could not be loaded: ${errorMessage(error)}`,
          );
      })
      .finally(() => {
        if (!lifetime.signal.aborted) setCredentialFileReady(true);
      });
    return () => lifetime.abort();
  }, [available]);

  useEffect(() => {
    const lifetime = new AbortController();
    const restorePreferences = () => {
      if (lifetime.signal.aborted) return;
      const path = readLastConfig();
      const root = readProjectRoot(path);
      setConfigPath(path);
      setRecentConfigs(readRecentConfigs());
      setProjectRoot(root);
      setLoadedProjectRoot(root);
      setNotifications(readNotifications());
      setSteamcmdAccess(readSteamCMDAccess());
      setPreferencesReady(true);
    };
    void initializePreferences().then(restorePreferences, (error: unknown) => {
      if (!lifetime.signal.aborted) {
        toast.warning(
          `Local preferences could not be loaded: ${errorMessage(error)}`,
        );
        restorePreferences();
      }
    });
    return () => lifetime.abort();
  }, []);

  useEffect(() => {
    if (summary && profile)
      rememberSelection(summary.path, summary.root, profile.name, {
        appId: profile.app_id,
        targets,
        preview,
        branch: setLive,
      });
  }, [summary, profile, targets, preview, setLive]);

  useEffect(() => {
    if (!available || summary) return;
    const lifetime = new AbortController();
    const version = credentialFileVersionRef.current;
    void backend()
      .CachedUsername()
      .then((account) => {
        if (
          lifetime.signal.aborted ||
          version !== credentialFileVersionRef.current
        )
          return;
        setUsername(account);
        steamLoginSessionRef.current.selectAccount(account);
        setSteamAuthStatus(account ? "cached" : "required");
      })
      .catch(() => {
        // A project can still restore the saved account if this lookup fails.
      });
    return () => {
      // A loaded project supplies newer account state than this startup lookup.
      lifetime.abort();
    };
  }, [available, summary]);

  useEffect(() => {
    if (!available) return;
    const lifetime = new AbortController();
    void backend()
      .SteamCMDLocation()
      .then((path) => {
        if (!lifetime.signal.aborted) setSteamcmdPath(path);
      })
      .catch(() => {
        // Without a SteamCMD path there is nothing to offer.
      });
    return () => lifetime.abort();
  }, [available]);

  useEffect(() => {
    if (
      !available ||
      !preferencesReady ||
      steamcmdAccess !== "allow" ||
      !steamcmdPath
    )
      return;
    const lifetime = new AbortController();
    void backend()
      .SteamCMDCachedAccounts()
      .then((accounts) => {
        if (!lifetime.signal.aborted) setSteamcmdAccounts(accounts ?? []);
      })
      .catch((error: unknown) => {
        if (!lifetime.signal.aborted)
          toast.warning(
            `SteamCMD accounts could not be read: ${errorMessage(error)}`,
          );
      });
    return () => lifetime.abort();
  }, [available, preferencesReady, steamcmdAccess, steamcmdPath]);

  useEffect(() => {
    if (!available || !preferencesReady || !readNotifications()) return;
    const lifetime = new AbortController();
    void backend()
      .ConfigureNotifications(true)
      .then((enabled) => {
        if (!lifetime.signal.aborted) {
          setNotifications(enabled);
          rememberNotifications(enabled);
        }
      })
      .catch((error: unknown) => {
        if (!lifetime.signal.aborted) {
          setNotifications(false);
          rememberNotifications(false);
          toast.warning(errorMessage(error));
        }
      });
    return () => lifetime.abort();
  }, [available, preferencesReady]);

  useEffect(() => {
    if (!available || !summary) return;
    const lifetime = new AbortController();
    const version = ++historyVersionRef.current;
    void backend()
      .GetReleaseHistory(summary.path, loadedProjectRoot)
      .then((records) => {
        if (!lifetime.signal.aborted && version === historyVersionRef.current)
          setHistory(records ?? []);
      })
      .catch((error: unknown) => {
        if (!lifetime.signal.aborted && version === historyVersionRef.current)
          setHistoryError(errorMessage(error));
      });
    return () => lifetime.abort();
  }, [available, summary, loadedProjectRoot]);

  useEffect(() => {
    if (!available || !window.runtime) return;
    const steamLoginSession = steamLoginSessionRef.current;
    const notifySteamPrompt = (kind: string) => {
      if (steamNotificationRef.current === kind) return;
      steamNotificationRef.current = kind;
      void backend()
        .NotifySteamInput()
        .catch(() => {
          /* Notifications do not interrupt authentication. */
        });
    };
    const unsubscribers = [
      window.runtime.EventsOn("operation:started", (action) => {
        const next: Operation = { action, phase: "running" };
        operationRef.current = next;
        setOperation(next);
        setSteamInputReady(false);
      }),
      window.runtime.EventsOn("release:event", (event) => {
        setProgress((current) => progressEvent(current, event));
        if (event.level === "warning" && !event.target)
          toast.warning(event.message);
        if (
          operationRef.current &&
          (event.target === "SteamCMD login" ||
            event.target === "SteamCMD upload")
        ) {
          steamOutputTailRef.current = (
            steamOutputTailRef.current + event.message
          ).slice(-160);
          const output = steamOutputTailRef.current;
          if (
            /invalid password|login failure|credentials expired/i.test(output)
          )
            steamLoginSessionRef.current.forget();
          const guardPrompt =
            event.kind === "steam-guard-prompt" ||
            /steam guard|authenticator|two.factor|guard code|verification code/i.test(
              output,
            );
          const passwordPrompt =
            event.kind === "steam-password-prompt" ||
            /password\s*:|enter (?:your )?password/i.test(output);
          const mobileApproval =
            /(?:approve|confirm|accept).*(?:steam|mobile|sign.?in|login)|waiting for.*(?:approval|confirmation)/i.test(
              output,
            );
          if (mobileApproval && !guardPrompt && !passwordPrompt)
            notifySteamPrompt("mobile");
          if (guardPrompt || passwordPrompt) {
            steamOutputTailRef.current = "";
            const promptSequence = ++steamPromptSequenceRef.current;
            const rememberedPassword = guardPrompt
              ? null
              : steamLoginSessionRef.current.passwordForPrompt();
            if (rememberedPassword) {
              const current = operationRef.current;
              setSteamInputReady(false);
              setSteamPrompt(null);
              const service = backend();
              void (
                credentialFileRef.current.enabled
                  ? service.SubmitSteamPassword(rememberedPassword)
                  : service.SubmitSteamInput(rememberedPassword)
              ).catch((error: unknown) => {
                if (
                  operationRef.current !== current ||
                  steamPromptSequenceRef.current !== promptSequence ||
                  !current
                )
                  return;
                steamLoginSessionRef.current.forget();
                setSteamInputReady(true);
                setSteamPrompt("password");
                setSteamAuthStatus("required");
                notifySteamPrompt("password");
                toast.error(errorMessage(error));
              });
              return;
            }
            if (!guardPrompt && credentialFileRef.current.enabled) {
              const current = operationRef.current;
              setSteamInputReady(false);
              setSteamPrompt(null);
              const showPrompt = () => {
                if (
                  operationRef.current !== current ||
                  steamPromptSequenceRef.current !== promptSequence ||
                  !current
                )
                  return;
                notifySteamPrompt("password");
                setSteamInputReady(true);
                setSteamPrompt("password");
                setSteamAuthStatus("required");
              };
              void backend()
                .SubmitSavedSteamPassword()
                .then((submitted) => {
                  if (!submitted) showPrompt();
                })
                .catch((error: unknown) => {
                  if (
                    operationRef.current !== current ||
                    steamPromptSequenceRef.current !== promptSequence
                  )
                    return;
                  toast.warning(
                    `Saved Steam password could not be used: ${errorMessage(error)}`,
                  );
                  showPrompt();
                });
              return;
            }
            notifySteamPrompt(guardPrompt ? "guard" : "password");
            setSteamInputReady(true);
            setSteamPrompt(guardPrompt ? "response" : "password");
            setSteamAuthStatus("required");
          }
        }
      }),
      window.runtime.EventsOn("operation:finished", (value) => {
        const cancelled = operationRef.current?.phase === "cancelling";
        const authenticationFailed =
          !value.ok &&
          /invalid password|login failure|no cached credentials|password is required|not logged on|expired|authenticat/i.test(
            value.error ?? "",
          );
        operationRef.current = null;
        setOperation(null);
        setSteamInputReady(false);
        setSteamPrompt(null);
        steamNotificationRef.current = null;
        if (
          value.history &&
          !clearedHistoryIdsRef.current.has(value.history.id)
        ) {
          const record = value.history;
          setHistory((records) =>
            [record, ...records.filter((item) => item.id !== record.id)].slice(
              0,
              50,
            ),
          );
          setRetry(
            record.status === "failed" || record.status === "cancelled"
              ? record
              : null,
          );
          setHistoryError(value.history_warning ?? "");
        }
        if (value.history_warning)
          toast.warning(
            `Local release history could not be saved: ${value.history_warning}`,
          );
        if (
          credentialFileRef.current.enabled &&
          ["login", "upload", "ship", "logout"].includes(value.action)
        ) {
          const version = credentialFileVersionRef.current;
          void backend()
            .GetCredentialFileSettings()
            .then((settings) => {
              if (version !== credentialFileVersionRef.current) return;
              credentialFileRef.current = settings;
              setCredentialFile(settings);
            })
            .catch((error: unknown) => toast.warning(errorMessage(error)));
        }
        setDoctorReport(
          value.action === "doctor" &&
            value.data &&
            typeof value.data === "object" &&
            "issues" in value.data
            ? (value.data as DoctorReport)
            : null,
        );
        steamOutputTailRef.current = "";
        if (
          value.action === "login" ||
          value.action === "upload" ||
          value.action === "ship"
        ) {
          steamLoginSessionRef.current.finish(value.ok, authenticationFailed);
        }
        if (value.action === "logout") steamLoginSessionRef.current.forget();
        if (value.action === "login") {
          setSteamAuthStatus(value.ok ? "verified" : "required");
          if (value.ok) setSteamLoginCount((count) => count + 1);
        }
        if (value.action === "logout" && value.ok)
          setSteamAuthStatus("required");
        if (value.action === "upload" || value.action === "ship") {
          if (authenticationFailed) setSteamAuthStatus("required");
          else if (value.ok) setSteamAuthStatus("verified");
        }
        setProgress((current) =>
          finishProgress(
            current,
            value.ok,
            value.ok ? "" : value.error || "Operation failed.",
            cancelled,
          ),
        );
        if (value.ok && value.action === "logout") setUsername("");
        if (value.ok && value.action === "login") {
          const data = value.data as { username?: string } | undefined;
          if (data?.username) setUsername(data.username);
        }
        const diagnostics =
          value.action === "doctor" || value.action === "plan";
        const structuredResult =
          value.data == null ? "" : JSON.stringify(value.data, null, 2);
        setResult(
          diagnostics
            ? [
                value.ok ? "" : value.error || "Operation failed.",
                structuredResult ||
                  (value.ok ? "Operation completed successfully." : ""),
              ]
                .filter(Boolean)
                .join("\n\n")
            : null,
        );
        if (value.ok) {
          toast.success(
            `${operationLabels[value.action]} completed successfully.`,
          );
        } else {
          const message = value.error || "Operation failed.";
          toast.error(message);
        }
      }),
    ];

    return () => {
      unsubscribers.forEach((unsubscribe) => unsubscribe());
      steamLoginSession.forget();
    };
  }, [available]);

  function changeConfigPath(path: string) {
    setConfigPath(path);
    setProjectRoot(readProjectRoot(path));
  }

  async function chooseConfigPath() {
    if (loadingRef.current || operationRef.current || !available) return;
    try {
      const path = await backend().OpenConfigDialog();
      if (path) changeConfigPath(path);
    } catch (error) {
      toast.error(errorMessage(error));
    }
  }

  function restoreLoadedSelection() {
    if (!summary || loadingRef.current || operationRef.current) return;
    setConfigPath(summary.path);
    setProjectRoot(loadedProjectRoot);
  }

  async function loadConfiguration(selection?: { path: string; root: string }) {
    if (loadingRef.current || operationRef.current || !available) return false;
    const path = (selection?.path ?? configPath).trim();
    const rootOverride = (selection?.root ?? projectRoot).trim();
    if (!path) {
      toast.warning("Choose a configuration file first.");
      return false;
    }
    loadingRef.current = true;
    setLoading(true);
    try {
      const config = await backend().LoadConfig(path, rootOverride);
      applyConfig(config, undefined, rootOverride);
      toast.success(`Loaded ${config.project}`);
      return true;
    } catch (error) {
      const message = errorMessage(error);
      setConfigurationError(message);
      toast.error(message);
      return false;
    } finally {
      loadingRef.current = false;
      setLoading(false);
    }
  }

  function reloadConfiguration() {
    if (!summary) return Promise.resolve(false);
    return loadConfiguration({ path: summary.path, root: loadedProjectRoot });
  }

  async function editConfiguration() {
    if (loadingRef.current || operationRef.current || !available) return;
    const path = configPath.trim();
    if (!path) return;
    loadingRef.current = true;
    setLoading(true);
    try {
      await backend().EditConfig(path);
      setConfigurationNeedsReload(true);
      toast.success("Save your YAML changes, then Reload and validate.");
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      loadingRef.current = false;
      setLoading(false);
    }
  }

  function openRecentConfig(path: string) {
    return loadConfiguration({ path, root: readProjectRoot(path) });
  }

  async function chooseProjectRoot() {
    if (loadingRef.current || operationRef.current || !available) return;
    try {
      const path = await backend().OpenProjectDialog();
      if (path) setProjectRoot(path);
    } catch (error) {
      toast.error(errorMessage(error));
    }
  }

  async function chooseNewConfigPath() {
    if (loadingRef.current || operationRef.current || !available) return "";
    try {
      return await backend().SaveConfigDialog();
    } catch (error) {
      toast.error(errorMessage(error));
      return "";
    }
  }

  async function createConfiguration(request: CreateConfigRequest) {
    if (loadingRef.current || operationRef.current || !available) return false;
    loadingRef.current = true;
    setLoading(true);
    try {
      const config = await backend().CreateConfig(request);
      applyConfig(config, "release");
      toast.success(
        `Created ${config.path}. Run Doctor to check tools, presets, and folders.`,
      );
      return true;
    } catch (error) {
      toast.error(errorMessage(error));
      return false;
    } finally {
      loadingRef.current = false;
      setLoading(false);
    }
  }

  function selectProfile(name: string) {
    if (locked || name === profileName) return;
    const selected = summary?.profiles.find((item) => item.name === name);
    const saved =
      summary && readSelections(summary.path, summary.root).profiles[name];
    const restore = saved?.appId === selected?.app_id ? saved : undefined;
    setProfileName(name);
    const names = selected?.targets.map((target) => target.name) ?? [];
    setTargets(
      restore
        ? restore.targets.filter((target) => names.includes(target))
        : names,
    );
    setPreview(restore?.preview ?? false);
    setSetLive(restore?.branch ?? "");
    setResult(null);
    setDoctorReport(null);
    setRetry(null);
    setProgress(emptyProgress);
  }

  function toggleTarget(name: string, checked: boolean) {
    if (locked) return;
    setTargets((current) =>
      checked
        ? [...new Set([...current, name])]
        : current.filter((target) => target !== name),
    );
  }

  function toggleTargets() {
    if (locked || !profile) return;
    setTargets(
      targets.length === profile.targets.length
        ? []
        : profile.targets.map((target) => target.name),
    );
  }

  async function startOperation(
    action: OperationAction,
    account = username,
    previous?: ReleaseRecord,
  ) {
    const accountOnly = action === "login" || action === "logout";
    if (
      (!accountOnly && (!summary || !profile)) ||
      loadingRef.current ||
      operationRef.current ||
      !available
    )
      return false;
    if (
      !previous &&
      action !== "logout" &&
      action !== "login" &&
      targets.length === 0
    ) {
      toast.warning("Select at least one target.");
      return false;
    }
    if (action === "login" && !account.trim()) {
      toast.warning("Enter a Steam account first.");
      return false;
    }
    if (summary && projectRoot.trim() !== loadedProjectRoot) {
      toast.warning("Load configuration to apply the project folder.");
      return false;
    }
    if (!accountOnly && summary) {
      if (!configurationReady) {
        toast.warning(
          "Reload and validate the configuration before running a release.",
        );
        return false;
      }
      // YAML may also have been edited outside the app. Refresh before executing
      // so users can review a changed destination and target list first.
      loadingRef.current = true;
      setLoading(true);
      try {
        const refreshed = await backend().LoadConfig(
          summary.path,
          loadedProjectRoot,
        );
        if (refreshed.config_hash !== summary.config_hash) {
          applyConfig(refreshed);
          toast.warning(
            "Configuration changed. Review the updated profile and targets, then run the action again.",
          );
          return false;
        }
      } catch (error) {
        const message = errorMessage(error);
        setConfigurationError(message);
        toast.error(message);
        return false;
      } finally {
        loadingRef.current = false;
        setLoading(false);
      }
    }
    setSteamPrompt(null);
    steamNotificationRef.current = null;
    setSteamInputReady(false);
    steamOutputTailRef.current = "";
    if (action === "login" || action === "upload" || action === "ship")
      steamLoginSessionRef.current.begin(account.trim());
    if (action === "logout") steamLoginSessionRef.current.forget();
    const next: Operation = { action, phase: "starting" };
    operationRef.current = next;
    setOperation(next);
    setResult(null);
    setDoctorReport(null);
    setRetry(null);
    const request: OperationRequest = previous
      ? { ...previous.request, action, retry_id: previous.id, notifications }
      : {
          action,
          config_path: summary?.path ?? "",
          project_root: summary ? loadedProjectRoot : "",
          profile: profile?.name ?? "",
          targets,
          username: account.trim(),
          preview,
          set_live: setLive.trim(),
          notifications,
        };
    const releaseProfile = summary?.profiles.find(
      (item) => item.name === request.profile,
    ) ??
      profile ?? { name: "", app_id: 0, targets: [] };
    if (previous) {
      setProfileName(request.profile);
      setTargets(request.targets);
      setPreview(request.preview);
      setSetLive(request.set_live);
    }
    setProgress(
      startProgress(action, releaseProfile, request.targets, {
        preview: request.preview,
        setLive: request.set_live,
      }),
    );
    try {
      await backend().StartOperation(request);
      // Completion is driven by Wails events, not the start request resolving.
      return true;
    } catch (error) {
      operationRef.current = null;
      setOperation(null);
      setProgress(emptyProgress);
      if (previous) setRetry(previous);
      setSteamInputReady(false);
      steamLoginSessionRef.current.discardPendingPassword();
      toast.error(errorMessage(error));
      return false;
    }
  }

  function retryRelease(record: ReleaseRecord) {
    const reason = retryUnavailableReason(record);
    if (reason) {
      toast.error(reason);
      return Promise.resolve(false);
    }
    return startOperation(retryAction(record), record.request.username, record);
  }

  async function clearHistory() {
    if (
      !summary ||
      !canClearHistory ||
      loadingRef.current ||
      clearingHistoryRef.current
    )
      return;
    const recordIds = history.map((record) => record.id);
    const clearingIds = new Set(recordIds);
    clearingHistoryRef.current = true;
    loadingRef.current = true;
    setClearingHistory(true);
    historyVersionRef.current++;
    try {
      await backend().ClearReleaseHistory(
        summary.path,
        loadedProjectRoot,
        recordIds,
      );
      for (const id of recordIds) clearedHistoryIdsRef.current.add(id);
      setHistory((records) =>
        records.filter((record) => !clearingIds.has(record.id)),
      );
      setHistoryError("");
      setRetry((record) =>
        record && !clearingIds.has(record.id) ? record : null,
      );
      toast.success("Release history cleared from this view.");
    } catch (error) {
      toast.error(
        `Release history could not be cleared: ${errorMessage(error)}`,
      );
    } finally {
      loadingRef.current = false;
      clearingHistoryRef.current = false;
      setClearingHistory(false);
    }
  }

  async function configureNotifications(enabled: boolean) {
    if (configuringNotifications) return;
    setConfiguringNotifications(true);
    try {
      const accepted = await backend().ConfigureNotifications(enabled);
      setNotifications(accepted);
      rememberNotifications(accepted);
    } catch (error) {
      toast.warning(errorMessage(error));
    } finally {
      setConfiguringNotifications(false);
    }
  }

  async function configureCredentialFile(enabled: boolean, browse = false) {
    if (locked || configuringCredentialFileRef.current) return;
    configuringCredentialFileRef.current = true;
    setConfiguringCredentialFile(true);
    try {
      let path = credentialFileRef.current.path;
      if (enabled && (browse || !path || !credentialFileRef.current.enabled)) {
        path = await backend().ChooseCredentialFile();
        if (!path) return;
      }
      const settings = await backend().ConfigureCredentialFile(enabled, path);
      credentialFileVersionRef.current++;
      credentialFileRef.current = settings;
      setCredentialFile(settings);
      steamLoginSessionRef.current.forget();
      if (settings.username) {
        setUsername(settings.username);
        steamLoginSessionRef.current.selectAccount(settings.username);
        setSteamAuthStatus("cached");
      }
    } catch (error) {
      toast.warning(
        `Credential file could not be configured: ${errorMessage(error)}`,
      );
    } finally {
      configuringCredentialFileRef.current = false;
      setConfiguringCredentialFile(false);
    }
  }

  async function signIn(account: string) {
    if (
      !account.trim() ||
      loadingRef.current ||
      operationRef.current ||
      !available
    )
      return false;
    setUsername(account.trim());
    return startOperation("login", account);
  }

  async function cancelOperation() {
    const current = operationRef.current;
    if (!current || current.phase === "cancelling") return;
    const next: Operation = { ...current, phase: "cancelling" };
    operationRef.current = next;
    setOperation(next);
    try {
      await backend().CancelOperation();
    } catch (error) {
      if (operationRef.current === next) {
        operationRef.current = current;
        setOperation(current);
      }
      toast.error(errorMessage(error));
    }
  }

  async function submitSteamInput(response: string) {
    if (!operationRef.current || !steamInputReady || !response) return false;
    const current = operationRef.current;
    const promptSequence = steamPromptSequenceRef.current;
    if (steamPrompt === "password")
      steamLoginSessionRef.current.submitPassword(response);
    try {
      if (steamPrompt === "password" && credentialFileRef.current.enabled)
        await backend().SubmitSteamPassword(response);
      else await backend().SubmitSteamInput(response);
      if (
        operationRef.current === current &&
        steamPromptSequenceRef.current === promptSequence
      ) {
        steamNotificationRef.current = null;
        setSteamInputReady(false);
        setSteamPrompt(null);
      }
      return true;
    } catch (error) {
      if (operationRef.current === current)
        steamLoginSessionRef.current.discardPendingPassword();
      toast.error(errorMessage(error));
      return false;
    }
  }

  async function changeSteamCMDAccess(access: "allow" | "deny") {
    await rememberSteamCMDAccess(access);
    setSteamcmdAccess(access);
  }

  function changeRememberSteamLogin(remember: boolean) {
    steamLoginSessionRef.current.remember(remember);
    setRememberSteamLogin(remember);
  }

  return {
    available,
    configPath,
    setConfigPath: changeConfigPath,
    recentConfigs,
    projectRoot,
    setProjectRoot,
    summary,
    configurationError,
    configurationNeedsReload,
    configurationReady,
    profile,
    targets,
    username,
    steamAuthStatus,
    steamLoginCount,
    rememberSteamLogin,
    setRememberSteamLogin: changeRememberSteamLogin,
    steamcmdAccess,
    setSteamcmdAccess: changeSteamCMDAccess,
    steamcmdPath,
    steamcmdAccounts: steamcmdAccess === "allow" ? steamcmdAccounts : [],
    steamPrompt,
    preview,
    setPreview,
    setLive,
    setSetLive,
    loading,
    busy,
    locked,
    preferencesReady,
    operation,
    progress,
    result,
    doctorReport,
    history,
    historyError,
    clearingHistory,
    canClearHistory,
    clearHistory,
    retry,
    retryRelease,
    notifications,
    configuringNotifications,
    credentialFile,
    configuringCredentialFile,
    configureCredentialFile,
    configureNotifications,
    loadConfiguration,
    reloadConfiguration,
    editConfiguration,
    openRecentConfig,
    chooseConfigPath,
    restoreLoadedSelection,
    chooseProjectRoot,
    chooseNewConfigPath,
    createConfiguration,
    selectProfile,
    toggleTarget,
    toggleTargets,
    startOperation,
    signIn,
    submitSteamInput,
    cancelOperation,
    clearProgress: () => setProgress(emptyProgress),
    clearResult: () => {
      setResult(null);
      setDoctorReport(null);
    },
  };
}

export type ReleaseConsole = ReturnType<typeof useReleaseConsole>;
