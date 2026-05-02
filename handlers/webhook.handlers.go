package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/google/go-github/v85/github"
	"github.com/pirjademl/vento-bot/config"
	"github.com/pirjademl/vento-bot/dtos"
	. "github.com/pirjademl/vento-bot/dtos"
	"github.com/pirjademl/vento-bot/persistence"
	"github.com/pirjademl/vento-bot/services"
	"github.com/pirjademl/vento-bot/utils"
	. "github.com/pirjademl/vento-bot/utils"
)

type Handler struct {
	DB     *persistence.Database
	Vector *services.VectorService
}

func NewHandler(db *persistence.Database, vs *services.VectorService) *Handler {
	return &Handler{
		DB:     db,
		Vector: vs,
	}
}

func (handler *Handler) WebHookHandler(w http.ResponseWriter, r *http.Request) {
	secret, ok := r.Context().Value(config.PemKey).([]byte)
	if !ok {
		panic("secret not found")
	}
	client_id, ok := r.Context().Value(config.ClientIdKey).(string)
	if !ok {
		panic("client id not found")

	}
	var webhook GitHubWebhook

	json.NewDecoder(r.Body).Decode(&webhook)
	installationId := webhook.Installation.ID
	token := CreateJwt(secret, client_id)
	installationToken, err := GetInstallationToken(token, int(installationId))
	if err != nil {
		panic(err)
	}
	defer r.Body.Close()
	header := r.Header.Get("X-Github-Event")
	fmt.Println(header)
	switch header {
	case "installation":
		println("installation case is running ")
		handler.DB.InsertInstallation(webhook)
		//handler.DB.InsertRepository()
		//CloneRepo(installationToken, webhook.Repository.Owner.Login, webhook.Repository.Name)
		break
	case "installation_repositories":
		if webhook.Action == "added" {
			handler.DB.InsertRepositoryAdded(installationId, webhook.RepositoriesAdded)
			for _, repo := range webhook.RepositoriesAdded {
				indexed, _ := handler.DB.IsIndexed(repo.Id)
				if !indexed {
					//go handler.StartIndexingFlow(installationToken, &repo, repo.DefaultBranch)
				}
			}
		}
	case "push":
		break
	case "pull_request":
		switch webhook.Action {
		case "opened", "reopened":
			prId := webhook.PullRequest.Id
			currentSHA := webhook.PullRequest.Head.Sha
			err := handler.DB.UpsertPullRequest(webhook)
			if err != nil {
				log.Printf("failed to sync PR record", err)
				return
			}

			isIndexed, _ := handler.DB.IsPrShaIndexed(prId, currentSHA)
			if !isIndexed {
				go handler.StartIndexingFlow(
					installationToken,
					webhook.Repository,
					webhook.PullRequest.Head.Ref,
					prId,
					currentSHA,
				)

			} else {
				log.Printf("PR %d is already up-to-date at SHA %s. Skipping indexing.", webhook.PullRequest.Number, currentSHA)
			}

			ghClient := services.GetClientFromToken(installationToken)

			go func(client *github.Client, wh dtos.GitHubWebhook) {
				ctx := context.Background()

				// 1. Fetch the raw diff
				opt := github.RawOptions{Type: github.Diff}
				diff, _, err := client.PullRequests.GetRaw(ctx,
					wh.Repository.Owner.Login,
					wh.Repository.Name,
					int(wh.PullRequest.Number),
					opt,
				)
				if err != nil {
					log.Printf("Error fetching diff: %v", err)
					return
				}

				// 2. Pass the diff string to your VectorService for Gemini Analysis
				insight, err := handler.Vector.AnalyzePR(ctx, diff, wh.PullRequest.Head.Repo.Id)
				if err != nil {
					log.Printf("Gemini Analysis failed: %v", err)
					comment := &github.IssueComment{Body: github.String(err.Error())}
					client.Issues.CreateComment(
						ctx,
						wh.Repository.Owner.Login,
						wh.Repository.Name,
						int(wh.PullRequest.Number),
						comment,
					)
					return
				}

				// 3. Post the insight back to the PR as a comment
				comment := &github.IssueComment{Body: github.Ptr(insight)}
				_, _, err = client.Issues.CreateComment(ctx,
					wh.Repository.Owner.Login,
					wh.Repository.Name,
					int(wh.PullRequest.Number),
					comment,
				)
				if err != nil {
					log.Printf("Failed to post comment: %v", err)
				}
			}(ghClient, webhook)

		case "closed":
			//if webhook.PullRequest.Merged {
			//	fmt.Println("PR Merged! Updating base branch index...")
			// The code is now in the main branch, trigger a re-index of the default branch
			//go handler.StartIndexingFlow(
			//	installationToken,
			//	webhook.Repository,
			//	webhook.Repository.DefaultBranch,
			//)
		}
		// Update PR status in DB
		//handler.DB.UpdatePullRequestStatus(webhook., "closed")
		handler.DB.UpdatePullRequestStatus(webhook.PullRequest.Id, "closed")
		break

	//case "pull_request":
	//	if webhook.Action == "closed" {
	//		CloneRepo(installationToken, webhook.Repository.Owner.Login, webhook.Repository.Name)
	//		return
	//	}
	//	if webhook.Action == "opened" {
	//		handler.DB.InsertPullRequests(webhook)
	//		CloneRepo(installationToken, webhook.Repository.Owner.Login, webhook.Repository.Name)
	//		return
	//	}
	//	if webhook.Action == "reopened" {
	//		fmt.Println("reopened")
	//		handler.DB.InsertPullRequests(webhook)
	//		CloneRepo(installationToken, webhook.Repository.Owner.Login, webhook.Repository.Name)
	//		return
	//	}
	case "check_suite":
		break

	}

}

func (h *Handler) StartIndexingFlow(
	token string,
	repo *dtos.Repository,
	ref string,
	prId int64,
	sha string,
) {
	log.Printf("Starting indexing for %s (ref: %s)", repo.Name, ref)
	_ = h.DB.UpdateRepoIndexingStatus(repo.Id, "indexing")

	localPath, err := utils.CloneRepo(token, repo.Owner.Login, repo.Name, ref)
	if err != nil {
		log.Printf("Clone failed: %v", err)
		h.DB.UpdateRepoIndexingStatus(repo.Id, "failed")
		return
	}
	defer os.RemoveAll(localPath)
	log.Printf("Repo cloned to: %s", localPath)

	chunks, err := services.CreateChunks(localPath)
	if err != nil || len(chunks) == 0 {
		log.Printf("Chunking failed or 0 chunks created. Error: %v", err)
		h.DB.UpdateRepoIndexingStatus(repo.Id, "failed")
		return
	}
	log.Printf("Created %d chunks", len(chunks))

	err = h.Vector.UpsertVectors(context.Background(), repo.Id, chunks)
	if err != nil {
		log.Printf("UpsertVectors failed: %v", err) // CHECK THIS LOG IN YOUR TERMINAL
		h.DB.UpdateRepoIndexingStatus(repo.Id, "failed")
		return
	}
	err = h.DB.UpdatePrIndexedSha(prId, sha)

	log.Printf("Indexing completed successfully for %s", repo.Name)
	h.DB.UpdateRepoIndexingStatus(repo.Id, "completed")
	h.DB.MarkAsIndexed(repo.Id)
}
