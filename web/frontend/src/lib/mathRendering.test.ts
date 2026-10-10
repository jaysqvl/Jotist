import assert from 'node:assert/strict';
import test from 'node:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import ReactMarkdown from 'react-markdown';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import rehypeHighlight from 'rehype-highlight';

function render(content: string) {
    // The same parser/plugin boundary used by chat responses and summaries.
    return renderToStaticMarkup(createElement(ReactMarkdown, {
        remarkPlugins: [remarkMath], rehypePlugins: [rehypeKatex, rehypeHighlight], children: content,
    }));
}

test('mathematical chat and summary content still renders after the KaTeX security update', () => {
    const html = render('A result: $x^2 + y^2 = z^2$.\n\n$$\n\\frac{1}{2} + \\sqrt{4}\n$$');
    assert.match(html, /class="katex"/);
    assert.match(html, /<math/);
    assert.match(html, /class="katex-display"/);
    assert.doesNotMatch(html, /katex-error/);
});

test('inherited trust cannot enable links or external images in mathematical content', () => {
    const inherited = Object.getOwnPropertyDescriptor(Object.prototype, 'trust');
    try {
        Object.defineProperty(Object.prototype, 'trust', { value: true, configurable: true });
        for (const expression of [
            '$\\href{javascript:alert(1)}{click}$',
            '$\\includegraphics{https://untrusted.invalid/tracker.png}$',
        ]) {
            const html = render(expression);
            assert.doesNotMatch(html, /<a\b|<img\b/);
            assert.match(html, /katex/);
        }
    } finally {
        if (inherited) Object.defineProperty(Object.prototype, 'trust', inherited);
        else Reflect.deleteProperty(Object.prototype, 'trust');
    }
});
