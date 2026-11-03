// Collapse transport envelopes into a readable transcript, preserving token spacing.
export function appendOutput(state, body) {
  let item;
  try { item = JSON.parse(body); } catch { state.text += body; return; }
  const type = item.type || '';
  if (type === 'start') { state.streamed = false; if(state.text) state.text += '\n\n'; return; }
  const delta = typeof item.delta === 'string' ? item.delta : item.delta?.text ?? item.delta?.content;
  const text = typeof delta === 'string' ? delta : typeof item.text === 'string' ? item.text : typeof item.content === 'string' ? item.content : '';
  if (type === 'done') {
    // done.text repeats the accumulated assistant answer on this transport.
    if (!state.streamed && text) state.text += text;
    state.streamed = false;
    return;
  }
  if (/delta|text|token|assistant|message/.test(type) && text) {
    state.text += text; state.streamed = true; return;
  }
  if (type === 'error') { state.text += '\n[Error] '+(text || item.message || 'Agent failed')+'\n'; return; }
  if (/tool/.test(type)) {
    const name = item.name || item.tool_name || item.tool?.name || '';
    const content = item.output ?? item.result ?? item.input ?? item.arguments ?? item.text ?? '';
    state.text += '\n['+type.replaceAll('_',' ')+(name ? ': '+name : '')+']\n';
    if (content) state.text += (typeof content === 'string' ? content : JSON.stringify(content,null,2))+'\n';
  }
}
