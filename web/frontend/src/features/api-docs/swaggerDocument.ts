import swaggerScript from 'swagger-ui-dist/swagger-ui-bundle.js?url';
import swaggerStyle from 'swagger-ui-dist/swagger-ui.css?url';
import frameScript from './swagger-frame.js?url';

function localAsset(path: string, origin: string): string {
    const asset = new URL(path, origin);
    if (asset.origin !== origin || /[\s;"<>]/.test(asset.href)) {
        throw new Error('Invalid Swagger UI asset URL');
    }
    return asset.href;
}

export function swaggerDocument(spec: Record<string, unknown>, origin: string, dark: boolean): string {
    const address = new URL(origin);
    const bundle = localAsset(swaggerScript, address.origin);
    const initializer = localAsset(frameScript, address.origin);
    const stylesheet = localAsset(swaggerStyle, address.origin);
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
        servers: [{ url: address.origin }],
    }).replaceAll('<', '\\u003c');

    return `<!doctype html>
<html lang="en"${dark ? ' class="dark-mode"' : ''}>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="${policy}">
  <meta name="referrer" content="no-referrer">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="${dark ? 'dark' : 'light'}">
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
