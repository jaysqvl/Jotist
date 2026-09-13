import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { TRANSCRIPTION_PRESETS, presetAlreadyAdded, type TranscriptionPreset } from "@/features/transcription/hooks/profilePresets";

export function TranscriptionPresetsDialog({ open, onOpenChange, onSelect, onAdd, existingNames }: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onSelect: (preset: TranscriptionPreset) => void;
    onAdd: (presets: TranscriptionPreset[]) => Promise<{ added: number; skipped: number }>;
    existingNames: string[];
}) {
    const [selected, setSelected] = useState<Set<string>>(new Set());
    const [adding, setAdding] = useState(false);
    const [message, setMessage] = useState("");
    const [error, setError] = useState("");
    useEffect(() => {
        if (open) { setSelected(new Set()); setMessage(""); setError(""); }
    }, [open]);
    const availableSelection = TRANSCRIPTION_PRESETS.filter((preset) => selected.has(preset.id) && !presetAlreadyAdded(preset, existingNames));
    const add = async () => {
        setAdding(true); setError(""); setMessage("");
        try {
            const result = await onAdd(availableSelection);
            setSelected(new Set());
            setMessage(`Added ${result.added} profile${result.added === 1 ? "" : "s"}.${result.skipped ? ` Skipped ${result.skipped} already added.` : ""}`);
        } catch (error) {
            setError(error instanceof Error ? error.message : "Could not add presets.");
        } finally { setAdding(false); }
    };
    return (
        <Dialog open={open} onOpenChange={(value) => { if (!adding) onOpenChange(value); }}>
            <DialogContent className="flex max-h-[85vh] flex-col bg-[var(--bg-card)] sm:max-w-3xl">
                <DialogHeader>
                    <DialogTitle>Quick Add Presets</DialogTitle>
                    <DialogDescription>Select presets to add as saved profiles, or edit one before adding. Existing names are skipped.</DialogDescription>
                </DialogHeader>
                <p className="text-xs leading-5 text-[var(--text-secondary)]">Hardware reference: Ryzen 7 5700G, 64 GB installed RAM with 32 GB available for models, and RTX 3060 with 12 GB VRAM. Memory use depends on recording length and settings; these presets do not guarantee fit. All presets inherit your saved context and Hugging Face access.</p>
                <div className="min-h-0 space-y-6 overflow-y-auto pr-1">
                    {(["existing", "recommended"] as const).map((origin) => (
                        <section key={origin} aria-labelledby={`presets-${origin}`}>
                            <h3 id={`presets-${origin}`} className="mb-3 text-sm font-semibold text-[var(--text-primary)]">{origin === "existing" ? "Your existing configurations" : "Recommended starting points"}</h3>
                            {origin === "recommended" && <p className="mb-3 text-xs text-[var(--text-secondary)]">Batch size 1, explicit devices, CPU Float32 or GPU Float16 where supported.</p>}
                            <div className="grid gap-3 sm:grid-cols-2">
                                {TRANSCRIPTION_PRESETS.filter((preset) => preset.origin === origin).map((preset) => {
                                    const alreadyAdded = presetAlreadyAdded(preset, existingNames);
                                    return <article key={preset.id} className="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-main)] p-4">
                                        <label className="flex items-start gap-3 text-sm font-medium text-[var(--text-primary)]">
                                            <input type="checkbox" checked={selected.has(preset.id) && !alreadyAdded} disabled={alreadyAdded || adding}
                                                onChange={(event) => setSelected((previous) => { const next = new Set(previous); if (event.target.checked) next.add(preset.id); else next.delete(preset.id); return next; })}
                                                className="mt-1 accent-brand-500" />
                                            <span className="min-w-0 break-words">{preset.name}</span>
                                        </label>
                                        {alreadyAdded && <p className="mt-2 text-xs font-medium text-[var(--success-solid)]">Already added</p>}
                                        <p className="mt-2 text-xs leading-5 text-[var(--text-secondary)]">{preset.description}</p>
                                        <p className="mt-2 text-xs leading-5 text-[var(--text-tertiary)]">{preset.notes}</p>
                                        <Button variant="outline" size="sm" className="mt-3" disabled={adding} onClick={() => onSelect(preset)}>Edit before adding</Button>
                                    </article>;
                                })}
                            </div>
                        </section>
                    ))}
                </div>
                {message && <p role="status" className="text-sm text-[var(--success-solid)]">{message}</p>}
                {error && <p role="alert" className="text-sm text-[var(--error)]">{error}</p>}
                <DialogFooter>
                    <Button variant="outline" disabled={adding} onClick={() => onOpenChange(false)}>Close</Button>
                    <Button disabled={adding || availableSelection.length === 0} onClick={() => void add()}>{adding ? "Adding…" : `Add selected (${availableSelection.length})`}</Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
