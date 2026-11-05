import type { DesktopBackend, DesktopRuntime } from "@/lib/types";

declare global {
  interface Window {
    go?: { main: { DesktopApp: DesktopBackend } };
    runtime?: DesktopRuntime;
  }
}
