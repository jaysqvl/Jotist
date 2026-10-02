import { useId, useState } from "react";
import { Check, ChevronsUpDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { FormField } from "./FormHelpers";
import type { ModelMetric } from "@/features/transcription/hooks/modelComparisonMetrics";

export interface ModelPickerOption {
    value: string;
    label: string;
    note?: string;
    keywords?: string;
    metrics?: ModelMetric[];
    disabled?: boolean;
}

interface ModelPickerProps {
    label: string;
    value: string;
    options: ModelPickerOption[];
    onValueChange: (value: string) => void;
    sort?: { value: string; onChange: (value: string) => void; options: { value: string; label: string }[] };
    footer?: string;
}

/** Compare the complete catalog where the model is chosen, with keyboard search. */
export function ModelPicker({ label, value, options, onValueChange, sort, footer }: ModelPickerProps) {
    const id = useId();
    const [open, setOpen] = useState(false);
    const [query, setQuery] = useState("");
    const selected = options.find((option) => option.value === value);
    const words = query.toLocaleLowerCase().trim().split(/\s+/).filter(Boolean);
    const visible = options.filter((option) => {
        const search = `${option.label} ${option.note ?? ""} ${option.keywords ?? ""}`.toLocaleLowerCase();
        return words.every((word) => search.includes(word));
    });

    return <FormField label={label} htmlFor={id}>
        <Popover modal open={open} onOpenChange={(next) => { setOpen(next); if (!next) setQuery(""); }}>
            <PopoverTrigger asChild>
                <Button id={id} type="button" variant="outline" role="combobox" aria-label={label} aria-expanded={open}
                    className="h-auto min-h-11 w-full justify-between gap-3 rounded-xl border-[var(--border-subtle)] bg-[var(--bg-main)] px-3 py-2.5 text-left font-normal text-[var(--text-primary)] shadow-none">
                    <span className="min-w-0 flex-1 whitespace-normal break-words leading-5">{selected?.label ?? "Choose a model"}</span>
                    <ChevronsUpDown className="size-4 shrink-0 text-[var(--text-tertiary)]" />
                </Button>
            </PopoverTrigger>
            <PopoverContent align="start" collisionPadding={16} className="flex max-h-[var(--radix-popover-content-available-height)] w-[var(--radix-popover-trigger-width)] max-w-[calc(100vw-2rem)] flex-col overflow-hidden rounded-xl border-[var(--border-subtle)] bg-[var(--bg-card)] p-0 shadow-xl">
                <Command shouldFilter={false} label={`Search ${label.toLowerCase()}`} className="h-auto min-h-0 flex-1 bg-transparent text-[var(--text-primary)] [&_[data-slot=command-input-wrapper]]:h-12 [&_[data-slot=command-input-wrapper]]:shrink-0 [&_[data-slot=command-input-wrapper]]:border-[var(--border-subtle)]">
                    <CommandInput aria-label={`Search ${label.toLowerCase()}`} placeholder="Search models or checkpoints…" value={query} onValueChange={setQuery} />
                    {sort && <div className="flex shrink-0 items-center justify-between gap-3 border-b border-[var(--border-subtle)] px-3 py-1.5 text-xs text-[var(--text-secondary)]">
                        <span className="shrink-0">{visible.length} models</span>
                        <label className="flex min-w-0 items-center gap-2">Sort
                            <select aria-label="Sort models" value={sort.value} onChange={(event) => sort.onChange(event.target.value)} onKeyDown={(event) => event.stopPropagation()}
                                className="h-11 min-w-0 max-w-full cursor-pointer rounded-lg bg-[var(--bg-main)] px-2 text-xs text-[var(--text-primary)] [color-scheme:light] dark:[color-scheme:dark]">
                                {sort.options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                            </select>
                        </label>
                    </div>}
                    <CommandList className="min-h-0 max-h-[min(24rem,45dvh)] flex-1 p-1" aria-label={`${label} choices`}>
                        <CommandEmpty>No matching models.</CommandEmpty>
                        {visible.map((option) => <CommandItem key={option.value} value={option.value} disabled={option.disabled}
                            onSelect={() => { onValueChange(option.value); setOpen(false); setQuery(""); }}
                            className="min-h-11 items-start gap-2 rounded-lg px-3 py-3 data-[selected=true]:bg-[var(--brand-light)] data-[selected=true]:text-[var(--text-primary)]">
                            <div className="min-w-0 flex-1 sm:grid sm:grid-cols-[minmax(0,1fr)_15rem] sm:items-center sm:gap-4">
                                <div className="min-w-0">
                                    <span className="block whitespace-normal break-words text-sm font-medium leading-5">{option.label}</span>
                                    {option.note && <span className="mt-0.5 block whitespace-normal break-words text-xs leading-4 text-[var(--text-tertiary)]">{option.note}</span>}
                                </div>
                                {!!option.metrics?.length && <dl className="mt-2 grid grid-cols-3 gap-2 sm:mt-0 sm:text-right">
                                    {option.metrics.map((metric) => <div key={metric.label} className="min-w-0">
                                        <dt className="text-[10px] leading-4 text-[var(--text-tertiary)]">{metric.label}</dt>
                                        <dd className="whitespace-normal text-xs font-medium leading-5 tabular-nums">{metric.value}</dd>
                                    </div>)}
                                </dl>}
                            </div>
                            <Check aria-hidden="true" className={`mt-0.5 size-4 shrink-0 text-[var(--brand-solid)] ${option.value === value ? "opacity-100" : "opacity-0"}`} />
                        </CommandItem>)}
                    </CommandList>
                </Command>
                {footer && <p className="shrink-0 border-t border-[var(--border-subtle)] px-3 py-2 text-[11px] leading-4 text-[var(--text-tertiary)]">{footer}</p>}
            </PopoverContent>
        </Popover>
    </FormField>;
}

export function ModelMetricSummary({ metrics, note }: { metrics: ModelMetric[]; note?: string }) {
    return <div className="space-y-2">
        <dl className={`grid grid-cols-2 gap-x-4 gap-y-3 rounded-xl bg-[var(--bg-main)] p-3 ${metrics.length === 3 ? "sm:grid-cols-3" : "sm:grid-cols-4"}`}>
            {metrics.map((metric) => <div key={metric.label} className="min-w-0">
                <dt className="text-xs leading-4 text-[var(--text-tertiary)]">{metric.label}</dt>
                <dd className="mt-1 break-words text-sm font-medium tabular-nums text-[var(--text-primary)]">{metric.value}</dd>
            </div>)}
        </dl>
        {note && <p className="text-xs leading-5 text-[var(--text-tertiary)]">{note}</p>}
    </div>;
}
