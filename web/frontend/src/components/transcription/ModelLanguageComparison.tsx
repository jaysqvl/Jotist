import { useState } from "react";
import { SelectField } from "./FormHelpers";
import { EUROPEAN_COMPARISON_LANGUAGES, MEASURED_LANGUAGES, modelLanguageComparison, multilingualScore, offeredLanguageLabel, offeredModelLanguages } from "@/features/transcription/hooks/modelLanguages";
import type { TranscriptionModelCapability, TranscriptionModelChoice } from "@/features/transcription/hooks/modelCapabilities";

export function ModelLanguageDetails({ model, variant }: { model: TranscriptionModelCapability; variant: string }) {
    const comparison = modelLanguageComparison(model, variant);
    if (!comparison) return null;
    const score = multilingualScore(comparison, EUROPEAN_COMPARISON_LANGUAGES);
    const offered = offeredModelLanguages(model, variant);
    return <div className="space-y-2 border-t border-[var(--border-subtle)] pt-3 text-xs leading-5 text-[var(--text-secondary)]">
        <dl className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <div><dt>Publisher recognition coverage</dt><dd className="font-medium text-[var(--text-primary)]">{comparison.publisher_coverage}</dd></div>
            <div><dt>Languages offered by Jotist</dt><dd className="font-medium text-[var(--text-primary)]">{offeredLanguageLabel(model, variant)}</dd></div>
            <div><dt>Six European languages · macro WER</dt><dd className="font-medium text-[var(--text-primary)]">{score === undefined ? "No comparable result" : `${score.toFixed(2)}%`}</dd></div>
            <div><dt>Hindi · Monsoon WER</dt><dd className="font-medium text-[var(--text-primary)]">{comparison.wer.hi ? `${comparison.wer.hi.mean_wer.toFixed(2)}%` : "No comparable result"}</dd></div>
        </dl>
        {comparison.notes && <p>{comparison.notes}</p>}
        <details>
            <summary className="cursor-pointer font-medium">Language lists, evaluation method and sources</summary>
            <div className="mt-2 space-y-2">
                <p>Jotist: {offered.includes("*") ? "No enumerated restriction; individual languages still need qualification." : offered.join(", ") || "Not recorded."}</p>
                {comparison.publisher_languages && <p>Publisher: {comparison.publisher_languages.join(", ")}</p>}
                <p><a href={comparison.publisher_source} target="_blank" rel="noreferrer" className="underline underline-offset-2">Publisher coverage</a> · retrieved {comparison.retrieved || "date not recorded"}.</p>
                {comparison.benchmark_notes && <p>{comparison.benchmark_notes}</p>}
                {comparison.benchmark_source && <p><a href={comparison.benchmark_source} target="_blank" rel="noreferrer" className="underline underline-offset-2">Pinned multilingual results</a>. Six-language comparison: German, French, Italian, Spanish, Portuguese and Dutch. Coverage is recognition capability, not verification of word alignment, speakers or mixed-language meetings.</p>}
            </div>
        </details>
    </div>;
}

export function ModelLanguageTable({ choices }: { choices: TranscriptionModelChoice[] }) {
    const [selection, setSelection] = useState("europe");
    const languages = selection === "europe" ? [...EUROPEAN_COMPARISON_LANGUAGES]
        : selection === "seven" ? MEASURED_LANGUAGES.map((language) => language.value) : [selection];
    const rows = choices.flatMap((choice) => {
        const comparison = choice.location === "local" ? modelLanguageComparison(choice.capability, choice.model) : undefined;
        return comparison && choice.capability ? [{ choice, comparison, capability: choice.capability, score: multilingualScore(comparison, languages) }] : [];
    }).filter((row, index, all) => all.findIndex((candidate) => candidate.comparison.model === row.comparison.model) === index)
        .sort((a, b) => (a.score ?? Infinity) - (b.score ?? Infinity) || a.choice.label.localeCompare(b.choice.label));
    if (rows.length === 0) return null;
    return <details className="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-main)] p-4 text-xs leading-5 text-[var(--text-secondary)]">
        <summary className="cursor-pointer text-sm font-medium text-[var(--text-primary)]">Compare models by language</summary>
        <div className="mt-3 space-y-3">
            <SelectField label="Published recognition comparison" value={selection} onValueChange={setSelection} options={[
                { value: "europe", label: "Six European languages · macro WER" },
                { value: "seven", label: "Those six + Hindi · macro WER" },
                ...MEASURED_LANGUAGES,
            ]} />
            <p>Lower WER is better. A mean requires results for every selected language. {selection === "europe" ? "German, French, Italian, Spanish, Portuguese and Dutch, using read-speech datasets." : selection === "hi" ? "Hindi uses the conversational Monsoon protocol." : selection === "seven" ? "This mean combines European read speech with a distinct Hindi conversational test." : "Language mean over available FLEURS, MCV and MLS datasets."} These are published results, not a Jotist runtime test.</p>
            <div className="max-h-80 overflow-auto rounded-lg border border-[var(--border-subtle)]">
                <table className="w-full min-w-[36rem] text-left">
                    <thead className="sticky top-0 bg-[var(--bg-card)] text-[var(--text-primary)]"><tr>
                        <th scope="col" className="p-2">Exact model</th><th scope="col" className="p-2">Publisher coverage</th><th scope="col" className="p-2">Jotist offers</th><th scope="col" className="p-2">WER / measured coverage</th>
                    </tr></thead>
                    <tbody>{rows.map(({ choice, comparison, capability, score }) => <tr key={comparison.model} className="border-t border-[var(--border-subtle)]">
                        <th scope="row" className="p-2 font-medium text-[var(--text-primary)]">{choice.label.replace(/^Local · /, "")}</th>
                        <td className="p-2">{comparison.publisher_coverage}</td>
                        <td className="p-2">{offeredLanguageLabel(capability, choice.model)}</td>
                        <td className="p-2 tabular-nums">{score === undefined ? "No comparable result" : `${score.toFixed(2)}%`} · {languages.filter((language) => comparison.wer[language]).length}/{languages.length}</td>
                    </tr>)}</tbody>
                </table>
            </div>
            <p>Publisher coverage can exceed the current language choices in Jotist. Missing results do not establish that a language is unsupported. English AMI meeting results and memory estimates remain separate above; choose All models to select another model.</p>
            {rows.find((row) => row.comparison.benchmark_source) && <p><a href={rows.find((row) => row.comparison.benchmark_source)!.comparison.benchmark_source} target="_blank" rel="noreferrer" className="underline underline-offset-2">Pinned source data</a> · retrieved {rows[0].comparison.retrieved}.</p>}
        </div>
    </details>;
}
