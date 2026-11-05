package behaviour

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/tag"
	"github.com/api-mittens/looster/internal/task"
)

const MaxIntervalSeconds = int64((1<<63 - 1) / time.Second)

type Rule struct {
	ID              int64  `json:"id,omitempty"`
	Trigger         string `json:"trigger"`
	Source          string `json:"source,omitempty"`
	Target          string `json:"target"`
	IntervalSeconds int64  `json:"interval_seconds,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
	TagPattern      string `json:"tag_pattern"`
	TagNegated      bool   `json:"tag_negated"`
}

func (r Rule) Validate() error {
	if r.Trigger == "tag" {
		if !tag.ValidName(r.Target) || r.Source != "" || r.IntervalSeconds != 0 || r.TagPattern != "" || r.TagNegated {
			return fmt.Errorf("TAG requires a valid name and a quoted description")
		}
		return nil
	}
	if r.TagPattern != "" {
		if !tagPattern.MatchString(r.TagPattern) {
			return fmt.Errorf("invalid tag wildcard %q", r.TagPattern)
		}
		if r.Trigger == "every" || r.Trigger == "agent" || r.Trigger == "prompt" {
			return fmt.Errorf("WHEN TAGGED only applies to ON and AFTER rules")
		}
	} else if r.TagNegated {
		return fmt.Errorf("NOT TAGGED requires a pattern")
	}
	if !agent.ValidName(r.Target) {
		return fmt.Errorf("invalid target agent name %q", r.Target)
	}
	switch r.Trigger {
	case "planning", "todo", "validated", "closed":
		if r.Source != "" || r.IntervalSeconds != 0 || r.Prompt != "" {
			return fmt.Errorf("ON status rules only specify a target agent")
		}
	case "completed":
		if !agent.ValidName(r.Source) || r.IntervalSeconds != 0 || r.Prompt != "" {
			return fmt.Errorf("AFTER rules require a source and target agent")
		}
	case "stop_planning", "stop_todo", "stop_validated", "stop_closed":
		if r.Source != "" || r.IntervalSeconds != 0 || r.Prompt != "" {
			return fmt.Errorf("AFTER STATUS STOP PINGING only specifies a target agent")
		}
	case "prompt", "agent":
		if r.Source != "" || r.IntervalSeconds != 0 || r.Trigger == "prompt" && strings.TrimSpace(r.Prompt) == "" {
			return fmt.Errorf("AGENT requires a quoted instruction; legacy PROMPT requires nonempty instructions")
		}
	case "every":
		if r.Source != "" || r.IntervalSeconds < 1 || r.IntervalSeconds > MaxIntervalSeconds || strings.TrimSpace(r.Prompt) == "" {
			return fmt.Errorf("EVERY requires a positive interval and a nonempty quoted prompt")
		}
	default:
		return fmt.Errorf("unknown trigger %q", r.Trigger)
	}
	return nil
}

var definition = regexp.MustCompile(`^(PROMPT|AGENT|TAG)\s+(\S+)\s+WITH\s+("(?:[^"\\]|\\.)*")\s*$`)

var recurring = regexp.MustCompile(`^EVERY\s+([0-9]+)\s+(SECONDS?|MINUTES?|HOURS?|DAYS?)\s+PING\s+(\S+)\s+WITH\s+("(?:[^"\\]|\\.)*")\s*$`)
var tagCondition = regexp.MustCompile(`\s+WHEN\s+(NOT\s+)?TAGGED\s+([A-Za-z*/-]+)(\s+|$)`)
var tagPattern = regexp.MustCompile(`^[A-Za-z*/-]+$`)

func parseRule(s string) (Rule, error) {
	f := strings.Fields(s)
	if len(f) > 0 && (f[0] == "ON" || f[0] == "AFTER") {
		if loc := tagCondition.FindStringSubmatchIndex(s); loc != nil {
			pattern := s[loc[4]:loc[5]]
			neg := loc[2] >= 0
			base := strings.TrimSpace(s[:loc[0]] + " " + s[loc[1]:])
			if strings.Contains(base, " WHEN ") {
				return Rule{}, fmt.Errorf("only one WHEN condition is allowed")
			}
			r, err := parseRule(base)
			if err != nil {
				return Rule{}, err
			}
			r.TagPattern = pattern
			r.TagNegated = neg
			return r, r.Validate()
		}
	}
	var r Rule
	switch {
	case len(f) == 4 && f[0] == "ON" && f[2] == "PING":
		status := task.Status(strings.ToLower(f[1]))
		if f[1] != strings.ToUpper(f[1]) || status.Validate() != nil {
			return Rule{}, fmt.Errorf("ON requires PLANNING, TODO, VALIDATED, or CLOSED")
		}
		r = Rule{Trigger: string(status), Target: f[3]}
	case len(f) == 4 && f[0] == "AFTER" && f[2] == "PING":
		r = Rule{Trigger: "completed", Source: f[1], Target: f[3]}
	case len(f) == 5 && f[0] == "AFTER" && f[2] == "COMPLETED" && f[3] == "PING":
		r = Rule{Trigger: "completed", Source: f[1], Target: f[4]}
	case len(f) == 5 && f[0] == "AFTER" && f[2] == "STOP" && f[3] == "PINGING":
		status := task.Status(strings.ToLower(f[1]))
		if f[1] == "VERIFIED" {
			status = task.Validated
		}
		if f[1] != strings.ToUpper(f[1]) || status.Validate() != nil {
			return Rule{}, fmt.Errorf("AFTER STATUS STOP PINGING requires PLANNING, TODO, VALIDATED, VERIFIED, or CLOSED")
		}
		r = Rule{Trigger: "stop_" + string(status), Target: f[4]}
	default:
		if m := definition.FindStringSubmatch(s); m != nil {
			r = Rule{Trigger: strings.ToLower(m[1]), Target: m[2]}
			if err := json.Unmarshal([]byte(m[3]), &r.Prompt); err != nil {
				return Rule{}, fmt.Errorf("invalid quoted agent prompt: %w", err)
			}
			if err := r.Validate(); err != nil {
				return Rule{}, err
			}
			return r, nil
		}
		m := recurring.FindStringSubmatch(s)
		if m == nil {
			return Rule{}, fmt.Errorf("expected AGENT name WITH \"instructions\", TAG name WITH \"description\", ON STATUS PING agent, AFTER agent [COMPLETED] PING agent, AFTER STATUS STOP PINGING agent (optionally WHEN [NOT] TAGGED pattern), or EVERY n UNITS PING agent WITH \"prompt\"")
		}
		n, err := strconv.ParseInt(m[1], 10, 64)
		unit := map[string]int64{"SECOND": 1, "MINUTE": 60, "HOUR": 3600, "DAY": 86400}[strings.TrimSuffix(m[2], "S")]
		if err != nil || n < 1 || n > MaxIntervalSeconds/unit {
			return Rule{}, fmt.Errorf("interval must be a positive whole number fitting a Go duration")
		}
		r = Rule{Trigger: "every", Target: m[3], IntervalSeconds: n * unit}
		if err = json.Unmarshal([]byte(m[4]), &r.Prompt); err != nil {
			return Rule{}, fmt.Errorf("invalid quoted prompt: %w", err)
		}
	}
	if err := r.Validate(); err != nil {
		return Rule{}, err
	}
	return r, nil
}

// Parse accepts semicolon-delimited statements and legacy line-delimited rules.
// Incomplete rules may continue onto following lines; quoted prompts use JSON
// escaping. A batch is fully validated before any caller persists it.
func Parse(src string) ([]Rule, error) {
	rules := []Rule{}
	seen := map[Rule]bool{}
	definitions := map[string]string{}
	var statement strings.Builder
	line, startLine := 1, 0
	quoted, escaped, comment := false, false, false
	flush := func(required bool) error {
		text := strings.TrimSpace(statement.String())
		if text == "" {
			statement.Reset()
			startLine = 0
			return nil
		}
		r, err := parseRule(text)
		if err != nil {
			if required {
				return fmt.Errorf("line %d: %w", startLine, err)
			}
			return nil
		}
		if r.Trigger == "prompt" || r.Trigger == "agent" || r.Trigger == "tag" {
			key := r.Trigger + ":" + r.Target
			if r.Trigger == "prompt" {
				key = "agent:" + r.Target
			}
			if previous, ok := definitions[key]; ok && previous != r.Prompt {
				kind := "AGENT"
				if r.Trigger == "tag" {
					kind = "TAG"
				}
				return fmt.Errorf("line %d: conflicting %s definitions for %q", startLine, kind, r.Target)
			}
			definitions[key] = r.Prompt
		}
		if !seen[r] {
			rules = append(rules, r)
			seen[r] = true
		}
		statement.Reset()
		startLine = 0
		return nil
	}
	for i := 0; i < len(src); i++ {
		ch := src[i]
		if ch == '\n' {
			if quoted {
				return nil, fmt.Errorf("line %d: raw newline in quoted prompt; use \\n", startLine)
			}
			comment = false
			if !conditionContinues(src[i+1:]) {
				if err := flush(false); err != nil {
					return nil, err
				}
			}
			if statement.Len() > 0 {
				statement.WriteByte(' ')
			}
			line++
			continue
		}
		if comment {
			continue
		}
		if !quoted {
			if ch == '-' && i+1 < len(src) && src[i+1] == '-' && (i == 0 || src[i-1] == ' ' || src[i-1] == '\t' || src[i-1] == '\r' || src[i-1] == '\n' || src[i-1] == ';' || src[i-1] == '"') {
				comment = true
				i++
				continue
			}
			if ch == ';' {
				if err := flush(true); err != nil {
					return nil, err
				}
				continue
			}
		}
		if startLine == 0 {
			if ch == ' ' || ch == '\t' || ch == '\r' {
				continue
			}
			startLine = line
		}
		statement.WriteByte(ch)
		if quoted {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				quoted = false
			}
		} else if ch == '"' {
			quoted = true
		}
		if statement.Len() > 1024*1024 {
			return nil, fmt.Errorf("line %d: statement exceeds 1 MiB", startLine)
		}
	}
	if err := flush(true); err != nil {
		return nil, err
	}
	return rules, nil
}

// Legacy newline-separated rules still terminate at a newline, except when the
// next non-comment token explicitly continues the current workflow condition.
func conditionContinues(rest string) bool {
	for {
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "--") {
			break
		}
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			return false
		}
		rest = rest[i+1:]
	}
	return strings.HasPrefix(rest, "WHEN") && (len(rest) == 4 || len(rest) > 4 && strings.ContainsRune(" \t\r\n", rune(rest[4])))
}
