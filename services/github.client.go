package services

import "github.com/google/go-github/v85/github"

func GetClientFromToken(token string) *github.Client {
	return github.NewClient(nil).WithAuthToken(token)
}
