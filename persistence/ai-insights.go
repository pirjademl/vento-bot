package persistence

import (
	"context"
	"errors"
	"log"

	"github.com/pirjademl/vento-bot/dtos"
)

func (db *Database) InsertInsight(insight *dtos.Insight) error {
	if insight.Body == "" || insight.PrId == 0 || insight.RepoID == 0 {
		log.Printf(
			"failed to insert ai insight err:validation failed body,PrId or RepoId is required to insert the record",
		)
		return errors.New("validation failed body,PrId or RepoId is required to insert the record")

	}
	ctx := context.Background()
	query := `INSERT INTO insights(id,repo_id,pr_id,body,commit_sha) values($1,$2,$3,$4,$5)`
	_, err := db.Client.Exec(ctx, query)
	if err != nil {
		return err

	}
	return nil

}

func (db *Database) GetInsights(repoId, prId int64) ([]string, error) {
	var content []string
	if repoId == 0 || prId == 0 {
		return nil, errors.New("repo_id and pr_id is required")
	}

	ctx := context.Background()

	query := `SELECT body from insights where repo_id=$1 AND pr_id=$2`

	result, err := db.Client.Query(ctx, query, repoId, prId)
	if err != nil {
		return nil, errors.New("unable to fetch insights  ")
	}
	for result.Next() {
		var body string
		result.Scan(&body)
		content = append(content, body)
	}
	return content, nil
}
