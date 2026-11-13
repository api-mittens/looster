package web

import (
	"net/http"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
)

type performanceMetrics struct {
	Validated           int      `json:"validated"`
	Closed              int      `json:"closed"`
	TaskBlockers        int      `json:"task_blockers"`
	TaskQuestions       int      `json:"task_questions"`
	AutonomousBlockers  int      `json:"autonomous_blockers"`
	AutonomousQuestions int      `json:"autonomous_questions"`
	Ratio               *float64 `json:"ratio"`
}
type performanceDay struct {
	Date string `json:"date"`
	performanceMetrics
}
type performanceResponse struct {
	Days       []performanceDay   `json:"days"`
	Week       performanceMetrics `json:"week"`
	Timezone   string             `json:"timezone"`
	WindowDays int                `json:"window_days"`
}

func performanceStart(now time.Time) time.Time {
	return now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -29)
}
func buildPerformance(now time.Time, tasks []task.Task, logs []agent.SessionLog) performanceResponse {
	now = now.UTC()
	start, weekStart := performanceStart(now), now.Add(-7*24*time.Hour)
	result := performanceResponse{Days: make([]performanceDay, 30), Timezone: "UTC", WindowDays: 30}
	for i := range result.Days {
		result.Days[i].Date = start.AddDate(0, 0, i).Format("2006-01-02")
	}
	apply := func(at time.Time, increment func(*performanceMetrics)) {
		if at.Before(start) || at.After(now) {
			return
		}
		increment(&result.Days[int(at.Sub(start)/(24*time.Hour))].performanceMetrics)
		if !at.Before(weekStart) {
			increment(&result.Week)
		}
	}
	seenTasks := map[int64]bool{}
	for _, t := range tasks {
		if seenTasks[t.ID] {
			continue
		}
		seenTasks[t.ID] = true
		apply(t.LastActivityAt, func(m *performanceMetrics) {
			switch t.Status {
			case task.Validated:
				m.Validated++
			case task.Closed:
				m.Closed++
			}
		})
	}
	seenLogs := map[int64]bool{}
	for _, l := range logs {
		if seenLogs[l.ID] {
			continue
		}
		seenLogs[l.ID] = true
		apply(l.CreatedAt, func(m *performanceMetrics) {
			switch l.Kind {
			case "blocker":
				if l.TaskID == 0 {
					m.AutonomousBlockers++
				} else {
					m.TaskBlockers++
				}
			case "question":
				if l.TaskID == 0 {
					m.AutonomousQuestions++
				} else {
					m.TaskQuestions++
				}
			}
		})
	}
	ratio := func(m *performanceMetrics) {
		if m.Closed > 0 {
			v := float64(m.Validated) / float64(m.Closed)
			m.Ratio = &v
		}
	}
	for i := range result.Days {
		ratio(&result.Days[i].performanceMetrics)
	}
	ratio(&result.Week)
	return result
}
func registerPerformance(mux *http.ServeMux, repo Repository, write func(http.ResponseWriter, *http.Request, any, error)) {
	mux.HandleFunc("GET /api/performance", func(w http.ResponseWriter, r *http.Request) {
		source, ok := repo.(app.PerformanceRepository)
		if !ok {
			http.Error(w, "Performance data unavailable", http.StatusNotImplemented)
			return
		}
		now := time.Now().UTC()
		tasks, logs, err := source.PerformanceData(r.Context(), performanceStart(now), now)
		if err != nil {
			write(w, r, nil, err)
			return
		}
		write(w, r, buildPerformance(now, tasks, logs), nil)
	})
}
