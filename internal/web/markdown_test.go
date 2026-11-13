//go:build uitest

package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMarkdownRenderer(t *testing.T) {
	runJSProbe(t, markdownProbe)
}

const markdownProbe = `
globalThis.markdownit = require('./static/vendor/markdown-it.v15.0.3.min.js');
const {renderMarkdown} = require('./static/markdown.mjs');
test('headings emphasis lists links code and tables use semantic HTML',()=>{
 const source='# Heading\n\nA **strong** and *emphasized* [link](https://example.com).\n\n- first\n- second\n\n1. ordered\n\n\x60\x60\x60js\nconst value = "<unsafe>";\n\x60\x60\x60\n\n| Name | Value |\n| --- | --- |\n| A | B |\n';
 const html=renderMarkdown(source);
 for(const pattern of [/<h1>Heading<\/h1>/, /<strong>strong<\/strong>/, /<em>emphasized<\/em>/, /<ul>/, /<ol>/, /<a href="https:\/\/example.com">link<\/a>/, /<pre><code class="language-js">/, /&lt;unsafe&gt;/, /<table>/, /<th>Name<\/th>/, /<td>B<\/td>/]) assert.match(html,pattern);
 assert.equal(source.includes('**strong**'),true,'rendering must not rewrite the source');
});
test('hostile HTML remains inert and unsafe URL schemes never become anchors',()=>{
 const source='<script>globalThis.pwned=1</script>\n\n<img src=x onerror="globalThis.pwned=2">\n\n[bad](javascript:alert(1)) [encoded](jav&#x61;script:alert(1)) [data](data:text/html;base64,PHNjcmlwdD4=)';
 const html=renderMarkdown(source);
 assert.doesNotMatch(html,/<(?:script|img)\b/i);
 assert.match(html,/&lt;script&gt;/);
 assert.doesNotMatch(html,/<a\b[^>]*href="(?:javascript|data):/i);
 assert.equal(globalThis.pwned,undefined);
});
test('plain text and empty input preserve display text without fabricated markup',()=>{
 assert.equal(renderMarkdown('Plain & ordinary text'),' <p>Plain &amp; ordinary text</p>\n'.trimStart());
 for(const value of ['',null,undefined]) assert.equal(renderMarkdown(value),'');
 assert.equal(renderMarkdown('  \n\n'),'');
});
`

func TestMarkdownEmbeddedAssets(t *testing.T) {
	handler := Handler(&testRepo{})
	for _, path := range []string{"/markdown.mjs", "/vendor/markdown-it.v15.0.3.min.js"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 || response.Body.Len() == 0 {
			t.Fatalf("embedded asset %s: status=%d bytes=%d", path, response.Code, response.Body.Len())
		}
		if strings.Contains(response.Body.String(), "<!doctype html>") {
			t.Fatalf("%s fell back to index instead of a local asset", path)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(response.Body.String(), `src="/vendor/markdown-it.v15.0.3.min.js"`) {
		t.Fatal("index must load the pinned local renderer without a build or CDN")
	}
}
