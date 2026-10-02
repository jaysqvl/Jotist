import { useId, useMemo, useState } from "react";
import { Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { FormField } from "./FormHelpers";
import type { TranscriptionModelCapability } from "@/features/transcription/hooks/modelCapabilities";
import {
    buildProfileChoices, filterAndSortProfiles, hasLanguageWER, profileComparisonNote,
    profileLanguageLabel, profileLanguageOptions, profileWER, PROFILE_SORT_OPTIONS,
    type ProfilePickerPreferences, type ProfileSort, type SavedTranscriptionProfile,
} from "@/features/transcription/hooks/profilePicker";

interface ProfilePickerProps {
    profiles: SavedTranscriptionProfile[];
    models: TranscriptionModelCapability[];
    preferences: ProfilePickerPreferences;
    onPreferencesChange: (patch: Partial<ProfilePickerPreferences>) => void;
    onValueChange: (value: string) => void;
    defaultProfileId?: string;
    catalogLoading: boolean;
    catalogError: boolean;
    disabled?: boolean;
}

export function ProfilePicker({ profiles, models, preferences, onPreferencesChange, onValueChange, defaultProfileId, catalogLoading, catalogError, disabled }: ProfilePickerProps) {
    const id = useId();
    const [query, setQuery] = useState("");
    const choices = useMemo(() => buildProfileChoices(profiles, models), [profiles, models]);
    const visible = useMemo(() => filterAndSortProfiles(choices, preferences, query), [choices, preferences, query]);
    const selected = choices.find((choice) => choice.profile.id === preferences.profileId);
    const languageOptions = profileLanguageOptions(choices, preferences.language);
    const werLabel = preferences.sort === "language_wer" ? `${profileLanguageLabel(preferences.language)} WER`
        : preferences.sort === "conversational" ? "Conv. WER" : "AMI WER";
    const changeLanguage = (language: string) => onPreferencesChange({ language,
        sort: preferences.sort === "language_wer" && !hasLanguageWER(language) ? "meeting" : preferences.sort });

    return <FormField label="Saved profile" htmlFor={id}>
            <div className="overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-card)]">
                <Command shouldFilter={false} defaultValue={preferences.profileId} label="Choose a saved profile" className="h-auto min-h-0 flex-1 bg-transparent text-[var(--text-primary)] [&_[data-slot=command-input-wrapper]]:h-12 [&_[data-slot=command-input-wrapper]]:shrink-0 [&_[data-slot=command-input-wrapper]]:border-[var(--border-subtle)]">
                    <CommandInput id={id} aria-label="Search profiles" placeholder="Search profiles, models or speakers…" value={query} onValueChange={setQuery} disabled={disabled} />
                    <div className="grid shrink-0 grid-cols-2 gap-2 border-b border-[var(--border-subtle)] px-3 py-2">
                        <label className="min-w-0 text-[11px] text-[var(--text-secondary)]">Run language
                            <select aria-label="Filter profiles by run language" value={preferences.language} onChange={(event) => changeLanguage(event.target.value)} onKeyDown={(event) => event.stopPropagation()} disabled={disabled}
                                className="mt-1 h-11 w-full min-w-0 cursor-pointer rounded-lg bg-[var(--bg-main)] px-2 text-xs text-[var(--text-primary)] [color-scheme:light] dark:[color-scheme:dark]">
                                {languageOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                            </select>
                        </label>
                        <label className="min-w-0 text-[11px] text-[var(--text-secondary)]">Sort by
                            <select aria-label="Sort profiles" value={preferences.sort} onChange={(event) => onPreferencesChange({ sort: event.target.value as ProfileSort })} onKeyDown={(event) => event.stopPropagation()} disabled={disabled}
                                className="mt-1 h-11 w-full min-w-0 cursor-pointer rounded-lg bg-[var(--bg-main)] px-2 text-xs text-[var(--text-primary)] [color-scheme:light] dark:[color-scheme:dark]">
                                {PROFILE_SORT_OPTIONS.map((option) => <option key={option.value} value={option.value} disabled={option.value === "language_wer" && !hasLanguageWER(preferences.language)}>{option.label}</option>)}
                            </select>
                        </label>
                    </div>
                    <CommandList aria-label="Saved profile choices" className="min-h-0 max-h-[min(20rem,35dvh)] flex-1 p-1">
                        <CommandEmpty className="px-3 py-6 text-center text-sm text-[var(--text-secondary)]">No matching profiles.
                            <Button variant="ghost" size="sm" className="mt-2" disabled={disabled} onClick={() => { setQuery(""); changeLanguage("all"); }}>Clear filters</Button>
                        </CommandEmpty>
                        {visible.map((choice) => {
                            const wer = profileWER(choice, preferences.sort, preferences.language);
                            return <CommandItem key={choice.profile.id} value={choice.profile.id} disabled={disabled} onSelect={() => onValueChange(choice.profile.id)}
                                className="min-h-11 items-start gap-2 rounded-lg px-3 py-3 data-[selected=true]:bg-[var(--brand-light)] data-[selected=true]:text-[var(--text-primary)]">
                                <div className="min-w-0 flex-1">
                                    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                                        <span className="break-words text-sm font-medium leading-5">{choice.profile.name}</span>
                                        {choice.profile.id === defaultProfileId && <span className="rounded bg-[var(--success-translucent)] px-1.5 text-[10px] leading-5 text-[var(--success-solid)]">Default</span>}
                                    </div>
                                    <p className="mt-0.5 break-words text-xs leading-4 text-[var(--text-secondary)]">{choice.subtitle}</p>
                                    <dl className="mt-2 grid grid-cols-3 gap-2 text-xs tabular-nums">
                                        {[{ label: werLabel, value: wer === undefined ? "—" : `${wer.toFixed(2)}%` },
                                            { label: "ASR VRAM est.", value: choice.model.location === "cloud" ? "Cloud" : choice.model.gpuSupported === false ? "CPU only" : choice.gpuVRAM ? `${choice.gpuVRAM} GB` : "—" },
                                            { label: "ASR RAM est.", value: choice.model.location === "cloud" ? "Cloud" : choice.cpuRAM ? `${choice.cpuRAM} GB` : "—" },
                                        ].map((metric) => <div key={metric.label} className="min-w-0"><dt className="text-[10px] leading-4 text-[var(--text-secondary)]">{metric.label}</dt><dd className="break-words font-medium leading-5">{metric.value}</dd></div>)}
                                    </dl>
                                </div>
                                <Check aria-hidden="true" className={`mt-0.5 size-4 shrink-0 text-[var(--brand-solid)] ${choice.profile.id === preferences.profileId ? "opacity-100" : "opacity-0"}`} />
                            </CommandItem>;
                        })}
                    </CommandList>
                </Command>
                <div className="shrink-0 space-y-1 border-t border-[var(--border-subtle)] px-3 py-2 text-[11px] leading-4 text-[var(--text-secondary)]">
                    <p>{visible.length} of {profiles.length} profiles{catalogLoading ? " · Loading model details…" : catalogError ? " · Model comparisons unavailable" : ""}</p>
                    {selected && <p className="break-words text-[var(--text-secondary)]">Selected: <span className="font-medium">{selected.profile.name}</span>{!visible.includes(selected) && " · outside these filters"}</p>}
                    <p>{profileComparisonNote(preferences.sort, preferences.language)}</p>
                </div>
            </div>
    </FormField>;
}
