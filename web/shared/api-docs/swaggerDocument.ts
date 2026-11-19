type SwaggerDocumentOptions = {
    origin: string;
    serverURL?: string;
    dark?: boolean;
    bundlePath: string;
    initializerPath: string;
    stylesheetPath: string;
};

function localAsset(path: string, origin: string): string {
    const asset = new URL(path, origin);
    if (asset.origin !== origin || /[\s;"<>]/.test(asset.href)) {
        throw new Error('Invalid Swagger UI asset URL');
    }
    return asset.href;
}

export function swaggerDocument(spec: Record<string, unknown>, options: SwaggerDocumentOptions): string {
    const origin = new URL(options.origin).origin;
    const { dark = false, serverURL = origin } = options;
    const bundle = localAsset(options.bundlePath, origin);
    const initializer = localAsset(options.initializerPath, origin);
    const stylesheet = localAsset(options.stylesheetPath, origin);
    const policy = [
        "default-src 'none'",
        `script-src ${bundle} ${initializer}`,
        `style-src 'unsafe-inline' ${stylesheet}`,
        'img-src data:',
        "connect-src 'none'",
        "base-uri 'none'",
        "form-action 'none'",
    ].join('; ');
    const json = JSON.stringify({
        ...spec,
        servers: [{ url: serverURL }],
    }).replaceAll('<', '\\u003c');

    return `<!doctype html>
<html lang="en"${dark ? ' class="dark-mode"' : ''}>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="${policy}">
  <meta name="referrer" content="no-referrer">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="${dark ? 'dark' : 'light'}">
  <meta name="jotist-parent-origin" content="${origin}">
  <title>Jotist API reference</title>
  <link rel="stylesheet" href="${stylesheet}">
  <style>
    body { margin: 0; padding: 0 4px 20px; }
    html { color-scheme: light; }
    html.dark-mode, html.dark-mode .swagger-ui { background-color: #171717; }
    .swagger-ui .info { margin: 20px 0; }
    .swagger-ui .info .title { font-size: 24px; }
    .swagger-ui .opblock-summary-path { overflow-wrap: anywhere; }
    .swagger-ui .scheme-container { padding: 10px 0; }
    .swagger-ui .jotist-request-preview p { font-size: 13px; margin-bottom: 12px; }
    @media (max-width: 550px) {
      .swagger-ui .wrapper { padding: 0 10px; }
      .swagger-ui .opblock-summary-description { display: none; }
      .swagger-ui .opblock .opblock-summary-path { font-size: 12px; }
    }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script id="jotist-api-spec" type="application/json">${json}</script>
  <script defer src="${bundle}"></script>
  <script defer src="${initializer}"></script>
</body>
</html>`;
}
