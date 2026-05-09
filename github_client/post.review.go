package githubclient

import (
	"context"
	"fmt"
	"log"

	"github.com/google/go-github/v85/github"
	"github.com/pirjademl/vento-bot/dtos"
)

func PostStructuredReview(
	ctx context.Context,
	ghClient *github.Client,
	owner string,
	repo string,
	prNumber int,
	headSHA string,
	review *dtos.StructuredReview,
) error {
	if len(review.LineComments) > 0 {
		comments := make([]*github.DraftReviewComment, 0, len(review.LineComments))
		for _, lc := range review.LineComments {
			comments = append(comments, &github.DraftReviewComment{
				Path: github.Ptr(lc.FilePath),
				Line: github.Ptr(lc.LineNumber),
				Side: github.Ptr("RIGHT"),
				Body: github.Ptr(lc.Body),
			})
		}

		_, _, err := ghClient.PullRequests.CreateReview(
			ctx,
			owner,
			repo,
			prNumber,
			&github.PullRequestReviewRequest{
				CommitID: github.Ptr(headSHA),
				Event:    github.Ptr("COMMENT"),
				Body:     github.Ptr(""),
				Comments: comments,
			},
		)
		if err != nil {
			log.Printf("failed to post line comments: %v", err)
			// fall through and still post the general comment
		}
	}

	if review.GeneralComment != "" {
		_, _, err := ghClient.Issues.CreateComment(
			ctx,
			owner,
			repo,
			prNumber,
			&github.IssueComment{
				Body: github.Ptr(review.GeneralComment),
			},
		)
		if err != nil {
			return fmt.Errorf("failed to post general comment: %w", err)
		}
	}

	return nil
}
