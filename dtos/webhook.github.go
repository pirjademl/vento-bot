package dtos

type GitHubWebhook struct {
	Action       string       `json:"action"`
	Installation Installation `json:"installation"`
	Repository   *Repository  `json:"repository,omitempty"`
	Sender       Sender       `json:"sender"`

	PullRequest *PullRequest `json:"pull_request,omitempty"`

	RepositoriesAdded   []Repository `json:"repositories_added,omitempty"`
	RepositoriesRemoved []Repository `json:"repositories_removed,omitempty"`

	Ref     string   `json:"ref,omitempty"`
	Before  string   `json:"before,omitempty"` // sha before commiiting
	After   string   `json:"after,omitempty"`  // sha after commmit
	Commits []Commit `json:"commits,omitempty"`
}
