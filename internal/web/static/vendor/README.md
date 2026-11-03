# Vendored D3

- Upstream: https://github.com/d3/d3
- Distribution: `d3` version **7.9.0**, `dist/d3.min.js` from the published npm tarball.
- Source archive: https://registry.npmjs.org/d3/-/d3-7.9.0.tgz
- License: ISC, retained verbatim in `D3-LICENSE.txt` and upstream file header.
- SHA-256 of `d3.v7.9.0.min.js`: `f2094bbf6141b359722c4fe454eb6c4b0f0e42cc10cc7af921fc158fceb86539`.

The upstream distribution is kept unmodified. It is served from the embedded Go
assets, not a runtime CDN. Application development and deployment require no
JavaScript build step or npm install. Only force-related exports are used via
`../d3-force-adapter.mjs`. Updating this dependency is an explicit replacement
of the pinned distribution and license followed by the graph/browser tests.
