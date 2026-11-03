import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
const require = createRequire(import.meta.url);
globalThis.markdownit = require('./vendor/markdown-it.v15.0.3.min.js');
const {renderMarkdown} = await import('./markdown.mjs');

test('default Markdown features render without changing source data', () => {
  const source = '# Heading\n\n**strong** *em* ~~deleted~~ `inline`\n\n- first\n- second\n\n1. ordered\n\n> quoted\n\n```js\nconst x = "<tag>";\n```\n\n| A | B |\n| --- | --- |\n| one | two |\n\n[safe](https://example.com)';
  const raw = {description:source,body:source};
  const snapshot = JSON.stringify(raw);
  const html = renderMarkdown(raw.description);
  for (const expected of ['<h1>Heading</h1>','<strong>strong</strong>','<em>em</em>','<s>deleted</s>','<code>inline</code>','<ul>','<ol>','<blockquote>','<pre><code class="language-js">','&lt;tag&gt;','<table>','href="https://example.com"']) assert.ok(html.includes(expected), expected);
  assert.equal(JSON.stringify(raw), snapshot);
});

test('raw HTML and attribute-shaped text remain inert', () => {
  const html = renderMarkdown('<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n<svg onload=alert(1)>\n\n**" onmouseover="alert(1)**');
  assert.ok(html.includes('&lt;script&gt;'));
  assert.ok(html.includes('&lt;img'));
  assert.doesNotMatch(html, /<(script|img|svg)\b/);
  const fence = renderMarkdown('```\" onmouseover=\"alert(1)\n<img src=x>\n```');
  assert.doesNotMatch(fence, /<img\b| onmouseover="/);
});

test('upstream default URL checks reject unsafe links and images', () => {
  for (const url of ['javascript:alert(1)','JaVaScRiPt:alert(1)','javascript&#x3A;alert(1)','jav&#x61;script:alert(1)','vbscript:msgbox(1)','file:///etc/passwd','data:text/html;base64,PHNjcmlwdD4=','data:image/svg+xml;base64,PHN2Zz4=']) {
    for (const prefix of ['', '!']) {
      const html = renderMarkdown(`${prefix}[unsafe](${url})`);
      assert.doesNotMatch(html, /<(a|img)\b/, url);
    }
  }
  for (const url of ['https://example.com/path','http://example.com','mailto:user@example.com','/relative','#anchor']) assert.match(renderMarkdown(`[safe](${url})`), /<a href=/);
});

test('empty input and existing description fallback stay predictable', () => {
  assert.equal(renderMarkdown(null), '');
  assert.equal(renderMarkdown(undefined), '');
  assert.equal(renderMarkdown(''), '');
  assert.equal(renderMarkdown('No description.'), '<p>No description.</p>\n');
  assert.match(renderMarkdown('line one\nline two'), /line one\nline two/);
});
