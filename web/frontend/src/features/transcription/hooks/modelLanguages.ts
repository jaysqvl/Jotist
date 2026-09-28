import { effectiveCheckpoint, type TranscriptionModelCapability } from "./modelCapabilities.ts";

export const EUROPEAN_COMPARISON_LANGUAGES = ["de", "fr", "it", "es", "pt", "nl"] as const;
export const MEASURED_LANGUAGES = [
    { value: "de", label: "German" }, { value: "fr", label: "French" },
    { value: "it", label: "Italian" }, { value: "es", label: "Spanish" },
    { value: "pt", label: "Portuguese" }, { value: "nl", label: "Dutch" },
    { value: "hi", label: "Hindi" },
] as const;

export interface ModelLanguageComparison {
    model: string;
    publisher_coverage: string;
    publisher_languages?: string[];
    publisher_source: string;
    notes?: string;
    retrieved?: string;
    benchmark_source?: string;
    benchmark_notes?: string;
    wer: Record<string, { mean_wer: number; datasets: Record<string, number> }>;
}

// Fixed adapters and old saved aliases still refer to their exact checkpoint.
// An unknown explicit repository never inherits a family's language scores.
const FIXED_ALIASES: Record<string, string[]> = {
    nvidia_parakeet: ["parakeet", "parakeet-tdt-0.6b-v3"],
    nvidia_canary: ["canary", "canary-1b-v2"],
    nvidia_canary_qwen: ["canary-qwen"],
    mistral_voxtral: ["voxtral", "mistralai/Voxtral-mini"],
    vibevoice_bitnet: ["vibevoice-bitnet"],
};

export function modelLanguageComparison(model?: TranscriptionModelCapability, variant?: string): ModelLanguageComparison | undefined {
    if (!model) return undefined;
    const exact = effectiveCheckpoint(model, variant);
    const checkpoint = !variant || variant === model.model_id || FIXED_ALIASES[model.model_family]?.includes(variant)
        ? model.metadata?.language_comparison_checkpoint || exact : exact;
    try {
        const rows: unknown = JSON.parse(model.metadata?.language_comparisons || "[]");
        const row = Array.isArray(rows) ? rows.find((entry) => entry && entry.model === checkpoint) : undefined;
        if (!row || typeof row.publisher_coverage !== "string" || typeof row.publisher_source !== "string" || !row.publisher_source.startsWith("https://")) return undefined;
        const wer: ModelLanguageComparison["wer"] = {};
        if (row.wer && typeof row.wer === "object") {
            for (const [language, value] of Object.entries(row.wer)) {
                if (!value || typeof value !== "object") continue;
                const result = value as Record<string, unknown>;
                if (typeof result.mean_wer !== "number" || !Number.isFinite(result.mean_wer) || result.mean_wer < 0) continue;
                const datasets: Record<string, number> = {};
                if (result.datasets && typeof result.datasets === "object") {
                    for (const [dataset, score] of Object.entries(result.datasets)) {
                        if (typeof score === "number" && Number.isFinite(score) && score >= 0) datasets[dataset] = score;
                    }
                }
                wer[language] = { mean_wer: result.mean_wer, datasets };
            }
        }
        const text = (key: string) => typeof row[key] === "string" ? row[key] as string : undefined;
        return {
            model: checkpoint, publisher_coverage: row.publisher_coverage, publisher_source: row.publisher_source,
            publisher_languages: Array.isArray(row.publisher_languages) ? row.publisher_languages.filter((language: unknown): language is string => typeof language === "string") : undefined,
            notes: text("notes"), retrieved: text("retrieved"), benchmark_notes: text("benchmark_notes"),
            benchmark_source: text("benchmark_source")?.startsWith("https://") ? text("benchmark_source") : undefined,
            wer,
        };
    } catch { return undefined; }
}

export function multilingualScore(comparison: ModelLanguageComparison | undefined, languages: readonly string[]) {
    if (!comparison || languages.length === 0) return undefined;
    const scores = languages.map((language) => comparison.wer[language]?.mean_wer);
    if (scores.some((score) => score === undefined)) return undefined;
    return scores.reduce<number>((total, score) => total + score!, 0) / scores.length;
}

export function offeredModelLanguages(model: TranscriptionModelCapability, variant: string) {
    if (model.model_family === "whisper" && variant.endsWith(".en")) return ["en"];
    return (model.supported_languages || []).filter((language) => language !== "auto");
}

export function offeredLanguageLabel(model: TranscriptionModelCapability, variant: string) {
    const languages = offeredModelLanguages(model, variant);
    if (languages.includes("*")) return "Not enumerated";
    if (languages.length === 1 && languages[0] === "en") return "English only";
    return languages.length ? `${languages.length} languages` : "Not recorded";
}
