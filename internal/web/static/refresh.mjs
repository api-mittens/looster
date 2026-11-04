// Reconcile server-rendered fragments without treating live UI state as data.
// Keys are local to each parent. Callers clear the root when its task/namespace changes.
// Retained nodes keep user state. Removed data is removed, and selections in
// edited text are clamped to its new length rather than freezing server updates.
const key = node => {
  if(node.nodeType !== 1) return null;
  if(node.hasAttribute('data-refresh-key')) return `${node.localName}:key:${node.getAttribute('data-refresh-key')}`;
  if(node.id) return `${node.localName}:id:${node.id}`;
  if(node.localName === 'option') return `option:${node.getAttribute('value') ?? node.textContent}`;
  return ['data-key','data-comment-id','data-section','data-tag','data-agent','data-task','data-action'].map(a => node.hasAttribute(a) ? `${node.localName}:${a}:${node.getAttribute(a)}` : '').find(Boolean);
};
const compatible = (a,b) => a.nodeType === b.nodeType && a.nodeName === b.nodeName;

export function patchHTML(root, html) {
  const template = root.ownerDocument.createElement('template');
  template.innerHTML = html;
  const document = root.ownerDocument, active = root.contains(document.activeElement) ? document.activeElement : null;
  const ancestors = []; for(let node = root.parentElement; node; node = node.parentElement) ancestors.push(node);
  const scroll = [...ancestors,root,...root.querySelectorAll('*')].map(n => [n,n.scrollTop,n.scrollLeft]);
  const inputSelection = active && typeof active.selectionStart === 'number' ? [active.selectionStart,active.selectionEnd,active.selectionDirection] : null;
  const selection = document.getSelection();
  const ranges = selection ? Array.from({length:selection.rangeCount},(_,i) => {
    const r = selection.getRangeAt(i);
    return r.intersectsNode(root) ? [r.startContainer,r.startOffset,r.endContainer,r.endOffset] : null;
  }).filter(Boolean) : [];
  const direction = ranges.length ? [selection.anchorNode,selection.anchorOffset,selection.focusNode,selection.focusOffset] : null;
  reconcile(root,template.content);
  if(active?.isConnected && document.activeElement !== active) active.focus({preventScroll:true});
  if(inputSelection && active.isConnected) active.setSelectionRange(...inputSelection);
  for(const [node,top,left] of scroll) if(node.isConnected) {
    if(node.scrollTop !== top) node.scrollTop = top;
    if(node.scrollLeft !== left) node.scrollLeft = left;
  }
  if(ranges.length && ranges.every(([a,,b]) => a.isConnected && b.isConnected)) {
    selection.removeAllRanges();
    for(const [a,start,b,end] of ranges) {
      const r = document.createRange(), length = n => n.nodeType === 3 ? n.length : n.childNodes.length;
      r.setStart(a,Math.min(start,length(a))); r.setEnd(b,Math.min(end,length(b))); selection.addRange(r);
    }
    if(ranges.length === 1 && selection.setBaseAndExtent) {
      const [a,start,b,end] = direction, length = n => n.nodeType === 3 ? n.length : n.childNodes.length;
      selection.setBaseAndExtent(a,Math.min(start,length(a)),b,Math.min(end,length(b)));
    }
  }
}

function reconcile(parent, desired) {
  const old = [...parent.childNodes], keyed = new Map(old.map(n => [key(n),n]).filter(([k]) => k)), used = new Set();
  let cursor = parent.firstChild;
  for(const next of [...desired.childNodes]) {
    const k = key(next);
    let node = k ? keyed.get(k) : old.find(n => !used.has(n) && !key(n) && compatible(n,next));
    if(used.has(node)) node = null;
    if(!node || !compatible(node,next)) node = next.cloneNode(true);
    else if(node.nodeType === 1) {
      for(const attr of [...node.attributes]) if(!next.hasAttribute(attr.name) && !(node.localName === 'details' && attr.name === 'open')) node.removeAttribute(attr.name);
      for(const attr of next.attributes) if(!(node.localName === 'details' && attr.name === 'open') && node.getAttribute(attr.name) !== attr.value) node.setAttribute(attr.name,attr.value);
      // These elements own their children or live form drafts.
      if(!['workflow-graph','task-agent-graph','textarea','input'].includes(node.localName)) reconcile(node,next);
    } else if(node.nodeValue !== next.nodeValue) node.nodeValue = next.nodeValue;
    used.add(node);
    if(node !== cursor) parent.insertBefore(node,cursor);
    cursor = node.nextSibling;
  }
  for(const node of old) if(!used.has(node)) node.remove();
}
