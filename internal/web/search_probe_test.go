//go:build uitest

package web

// Behavioral probes execute only through their Go test entrypoints.
const searchProbe = `import {fuzzyScore,searchItems} from './static/search.mjs';

test('fuzzy search matches ordered, case insensitive characters', () => {
  assert.ok(fuzzyScore('tsk1','TSK-1 · Example') >= 0);
  assert.equal(fuzzyScore('xyz','Example'),-1);
  assert.ok(fuzzyScore('design','design') > fuzzyScore('design','A design task'));
});

test('search ranks closer matches first', () => {
  const found=searchItems([{label:'distant',search:'A design task'},{label:'exact',search:'design'}],'design');
  assert.equal(found[0].label,'exact');
});
`
