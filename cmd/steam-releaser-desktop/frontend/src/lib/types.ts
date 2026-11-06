// JSON contracts exposed by cmd/steam-releaser-desktop/app.go.
export interface ConfigSummary {
  path: string;
  config_hash: string;
  project: string;
  root: string;
  engine: string;
  default_profile: string;
  profiles: ProfileSummary[];
  username: string;
}

export interface ProfileSummary {
  name: string;
  app_id: number;
  targets: TargetSummary[];
}

export interface TargetSummary {
  name: string;
  platform: string;
  architecture: string;
  depot_id: number;
  output: string;
  build_setting?: string;
  build_args?: string[] | null;
  build_env?: Record<string, string> | null;
}

export interface CreateConfigRequest {
  path: string;
  project_root?: string;
  name: string;
  engine: string;
  app_id: number;
  targets?: CreateTargetRequest[];
}

export interface FileMapping {
  local_path: string;
  depot_path: string;
  recursive?: boolean | null;
}

export interface CreateTargetRequest {
  name: string;
  platform: string;
  depot_id: number;
  path: string;
  build_setting: string;
  mappings?: FileMapping[];
  exclusions?: string[];
}

export interface ProjectDetection {
  engine: string;
  name: string;
  presets: { name: string; platform: string; output: string }[];
  profiles: string[];
  existing: string;
}

export interface DiscoveredBuild {
  path: string;
  build: ImportedBuild;
}

export interface ImportedBuild {
  app_id: number;
  description: string;
  content_root: string;
  depots: {
    depot_id: number;
    platform: string;
    content: string;
    mappings: FileMapping[];
    exclusions: string[];
  }[];
}

export interface CommandPlan {
  label: string;
  program: string;
  args: string[] | null;
  working_dir: string;
  environment?: Record<string, string>;
  warnings?: string[];
}

export interface ReleasePlan {
  project: string;
  profile: string;
  engine: string;
  parallelism: number;
  prepare?: CommandPlan[] | null;
  targets: {
    name: string;
    output: string;
    content: string;
    depot_id: number;
    command?: CommandPlan | null;
  }[];
  upload: {
    app_id: number;
    depot_ids: number[];
    build_script: string;
    preview: boolean;
    set_live?: string;
  };
}

export type OperationAction =
  "doctor" | "plan" | "build" | "upload" | "ship" | "login" | "logout";

export interface OperationRequest {
  action: OperationAction;
  config_path: string;
  project_root: string;
  profile: string;
  targets: string[];
  username: string;
  preview: boolean;
  set_live: string;
  notifications?: boolean;
  retry_id?: string;
}

export interface DoctorIssue {
  severity: "info" | "warning" | "error";
  code: string;
  message: string;
  target?: string;
}

export interface DoctorReport {
  project: string;
  profile?: string;
  issues: DoctorIssue[] | null;
}

export interface ReleaseRecord {
  id: string;
  request: OperationRequest;
  project: string;
  root: string;
  app_id: number;
  started_at: string;
  finished_at?: string;
  duration_ms: number;
  status: "running" | "complete" | "failed" | "cancelled";
  error?: string;
  config_hash: string;
  commit?: string;
  version?: string;
  dirty: boolean;
  build_id?: string;
  completed_targets: string[];
  build_ready: boolean;
  custom_upload_script?: boolean;
  target_fingerprints?: Record<string, string>;
  upload_fingerprint?: string;
  logs: string;
}

export interface ReleaseEvent {
  level: "trace" | "info" | "success" | "warning" | "error";
  message: string;
  target?: string;
  kind?:
    | "build-start"
    | "build-complete"
    | "build-failed"
    | "build-prebuilt"
    | "build-reused"
    | "steam-password-prompt"
    | "steam-guard-prompt"
    | "build-ready";
}

export interface OperationResult {
  action: OperationAction;
  ok: boolean;
  error?: string;
  data?: unknown;
  history?: ReleaseRecord;
  history_warning?: string;
}

export interface CredentialFileSettings {
  enabled: boolean;
  path: string;
  username: string;
  has_credentials: boolean;
  problem?: string;
}

export interface DesktopBackend {
  GetPreferences(
    legacy: Record<string, string>,
  ): Promise<Record<string, string>>;
  SavePreferences(values: Record<string, string>): Promise<void>;
  GetCredentialFileSettings(): Promise<CredentialFileSettings>;
  ChooseCredentialFile(): Promise<string>;
  ConfigureCredentialFile(
    enabled: boolean,
    path: string,
  ): Promise<CredentialFileSettings>;
  OpenConfigDialog(): Promise<string>;
  OpenProjectDialog(): Promise<string>;
  SaveConfigDialog(): Promise<string>;
  LoadConfig(path: string, projectRoot: string): Promise<ConfigSummary>;
  CreateConfig(request: CreateConfigRequest): Promise<ConfigSummary>;
  PreviewConfig(request: CreateConfigRequest): Promise<string>;
  DetectProject(root: string): Promise<ProjectDetection>;
  OpenVDFDialog(): Promise<string>;
  ImportVDF(path: string): Promise<ImportedBuild>;
  DiscoverBuildScripts(root: string): Promise<DiscoveredBuild[]>;
  SteamAppName(appId: number): Promise<string>;
  EditConfig(path: string): Promise<void>;
  CachedUsername(): Promise<string>;
  SteamCMDLocation(): Promise<string>;
  SteamCMDCachedAccounts(): Promise<string[] | null>;
  StartOperation(request: OperationRequest): Promise<void>;
  SubmitSteamInput(response: string): Promise<void>;
  SubmitSteamPassword(password: string): Promise<void>;
  SubmitSavedSteamPassword(): Promise<boolean>;
  CancelOperation(): Promise<void>;
  CheckReadiness(request: OperationRequest): Promise<DoctorReport>;
  GetReleaseHistory(path: string, root: string): Promise<ReleaseRecord[]>;
  ClearReleaseHistory(
    path: string,
    root: string,
    recordIds: string[],
  ): Promise<void>;
  ConfigureNotifications(enabled: boolean): Promise<boolean>;
  NotifySteamInput(): Promise<void>;
}

export interface DesktopEvents {
  "operation:started": OperationAction;
  "release:event": ReleaseEvent;
  "operation:finished": OperationResult;
}

export interface DesktopRuntime {
  EventsOn<K extends keyof DesktopEvents>(
    name: K,
    callback: (data: DesktopEvents[K]) => void,
  ): () => void;
}

export const operationLabels: Record<OperationAction, string> = {
  doctor: "Doctor",
  plan: "Plan",
  build: "Build",
  upload: "Upload",
  ship: "Build & Deploy",
  login: "Sign in to Steam",
  logout: "Clear cached Steam account",
};
