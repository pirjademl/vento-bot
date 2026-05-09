package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/pirjademl/vento-bot/config"
	"github.com/pirjademl/vento-bot/handlers"
	"github.com/pirjademl/vento-bot/persistence"
	"github.com/pirjademl/vento-bot/services"
)

var (
	SYSTEM_INSTRUCTION string = os.Getenv("PULL_REQUEST_SYSTEM_INSTRUCTION")
)

func main() {
	log.SetFlags(log.Lshortfile | log.LstdFlags)

	config.LoadEnv()
	systemInstruction := strings.ReplaceAll(
		os.Getenv("PULL_REQUEST_SYSTEM_INSTRUCTION"),
		`\n`,
		"\n",
	)
	if systemInstruction == "" {
		log.Fatal(" PULL_REQUEST_SYTEM_INSTRUCTION NOT FOUND !!")
		return
	}

	conn, err := config.ConnectDB()
	if err != nil {
		log.Println(err.Error())
		panic(err)
	}
	fmt.Println(conn.IsClosed())
	dbLayer := persistence.NewDatabase(conn)

	clientId := os.Getenv("CLIENT_ID")

	contents, err := os.ReadFile(os.Getenv("PEM_FILE_PATH"))
	if err != nil {
		panic(err)
	}
	vectorService, err := services.NewVectorService("vento_vectors")
	if err != nil {
		log.Fatalf("Vector Service initialization failed: %v", err)
	}
	err = vectorService.InitCollection(context.Background())
	vectorService.CreatePayloadIndex()

	if err != nil {
		log.Fatalf("creating collection err %s", err)
	}

	handler := handlers.NewHandler(dbLayer, vectorService)

	http.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), config.PemKey, contents))
		r = r.WithContext(context.WithValue(r.Context(), config.ClientIdKey, clientId))
		handler.WebHookHandler(w, r)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "application running")

	})
	http.ListenAndServe(":8080", nil)
}
