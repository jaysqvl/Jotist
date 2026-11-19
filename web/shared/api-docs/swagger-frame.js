const spec = JSON.parse(document.getElementById('jotist-api-spec').textContent);
const SwaggerUIBundle = globalThis.SwaggerUIBundle;

const appOrigin = document.querySelector('meta[name="jotist-parent-origin"]').content;
globalThis.addEventListener('message', event => {
    if (event.source !== globalThis.parent || event.origin !== appOrigin) return;
    const { type, theme } = event.data ?? {};
    if (type === 'jotist-swagger-theme' && (theme === 'light' || theme === 'dark')) {
        document.documentElement.classList.toggle('dark-mode', theme === 'dark');
    }
});

function withCredentialPlaceholder(request, path, method) {
    const security = spec.paths[path]?.[method]?.security ?? spec.security ?? [];
    if (security.some(requirement => Object.hasOwn(requirement, 'ApiKeyAuth'))) {
        return request.setIn(['headers', 'X-API-Key'], 'YOUR_JOTIST_API_KEY');
    }
    if (security.some(requirement => Object.hasOwn(requirement, 'BearerAuth'))) {
        return request.setIn(['headers', 'Authorization'], 'Bearer YOUR_JOTIST_TOKEN');
    }
    return request;
}

// Swagger UI names files in its shell snippet and uses curl -F for
// multipart values. Use placeholders when those syntaxes would reinterpret
// an entered value. Files are represented by their metadata only.
function prepareSnippet(request) {
    let substituted = false;
    let fileNumber = 0;
    function fileReference(file) {
        fileNumber += 1;
        const safeName = /^[\w .-]+$/.test(file.name) && file.name !== '-';
        const safeType = !file.type || /^[\w.+-]+\/[\w.+-]+$/.test(file.type);
        if (!safeName || !safeType) substituted = true;
        return new File([], safeName ? file.name : `UPLOAD_FILE_${fileNumber}`, { type: safeType ? file.type : '' });
    }
    const body = request.get('body');
    if (body instanceof File) {
        request = request.set('body', fileReference(body));
    } else if (request.getIn(['headers', 'Content-Type']) === 'multipart/form-data' && body?.map) {
        request = request.set('body', body.map(value => {
            if (value instanceof File) return fileReference(value);
            if (typeof value !== 'string' || !/^(?:[@<])|;/.test(value)) return value;
            substituted = true;
            return 'FORM_VALUE';
        }));
    }
    return { request, substituted };
}

function copyOnlyPlugin({ React: { createElement: element } }) {
    return {
        // Swagger UI builds and stores the request before this transport hook.
        // Complete the local preview without constructing an HTTP response.
        fn: { execute: async () => ({ preview: true }) },
        wrapComponents: {
            authorizeBtn: () => () => null,
            authorizeOperationBtn: () => () => null,
            execute: Original => class BuildCurl extends Original {
                render() {
                    return element('button', {
                        type: 'button',
                        className: 'btn execute opblock-control__btn',
                        onClick: this.onClick,
                        disabled: this.props.disabled,
                    }, 'Build cURL');
                }
            },
            liveResponse: () => ({ specSelectors, path, method, getComponent }) => {
                const draft = specSelectors.requestFor(path, method);
                if (!draft) return null;
                const { request, substituted } = prepareSnippet(withCredentialPlaceholder(draft, path, method));
                const RequestSnippets = getComponent('RequestSnippets', true);
                return element('div', { className: 'jotist-request-preview' },
                    element('p', null, substituted
                        ? 'Replace file or form placeholders with your local values before using this command.'
                        : 'Copy the command to your terminal. Use the shown file names or update their paths.'),
                    element(RequestSnippets, { request }),
                );
            },
        },
    };
}

// Keep reference links inside the frame. API examples are run in Jotist.
document.addEventListener('click', (event) => {
    const link = event.target instanceof Element && event.target.closest('a[href]');
    if (link && !link.getAttribute('href').startsWith('#')) event.preventDefault();
}, true);

SwaggerUIBundle({
    spec,
    dom_id: '#swagger-ui',
    layout: 'BaseLayout',
    docExpansion: 'list',
    defaultModelsExpandDepth: 0,
    filter: true,
    persistAuthorization: false,
    queryConfigEnabled: false,
    validatorUrl: null,
    showMutatedRequest: false,
    requestSnippetsEnabled: true,
    requestSnippets: { languages: ['curl_bash'] },
    plugins: [copyOnlyPlugin],
});
