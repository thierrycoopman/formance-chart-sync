package changed

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type commit struct {
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
}

type pushEvent struct {
	Commits []commit `json:"commits"`
}

// Files returns absolute paths of added/modified files from the GitHub push
// event payload at eventPath, rooted at workspace.
//
// Returns nil if the payload cannot be read or parsed (e.g. on pull_request
// events where commits are not listed). Callers must treat nil as "no filter
// applied" and process all matched charts.
func Files(eventPath, workspace string) []string {
	if eventPath == "" {
		return nil
	}
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return nil
	}
	var event pushEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil
	}
	if len(event.Commits) == 0 {
		return nil
	}
	var files []string
	for _, c := range event.Commits {
		for _, f := range append(c.Added, c.Modified...) {
			files = append(files, filepath.Join(workspace, f))
		}
	}
	return files
}
