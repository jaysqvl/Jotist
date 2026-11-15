import { useState, type FormEvent } from "react";
import { Loader2, Save, Users } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useSaveSpeakerMappings, useSpeakerRenameMappings } from "@/features/transcription/hooks/useTranscriptionSpeakers";

interface SpeakerRenameDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    transcriptionId: string;
    initialSpeakers: string[];
}

export default function SpeakerRenameDialog({ open, onOpenChange, transcriptionId, initialSpeakers }: SpeakerRenameDialogProps) {
    const save = useSaveSpeakerMappings(transcriptionId);

    function closeDialog() {
        save.reset();
        onOpenChange(false);
    }

    return (
        <Dialog open={open} onOpenChange={(nextOpen) => {
            if (nextOpen) onOpenChange(true);
            else if (!save.isPending) closeDialog();
        }}>
            <DialogContent
                className="sm:max-w-md"
                showCloseButton={!save.isPending}
                onEscapeKeyDown={(event) => { if (save.isPending) event.preventDefault(); }}
                onInteractOutside={(event) => { if (save.isPending) event.preventDefault(); }}
            >
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        <Users className="h-5 w-5" />
                        Rename Speakers
                    </DialogTitle>
                    <DialogDescription>Speaker names apply to this recording.</DialogDescription>
                </DialogHeader>
                {open && (
                    <SpeakerRenameEditor
                        key={transcriptionId}
                        transcriptionId={transcriptionId}
                        initialSpeakers={initialSpeakers}
                        save={save}
                        onClose={closeDialog}
                    />
                )}
            </DialogContent>
        </Dialog>
    );
}

interface SpeakerRenameFormProps {
    initialSpeakers: string[];
    save: ReturnType<typeof useSaveSpeakerMappings>;
    onClose: () => void;
}

function SpeakerRenameEditor({ transcriptionId, initialSpeakers, save, onClose }: SpeakerRenameFormProps & { transcriptionId: string }) {
    const { data: savedNames, isError, isFetching, refetch } = useSpeakerRenameMappings(transcriptionId);

    if (savedNames) {
        return <SpeakerRenameForm initialSpeakers={initialSpeakers} savedNames={savedNames} save={save} onClose={onClose} />;
    }

    if (isError && !isFetching) {
        return (
            <>
                <p role="alert" className="text-sm text-[var(--error-solid)]">Could not load speaker names.</p>
                <DialogFooter>
                    <Button variant="outline" onClick={onClose}>Cancel</Button>
                    <Button onClick={() => void refetch()}>Try again</Button>
                </DialogFooter>
            </>
        );
    }

    return (
        <p role="status" className="flex items-center justify-center gap-2 py-6 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" />
            Loading speakers…
        </p>
    );
}

function SpeakerRenameForm({ initialSpeakers, savedNames, save, onClose }: SpeakerRenameFormProps & { savedNames: Record<string, string> }) {
    // Snapshot the names for this edit session. Polling and playback can
    // re-render the recording while the user is typing.
    const [draft, setDraft] = useState(() => ({
        ...Object.fromEntries(initialSpeakers.map((speaker) => [speaker, speaker])),
        ...savedNames,
    }));
    const speakers = Object.keys(draft).sort();

    function handleSubmit(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        if (save.isPending || speakers.length === 0) return;
        save.mutate(draft, { onSuccess: onClose });
    }

    return (
        <form className="space-y-4" onSubmit={handleSubmit}>
            {save.isError && <p role="alert" className="text-sm text-[var(--error-solid)]">{save.error.message}</p>}
            {speakers.length > 0 ? (
                <div className="max-h-60 space-y-3 overflow-y-auto">
                    {speakers.map((speaker) => (
                        <div key={speaker} className="space-y-1">
                            <Label htmlFor={`speaker-${speaker}`} className="text-xs text-muted-foreground">{speaker}</Label>
                            <Input
                                id={`speaker-${speaker}`}
                                value={draft[speaker]}
                                onChange={(event) => {
                                    const value = event.target.value;
                                    setDraft((current) => ({ ...current, [speaker]: value }));
                                }}
                                disabled={save.isPending}
                                placeholder={`Enter custom name for ${speaker}`}
                            />
                        </div>
                    ))}
                </div>
            ) : (
                <p className="py-4 text-center text-sm text-muted-foreground">No speakers found in this transcript.</p>
            )}
            <DialogFooter>
                <Button type="button" variant="outline" disabled={save.isPending} onClick={onClose}>Cancel</Button>
                <Button type="submit" disabled={save.isPending || speakers.length === 0} className="min-w-[100px]">
                    {save.isPending ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
                    {save.isPending ? "Saving…" : "Save"}
                </Button>
            </DialogFooter>
        </form>
    );
}
