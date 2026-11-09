package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

// Old acceptance fixtures describe a whole dataset for both Graph and List.
// Adapt that dataset on the test server, never in the shipped client: browser
// tests receive the same bounded column wire contract as production.
func paginatedBoardFixture(t *testing.T, r *http.Request, data any) any {
	t.Helper()
	if r.URL.Path != "/api/board/list" {
		return data
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var board struct {
		Tasks    []map[string]any `json:"tasks"`
		Agents   []map[string]any `json:"agents"`
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &board); err != nil {
		t.Fatal(err)
	}
	names, standalone, taskAgents := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, a := range board.Agents {
		name, _ := a["name"].(string)
		names[name] = true
		if a["taskless_only"] == true {
			standalone[name] = true
		}
	}
	for _, s := range board.Sessions {
		name, _ := s["agent"].(string)
		names[name] = true
		if s["task_id"] == float64(0) {
			standalone[name] = true
		} else {
			taskAgents[name] = true
		}
	}
	ordered := []string{}
	for name := range names {
		if name != "" && (!standalone[name] || taskAgents[name]) {
			ordered = append(ordered, name)
		}
	}
	sort.Strings(ordered)
	keys := []string{"planning", "todo", "validated", "closed"}
	for _, name := range ordered {
		keys = append(keys, "agent:"+name)
	}
	pages := map[string]int{}
	_ = json.Unmarshal([]byte(r.URL.Query().Get("pages")), &pages)
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	activity := func(row map[string]any) time.Time {
		for _, key := range []string{"last_activity_at", "created_at"} {
			if v, ok := row[key].(string); ok {
				if at, err := time.Parse(time.RFC3339Nano, v); err == nil {
					return at
				}
			}
		}
		return time.Time{}
	}
	priority := func(row map[string]any) int {
		value := 0
		if logs, ok := row["session_logs"].([]any); ok {
			for _, raw := range logs {
				log := raw.(map[string]any)
				if log["kind"] == "blocker" {
					return 2
				}
				if log["kind"] == "question" {
					value = 1
				}
			}
		}
		return value
	}
	columns := []map[string]any{}
	for _, key := range keys {
		rows := []map[string]any{}
		for _, row := range board.Tasks {
			agent := strings.HasPrefix(key, "agent:")
			include := row["status"] == key
			fields := []string{fmt.Sprint(row["name"]), fmt.Sprint(row["description"]), fmt.Sprint(row["status"]), fmt.Sprintf("TSK-%.0f", row["id"])}
			if tags, ok := row["tags"].([]any); ok {
				for _, tag := range tags {
					fields = append(fields, fmt.Sprint(tag))
				}
			}
			if agent {
				include = false
			}
			for _, s := range board.Sessions {
				if s["task_id"] == row["id"] {
					fields = append(fields, fmt.Sprint(s["agent"]))
					if agent && s["agent"] == strings.TrimPrefix(key, "agent:") && s["status"] == "running" {
						include = true
					}
				}
			}
			if include && strings.Contains(strings.ToLower(strings.Join(fields, " ")), search) {
				rows = append(rows, row)
			}
		}
		promote := strings.HasPrefix(key, "agent:") || key == "planning" || key == "todo"
		sort.SliceStable(rows, func(i, j int) bool {
			if promote && priority(rows[i]) != priority(rows[j]) {
				return priority(rows[i]) > priority(rows[j])
			}
			a, b := activity(rows[i]), activity(rows[j])
			if !a.Equal(b) {
				return a.After(b)
			}
			return rows[i]["id"].(float64) < rows[j]["id"].(float64)
		})
		total := len(rows)
		maxPage := (total + 14) / 15
		if maxPage < 1 {
			maxPage = 1
		}
		page := pages[key]
		if page < 1 {
			page = 1
		}
		if page > maxPage {
			page = maxPage
		}
		start := (page - 1) * 15
		end := start + 15
		if end > total {
			end = total
		}
		columns = append(columns, map[string]any{"key": key, "total": total, "page": page, "page_size": 15, "tasks": rows[start:end]})
	}
	return map[string]any{"tasks": []any{}, "agents": board.Agents, "sessions": board.Sessions, "columns": columns}
}
