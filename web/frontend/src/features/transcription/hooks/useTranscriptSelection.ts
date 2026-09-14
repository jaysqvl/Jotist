import { useState, useEffect } from "react";
import type { RefObject } from "react";
import { useIsMobile } from "@/hooks/use-mobile";

interface WordOffset {
    startChar: number;
    endChar: number;
    startTime: number;
    endTime: number;
}

export function useTranscriptSelection(
    transcriptRef: RefObject<HTMLElement>,
    offsets: WordOffset[]
) {
    const isMobile = useIsMobile();
    const [showSelectionMenu, setShowSelectionMenu] = useState(false);
    const [pendingSelection, setPendingSelection] = useState<{ startIdx: number; endIdx: number; startTime: number; endTime: number; quote: string } | null>(null);
    const [selectionViewportPos, setSelectionViewportPos] = useState<{ x: number, y: number }>({ x: 0, y: 0 });
    const [showEditor, setShowEditor] = useState(false);

    useEffect(() => {
        const el = transcriptRef.current;
        if (!el) return;

        // Skip custom selection logic on mobile devices to defer to native handling
        if (isMobile) {
            return;
        }

        const handleSelection = () => {
            const sel = window.getSelection();
            if (!sel || sel.isCollapsed) { setShowSelectionMenu(false); setShowEditor(false); return; }

            // Ensure selection is within our transcript container
            if (!el.contains(sel.anchorNode) || !el.contains(sel.focusNode)) return;

            // Handle Text Node selection (Compact View) or Element selection (Expanded View fallback)
            // For Compact View, we expect a single text node or similar.
            const range = sel.getRangeAt(0);

            // If offsets are missing (no word-level data), we can't provide seek/timestamps
            if (!offsets || offsets.length === 0) {
                setShowSelectionMenu(false);
                return;
            }

            const startChar = range.startOffset;
            const endChar = range.endOffset;

            // Find start and end words based on character offsets
            const startWordIdx = offsets.findIndex(w => startChar >= w.startChar && startChar <= w.endChar) !== -1
                ? offsets.findIndex(w => startChar >= w.startChar && startChar <= w.endChar)
                // If not exactly on a word, find the one after startChar (or nearest)
                : offsets.findIndex(w => w.startChar >= startChar);

            // For end word, similar logic
            let endWordIdx = offsets.findIndex(w => endChar >= w.startChar && endChar <= w.endChar);
            if (endWordIdx === -1) {
                // If not on exact word, find one before
                for (let i = offsets.length - 1; i >= 0; i--) {
                    if (offsets[i].endChar <= endChar) {
                        endWordIdx = i;
                        break;
                    }
                }
            }

            if (startWordIdx === -1 || endWordIdx === -1 || endWordIdx < startWordIdx) {
                setShowSelectionMenu(false);
                return;
            }

            const startWord = offsets[startWordIdx];
            const endWord = offsets[endWordIdx];

            const quote = range.toString().trim();

            setPendingSelection({
                startIdx: startWordIdx,
                endIdx: endWordIdx,
                startTime: startWord.startTime,
                endTime: endWord.endTime,
                quote
            });
            setShowSelectionMenu(true);

            const rect = range.getBoundingClientRect();
            const centerX = rect.left + rect.width / 2;
            const clampedX = Math.min(window.innerWidth - 16, Math.max(16, centerX));
            let bubbleY = rect.top - 10;
            if (bubbleY < 12) {
                bubbleY = rect.bottom + 8;
            }
            setSelectionViewportPos({ x: clampedX, y: bubbleY });
        };

        el.addEventListener('mouseup', handleSelection);
        return () => el.removeEventListener('mouseup', handleSelection);
    }, [offsets, isMobile, transcriptRef]);

    // Hide selection bubble when selection collapses
    useEffect(() => {
        const onSelectionChange = () => {
            if (showEditor) return;
            const sel = window.getSelection();
            if (!sel || sel.isCollapsed) {
                setShowSelectionMenu(false);
                setPendingSelection(null);
            }
        };
        document.addEventListener('selectionchange', onSelectionChange);
        return () => document.removeEventListener('selectionchange', onSelectionChange);
    }, [showEditor]);

    const openEditor = () => {
        setShowEditor(true);
        setShowSelectionMenu(false);
    };

    const closeEditor = () => {
        setShowEditor(false);
        setPendingSelection(null);
        window.getSelection()?.removeAllRanges();
    };

    return {
        showSelectionMenu,
        pendingSelection,
        selectionViewportPos,
        showEditor,
        openEditor,
        closeEditor
    };
}
