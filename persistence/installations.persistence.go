package persistence

import (
	"context"

	. "github.com/pirjademl/vento-bot/dtos"
)

// persistence layer
func (db *Database) InsertInstallation(hook GitHubWebhook) (bool, error) {
	query := `
        INSERT INTO installations (id, account_login, account_id)
        VALUES ($1, $2, $3)
        ON CONFLICT (id) DO UPDATE 
        SET account_login = EXCLUDED.account_login;
    `

	_, err := db.Client.Exec(context.Background(), query,
		hook.Installation.ID,
		hook.Sender.Login,
		hook.Sender.Id,
	)
	if err != nil {
		return false, err
	}
	return true, nil

}
