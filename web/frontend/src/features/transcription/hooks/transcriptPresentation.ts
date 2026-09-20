export interface TimedSegment {
    start: number;
    end: number;
    text: string;
    speaker?: string;
}

export interface TimedWord {
    start: number;
    end: number;
    word: string;
    speaker?: string;
}

export interface TranscriptPresentation {
    version: 1;
    grouping: "native_segments" | "speaker_turns";
    segments: Array<TimedSegment & { word_indices: number[] }>;
    speaker_labels: Record<string, string>;
    inferred_point_speakers: number;
}

export interface PresentedTranscript {
    segments?: TimedSegment[];
    word_segments?: TimedWord[];
    presentation?: TranscriptPresentation;
}

// The server owns grouping and word membership. Older servers retain their
// segment text; the client never expands rows by searching nearby timestamps.
export function transcriptDisplaySegments(transcript: PresentedTranscript) {
    if (transcript.presentation?.version === 1) return transcript.presentation.segments;
    return (transcript.segments || []).map((segment) => ({ ...segment, word_indices: [] as number[] }));
}

export function transcriptSpeakerLabel(id: string, transcript?: PresentedTranscript, custom: Record<string, string> = {}): string {
    return custom[id] || transcript?.presentation?.speaker_labels[id] || id;
}

// Offsets point into the exact displayed text, including punctuation and Unicode.
// Alignment can differ from native text; an unmatched token gets no highlight.
export function transcriptRowOffsets(text: string, indices: number[], words: TimedWord[]) {
    let cursor = 0;
    const offsets: { startChar: number; endChar: number; startTime: number; endTime: number; word: string }[] = [];
    for (const index of indices) {
        const word = words[index];
        const token = word?.word.trim();
        if (!word || !token) continue;
        const start = text.indexOf(token, cursor);
        if (start < 0) continue;
        // Do not match an English word inside an unrelated longer word.
        if (/^[a-z0-9]/i.test(token) && start > 0 && /[a-z0-9]/i.test(text[start - 1])) continue;
        if (/[a-z0-9]$/i.test(token) && /[a-z0-9]/i.test(text[start + token.length] || "")) continue;
        offsets.push({ startChar: start, endChar: start + token.length, startTime: word.start, endTime: word.end, word: token });
        cursor = start + token.length;
    }
    return offsets;
}
