const esc = value => String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));

// Construct only same-origin backend routes, never trust an object key or filename
// as a URL. The backend validates bytes before serving an inline image.
export function attachmentURL(taskID, attachmentID, action, namespace = '') {
  const path = `/api/tasks/TSK-${encodeURIComponent(taskID)}/attachments/${encodeURIComponent(attachmentID)}/${action === 'preview' ? 'preview' : 'download'}`;
  return path + (namespace ? `?namespace=${encodeURIComponent(namespace)}` : '');
}

export function attachmentsHTML(attachments, taskID, eventID, namespace = '') {
  const files = (attachments || []).filter(file => eventID == null ? file.event_id == null : String(file.event_id) === String(eventID));
  if (!files.length) return '';
  return `<ul class="attachments" aria-label="Attachments">${files.map(file => {
    const download = esc(attachmentURL(taskID,file.id,'download',namespace));
    const preview = file.previewable === true ? `<img class="attachment-preview" src="${esc(attachmentURL(taskID,file.id,'preview',namespace))}" alt="Preview of ${esc(file.name)}" loading="lazy">` : '';
    return `<li>${preview}<a href="${download}" download>${esc(file.name || 'Unnamed file')}</a><span class="muted">${esc(file.size)} bytes</span></li>`;
  }).join('')}</ul>`;
}

export function attachmentUploadHTML(eventID = null) {
  const target = eventID == null ? 'description' : `comment ${eventID}`;
  return `<form class="attachment-upload"${eventID == null ? '' : ` data-event-id="${esc(eventID)}"`}><label>Attach files to ${esc(target)} <input type="file" name="files" multiple required></label><button type="submit">Upload files</button><p class="attachment-result form-error" role="status" aria-live="polite"></p></form>`;
}
