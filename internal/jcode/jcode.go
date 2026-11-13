// Package jcode adapts Jcode's headless NDJSON command and saved session format.
package jcode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/namespace"
)

type Harness struct {
	Binary      string
	SessionsDir string
	DatabaseURL string
	Objects     app.Objects
	Environment map[string]string
}

var _ app.Harness = (*Harness)(nil)
var sessionName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (h *Harness) Run(ctx context.Context, r app.Run, emit func(app.Output) error) (app.Result, error) {
	if r.Namespace == "" {
		r.Namespace = namespace.Default
	}
	if err := namespace.Validate(r.Namespace); err != nil {
		return app.Result{}, err
	}
	dir := h.SessionsDir
	if dir == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return app.Result{}, e
		}
		dir = filepath.Join(home, ".jcode", "sessions")
	}
	if r.SessionID != "" {
		if !sessionName.MatchString(r.SessionID) {
			return app.Result{}, errors.New("invalid saved session ID")
		}
		path := filepath.Join(dir, r.SessionID+".json")
		if _, e := os.Stat(path); os.IsNotExist(e) {
			if h.Objects == nil {
				return app.Result{}, errors.New("saved session is missing locally and no archive store is configured")
			}
			prefix := r.ArchivePrefix
			if prefix == "" {
				prefix = fmt.Sprintf("sessions/%s/TSK-%d", r.Agent, r.Task.ID)
				if r.Namespace != namespace.Default {
					prefix = "namespaces/" + r.Namespace + "/" + prefix
				}
			}
			key := prefix + "/" + r.SessionID + ".json"
			archive, e := h.Objects.Get(ctx, key)
			if e != nil {
				return app.Result{}, fmt.Errorf("restore session archive: %w", e)
			}
			history, readErr := io.ReadAll(io.LimitReader(archive, 256*1024*1024+1))
			closeErr := archive.Close()
			if readErr != nil {
				return app.Result{}, readErr
			}
			if closeErr != nil {
				return app.Result{}, closeErr
			}
			if len(history) > 256*1024*1024 || !json.Valid(history) {
				return app.Result{}, errors.New("invalid or oversized session archive")
			}
			if e = os.MkdirAll(dir, 0700); e != nil {
				return app.Result{}, e
			}
			f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return app.Result{}, e
			}
			_, writeErr := f.Write(history)
			closeErr = f.Close()
			if writeErr != nil {
				_ = os.Remove(path)
				return app.Result{}, writeErr
			}
			if closeErr != nil {
				_ = os.Remove(path)
				return app.Result{}, closeErr
			}
		} else if e != nil {
			return app.Result{}, e
		}
	}
	if r.Workdir != "" {
		if e := os.MkdirAll(r.Workdir, 0700); e != nil {
			return app.Result{}, e
		}
	}
	binary := h.Binary
	if binary == "" {
		binary = "jcode"
	}
	args := []string{"run", "--ndjson", "--no-update", "--quiet"}
	if r.Workdir != "" {
		args = append(args, "-C", r.Workdir)
	}
	if r.SessionID != "" {
		args = append(args, "--resume", r.SessionID)
	}
	args = append(args, "--", r.Prompt)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = 5 * time.Second
	// Share the connection settings, but force the child into its agent role.
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		_, replaced := h.Environment[key]
		if key != "TRACEPARENT" && key != "TRACESTATE" && key != "BAGGAGE" && key != "LOOSTER_NAMESPACE" && key != "LOOSTER_DATABASE_URL" && key != "PATH" && key != "LOOSTER_ROLE" && key != "LOOSTER_AGENT" && key != "LOOSTER_TASK_ID" && key != "LOOSTER_JOB_ID" && key != "LOOSTER_SESSION_ID" && !strings.HasPrefix(key, "LOOSTER_AGENT_DATABASE_URLS_") && !replaced {
			cmd.Env = append(cmd.Env, v)
		}
	}
	for key, value := range h.Environment {
		if key == "TRACEPARENT" || key == "TRACESTATE" || key == "BAGGAGE" || key == "LOOSTER_NAMESPACE" || key == "LOOSTER_ROLE" || key == "LOOSTER_AGENT" || key == "LOOSTER_TASK_ID" || key == "LOOSTER_JOB_ID" || key == "LOOSTER_SESSION_ID" || key == "LOOSTER_DATABASE_URL" || strings.HasPrefix(key, "LOOSTER_AGENT_DATABASE_URLS_") {
			continue
		}
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	executable, e := os.Executable()
	if e != nil {
		return app.Result{}, e
	}
	cmd.Env = append(cmd.Env, "PATH="+filepath.Dir(executable)+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, "LOOSTER_JOB_ID="+fmt.Sprint(r.JobID))
	if r.SessionID != "" {
		cmd.Env = append(cmd.Env, "LOOSTER_SESSION_ID="+r.SessionID)
	}
	cmd.Env = append(cmd.Env, "LOOSTER_NAMESPACE="+r.Namespace, "LOOSTER_DATABASE_URL="+h.DatabaseURL, "LOOSTER_ROLE=agent", "LOOSTER_AGENT="+r.Agent)
	if r.Task.ID > 0 {
		cmd.Env = append(cmd.Env, "LOOSTER_TASK_ID="+fmt.Sprintf("TSK-%d", r.Task.ID))
	}
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		return app.Result{}, e
	}
	var stderr cappedBuffer
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		return app.Result{}, e
	}
	scan := bufio.NewScanner(pipe)
	scan.Buffer(make([]byte, 4096), 16*1024*1024)
	sid := r.SessionID
	var streamErr error
	done := false
	for scan.Scan() {
		line := scan.Text()
		var v struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
		}
		if e = json.Unmarshal([]byte(line), &v); e != nil {
			streamErr = fmt.Errorf("invalid Jcode NDJSON: %w", e)
			break
		}
		if v.SessionID != "" {
			sid = v.SessionID
		}
		if v.Type == "done" {
			done = true
		}
		if v.Type == "error" {
			streamErr = fmt.Errorf("Jcode error: %s", line)
			break
		}
		if e = emit(app.Output{Text: line, SessionID: sid}); e != nil {
			streamErr = e
			break
		}
	}
	if streamErr == nil {
		streamErr = scan.Err()
	}
	if streamErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return app.Result{}, ctx.Err()
	}
	if streamErr != nil {
		return app.Result{}, streamErr
	}
	if waitErr != nil {
		return app.Result{}, fmt.Errorf("jcode: %w: %s", waitErr, stderr.String())
	}
	if !done {
		return app.Result{}, errors.New("Jcode exited without a done event")
	}
	if !sessionName.MatchString(sid) {
		return app.Result{}, fmt.Errorf("invalid or missing Jcode session ID %q", sid)
	}
	history, e := os.ReadFile(filepath.Join(dir, sid+".json"))
	if e != nil {
		return app.Result{}, fmt.Errorf("read full Jcode session history: %w", e)
	}
	if !json.Valid(history) {
		return app.Result{}, errors.New("Jcode session history is not valid JSON")
	}
	return app.Result{SessionID: sid, History: history}, nil
}

type cappedBuffer struct{ bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := 65536 - b.Len()
	if left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
