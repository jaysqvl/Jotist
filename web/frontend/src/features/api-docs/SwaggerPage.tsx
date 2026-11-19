import { useEffect, useRef, useState } from 'react';
import { ArrowLeft, BookOpen, ChevronDown } from 'lucide-react';
import { Link } from 'react-router-dom';
import { Header } from '@/components/Header';
import { MainLayout } from '@/components/layout/MainLayout';
import { useTheme } from '@/contexts/ThemeContext';
import swaggerLicense from 'swagger-ui-dist/LICENSE?raw';
import swaggerNotice from 'swagger-ui-dist/NOTICE?raw';
import bundleNotices from 'swagger-ui-dist/swagger-ui-bundle.js.LICENSE.txt?url';
import { swaggerDocument } from './swaggerDocument';
import { SwaggerExamples } from './SwaggerExamples';

const specPath = '/api/v1/docs/spec';
const licenseDownload = 'data:text/plain;charset=utf-8,' + encodeURIComponent(swaggerLicense);
const noticeDownload = 'data:text/plain;charset=utf-8,' + encodeURIComponent(swaggerNotice);

function applyFrameTheme(frame: HTMLIFrameElement | null, theme: 'light' | 'dark') {
    // The sandbox has an opaque origin. Only the palette choice crosses it.
    frame?.contentWindow?.postMessage({ type: 'jotist-swagger-theme', theme }, '*');
}

export default function SwaggerPage() {
    const { theme } = useTheme();
    const initialTheme = useRef(theme);
    const iframe = useRef<HTMLIFrameElement>(null);
    const [frame, setFrame] = useState<string | null>(null);
    const [referenceError, setReferenceError] = useState(false);

    useEffect(() => {
        const request = new AbortController();
        async function loadReference() {
            try {
                const response = await fetch(specPath, {
                    cache: 'no-store', redirect: 'error', credentials: 'omit', signal: request.signal,
                });
                if (!response.ok) throw new Error('API reference unavailable');
                const spec: unknown = await response.json();
                if (!spec || typeof spec !== 'object' || !('openapi' in spec) || !('paths' in spec)) {
                    throw new Error('Invalid API specification');
                }
                if (!request.signal.aborted) {
                    setFrame(swaggerDocument(spec, window.location.origin, initialTheme.current === 'dark'));
                }
            } catch {
                if (!request.signal.aborted) setReferenceError(true);
            }
        }
        void loadReference();
        return () => request.abort();
    }, []);

    useEffect(() => {
        applyFrameTheme(iframe.current, theme);
    }, [theme]);

    return (
        <MainLayout header={<Header />}>
            <div className="space-y-5">
                <div>
                    <Link to="/settings?tab=apikeys" className="inline-flex items-center gap-1.5 text-sm text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
                        <ArrowLeft aria-hidden="true" className="size-4" /> API Keys
                    </Link>
                    <h1 className="mt-3 flex items-center gap-2 text-2xl font-display font-bold text-[var(--text-primary)]">
                        <BookOpen aria-hidden="true" className="size-5 text-[var(--brand-solid)]" /> API reference
                    </h1>
                    <p className="mt-1 text-sm text-[var(--text-secondary)]">
                        Open an endpoint to see its fields and responses. Select Try it out, then Build cURL to prepare a command.
                    </p>
                </div>
                {frame ? (
                    <>
                        <details className="group rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-card)]">
                            <summary className="flex cursor-pointer list-none items-center justify-between rounded-[var(--radius-card)] px-4 py-3 text-sm font-semibold text-[var(--text-primary)] focus-visible:outline-[var(--brand-solid)] sm:px-5 [&::-webkit-details-marker]:hidden">
                                Quick checks
                                <ChevronDown aria-hidden="true" className="size-4 text-[var(--text-secondary)] transition-transform group-open:rotate-180 motion-reduce:transition-none" />
                            </summary>
                            <SwaggerExamples />
                        </details>
                        <iframe
                            ref={iframe}
                            title="Swagger API reference"
                            sandbox="allow-scripts"
                            referrerPolicy="no-referrer"
                            srcDoc={frame}
                            onLoad={event => applyFrameTheme(event.currentTarget, theme)}
                            className="block h-[68vh] min-h-96 w-full rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-card)] [color-scheme:light] dark:[color-scheme:dark]"
                        />
                    </>
                ) : (
                    <p role="status" className="text-sm text-[var(--text-secondary)]">
                        {referenceError ? 'Could not load the API reference. Reload the page to try again.' : 'Loading API reference…'}
                    </p>
                )}
                <p className="flex flex-wrap gap-3 text-xs text-[var(--text-tertiary)]">
                    <a className="hover:underline" href={licenseDownload} download="swagger-ui-LICENSE.txt">Swagger UI license</a>
                    <a className="hover:underline" href={noticeDownload} download="swagger-ui-NOTICE.txt">Notices</a>
                    <a className="hover:underline" href={bundleNotices} download="swagger-ui-bundle.LICENSE.txt">Bundle notices</a>
                </p>
            </div>
        </MainLayout>
    );
}
