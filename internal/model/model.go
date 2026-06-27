package model

const (
	CommitType       = "commit"
	CommitFileType   = "commit_file"
	CommitParentType = "commit_parent"
	DevType          = "dev"
)

type CommitInfo struct {
	Type           string   `json:"type"`
	SHA            string   `json:"sha"`
	Tree           string   `json:"tree"`
	Parents        []string `json:"parents"`
	AuthorName     string   `json:"author_name"`
	AuthorEmail    string   `json:"author_email"`
	AuthorDate     string   `json:"author_date"`
	CommitterName  string   `json:"committer_name"`
	CommitterEmail string   `json:"committer_email"`
	CommitterDate  string   `json:"committer_date"`
	Message        string   `json:"message"`
	Additions      int64    `json:"additions"`
	Deletions      int64    `json:"deletions"`
	FilesChanged   int      `json:"files_changed"`
}

type CommitFileInfo struct {
	Type         string `json:"type"`
	Commit       string `json:"commit"`
	PathCurrent  string `json:"path_current"`
	PathPrevious string `json:"path_previous"`
	Status       string `json:"status"`
	OldHash      string `json:"old_hash"`
	NewHash      string `json:"new_hash"`
	// OldSize/NewSize are pointers so the JSON can distinguish three
	// states: absent (nil → not resolved, e.g. blob sizes disabled or a
	// null hash) vs. a resolved value, which may legitimately be 0 for an
	// empty blob. A scalar int64 with omitempty would collapse a real
	// 0-byte size into "absent", breaking the --blob-sizes contract.
	OldSize      *int64 `json:"old_size,omitempty"`
	NewSize      *int64 `json:"new_size,omitempty"`
	Additions    int64  `json:"additions"`
	Deletions    int64  `json:"deletions"`
}

type CommitParentInfo struct {
	Type      string `json:"type"`
	SHA       string `json:"sha"`
	ParentSHA string `json:"parent_sha"`
}

type DevInfo struct {
	Type  string `json:"type"`
	DevID string `json:"dev_id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}
