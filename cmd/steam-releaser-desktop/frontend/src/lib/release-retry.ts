import type { OperationAction, ReleaseRecord } from "@/lib/types";

export function hasUploadCheckpoint(record: ReleaseRecord): boolean {
  return /^[0-9a-f]{64}$/.test(record.upload_fingerprint ?? "");
}

export function retryAction(record: ReleaseRecord): OperationAction {
  if (
    record.request.action === "ship" &&
    record.build_ready &&
    hasUploadCheckpoint(record)
  )
    return "upload";
  return record.request.action;
}

export function retryUnavailableReason(record: ReleaseRecord): string {
  if (record.status !== "failed" && record.status !== "cancelled") return "";
  if (
    record.custom_upload_script &&
    (record.request.action === "ship" || record.request.action === "upload")
  )
    return "Custom Steam scripts cannot be verified for automatic retry. Review the script and start a new operation.";
  if (hasUploadCheckpoint(record)) return "";
  if (record.request.action === "upload")
    return "No artifact checkpoint was saved for this attempt. Start a new upload.";
  if (record.request.action === "ship" && record.build_ready)
    return "No artifact checkpoint was saved for this attempt. Start a new build and deploy.";
  return "";
}
