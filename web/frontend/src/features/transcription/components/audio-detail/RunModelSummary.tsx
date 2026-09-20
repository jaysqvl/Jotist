import type { Transcript } from "@/features/transcription/hooks/useAudioDetail";
import { executionModelSummary, type ExecutionSettings } from "@/features/transcription/hooks/executionPresentation";

export function RunModelSummary({ parameters, transcript, detailed = false }: {
    parameters: ExecutionSettings;
    transcript?: Transcript | null;
    detailed?: boolean;
}) {
    return <dl aria-label="Run models" className="space-y-2 text-sm">
        {executionModelSummary(parameters, transcript).map((row) => <div key={row.label} className="grid gap-x-3 gap-y-0.5 sm:grid-cols-[100px_minmax(0,1fr)]">
            <dt className="text-[var(--text-secondary)]">{row.label}</dt>
            <dd className="min-w-0">
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
                    <span className="font-medium text-[var(--text-primary)]">{row.model}{!row.recorded && !row.disabled && row.checkpoint && <span className="ml-1 font-normal text-xs text-[var(--text-secondary)]">(requested model)</span>}</span>
                    {!row.disabled && <span className="text-xs text-[var(--text-secondary)]">{row.runtime}</span>}
                </div>
                {detailed && !row.disabled && <p className="mt-1 break-all text-xs text-[var(--text-secondary)]">
                    {row.recorded ? "Recorded model ID" : "Requested model ID"}: <code>{row.checkpoint || "Not recorded"}</code>
                    {!row.recorded && " · Exact model used was not recorded in this run’s output."}
                </p>}
            </dd>
        </div>)}
    </dl>;
}
