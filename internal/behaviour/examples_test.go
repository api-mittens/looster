package behaviour

import (
	"os"
	"testing"
)

func TestShippedManifests(t *testing.T) {
	for _, name := range []string{"behaviour.example", "behaviour_example.txt"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile("../../" + name)
			if err != nil {
				t.Fatal(err)
			}
			rules, err := Parse(string(source))
			if err != nil {
				t.Fatal(err)
			}
			if len(rules) == 0 {
				t.Fatal("example must define a complete workflow")
			}
			declared := map[string]bool{}
			for _, rule := range rules {
				if rule.Trigger == "agent" {
					declared[rule.Target] = true
				}
			}
			for _, rule := range rules {
				if rule.Trigger != "tag" && !declared[rule.Target] {
					t.Errorf("target agent %q is not declared", rule.Target)
				}
				if rule.Source != "" && !declared[rule.Source] {
					t.Errorf("source agent %q is not declared", rule.Source)
				}
			}
		})
	}
}
