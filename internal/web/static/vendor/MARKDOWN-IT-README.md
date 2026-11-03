# Vendored markdown-it

- Upstream: https://github.com/markdown-it/markdown-it
- Pinned version: **15.0.3**, published npm `dist/browser/markdown-it.umd.min.js`, copied unmodified as `markdown-it.v15.0.3.min.js`.
- Archive: https://registry.npmjs.org/markdown-it/-/markdown-it-15.0.3.tgz
- Bundle SHA-256: `391f2aa638e75e6c7859d0a5c5650ef0956722a45998f17e05b83bc655778e1e`.
- Original source map retained as `markdown-it.umd.min.js.map`, matching the unchanged bundle's sourceMappingURL.
- MIT license retained verbatim in `MARKDOWN-IT-LICENSE.txt`.
- **`MARKDOWN-IT-THIRD-PARTY-LICENSES.txt` must also accompany redistribution.** It includes full notices for all six browser-reachable packages: markdown-it 15.0.3, mdurl 2.1.0, uc.micro 3.0.0, entities 8.1.0 (BSD-2-Clause), linkify-it 6.1.0, and punycode.js 2.3.1. Other packages are MIT. CLI-only argparse is not in this browser bundle.

## Provenance and verification

Bundle and map are byte copies from the published archive, not locally built or minified.
The root tarball's SHA-512 integrity was checked against the exact-version npm
metadata. Dependency versions/integrities came from the release-tagged lockfile:
https://raw.githubusercontent.com/markdown-it/markdown-it/15.0.3/package-lock.json
The published UMD map's `sources` entries identify its five bundled external
packages. Their archive SHA-512 values were checked against that lockfile, then
all archive license/notice files were retained verbatim. Archive SHA-256 and
integrity values appear in the full notices. This is lockfile and source-map
provenance, not a claim to reproduce upstream's build environment.

## Runtime and safety

`index.html` loads the local UMD before `app.js`. No runtime CDN, npm install,
JavaScript build step, or external highlighter is required. `../markdown.mjs`
uses `markdownit({html:false})`, the default preset and default link validator,
without plugins or validator overrides. Only task descriptions and comment bodies
are rendered. Titles, agents, prompts, memories, logs, forms, search, and API
payloads retain their existing plain-text/raw behavior.

Primary upstream guidance:
- https://github.com/markdown-it/markdown-it/blob/15.0.3/docs/usage.md
- https://github.com/markdown-it/markdown-it/blob/15.0.3/docs/safety.md
- https://markdown-it.github.io/markdown-it/interfaces/MarkdownIt.html

HTML stays escaped, and upstream's default validator rejects javascript:,
vbscript:, file:, and unsafe data: URLs. It intentionally allows certain raster
image data URLs. Normal Markdown images can make external requests. Do not
replace the validator with an always-true function, enable raw HTML, or inject
unescaped highlighter/plugin output.

## Updating

Replace only with an explicit pinned upstream distribution. Recheck archive
integrity, bundle/map byte equality and hashes, source-map dependency inventory,
release lockfile versions, and complete license notices. Re-run Markdown feature,
HTML/URL attack, raw-data, empty-state, and responsive light/dark browser checks.
