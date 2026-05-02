package services

import "github.com/google/go-github/v85/github"

// In your handler or a utility package
func GetClientFromToken(token string) *github.Client {
	// This creates a client that will use the Installation Token
	// for all subsequent API calls.
	return github.NewClient(nil).WithAuthToken(token)
}
