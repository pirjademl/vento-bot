package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/google/go-github/v85/github"
	"github.com/pirjademl/vento-bot/config"
	"github.com/pirjademl/vento-bot/dtos"
	. "github.com/pirjademl/vento-bot/dtos"
	githubclient "github.com/pirjademl/vento-bot/github_client"
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
		case "closed":
			if webhook.PullRequest.Merged {
				// documentation update logic

			}

		case "opened", "reopened":
			ghClient := services.GetClientFromToken(installationToken)

			go func(client *github.Client, wh dtos.GitHubWebhook) {
				ctx := context.Background()

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

				aiInsight, err := handler.Vector.AnalyzePR(ctx, diff, wh.PullRequest.Head.Repo.Id)
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

				//save this

				insights := &dtos.Insight{
					Body:      aiInsight,
					RepoID:    wh.Repository.Id,
					PrId:      wh.PullRequest.Id,
					CommitSHA: wh.PullRequest.Head.Sha,
				}
				err = handler.DB.InsertInsight(insights)
				if err != nil {
					log.Printf("error inserting ai insight into db")
				}

				comment := &github.IssueComment{Body: github.Ptr(aiInsight)}
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
		ctx := context.Background()
		ghClient := services.GetClientFromToken(installationToken)
		branchName := strings.TrimPrefix(webhook.Ref, "refs/heads/")

		pulls, _, err := ghClient.PullRequests.List(
			ctx,
			webhook.Repository.Owner.Login,
			webhook.Repository.Name,
			&github.PullRequestListOptions{
				Head:  webhook.Sender.Login + ":" + branchName,
				State: "open",
			},
		)
		if err != nil {
			log.Printf("filtering the push request with open  pr is failed  ", err.Error())
			return
		}

		if len(pulls) == 0 {
			return
		}
		openpr := pulls[0]

		comparison, _, err := ghClient.Repositories.CompareCommits(ctx,
			webhook.Repository.Owner.Login,
			webhook.Repository.Name,
			webhook.Before,
			webhook.After,
			nil,
		)
		if err != nil {
			log.Printf("error comparing two commits %s", err.Error())
			return
		}
		var diffBuilder strings.Builder
		for _, file := range comparison.Files {
			patch := file.GetPatch()
			if patch == "" {
				continue // no patch means no valid line numbers, skip it
			}
			diffBuilder.WriteString(fmt.Sprintf("--- %s\n", file.GetFilename()))
			diffBuilder.WriteString(file.GetPatch())
			diffBuilder.WriteString("\n")
		}
		incrementalDiff := diffBuilder.String()

		insight, err := handler.Vector.AnalyzePR(ctx, incrementalDiff, webhook.Repository.Id)
		if err != nil {
			log.Printf("error getting insights about push commits %s", err.Error())
			return
		}

		structedReview, err := handler.Vector.ExtractStructuredReview(ctx, insight, incrementalDiff)
		if err != nil {
			log.Printf("error comparing two commits %s", err.Error())
			return
		}
		githubclient.PostStructuredReview(
			ctx,
			ghClient,
			webhook.Repository.Owner.Login,
			webhook.Repository.Name,
			openpr.GetNumber(),
			webhook.After,
			structedReview,
		)
		defaultBranch := webhook.Repository.DefaultBranch

		if branchName != defaultBranch {
			log.Printf("push to %s is not the default branch , skipping vector sync", branchName)
			return
		}

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
		if webhook.PullRequest == nil {
			return
		}

	case "check_suite":
		break

	case "issue_comment":
		switch webhook.Action {
		case "created":
			ctx := context.Background()
			comment := webhook.Comment.Body
			if strings.HasPrefix(comment, "@vento-bot") {
				// Strip the bot mention to get the actual question
				question := strings.TrimSpace(strings.TrimPrefix(comment, "@vento-bot"))

				contents, err := handler.DB.GetInsights(
					webhook.Repository.Id,
					int64(webhook.Issue.Number),
				)
				if err != nil {
					log.Printf("unable to get the conversation history: %v", err)
					return
				}

				ghClient := services.GetClientFromToken(installationToken)

				issueComments, _, err := ghClient.Issues.ListComments(
					ctx,
					webhook.Repository.Owner.Login,
					webhook.Repository.Name,
					webhook.Issue.Number,
					&github.IssueListCommentsOptions{
						ListOptions: github.ListOptions{PerPage: 5},
					},
				)
				if err != nil {
					log.Printf("unable to fetch recent comments: %v", err)
					return
				}

				// 3. Format comments into string slice
				recentComments := make([]string, 0, len(issueComments))
				for _, c := range issueComments {
					recentComments = append(recentComments, fmt.Sprintf(
						"@%s: %s", c.User.GetLogin(), c.GetBody(),
					))
				}

				answer, err := handler.Vector.ProvideAnswerOnComments(
					ctx,
					question,
					webhook.Repository.Id,
					contents, // []string of previous AI insights
					recentComments,
				)
				if err != nil {
					log.Printf("unable to generate answer: %v", err)
					return
				}

				botComment := fmt.Sprintf("🤖 **Vento Bot**\n\n%s", answer)
				_, _, err = ghClient.Issues.CreateComment(
					ctx,
					webhook.Repository.Owner.Login,
					webhook.Repository.Name,
					webhook.Issue.Number,
					&github.IssueComment{Body: &botComment},
				)
				if err != nil {
					log.Printf("unable to post bot comment: %v", err)
					return
				}
			}
		}

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
