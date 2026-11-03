// Score ordered characters, rewarding word starts and adjacent matches.
export function fuzzyScore(query, value) {
  const needle = query.trim().toLowerCase(), hay = String(value || '').toLowerCase();
  if (!needle) return 0;
  let at = 0, score = 0, previous = -2;
  for (const character of needle) {
    const found = hay.indexOf(character, at);
    if (found < 0) return -1;
    score += 1 + (found === previous + 1 ? 3 : 0) + (found === 0 || /[\s/._:#-]/.test(hay[found - 1]) ? 4 : 0);
    previous = found;
    at = found + 1;
  }
  return score - hay.length / 100;
}

export function searchItems(items, query) {
  return items.map(item => ({item, score: fuzzyScore(query, item.search)}))
    .filter(result => result.score >= 0)
    .sort((a,b) => b.score - a.score || a.item.label.localeCompare(b.item.label))
    .map(result => result.item);
}
