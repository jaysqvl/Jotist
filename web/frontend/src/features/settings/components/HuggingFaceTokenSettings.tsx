import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAuth } from "@/features/auth/hooks/useAuth";

export function HuggingFaceTokenSettings() {
    const { getAuthHeaders } = useAuth();
    const [token, setToken] = useState("");
    const [hasToken, setHasToken] = useState(false);
    const [loaded, setLoaded] = useState(false);
    const [saving, setSaving] = useState(false);
    const [message, setMessage] = useState("");
    const [error, setError] = useState("");
    useEffect(() => {
        const controller = new AbortController();
        void fetch("/api/v1/user/settings", { headers: getAuthHeaders(), signal: controller.signal }).then(async (response) => {
            if (!response.ok) throw new Error("Could not load token settings.");
            const settings = await response.json();
            setHasToken(settings.has_hf_token === true);
            setLoaded(true);
        }).catch((error) => {
            if (!controller.signal.aborted) setError(error instanceof Error ? error.message : "Could not load token settings.");
        });
        return () => controller.abort();
    }, [getAuthHeaders]);
    const save = async (replacement: string) => {
        setSaving(true); setError(""); setMessage("");
        try {
            const response = await fetch("/api/v1/user/settings", {
                method: "PUT", headers: { "Content-Type": "application/json", ...getAuthHeaders() }, body: JSON.stringify({ hf_token: replacement }),
            });
            const settings = await response.json();
            if (!response.ok) throw new Error(settings.error || "Could not save the token.");
            setHasToken(settings.has_hf_token === true); setToken("");
            setMessage(replacement ? "Default token saved." : "Default token removed.");
        } catch (error) {
            setError(error instanceof Error ? error.message : "Could not save the token.");
        } finally { setSaving(false); }
    };
    return (
        <section className="space-y-4 rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-main)]/50 p-4 sm:p-6" aria-labelledby="hf-token-settings-title">
            <div>
                <h3 id="hf-token-settings-title" className="text-lg font-medium text-[var(--text-primary)]">Hugging Face token</h3>
                <p className="mt-1 text-sm leading-6 text-[var(--text-secondary)]">Save a default for gated model downloads. Profiles and individual runs can override it. This token is used for model downloads, not hosted transcription.</p>
            </div>
            <div className="space-y-2">
                <Label htmlFor="saved-hf-token">{hasToken ? "Replace saved token" : "Default token"}</Label>
                <Input id="saved-hf-token" type="password" autoComplete="new-password" placeholder={hasToken ? "Token saved · enter a replacement" : "hf_..."} value={token}
                    disabled={!loaded || saving} onChange={(event) => { setToken(event.target.value); setMessage(""); }} />
                <p className="text-xs text-[var(--text-secondary)]">{!loaded ? "Loading token status…" : hasToken ? "A token is saved. Its value is never shown here." : "No default token saved."}</p>
            </div>
            <div className="flex flex-wrap gap-3">
                <Button disabled={!loaded || saving || !token.trim()} onClick={() => void save(token.trim())}>{saving ? "Saving…" : "Save default token"}</Button>
                {hasToken && <Button variant="outline" disabled={saving} onClick={() => void save("")}>Remove saved token</Button>}
            </div>
            {message && <p role="status" className="text-sm text-[var(--success-solid)]">{message}</p>}
            {error && <p role="alert" className="text-sm text-[var(--error)]">{error}</p>}
        </section>
    );
}
