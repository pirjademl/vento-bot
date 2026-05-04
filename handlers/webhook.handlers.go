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
	case "pull_request", "check suite":
		switch webhook.Action {
		case "opened", "reopened":
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

	case "push":
		fmt.Println("Processing push event for sync...")

		// 1. Identify changes
		changed := make(map[string]bool)
		removed := make(map[string]bool)
		for _, commit := range webhook.Commits {
			for _, file := range commit.Added {
				changed[file] = true
			}
			for _, file := range commit.Modified {
				changed[file] = true
			}
			for _, file := range commit.Removed {
				removed[file] = true
			}
		}

		ctx := context.Background()
		ghClient := services.GetClientFromToken(installationToken)
		repoID := webhook.Repository.Id
		owner := webhook.Repository.Owner.Login
		repoName := webhook.Repository.Name

		// 2. Fetch changed files to a temporary directory
		// Using webhook.After (the head SHA) ensures we get the latest code version
		path, err := FetchChangedFiles(
			ctx,
			ghClient,
			owner,
			repoName,
			webhook.After,
			changed,
		)
		if err != nil {
			log.Printf("Sync fetch failed: %v", err)
			return
		}
		defer os.RemoveAll(path) // Cleanup /tmp after sync is finished

		// 3. REUSE EXISTING FLOW: Create chunks from the temp directory
		chunks, err := services.CreateChunks(path)
		if err != nil {
			log.Printf("Sync chunking failed: %v", err)
			return
		}

		// 4. Update Vector DB: Delete stale points first, then Upsert
		// Delete points for both Modified and Removed files
		for file := range changed {
			handler.Vector.DeleteFilePoints(ctx, repoID, file)
		}
		for file := range removed {
			handler.Vector.DeleteFilePoints(ctx, repoID, file)
		}

		// 5. Natural order: Upsert the fresh chunks
		if len(chunks) > 0 {
			err = handler.Vector.UpsertVectors(ctx, repoID, chunks)
			if err != nil {
				log.Printf("Sync upsert failed: %v", err)
			}
		}

		fmt.Printf("Successfully synced %d chunks for repo %d\n", len(chunks), repoID)
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
