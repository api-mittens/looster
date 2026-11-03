# Vendored D3

- Upstream: https://github.com/d3/d3
- Distribution: `d3` version **7.9.0**, `dist/d3.min.js` from the published npm tarball.
- Source archive: https://registry.npmjs.org/d3/-/d3-7.9.0.tgz
- Umbrella license: ISC, retained verbatim in `D3-LICENSE.txt` and upstream file header.
- Complete notices: **`D3-THIRD-PARTY-LICENSES.txt`**, which must accompany redistribution of this full bundle. The umbrella license alone is not sufficient.
- SHA-256 of `d3.v7.9.0.min.js`: `f2094bbf6141b359722c4fe454eb6c4b0f0e42cc10cc7af921fc158fceb86539`.

The upstream distribution is kept unmodified. It is served from the embedded Go
assets, not a runtime CDN. Application development and deployment require no
JavaScript build step or npm install. Only force-related exports are used via
`../d3-force-adapter.mjs`, but **the complete D3 bundle is shipped**, so notices
cover the complete bundle rather than only the exports used by the application.

## Notice scope and provenance

`D3-THIRD-PARTY-LICENSES.txt` contains an exact **38-package** runtime inventory,
including `d3` itself, each package's tarball URL, declared license, archive
SHA-256, integrity value, dependency edges, and verbatim license/notice files.
It also contains the full Apache License 2.0 referenced by ColorBrewer's notice.
The inventory distinguishes package-level license metadata from additional
licenses and copyrights inside the actual license files. In particular:

- `d3-delaunay@6.0.4`: Observable, Inc. and Mapbox ISC notices.
- `delaunator@5.0.1`: Mapbox ISC notice.
- `internmap@2.0.3`: Mike Bostock ISC notice.
- `robust-predicates@3.0.2`: full Unlicense dedication and disclaimer.
- `d3-ease@3.0.1`: BSD-3-Clause, including Robert Penner's copyright.
- `d3-geo@3.1.1`: ISC plus the GeographicLib MIT notice, including Charles Karney's copyright.
- `d3-scale-chromatic@3.1.0`: ISC plus the ColorBrewer Apache-2.0 notice, including Cynthia Brewer, Mark Harrower, and The Pennsylvania State University.

Primary version sources are the release-tagged runtime dependency declarations
and exact Yarn lock, not today's semver resolution:

- https://raw.githubusercontent.com/d3/d3/v7.9.0/package.json
- https://raw.githubusercontent.com/d3/d3/v7.9.0/yarn.lock
- https://registry.npmjs.org/d3/7.9.0

A one-off Go standard-library audit followed every published `dependencies`
selector through that lock, downloaded all 38 exact npm tarballs, and checked
every dependency archive against both its lockfile SRI and resolved SHA-1.
The root `d3` archive was checked against the exact npm version's SRI and SHA-1.
Its `dist/d3.min.js` and `LICENSE` were byte-compared with the checked-in bundle
and `D3-LICENSE.txt`. No Node/npm commands or JavaScript tests were used.

A separate traversal of static ESM imports/exports from `d3/src/index.js`
resolved **34 browser-reachable packages and 566 source modules**, including
`d3` and the `d3-transition` peer dependency on `d3-selection@3.0.0` (range
`2 - 3`). Four additional runtime packages are CLI-only dependencies of
`d3-dsv`: `commander@7.2.0`, `iconv-lite@0.6.3`, `rw@1.3.3`, and
`safer-buffer@2.1.2`. Their notices are included conservatively for complete
published-package runtime closure, although they are not browser imports.
Development/build dependencies are not included.

The source traversal is not a rebuild, tree-shaking analysis, or a module
manifest recovered from minified output. Exact subdependency versions are
attributed to the release's lockfile, with the unchanged published bundle
verified independently by byte comparison. This documents the evidence and
its boundary rather than claiming to reconstruct upstream's build environment.

## Updating

Updating D3 is an explicit replacement of the pinned distribution and its
notices. Repeat the exact-version runtime graph and archive-integrity checks,
retain every package's complete notices (including secondary license sections),
update this README and the inventory, and verify the new bundle hash. Run the
project's graph/browser acceptance checks when changing executable assets.
Do not remove licenses merely because the application uses only a subset of
exports from the full distribution.
