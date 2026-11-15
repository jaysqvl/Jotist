import { useRef, useEffect, useMemo } from "react";
import { createPortal } from "react-dom";
import { TranscriptView } from "@/components/transcript/TranscriptView";
import { useNotes, useCreateNote } from "@/features/transcription/hooks/useTranscriptionNotes";
import { useSelectionMenu } from "@/features/transcription/hooks/useSelectionMenu";
import { TranscriptSelectionMenu } from "./TranscriptSelectionMenu";
import { NoteEditorDialog } from "./NoteEditorDialog";
import { useIsDesktop } from "@/hooks/useIsDesktop";
import { computeWordOffsets } from "@/features/transcription/hooks/useKaraokeHighlight";
import type { Transcript } from "@/features/transcription/hooks/useAudioDetail";
import { cn } from "@/lib/utils";

interface TranscriptSectionProps {
    audioId: string;
    currentWordIndex: number | null;
    currentTime: number;
    isPlaying: boolean;
    onSeek: (time: number) => void;
    // Lifted State Props
    transcript: Transcript | undefined;
    speakerMappings: Record<string, string>;
    transcriptMode: 'compact' | 'expanded';
    autoScrollEnabled: boolean;
    setNotesOpen: (open: boolean) => void;
}

export function TranscriptSection({
    audioId,
    currentWordIndex,
    currentTime,
    isPlaying,
    onSeek,
    transcript,
    speakerMappings,
    transcriptMode,
    autoScrollEnabled,
    setNotesOpen,
    className
}: TranscriptSectionProps & { className?: string }) {
    const isDesktop = useIsDesktop();

    // Data hooks
    const { data: notes = [] } = useNotes(audioId);
    const { mutate: createNote } = useCreateNote(audioId);

    // Refs
    const transcriptRef = useRef<HTMLDivElement>(null);
    const highlightedWordRef = useRef<HTMLSpanElement>(null);

    // Compute offsets for selection logic
    const words = useMemo(() => transcript?.word_segments || [], [transcript?.word_segments]);
    const { offsets } = useMemo(() => computeWordOffsets(words), [words]);

    // Unified Selection Hook for both desktop and mobile
    const {
        menuState,
        showEditor,
        openEditor,
        closeEditor
    } = useSelectionMenu(transcriptRef, offsets);

    // Auto-scroll logic
    useEffect(() => {
        if (currentWordIndex !== null && highlightedWordRef.current && autoScrollEnabled) {
            const highlightedElement = highlightedWordRef.current;
            const highlightedRect = highlightedElement.getBoundingClientRect();
            const viewportHeight = window.innerHeight;
            const buffer = viewportHeight * 0.2; // 20%
            const isAboveView = highlightedRect.top < buffer;
            const isBelowView = highlightedRect.bottom > (viewportHeight - buffer);

            if (isAboveView || isBelowView) {
                highlightedElement.scrollIntoView({
                    behavior: 'smooth',
                    block: 'center',
                });
            }
        }
    }, [currentWordIndex, autoScrollEnabled]);

    useEffect(() => {
        // Only enable click-to-seek on desktop devices with fine pointer
        if (!isDesktop) return;
        const el = transcriptRef.current;
        if (!el) return;
        const onClick = (e: MouseEvent) => {
            if (!(e.metaKey || e.ctrlKey)) return;
            const target = e.target as HTMLElement | null;
            if (!target) return;
            const wordEl = target.closest('span[data-word-index]') as HTMLElement | null;
            if (!wordEl) return;
            const startAttr = wordEl.getAttribute('data-start');
            const start = startAttr ? parseFloat(startAttr) : NaN;
            if (isNaN(start)) return;

            e.preventDefault();
            e.stopPropagation();
            onSeek(start);
        };
        el.addEventListener('click', onClick);
        return () => el.removeEventListener('click', onClick);
    }, [onSeek, isDesktop]);

    const handleSaveNote = (content: string) => {
        if (menuState) {
            createNote({
                start_time: menuState.startTime,
                end_time: menuState.endTime,
                content: content,
                quote: menuState.selectedText,
                start_word_index: menuState.startIdx,
                end_word_index: menuState.endIdx
            });
            closeEditor();
            setNotesOpen(true);
        }
    };

    const handleListenFromHere = () => {
        if (menuState) {
            onSeek(menuState.startTime);
            closeEditor();
        }
    };

    if (!transcript) return null;

    return (
        <div className="md:glass-card md:rounded-[var(--radius-card)] md:border-[var(--border-subtle)] md:shadow-[var(--shadow-card)] md:hover:shadow-[var(--shadow-float)] p-4 md:p-6 min-h-[500px] transition-shadow">
            {/* 
                  TOOLBAR REMOVED -> Moved to Context Menu 
                */}

            {/* Transcript Content - Systematic Typography */}
            <div
                className={cn("relative font-sans", className)}
                style={{
                    // Allow text selection to work in children
                    WebkitUserSelect: 'text',
                    userSelect: 'text'
                }}
            >
                <div className="w-full text-[var(--text-secondary)] leading-relaxed">
                    <div
                        ref={transcriptRef}
                        className="relative"
                        style={{
                            // Ensure this container doesn't interfere with touch events
                            touchAction: 'pan-y pinch-zoom'
                        }}
                    >
                        <TranscriptView
                            transcript={transcript}
                            mode={transcriptMode}
                            currentWordIndex={currentWordIndex}
                            currentTime={currentTime}
                            isPlaying={isPlaying}
                            notes={notes}
                            onSeek={onSeek}
                            highlightedWordRef={highlightedWordRef}
                            speakerMappings={speakerMappings}
                            autoScrollEnabled={autoScrollEnabled}
                        />
                    </div>
                </div>
            </div>

            {/* Portals */}
            {createPortal(
                <>
                    {/* Selection Menu Bubble (Glass) - Unified for desktop and mobile */}
                    {!showEditor && (
                        <TranscriptSelectionMenu
                            menuState={menuState}
                            onAddNote={openEditor}
                            onListenFromHere={handleListenFromHere}
                        />
                    )}

                    {/* Note Editor Dialog */}
                    <NoteEditorDialog
                        isOpen={showEditor}
                        quote={menuState?.selectedText || ""}
                        position={menuState ? { x: menuState.x, y: menuState.y } : { x: 0, y: 0 }}
                        onSave={handleSaveNote}
                        onCancel={closeEditor}
                    />
                </>,
                document.body
            )}
        </div>
    );
}
