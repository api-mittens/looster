package postgres

import (
	"context"
	"testing"

	"github.com/api-mittens/looster/internal/behaviour"
)

func TestInvalidAgentDefinitionBatchesNeverReachDatabase(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rules []behaviour.Rule
	}{
		{"empty create", nil},
		{"empty prompt", []behaviour.Rule{{Trigger: "prompt", Target: "coder"}}},
		{"conflicting definitions", []behaviour.Rule{{Trigger: "prompt", Target: "coder", Prompt: "Implement"}, {Trigger: "prompt", Target: "coder", Prompt: "Review"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A zero Store cannot open a transaction: invalid input must fail beforehand.
			s := &Store{}
			if err := s.CreateBehaviour(context.Background(), tc.rules); err == nil {
				t.Fatal("invalid definition batch was accepted")
			}
		})
	}
}
