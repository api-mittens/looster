package cli

import (
	"fmt"

	"github.com/api-mittens/looster/internal/memory"
)

func memoryViews(memories []memory.Memory) []any {
	out := []any{}
	for _, m := range memories {
		out = append(out, struct {
			Ref string `json:"ref"`
			memory.Memory
		}{fmt.Sprintf("MEM-%d", m.ID), m})
	}
	return out
}
