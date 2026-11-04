package scripts_test

import (
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v2"
)

// Keep feature pushes from duplicating PR checks without disabling master or releases.
func TestCITriggerPolicy(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On map[string]struct {
			Branches []string `yaml:"branches"`
			Tags     []string `yaml:"tags"`
		} `yaml:"on"`
		Concurrency struct {
			Group  string `yaml:"group"`
			Cancel string `yaml:"cancel-in-progress"`
		} `yaml:"concurrency"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(workflow.On["push"].Branches, []string{"master"}) {
		t.Fatalf("push checks must only run on master: %v", workflow.On["push"].Branches)
	}
	if !reflect.DeepEqual(workflow.On["push"].Tags, []string{"v*"}) {
		t.Fatal("version tags must retain release CI")
	}
	for _, event := range []string{"pull_request", "workflow_dispatch"} {
		if _, ok := workflow.On[event]; !ok {
			t.Errorf("missing %s trigger", event)
		}
	}
	if len(workflow.On) != 3 {
		t.Fatalf("unexpected workflow triggers: %v", workflow.On)
	}
	if workflow.Concurrency.Group != "${{ github.workflow }}-${{ github.event_name }}-${{ github.event.pull_request.number || github.ref }}" {
		t.Fatal("concurrency must isolate workflow, event and PR or ref")
	}
	if workflow.Concurrency.Cancel != "${{ github.event_name == 'pull_request' }}" {
		t.Fatal("only superseded PR runs may be cancelled, never releases")
	}
}
