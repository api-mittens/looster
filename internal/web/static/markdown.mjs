// The pinned, locally served UMD script in index.html initializes markdownit.
// Keep this instance private: no raw HTML, plugins, custom link validator, or
// highlighter. The upstream default validator rejects unsafe URL schemes.
const markdown = globalThis.markdownit({html:false});

/** Render display-only task text. Never replace the source API/form values. */
export function renderMarkdown(source) {
  return markdown.render(String(source ?? ''));
}
