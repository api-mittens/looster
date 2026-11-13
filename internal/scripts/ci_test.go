package scripts_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

// Branch pushes, including merges to master, must not duplicate PR checks.
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
	if len(workflow.On["push"].Branches) != 0 {
		t.Fatalf("branch pushes must not duplicate PR checks: %v", workflow.On["push"].Branches)
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

func TestCIReleaseBuildPolicy(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			If       string            `yaml:"if"`
			Needs    any               `yaml:"needs"`
			Env      map[string]string `yaml:"env"`
			Strategy struct {
				Matrix struct {
					Include []map[string]string `yaml:"include"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	if len(workflow.Jobs) != 3 {
		t.Fatalf("unexpected CI jobs: %v", workflow.Jobs)
	}
	test := workflow.Jobs["test"]
	if test.If != "" || len(test.Strategy.Matrix.Include) != 0 {
		t.Fatal("the single test/CLI job must run without a platform matrix")
	}
	builds := 0
	for _, step := range test.Steps {
		builds += strings.Count(step.Run, "go build ")
		if strings.Contains(step.Run, "scripts/release-archive.sh") || strings.Contains(step.Run, "scripts/check-native-deps.sh ") {
			t.Fatal("ordinary CI must not package or inspect release binaries")
		}
	}
	if builds != 1 || test.Env["CC"] != "" || test.Env["CGO_LDFLAGS"] != "" {
		t.Fatal("ordinary CI must build one CLI without release-only static linker settings")
	}
	for _, name := range []string{"build", "release"} {
		if workflow.Jobs[name].If != "startsWith(github.ref, 'refs/tags/v')" {
			t.Fatalf("%s must only run for version tags", name)
		}
	}
	build := workflow.Jobs["build"]
	if build.Needs != "test" || len(build.Strategy.Matrix.Include) != 4 {
		t.Fatal("release packaging must wait for tests and retain all four native targets")
	}
	if !reflect.DeepEqual(workflow.Jobs["release"].Needs, []any{"test", "build"}) {
		t.Fatal("publication must require both tests and native packaging")
	}
}
