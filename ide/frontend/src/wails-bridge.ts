import type * as GeneratedApp from "../wailsjs/go/main/App";
import { api } from "./state";
import type { WailsBackendApp } from "./types";

/**
 * Compile-time contract linking TypeScript callers (WailsBackendApp in types.ts)
 * to Wails-generated bindings (wailsjs/go/main/App.d.ts).
 * Updating Go *App without re-running `wails generate module` or updating types.ts
 * will break `tsc --noEmit` here.
 */
type GeneratedAppSignatures = {
  OpenProject: typeof GeneratedApp.OpenProject;
  CloseProject: typeof GeneratedApp.CloseProject;
  AppVersion: typeof GeneratedApp.AppVersion;
  CheckForUpdates: typeof GeneratedApp.CheckForUpdates;
  DownloadUpdate: typeof GeneratedApp.DownloadUpdate;
  GetDiff: typeof GeneratedApp.GetDiff;
  GetWorkspaceGitRepos: typeof GeneratedApp.GetWorkspaceGitRepos;
  GitBlameFile: typeof GeneratedApp.GitBlameFile;
  GitFileHistory: typeof GeneratedApp.GitFileHistory;
  ListConflictedFiles: typeof GeneratedApp.ListConflictedFiles;
  ResolveConflictBlock: typeof GeneratedApp.ResolveConflictBlock;
  MarkConflictResolved: typeof GeneratedApp.MarkConflictResolved;
  ResidentStatus: typeof GeneratedApp.ResidentStatus;
  ResidentSetIsolatedSandbox: typeof GeneratedApp.ResidentSetIsolatedSandbox;
  ResidentSetExperimentalUnisolated: typeof GeneratedApp.ResidentSetExperimentalUnisolated;
};

type VerifyMethodNamesPresentInManualInterface = {
  [K in keyof GeneratedAppSignatures]: NonNullable<WailsBackendApp[K]>;
};

export type WailsBindingContractCheck = VerifyMethodNamesPresentInManualInterface;

export function getWailsAppBridge(): WailsBackendApp | undefined {
  return api();
}
