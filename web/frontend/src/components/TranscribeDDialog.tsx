import { useState, useEffect, useCallback } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Loader2, SlidersHorizontal } from "lucide-react";
import type { WhisperXParams } from "@/features/transcription/types";
import { useAuth } from "@/features/auth/hooks/useAuth";
import { sortProfilesByName } from "@/lib/profiles";
import { CheckpointReuseField } from "./transcription/RecoveryPolicyFields";
import { recoveryModeLabel, type RunSubmissionOptions } from "@/features/transcription/hooks/recoveryPolicy";
import { DEFAULT_EXECUTION_POLICY, executionPolicySummary, previewCheckpointReuse, type ExecutionPolicy } from "@/features/transcription/hooks/executionPolicy";
import { normalizeModelCapabilities, type TranscriptionModelCapability } from "@/features/transcription/hooks/modelCapabilities";
import { profilePickerStorageKey, readProfilePickerPreferences, restoreProfileSelection, saveProfilePickerPreferences, type ProfilePickerPreferences, type SavedTranscriptionProfile } from "@/features/transcription/hooks/profilePicker";
import { ProfilePicker } from "./transcription/ProfilePicker";

interface TranscribeDDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onStartTranscription: (params: WhisperXParams, profileId?: string, profileName?: string, options?: RunSubmissionOptions) => void;
  loading?: boolean;
  title?: string;
  description?: string;
  actionLabel?: string;
  loadingLabel?: string;
  onAdvanced?: () => void;
}

export function TranscribeDDialog({
  open,
  onOpenChange,
  onStartTranscription,
  loading = false,
  title,
  description,
  actionLabel = "Start Transcription",
  loadingLabel = "Starting...",
  onAdvanced,
}: TranscribeDDialogProps) {
  const { getAuthHeaders, token } = useAuth();
  const storageKey = profilePickerStorageKey(token);
  const [preferences, setPreferences] = useState(() => readProfilePickerPreferences(storageKey));
  const [profiles, setProfiles] = useState<SavedTranscriptionProfile[]>([]);
  const [profilesLoading, setProfilesLoading] = useState(false);
  const [profilesError, setProfilesError] = useState(false);
  const [reload, setReload] = useState(0);
  const [defaultProfile, setDefaultProfile] = useState<SavedTranscriptionProfile | null>(null);
  const [models, setModels] = useState<TranscriptionModelCapability[]>([]);
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [catalogError, setCatalogError] = useState(false);
  const [reuseOverride, setReuseOverride] = useState<boolean | undefined>();
  const [sharedPolicy, setSharedPolicy] = useState<ExecutionPolicy | null>(null);
  const selectedProfile = profiles.find((profile) => profile.id === preferences.profileId);
  const updatePreferences = useCallback((patch: Partial<ProfilePickerPreferences>) => {
    setPreferences((previous) => {
      const next = { ...previous, ...patch };
      saveProfilePickerPreferences(storageKey, next);
      return next;
    });
  }, [storageKey]);

  // Fetch profiles when dialog opens
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    const remembered = readProfilePickerPreferences(storageKey);
    setPreferences(remembered);
    setReuseOverride(undefined);
    setProfiles([]);
    setDefaultProfile(null);
    setProfilesLoading(true);
    setProfilesError(false);
    setModels([]);
    setCatalogLoading(true);
    setCatalogError(false);
    const load = async () => {
      const options = { headers: getAuthHeaders(), signal: controller.signal };
      const [profileResult, defaultResult, catalogResult] = await Promise.allSettled([
        fetch("/api/v1/profiles", options).then(async (response) => {
          if (!response.ok) throw new Error("Profiles unavailable");
          return sortProfilesByName(await response.json() as SavedTranscriptionProfile[]);
        }),
        fetch("/api/v1/user/default-profile", options).then(async (response) => response.ok ? await response.json() as SavedTranscriptionProfile : null),
        fetch("/api/v1/transcription/models", options).then(async (response) => {
          if (!response.ok) throw new Error("Model comparisons unavailable");
          const data = await response.json();
          return normalizeModelCapabilities(data.models ?? {});
        }),
      ]);
      if (controller.signal.aborted) return;
      if (profileResult.status === "fulfilled") {
        const loadedProfiles = profileResult.value;
        const loadedDefault = defaultResult.status === "fulfilled" ? defaultResult.value : null;
        const next = { ...remembered, profileId: restoreProfileSelection(loadedProfiles, remembered.profileId, loadedDefault?.id) };
        setProfiles(loadedProfiles);
        setDefaultProfile(loadedDefault);
        setPreferences(next);
        saveProfilePickerPreferences(storageKey, next);
      } else setProfilesError(true);
      if (catalogResult.status === "fulfilled") setModels(catalogResult.value);
      else setCatalogError(true);
      setProfilesLoading(false);
      setCatalogLoading(false);
    };
    void load();
    return () => controller.abort();
  }, [open, getAuthHeaders, storageKey, reload]);

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    setSharedPolicy(null);
    void fetch("/api/v1/user/settings", { headers: getAuthHeaders(), signal: controller.signal })
      .then(async (response) => { if (response.ok) { const data = await response.json(); if (!controller.signal.aborted) setSharedPolicy(data.execution_policy ?? DEFAULT_EXECUTION_POLICY); } })
      .catch(() => {});
    return () => controller.abort();
  }, [open, getAuthHeaders]);

  const handleStartTranscription = () => {
    if (selectedProfile) {
      onStartTranscription(selectedProfile.parameters, selectedProfile.id, selectedProfile.name, { reuse_checkpoints: reuseOverride });
    }
  };

  const handleProfileChange = (value: string) => {
    updatePreferences({ profileId: value });
    setReuseOverride(undefined);
  };



  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[calc(100dvh-2rem)] flex-col sm:max-w-xl glass-card rounded-[var(--radius-card)] p-0 gap-0 overflow-hidden border border-[var(--border-subtle)] shadow-[var(--shadow-float)]">
        <DialogHeader className="p-6 pb-2">
          <DialogTitle className="text-xl font-bold tracking-tight text-[var(--text-primary)]">
            {title || "Transcribe with Profile"}
          </DialogTitle>
          <DialogDescription className="text-[var(--text-secondary)] text-sm mt-1.5">
            {description || "Choose a saved profile to start transcription with your preferred settings."}
          </DialogDescription>
        </DialogHeader>



        <div className="min-h-0 space-y-4 overflow-y-auto px-6 py-2">
          <div className="space-y-2">
            {profilesLoading ? (
              <div className="flex items-center space-x-2 p-3 bg-[var(--bg-main)]/50 rounded-[var(--radius-btn)] border border-[var(--border-subtle)]">
                <Loader2 className="h-4 w-4 animate-spin text-[var(--text-tertiary)]" />
                <span className="text-sm text-[var(--text-secondary)]">Loading profiles...</span>
              </div>
            ) : profilesError ? (
              <div role="alert" className="rounded-xl border border-[var(--border-subtle)] p-3 text-sm text-[var(--text-secondary)]">
                Could not load saved profiles. <Button size="sm" variant="ghost" onClick={() => setReload((value) => value + 1)}>Try again</Button>
              </div>
            ) : profiles.length === 0 ? (
              <div className="p-3 bg-[var(--bg-main)]/50 rounded-[var(--radius-btn)] border border-[var(--border-subtle)]">
                <span className="text-sm text-[var(--text-secondary)]">No profiles available</span>
              </div>
            ) : (
              <ProfilePicker profiles={profiles} models={models} preferences={preferences} onPreferencesChange={updatePreferences}
                onValueChange={handleProfileChange} defaultProfileId={defaultProfile?.id}
                catalogLoading={catalogLoading} catalogError={catalogError} disabled={loading} />
            )}
          </div>
          {selectedProfile && <div className="space-y-3">
            <p className="text-xs text-[var(--text-secondary)]">{selectedProfile.parameters.execution_policy_source === "global"
              ? `Shared execution defaults${sharedPolicy ? ` · ${executionPolicySummary(sharedPolicy)}` : " · resolved when queued"}`
              : `Saved recovery override · ${recoveryModeLabel(selectedProfile.parameters)}`}</p>
            <details className="rounded-xl border border-[var(--border-subtle)]">
              <summary className="cursor-pointer px-3 py-3 text-sm font-medium">Run overrides</summary>
              <div className="px-3 pb-3"><CheckpointReuseField value={reuseOverride ?? previewCheckpointReuse(selectedProfile.parameters, sharedPolicy ?? undefined)} onChange={setReuseOverride} /></div>
            </details>
          </div>}
        </div>

        <DialogFooter className="p-6 pt-2 gap-3">
          <Button
            variant="ghost"
            onClick={() => onOpenChange(false)}
            className="rounded-[var(--radius-btn)] text-[var(--text-secondary)] hover:bg-[var(--secondary)] hover:text-[var(--text-primary)]"
          >
            Cancel
          </Button>
          {onAdvanced && (
            <Button
              variant="outline"
              onClick={onAdvanced}
              disabled={loading}
              className="rounded-[var(--radius-btn)] border-[var(--border-subtle)] bg-[var(--bg-main)] text-[var(--text-secondary)] hover:bg-[var(--bg-card)] hover:text-[var(--text-primary)]"
            >
              <SlidersHorizontal className="mr-2 h-4 w-4" />
              Advanced
            </Button>
          )}
          <Button
            onClick={handleStartTranscription}
            disabled={loading || !selectedProfile || profilesLoading}
            className="min-w-[140px] !bg-[image:var(--brand-gradient)] hover:!opacity-90 !text-white border-none shadow-lg shadow-brand-500/20"
          >
            {loading ? (
              <>
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                {loadingLabel}
              </>
            ) : (
              actionLabel
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog >
  );
}
