declare global {
    interface Window {
        __jotist_original_fetch?: typeof window.fetch;
    }
}

export {};
