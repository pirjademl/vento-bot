package config

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var PemKey string = "pemkey"
var ClientIdKey string = "client_id"

type PGConn struct {
	DB *pgx.Conn
}

func ConnectDB() (*pgx.Conn, error) {
	DBUser := os.Getenv("DATABASE_USER")
	DBName := os.Getenv("DATABASE_NAME")
	DBPASSWORD := os.Getenv("DATABASE_PASSWORD")

	if DBUser == "" || len(DBUser) == 0 || DBName == "" || DBPASSWORD == "" {
		log.Fatal("unable to get db info to connect to db")
		return nil, errors.New("env variable error")

	}

	connstr := os.Getenv("DATABASE_URL")

	db, err := pgx.Connect(context.Background(), connstr)
	if err != nil {
		if pgErr, ok := err.(*pgconn.PgError); ok {
			fmt.Printf("Postgres Error Code: %s\n", pgErr.Code)
			fmt.Printf("Message: %s\n", pgErr.Message)
			fmt.Printf("Detail: %s\n", pgErr.Detail)
		} else {
			fmt.Printf("Generic Connection Error: %v\n", err)
		}
	}

	if err != nil {
		return nil, errors.New("cannot connect to postgres databse")

	}
	return db, nil

}
