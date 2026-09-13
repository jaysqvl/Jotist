import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { TranscriptionContextFields } from "@/components/transcription/TranscriptionContextFields";
import { useAuth } from "@/features/auth/hooks/useAuth";
import { Loader2 } from "lucide-react";

export function TranscriptionContextSettings() {
    const { getAuthHeaders } = useAuth();
    const [context, setContext] = useState("");
    const [terms, setTerms] = useState("");
    const [saved, setSaved] = useState({ context: "", terms: "" });
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState("");
    const [success, setSuccess] = useState("");
    const [loaded, setLoaded] = useState(false);

    useEffect(() => {
        const controller = new AbortController();
        const load = async () => {
            try {
                const response = await fetch("/api/v1/user/settings", { headers: getAuthHeaders(), signal: controller.signal });
                if (!response.ok) throw new Error("Could not load transcription defaults.");
                const settings = await response.json();
                const next = { context: settings.transcription_context ?? "", terms: settings.transcription_context_terms ?? "" };
                setContext(next.context);
                setTerms(next.terms);
                setSaved(next);
                setLoaded(true);
            } catch (error) {
                if (!controller.signal.aborted) setError(error instanceof Error ? error.message : "Could not load transcription defaults.");
            } finally {
                if (!controller.signal.aborted) setLoading(false);
            }
        };
        void load();
        return () => controller.abort();
    }, [getAuthHeaders]);

    const save = async () => {
        setSaving(true);
        setError("");
        setSuccess("");
        try {
            const response = await fetch("/api/v1/user/settings", {
                method: "PUT",
                headers: { "Content-Type": "application/json", ...getAuthHeaders() },
                body: JSON.stringify({ transcription_context: context, transcription_context_terms: terms }),
            });
            const result = await response.json();
            if (!response.ok) throw new Error(result.error || "Could not save transcription defaults.");
            const next = { context: result.transcription_context ?? "", terms: result.transcription_context_terms ?? "" };
            setContext(next.context);
            setTerms(next.terms);
            setSaved(next);
            setSuccess("Saved. New transcriptions that inherit these defaults will use them.");
        } catch (error) {
            setError(error instanceof Error ? error.message : "Could not save transcription defaults.");
        } finally {
            setSaving(false);
        }
    };

    return (
        <section className="space-y-5 rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-main)]/50 p-4 shadow-sm sm:p-6" aria-labelledby="transcription-context-title">
            <div>
                <h3 id="transcription-context-title" className="text-lg font-medium text-[var(--text-primary)]">Transcription context</h3>
                <p className="mt-1 text-sm leading-6 text-[var(--text-secondary)]">Help supported models recognize your topics and terminology. Save defaults here, then override them in a profile or individual run.</p>
            </div>
            {loading ? <p className="text-sm text-[var(--text-secondary)]">Loading defaults…</p> : (
                <TranscriptionContextFields
                    context={context} terms={terms}
                    onContextChange={(value) => { setContext(value ?? ""); setSuccess(""); }}
                    onTermsChange={(value) => { setTerms(value ?? ""); setSuccess(""); }}
                    disabled={!loaded || saving} idPrefix="saved-transcription"
                />
            )}
            <p className="text-xs leading-5 text-[var(--text-secondary)]">Each model uses the context or vocabulary it supports. These defaults apply to future transcriptions; existing transcripts stay as they are.</p>
            {error && <p role="alert" className="text-sm text-[var(--error)]">{error}</p>}
            {success && <p role="status" className="text-sm text-[var(--success-solid)]">{success}</p>}
            <Button onClick={() => void save()} disabled={loading || saving || !loaded || (context === saved.context && terms === saved.terms)} className="w-full sm:w-auto">
                {saving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                {saving ? "Saving…" : "Save context defaults"}
            </Button>
        </section>
    );
}
