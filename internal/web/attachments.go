package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/event"
	"github.com/api-mittens/looster/internal/namespace"
	"github.com/api-mittens/looster/internal/task"
)

const maxAttachmentBytes = 10 << 20
const maxImageDimension = 8192
const maxImagePixels = 16 << 20

type attachmentRepository interface {
	Attachments(context.Context, int64) ([]task.Attachment, error)
	AddAttachment(context.Context, task.Attachment) (task.Attachment, error)
}

type attachmentView struct {
	ID          int64  `json:"id"`
	TaskID      int64  `json:"task_id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	EventID     *int64 `json:"event_id,omitempty"`
	Previewable bool   `json:"previewable"`
}

func attachmentJSON(a task.Attachment) attachmentView {
	ext := strings.ToLower(path.Ext(a.Name))
	return attachmentView{ID: a.ID, TaskID: a.TaskID, Name: a.Name, Size: a.Size, EventID: a.EventID, Previewable: ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif"}
}

func attachmentPrefix(selected string, id int64) string {
	prefix := "tasks/" + task.ID(id) + "/"
	if selected != namespace.Default {
		prefix = "namespaces/" + selected + "/" + prefix
	}
	return prefix
}

func attachmentKeyInScope(key, prefix string) bool {
	return strings.HasPrefix(key, prefix) && path.Clean(key) == key && !strings.Contains(key, "\\") && len(key) > len(prefix)
}

func attachmentError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, app.ErrNotFound) {
		code = http.StatusNotFound
	}
	if errors.Is(err, app.ErrForbidden) {
		code = http.StatusForbidden
	}
	http.Error(w, http.StatusText(code), code)
}

// Browser writes must originate on this exact origin. Requests without Origin
// remain available to non-browser API clients, unless Fetch Metadata says otherwise.
func attachmentSameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && u.Scheme == scheme && strings.EqualFold(u.Host, r.Host) && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

func registerAttachments(mux *http.ServeMux, repo Repository, objects app.Objects, selected string) {
	serve := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodPost && !attachmentSameOrigin(r) {
			http.Error(w, "Cross-origin write denied", 403)
			return
		}
		id, err := task.ParseID(r.PathValue("id"))
		if err != nil || id <= 0 {
			http.Error(w, "Invalid task ID", 400)
			return
		}
		if _, err = repo.Task(r.Context(), id); err != nil {
			attachmentError(w, err)
			return
		}
		files, ok := repo.(attachmentRepository)
		if !ok || objects == nil {
			http.Error(w, "S3 attachment storage unavailable", 503)
			return
		}
		if r.Method == http.MethodPost {
			uploadAttachment(w, r, files, repo, objects, id, attachmentPrefix(selected, id))
			return
		}
		all, err := files.Attachments(r.Context(), id)
		if err != nil {
			attachmentError(w, err)
			return
		}
		if r.PathValue("attachment") == "" {
			out := []attachmentView{}
			for _, a := range all {
				if a.TaskID == id && attachmentKeyInScope(a.Key, attachmentPrefix(selected, id)) {
					out = append(out, attachmentJSON(a))
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		aid, err := strconv.ParseInt(r.PathValue("attachment"), 10, 64)
		if err != nil || aid <= 0 {
			http.Error(w, "Invalid attachment ID", 400)
			return
		}
		for _, a := range all {
			if a.ID != aid || a.TaskID != id || !attachmentKeyInScope(a.Key, attachmentPrefix(selected, id)) {
				continue
			}
			body, err := objects.Get(r.Context(), a.Key)
			if err != nil {
				attachmentError(w, err)
				return
			}
			defer body.Close()
			data, err := io.ReadAll(io.LimitReader(body, maxAttachmentBytes+1))
			if err != nil {
				attachmentError(w, err)
				return
			}
			if len(data) > maxAttachmentBytes {
				http.Error(w, "Attachment too large", 413)
				return
			}
			disposition := "attachment"
			contentType := "application/octet-stream"
			if strings.HasSuffix(r.URL.Path, "/preview") {
				cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
				if err != nil || (format != "png" && format != "jpeg" && format != "gif") || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxImageDimension || cfg.Height > maxImageDimension || int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
					http.Error(w, "Preview unavailable", 415)
					return
				}
				img, _, err := image.Decode(bytes.NewReader(data))
				if err != nil {
					http.Error(w, "Preview unavailable", 415)
					return
				}
				var encoded bytes.Buffer
				if err := png.Encode(&encoded, img); err != nil {
					attachmentError(w, err)
					return
				}
				data = encoded.Bytes()
				contentType, disposition = "image/png", "inline"
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": safeAttachmentName(a.Name)}))
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data)
			return
		}
		http.NotFound(w, r)
	}
	mux.HandleFunc("GET /api/tasks/{id}/attachments", serve)
	mux.HandleFunc("POST /api/tasks/{id}/attachments", serve)
	mux.HandleFunc("GET /api/tasks/{id}/attachments/{attachment}/download", serve)
	mux.HandleFunc("GET /api/tasks/{id}/attachments/{attachment}/preview", serve)
}

func safeAttachmentName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." || name == "/" {
		return "attachment"
	}
	if len(name) > 255 {
		return "attachment"
	}
	return name
}

func uploadAttachment(w http.ResponseWriter, r *http.Request, repo attachmentRepository, dashboard Repository, objects app.Objects, id int64, prefix string) {
	// Allow bounded multipart framing overhead in addition to the file limit.
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentBytes+(64<<10))
	err := r.ParseMultipartForm(maxAttachmentBytes + (64 << 10))
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "Upload too large", 413)
		} else {
			http.Error(w, "Invalid multipart upload", 400)
		}
		return
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 {
		http.Error(w, "Exactly one file is required", 400)
		return
	}
	var eventID *int64
	for key, values := range r.MultipartForm.Value {
		if key != "event_id" || len(values) != 1 {
			http.Error(w, "Invalid upload fields", 400)
			return
		}
		parsed, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || parsed <= 0 {
			http.Error(w, "Invalid comment ID", 400)
			return
		}
		events, err := dashboard.Logs(r.Context(), event.Filter{TaskID: id, Kinds: []string{"comment"}, After: parsed - 1, Until: parsed, Limit: 1})
		if err != nil {
			attachmentError(w, err)
			return
		}
		valid := false
		for _, e := range events {
			if e.ID == parsed && e.TaskID != nil && *e.TaskID == id && e.Kind == "comment" {
				valid = true
			}
		}
		if !valid {
			http.Error(w, "Comment does not belong to task", 400)
			return
		}
		eventID = &parsed
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File is required", 400)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAttachmentBytes+1))
	if err != nil {
		http.Error(w, "Could not read upload", 400)
		return
	}
	if len(data) > maxAttachmentBytes {
		http.Error(w, "Upload too large", 413)
		return
	}
	token := make([]byte, 24)
	if _, err = rand.Read(token); err != nil {
		attachmentError(w, err)
		return
	}
	key := prefix + hex.EncodeToString(token) + "/" + safeAttachmentName(header.Filename)
	cleanup := func() {
		if d, ok := objects.(interface {
			Delete(context.Context, string) error
		}); ok {
			_ = d.Delete(context.WithoutCancel(r.Context()), key)
		}
	}
	if err = objects.Put(r.Context(), key, bytes.NewReader(data), int64(len(data))); err != nil {
		cleanup()
		attachmentError(w, err)
		return
	}
	a, err := repo.AddAttachment(r.Context(), task.Attachment{TaskID: id, EventID: eventID, Name: safeAttachmentName(header.Filename), Key: key, Size: int64(len(data))})
	if err != nil {
		cleanup()
		attachmentError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(attachmentJSON(a))
}
