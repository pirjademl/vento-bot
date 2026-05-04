package dtos

import (
	"database/sql"
	"time"
)

type Insight struct {
	ID        int64        `json:"id,omitempty"`
	RepoID    int64        `json:"repo_id,omitempty"`
	PrId      int64        `json:"pr_id,omitempty"`
	Body      string       `json:"body,omitempty"`
	CommitSHA string       `json:"commit_sha,omitempty"`
	CreatedAt time.Time    `json:"created_at,omitempty"`
	DeletedAt sql.NullTime `json:"-"`
}
