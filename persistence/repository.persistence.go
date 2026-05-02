package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/pirjademl/vento-bot/dtos"
)

func (db *Database) InsertRepositoryAdded(
	installationId int64,
	RepositoriesAdded []dtos.Repository,
) (bool, error) {
	//db.Client.batc
	//
	batch := &pgx.Batch{}
	query := `
        INSERT INTO repositories (id, full_name, clone_url,is_indexed,installation_id)
        VALUES ($1, $2, $3, $4,$5)
        ON CONFLICT (id) DO UPDATE 
        SET full_name = EXCLUDED.full_name,
            clone_url = EXCLUDED.clone_url;
    `

	for _, repo := range RepositoriesAdded {
		batch.Queue(query, repo.Id, repo.FullName, "", false, installationId)
	}

	results := db.Client.SendBatch(context.Background(), batch)
	defer results.Close()

	for i := 0; i < len(RepositoriesAdded); i++ {
		_, err := results.Exec()
		if err != nil {
			fmt.Errorf("error executing batch item %d: %v", i, err)
			return false, errors.New("error sending batch updates")
		}
	}
	return true, nil
}

func (db *Database) UpdateRepoIndexingStatus(repoID int64, status string) error {
	query := `UPDATE repositories SET indexing_status = $1, updated_at = now() WHERE id = $2`
	_, err := db.Client.Exec(context.Background(), query, status, repoID)
	return err
}

func (db *Database) MarkAsIndexed(repoID int64) error {
	query := `UPDATE repositories SET is_indexed = true WHERE id = $1`
	_, err := db.Client.Exec(context.Background(), query, repoID)
	return err
}

func (db *Database) IsIndexed(repoID int64) (bool, error) {
	var indexed bool
	query := `SELECT is_indexed FROM repositories WHERE id = $1`
	err := db.Client.QueryRow(context.Background(), query, repoID).Scan(&indexed)
	return indexed, err
}
