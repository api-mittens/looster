// Browser-ready upstream distribution, vendored locally with its ISC license.
// Browsers execute its UMD global branch, Node's test runner uses its CJS branch.
// Neither environment needs bundling, package installation or a runtime CDN.
import * as distribution from './vendor/d3.v7.9.0.min.js';
const d3 = globalThis.d3 ?? distribution.default;
if (!d3?.forceSimulation) throw new Error('Local D3 force distribution could not be loaded');
export const {forceSimulation, forceLink, forceManyBody, forceCollide, forceX, forceY, forceCenter, randomLcg} = d3;
