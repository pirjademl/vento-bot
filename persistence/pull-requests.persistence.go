package persistence

import (
	"context"
	"fmt"

	"github.com/pirjademl/vento-bot/dtos"
)

func (db *Database) UpsertPullRequest(wh dtos.GitHubWebhook) error {
	query := `
        INSERT INTO pull_requests (id, repo_id, number, state, last_indexed_sha)
        VALUES ($1, $2, $3, $4, '')
        ON CONFLICT (id) DO UPDATE 
        SET state = EXCLUDED.state,
            number = EXCLUDED.number;
    `
	_, err := db.Client.Exec(context.Background(), query,
		wh.PullRequest.Id,
		wh.Repository.Id,
		wh.PullRequest.Number,
		wh.PullRequest.State,
	)
	return err
}

func (db *Database) UpdatePullRequestStatus(prID int64, status string) error {
	query := `UPDATE pull_requests SET status = $1, updated_at = now() WHERE id = $2`
	_, err := db.Client.Exec(context.Background(), query, status, prID)
	return err
}

// persistence/repositories.go

// IsPrShaIndexed checks if the version of the code (SHA) for a specific PR
// has already been processed and stored in our vector database.
func (db *Database) IsPrShaIndexed(prID int64, sha string) (bool, error) {
	var lastSha string

	// We query the pull_requests table using the unique GitHub PR ID
	query := `SELECT last_indexed_sha FROM pull_requests WHERE id = $1`

					// Using context.Background() or passing a ctx from the handler is better practice
	err := db.Client.QueryRow(context.Background(), query, prID).Scan(&lastSha)

	if err != nil {
		// If no row is found, it's a brand new PR, so it's definitely not indexed
		if err.Error() == "no rows in result set" {
			return false, nil
		}
		return false, fmt.Errorf("failed to check PR SHA: %w", err)
	}

	// Return true only if the SHA in the DB matches the current one from the webhook
	return lastSha == sha, nil
}
func (db *Database) UpdatePrIndexedSha(prID int64, sha string) error {
	query := `UPDATE pull_requests SET last_indexed_sha = $1 WHERE id = $2`
	_, err := db.Client.Exec(context.Background(), query, sha, prID)
	return err
}
