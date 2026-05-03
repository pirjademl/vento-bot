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
		handler.DB.InsertInstallation(webhook)
		if len(webhook.RepositoriesAdded) > 0 {
			handler.DB.InsertRepositoryAdded(installationId, webhook.RepositoriesAdded)
		}
	case "installation_repositories":
		switch webhook.Action {
		case "added":
			handler.DB.InsertRepositoryAdded(installationId, webhook.RepositoriesAdded)
			for _, repo := range webhook.RepositoriesAdded {
				handler.StartIndexingFlow(installationToken, &repo, webhook.Sender.Login)
			}
		}
	case "pull_request":
		switch webhook.Action {
		case "opened", "reopened":
			// get the diff
			//get the deletion and the addition
			ghClient := services.GetClientFromToken(installationToken)
			//go func(client *github.Client, wh dtos.GitHubWebhook) {
			//	cntx := context.Background()
			//	//pullReq, response, err := client.PullRequests.Get(
			//	//	cntx,
			//	//wh.Repository.Owner.Login,
			//	//wh.Repository.Name,
			//	//int(wh.PullRequest.Number),
			//	//)
			//	//if err != nil {
			//	//	log.Printf("error %s", err.Error())
			//	//	return
			//	//}
			//	//fmt.Println(pullReq.GetCommits())
			//	comp, _, err := client.Repositories.CompareCommits(
			//		cntx,
			//		wh.Repository.Owner.Login,
			//		wh.Repository.Name,
			//		wh.PullRequest.Base.Ref,
			//		wh.PullRequest.Head.Ref,
			//		nil,
			//	)

			//}(ghClient, webhook)
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

		}

	case "check_suite":
		break

	}

}

func (h *Handler) StartIndexingFlow(
	token string,
	repo *dtos.Repository,
	sender string,
) {

	localPath, err := utils.CloneRepo(token, sender, repo.Name)
	if err != nil {
		log.Printf("Clone failed: %v", err)
		h.DB.UpdateRepoIndexingStatus(repo.Id, "failed")
		return
	}
	defer os.RemoveAll(localPath)
	log.Printf("Repo cloned to: %s", localPath)

	chunks, err := services.CreateChunks(localPath)
	if err != nil || len(chunks) == 0 {
		log.Printf("Chunking failed or 0 chunks created. Error: %v", err.Error())
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
	log.Printf("Indexing completed successfully for %s", repo.Name)
	h.DB.UpdateRepoIndexingStatus(repo.Id, "completed")
	h.DB.MarkAsIndexed(repo.Id)
}
