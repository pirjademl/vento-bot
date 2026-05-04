package dtos

import (
	"time"
)

type Comment struct {
	ID        int64     `json:"id"`
	NodeID    string    `json:"node_id"`
	URL       string    `json:"url"`
	HTMLURL   string    `json:"html_url"`
	Body      string    `json:"body"`
	User      *Owner    `json:"user"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	PerformedViaGitHubApp interface{} `json:"performed_via_github_app"`
}
