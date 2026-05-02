package persistence

import "github.com/jackc/pgx/v5"

type Database struct {
	Client *pgx.Conn
}

func NewDatabase(con *pgx.Conn) *Database {
	return &Database{
		Client: con,
	}
}
