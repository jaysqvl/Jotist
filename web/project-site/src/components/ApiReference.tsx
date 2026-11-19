import { useEffect, useState } from 'react';
import swaggerBundle from 'swagger-ui-dist/swagger-ui-bundle.js?url';
import swaggerStyles from 'swagger-ui-dist/swagger-ui.css?url';
import swaggerLicense from 'swagger-ui-dist/LICENSE?raw';
import swaggerNotice from 'swagger-ui-dist/NOTICE?raw';
import bundleNotices from 'swagger-ui-dist/swagger-ui-bundle.js.LICENSE.txt?url';
import frameScript from '../../../shared/api-docs/swagger-frame.js?url';
import { swaggerDocument } from '../../../shared/api-docs/swaggerDocument';
import { JotistLogo } from './JotistLogo';

const specPath = '/api/openapi.json';

export default function ApiReference() {
    const [frame, setFrame] = useState<string>();
    const [failed, setFailed] = useState(false);

    useEffect(() => {
        const request = new AbortController();
        async function loadReference() {
            try {
                const response = await fetch(specPath, { signal: request.signal });
                if (!response.ok) throw new Error('API reference unavailable');
                const spec = await response.json();
                setFrame(swaggerDocument(spec, {
                    origin: window.location.origin,
                    serverURL: spec.servers[0].url,
                    bundlePath: swaggerBundle,
                    initializerPath: frameScript,
                    stylesheetPath: swaggerStyles,
                }));
            } catch {
                if (!request.signal.aborted) setFailed(true);
            }
        }
        void loadReference();
        return () => request.abort();
    }, []);

    return (
        <div className="min-h-screen bg-white">
            <header className="border-b border-gray-100">
                <div className="mx-auto max-w-7xl px-4 py-3 sm:px-6 lg:px-8">
                    <a href="/" className="inline-flex items-center gap-3">
                        <JotistLogo />
                        <span className="text-gray-300">/</span>
                        <span className="text-sm font-semibold text-[#6356E5]">API Reference</span>
                    </a>
                </div>
            </header>
            <main className="mx-auto max-w-7xl space-y-5 px-4 py-8 sm:px-6 lg:px-8">
                <div>
                    <h1 className="text-2xl font-bold text-gray-900">Jotist API</h1>
                    <p className="mt-2 text-sm text-gray-600">
                        Use your Jotist server address in copied commands. Open <code>/swagger/</code> on your server to browse and run quick checks after signing in.
                    </p>
                    <a href={specPath} download className="mt-3 inline-block text-sm font-medium text-[#6356E5] underline">
                        Download OpenAPI spec
                    </a>
                </div>
                {frame ? (
                    <iframe
                        title="Swagger API reference"
                        sandbox="allow-scripts"
                        referrerPolicy="no-referrer"
                        srcDoc={frame}
                        className="block h-[72vh] min-h-96 w-full rounded-xl border border-gray-200"
                    />
                ) : (
                    <p role="status" className="text-sm text-gray-600">
                        {failed ? 'Could not load the API reference. Reload the page to try again.' : 'Loading API reference…'}
                    </p>
                )}
                <div className="flex flex-wrap gap-3 text-xs text-gray-500">
                    <a href={`data:text/plain;charset=utf-8,${encodeURIComponent(swaggerLicense)}`} download="swagger-ui-LICENSE.txt">Swagger UI license</a>
                    <a href={`data:text/plain;charset=utf-8,${encodeURIComponent(swaggerNotice)}`} download="swagger-ui-NOTICE.txt">Notices</a>
                    <a href={bundleNotices} download="swagger-ui-bundle.LICENSE.txt">Bundle notices</a>
                </div>
            </main>
        </div>
    );
}
