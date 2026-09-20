import { Textarea } from "@/components/ui/textarea";
import { FormField, SwitchField, inputClassName } from "./FormHelpers";

export const CONTEXT_EXAMPLE = "Software engineering meeting about our API, deployment pipeline, and database migrations.";
export const TERMS_EXAMPLE = "Kubernetes\nPostgreSQL\nTypeScript\ngRPC\nJotist";

interface ContextFieldsProps {
    context: string | null | undefined;
    terms: string | null | undefined;
    onContextChange: (value: string | null) => void;
    onTermsChange: (value: string | null) => void;
    defaultContext?: string;
    defaultTerms?: string;
    allowInheritance?: boolean;
    proseSupported?: boolean;
    termsSupported?: boolean;
    disabled?: boolean;
    idPrefix: string;
}

export function TranscriptionContextFields({
    context, terms, onContextChange, onTermsChange,
    defaultContext = "", defaultTerms = "", allowInheritance = false,
    proseSupported = true, termsSupported = true, disabled = false, idPrefix,
}: ContextFieldsProps) {
    const fields = [
        { key: "context", label: "Meeting context", value: context, defaultValue: defaultContext, supported: proseSupported, change: onContextChange, placeholder: CONTEXT_EXAMPLE, rows: 3, maxLength: 4000,
            help: "Briefly describe the topic and background. Use factual context, not instructions to summarize or rewrite speech. For Whisper, try Vocabulary first: paragraph prompts can make it omit speech. Compare a short recording before saving a broad default." },
        { key: "terms", label: "Vocabulary", value: terms, defaultValue: defaultTerms, supported: termsSupported, change: onTermsChange, placeholder: TERMS_EXAMPLE, rows: 5, maxLength: 8000,
            help: "One name, acronym, product, or technical term per line. Include the exact spelling you want the model to recognize." },
    ];

    return (
        <div className="space-y-5">
            {fields.filter((field) => field.supported).map((field) => {
                const inherited = allowInheritance && field.value == null;
                const inputId = `${idPrefix}-${field.key}`;
                return (
                    <div key={field.key} className="space-y-3">
                        {allowInheritance && (
                            <SwitchField
                                id={`${inputId}-inherit`}
                                label={`Use saved ${field.key === "context" ? "meeting context" : "vocabulary"}`}
                                checked={inherited}
                                onCheckedChange={(checked) => field.change(checked ? null : field.defaultValue)}
                            />
                        )}
                        <FormField label={field.label} htmlFor={inputId} optional>
                            <Textarea
                                id={inputId}
                                aria-describedby={`${inputId}-help`}
                                value={inherited ? field.defaultValue : field.value ?? ""}
                                onChange={(event) => field.change(event.target.value)}
                                placeholder={inherited && !field.defaultValue ? "No saved default. Add one in Settings → Transcription." : field.placeholder}
                                disabled={disabled || inherited}
                                rows={field.rows}
                                maxLength={field.maxLength}
                                className={`${inputClassName} h-auto min-h-[88px] resize-y disabled:opacity-70`}
                            />
                            <p id={`${inputId}-help`} className="text-xs leading-5 text-[var(--text-secondary)]">
                                {inherited ? "Uses the saved default when a new transcription is queued." : field.help}
                                {allowInheritance && !inherited && " Leave blank to use none for this profile or run."}
                            </p>
                        </FormField>
                    </div>
                );
            })}
        </div>
    );
}
