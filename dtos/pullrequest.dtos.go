package dtos

type PullRequest struct {
	Id      int64  `json:"id"`
	Number  int64  `json:"number"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
	DiffURL string `json:"diff_url"`
	Head    Branch `json:"head"`
	Base    Branch `json:"base"` // base branch  main or master
}

type Branch struct {
	Label string     `json:"label"`
	Ref   string     `json:"ref"`
	Sha   string     `json:"sha"` // commit hash
	Repo  Repository `json:"repo"`
}
