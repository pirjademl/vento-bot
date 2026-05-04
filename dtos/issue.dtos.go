package dtos

import "time"

type IssueDTO struct {
	ID      int64  `json:"id"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	Locked  bool   `json:"locked"`
	HTMLURL string `json:"html_url"`
	User    *Owner `json:"user"` // issue author
	//Assignees []ActorDTO    `json:"assignees"`
	//	Labels    []LabelDTO    `json:"labels"`
	//	Milestone *MilestoneDTO `json:"milestone,omitempty"`
	IsPR      bool       `json:"-"` // derived: true when pull_request key exists in payload
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}
