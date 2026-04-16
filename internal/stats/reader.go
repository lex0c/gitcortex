package stats

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"gitcortex/internal/model"
)

type Dataset struct {
	Commits []model.CommitInfo
	Files   []model.CommitFileInfo
	Parents []model.CommitParentInfo
	Devs    []model.DevInfo
}

func LoadJSONL(path string) (*Dataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	return readJSONL(f)
}

func readJSONL(r io.Reader) (*Dataset, error) {
	var ds Dataset
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 1<<20)
	scanner.Buffer(buf, 10<<20)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var peek struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &peek); err != nil {
			return nil, fmt.Errorf("line %d: parse type: %w", lineNum, err)
		}

		switch peek.Type {
		case model.CommitType:
			var c model.CommitInfo
			if err := json.Unmarshal(line, &c); err != nil {
				return nil, fmt.Errorf("line %d: parse commit: %w", lineNum, err)
			}
			ds.Commits = append(ds.Commits, c)

		case model.CommitFileType:
			var cf model.CommitFileInfo
			if err := json.Unmarshal(line, &cf); err != nil {
				return nil, fmt.Errorf("line %d: parse commit_file: %w", lineNum, err)
			}
			ds.Files = append(ds.Files, cf)

		case model.CommitParentType:
			var cp model.CommitParentInfo
			if err := json.Unmarshal(line, &cp); err != nil {
				return nil, fmt.Errorf("line %d: parse commit_parent: %w", lineNum, err)
			}
			ds.Parents = append(ds.Parents, cp)

		case model.DevType:
			var d model.DevInfo
			if err := json.Unmarshal(line, &d); err != nil {
				return nil, fmt.Errorf("line %d: parse dev: %w", lineNum, err)
			}
			ds.Devs = append(ds.Devs, d)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	return &ds, nil
}

func parseDate(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
