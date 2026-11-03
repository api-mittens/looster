package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/task"
)

func printTaskOverview(w io.Writer, tasks []task.Task, sessions []agent.Session) error {
	rows := [][]string{{"TASK", "STATUS", "NAME", "AGENT", "STATE", "SESSION"}}
	byTask := make(map[int64][]agent.Session)
	for _, s := range sessions {
		byTask[s.TaskID] = append(byTask[s.TaskID], s)
	}
	seen := make(map[int64]bool)
	for _, t := range tasks {
		seen[t.ID] = true
		if len(byTask[t.ID]) == 0 {
			rows = append(rows, []string{task.ID(t.ID), string(t.Status), t.Name, "-", "-", "-"})
		}
		for _, s := range byTask[t.ID] {
			rows = append(rows, []string{task.ID(t.ID), string(t.Status), t.Name, s.Agent, s.Status, s.SessionID})
		}
	}
	// Keep sessions visible if tasks changed between the two database reads.
	for _, s := range sessions {
		if !seen[s.TaskID] {
			rows = append(rows, []string{task.ID(s.TaskID), "-", "-", s.Agent, s.Status, s.SessionID})
		}
	}
	widths := make([]int, len(rows[0]))
	limits := []int{20, 16, 44, 24, 12, 40}
	for _, row := range rows {
		for i, value := range row {
			value = strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return ' '
				}
				return r
			}, value)
			if value == "" {
				value = "-"
			}
			if utf8.RuneCountInString(value) > limits[i] {
				value = string([]rune(value)[:limits[i]-3]) + "..."
			}
			row[i] = value
			widths[i] = max(widths[i], utf8.RuneCountInString(value))
		}
	}
	var b strings.Builder
	border := func() {
		b.WriteByte('+')
		for _, width := range widths {
			b.WriteString(strings.Repeat("-", width+2))
			b.WriteByte('+')
		}
		b.WriteByte('\n')
	}
	border()
	for n, row := range rows {
		b.WriteByte('|')
		for i, value := range row {
			fmt.Fprintf(&b, " %-*s |", widths[i], value)
		}
		b.WriteByte('\n')
		if n == 0 {
			border()
		}
	}
	if len(rows) > 1 {
		border()
	} else {
		b.WriteString("No tasks or sessions.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
