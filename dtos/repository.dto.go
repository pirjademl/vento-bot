package dtos

type Repository struct {
	Id            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Owner         Owner  `json:"owner"`
}
