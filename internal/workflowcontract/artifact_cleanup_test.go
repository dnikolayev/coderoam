package workflowcontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readWorkflow(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestArtifactCleanupContract(t *testing.T) {
	release := readWorkflow(t, "release.yml")
	if strings.Count(release, "actions/upload-artifact@") != strings.Count(release, "retention-days: 1") {
		t.Fatal("every uploaded artifact must expire after one day")
	}

	cleanup := readWorkflow(t, "artifact-cleanup.yml")
	for _, contract := range []string{
		"workflows: [Release]",
		"types: [completed]",
		"workflow_run.conclusion == 'success'",
		"/actions/runs/${RUN_ID}/artifacts?per_page=100",
		"--jq '.artifacts[].id' >\"${ids_file}\"",
	} {
		if !strings.Contains(cleanup, contract) {
			t.Fatalf("cleanup workflow is missing %q", contract)
		}
	}
	if strings.Count(cleanup, "ids_file=\"$(mktemp)\"") != 2 ||
		strings.Count(cleanup, "trap 'rm -f \"${ids_file}\"' EXIT") != 2 {
		t.Fatal("every artifact deletion must snapshot all pages before deleting")
	}

	parts := strings.Split(cleanup, "  delete-stale-artifacts:")
	if len(parts) != 2 || !strings.Contains(parts[1], "1 day ago") ||
		!strings.Contains(parts[1], ".expired == false and .created_at < $cutoff") ||
		!strings.Contains(parts[1], ".id' >\"${ids_file}\"") ||
		strings.Contains(parts[1], ".workflow_run.id") ||
		strings.Contains(parts[1], "/actions/runs/${run_id}") {
		t.Fatal("stale sweep must delete every nonexpired artifact older than one day")
	}
}
