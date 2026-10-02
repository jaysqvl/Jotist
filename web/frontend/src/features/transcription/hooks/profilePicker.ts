import type { WhisperXParams } from "../types.ts";
import {
    gpuMemoryEstimate, modelChoiceValue, modelMemoryEstimate, transcriptionModelChoices,
    transcriptionModelLabel, transcriptionPrecision, type ModelSort,
    type TranscriptionModelCapability, type TranscriptionModelChoice,
} from "./modelCapabilities.ts";
import { MEASURED_LANGUAGES, modelLanguageComparison } from "./modelLanguages.ts";
import { diarizationModelLabel } from "./executionPresentation.ts";

export interface SavedTranscriptionProfile {
    id: string;
    name: string;
    description?: string;
    is_default: boolean;
    parameters: WhisperXParams;
    created_at: string;
    updated_at: string;
}

export type ProfileSort = ModelSort | "name" | "updated" | "language_wer";
export interface ProfilePickerPreferences {
    sort: ProfileSort;
    language: string;
    profileId: string;
}
export const DEFAULT_PROFILE_PICKER_PREFERENCES: ProfilePickerPreferences = { sort: "meeting", language: "all", profileId: "" };
export const PROFILE_SORT_OPTIONS: { value: ProfileSort; label: string }[] = [
    { value: "meeting", label: "Best meeting WER" },
    { value: "conversational", label: "Best conversation WER" },
    { value: "language_wer", label: "Best language WER" },
    { value: "recommended", label: "English meeting guidance" },
    { value: "gpu_memory", label: "Lowest GPU memory" },
    { value: "memory", label: "Lowest CPU memory" },
    { value: "name", label: "Name A–Z" },
    { value: "updated", label: "Recently updated" },
];

export function hasLanguageWER(language: string) {
    return MEASURED_LANGUAGES.some((entry) => entry.value === language);
}

export function profileLanguageLabel(language: string) {
    if (language === "auto") return "Auto-detect";
    try { return new Intl.DisplayNames(["en"], { type: "language" }).of(language) || language; }
    catch { return language; }
}

export function profilePickerStorageKey(token: string | null): string | undefined {
    // User ID only scopes display preferences; this is never an auth check.
    try {
        const payload = JSON.parse(atob(token!.split(".")[1].replace(/-/g, "+").replace(/_/g, "/")));
        if (!Number.isSafeInteger(payload.user_id) || payload.user_id <= 0) return undefined;
        return `jotist.profile-picker.v1:${payload.user_id}`;
    } catch { return undefined; }
}

export function parseProfilePickerPreferences(raw: string | null): ProfilePickerPreferences {
    try {
        const value: unknown = JSON.parse(raw || "null");
        if (!value || typeof value !== "object" || Array.isArray(value)) return { ...DEFAULT_PROFILE_PICKER_PREFERENCES };
        const saved = value as Record<string, unknown>;
        const language = typeof saved.language === "string" && /^(all|other|auto|[a-z]{2,3}(?:-[a-z0-9]{2,8})*)$/.test(saved.language)
            ? saved.language : "all";
        const sort = PROFILE_SORT_OPTIONS.find((option) => option.value === saved.sort)?.value ?? "meeting";
        return { sort: sort === "language_wer" && !hasLanguageWER(language) ? "meeting" : sort, language,
            profileId: typeof saved.profileId === "string" ? saved.profileId : "" };
    } catch { return { ...DEFAULT_PROFILE_PICKER_PREFERENCES }; }
}

type PreferenceStorage = Pick<Storage, "getItem" | "setItem">;
export function readProfilePickerPreferences(key?: string, storage?: PreferenceStorage): ProfilePickerPreferences {
    if (!key) return { ...DEFAULT_PROFILE_PICKER_PREFERENCES };
    try { return parseProfilePickerPreferences((storage ?? window.localStorage).getItem(key)); }
    catch { return { ...DEFAULT_PROFILE_PICKER_PREFERENCES }; }
}

export function saveProfilePickerPreferences(key: string | undefined, preferences: ProfilePickerPreferences, storage?: PreferenceStorage) {
    if (!key) return;
    try { (storage ?? window.localStorage).setItem(key, JSON.stringify(parseProfilePickerPreferences(JSON.stringify(preferences)))); }
    catch { /* Browsing and selecting still work when browser storage is unavailable. */ }
}

export function restoreProfileSelection(profiles: SavedTranscriptionProfile[], rememberedId: string, defaultId?: string) {
    return [rememberedId, defaultId, profiles.find((profile) => profile.is_default)?.id, profiles[0]?.id]
        .find((id) => profiles.some((profile) => profile.id === id)) || "";
}

export interface ProfileChoice {
    profile: SavedTranscriptionProfile;
    model: TranscriptionModelChoice;
    language: string;
    gpuVRAM?: string;
    cpuRAM?: string;
    subtitle: string;
}

export function buildProfileChoices(profiles: SavedTranscriptionProfile[], models: TranscriptionModelCapability[]): ProfileChoice[] {
    const catalog = transcriptionModelChoices(models);
    return profiles.map((profile) => {
        const params = profile.parameters;
        const key = modelChoiceValue(params.model_family, params.model);
        const model = catalog.find((choice) => choice.value === key)
            ?? transcriptionModelChoices(models, params).find((choice) => choice.value === key)!;
        const language = params.language?.trim().toLowerCase() || "auto";
        const memory = model.capability ? modelMemoryEstimate(model.capability, params.model) : undefined;
        const device = model.location === "cloud" ? "Cloud" : params.device === "cuda" ? "GPU" : params.device === "cpu" ? "CPU" : "Auto device";
        const speakers = !params.diarize ? "Speakers off" : params.diarize_model === "native" ? "Native speakers"
            : diarizationModelLabel(params.diarization_checkpoint || params.diarize_model);
        return { profile, model, language, cpuRAM: model.cpuRAM,
            gpuVRAM: memory && model.location !== "cloud" && memory.gpuSupported !== false ? gpuMemoryEstimate(memory, transcriptionPrecision(params)).value : undefined,
            subtitle: `${transcriptionModelLabel(params.model_family, params.model)} · ${profileLanguageLabel(language)} · ${device} · ${speakers}` };
    });
}

export function profileLanguageOptions(choices: ProfileChoice[], selectedLanguage: string) {
    const languages = new Set(choices.map((choice) => choice.language).filter((language) => language !== "auto" && language !== "en"));
    if (!["all", "other", "auto", "en"].includes(selectedLanguage)) languages.add(selectedLanguage);
    return [
        { value: "all", label: "All languages" }, { value: "en", label: "English" },
        { value: "other", label: "Other languages" }, { value: "auto", label: "Auto-detect" },
        ...[...languages].map((value) => ({ value, label: profileLanguageLabel(value) })).sort((a, b) => a.label.localeCompare(b.label)),
    ];
}

export function profileWER(choice: ProfileChoice, sort: ProfileSort, language: string) {
    if (sort === "language_wer") return modelLanguageComparison(choice.model.capability, choice.profile.parameters.model)?.wer[language]?.mean_wer;
    return sort === "conversational" ? choice.model.conversationalWER : choice.model.meetingWER;
}

function memoryUpperBound(range?: string) {
    const numbers = range?.match(/\d+(?:\.\d+)?/g)?.map(Number);
    return numbers?.length ? Math.max(...numbers) : Infinity;
}

export function filterAndSortProfiles(choices: ProfileChoice[], preferences: ProfilePickerPreferences, query: string): ProfileChoice[] {
    const words = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
    const visible = choices.filter((choice) => {
        const languageMatches = preferences.language === "all" || (preferences.language === "other"
            ? !["auto", "en"].includes(choice.language) : choice.language === preferences.language);
        const text = `${choice.profile.name} ${choice.profile.description || ""} ${choice.subtitle} ${choice.profile.parameters.model} ${choice.profile.parameters.model_family}`.toLocaleLowerCase();
        return languageMatches && words.every((word) => text.includes(word));
    });
    const score = (choice: ProfileChoice) => {
        if (preferences.sort === "name") return 0;
        if (preferences.sort === "updated") { const time = Date.parse(choice.profile.updated_at); return Number.isFinite(time) ? -time : Infinity; }
        if (preferences.sort === "recommended") return choice.model.recommendationRank ?? Infinity;
        if (preferences.sort === "gpu_memory") return memoryUpperBound(choice.gpuVRAM);
        if (preferences.sort === "memory") return memoryUpperBound(choice.cpuRAM);
        return profileWER(choice, preferences.sort, preferences.language) ?? Infinity;
    };
    const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });
    return visible.sort((a, b) => score(a) - score(b) || collator.compare(a.profile.name, b.profile.name) || a.profile.id.localeCompare(b.profile.id));
}

export function profileComparisonNote(sort: ProfileSort, language: string) {
    if (sort === "language_wer") return `Published ${profileLanguageLabel(language)} WER · lower is better. Memory estimates cover transcription only.`;
    return "Published English WER · lower is better. Memory estimates cover transcription only. — = no comparable result.";
}
