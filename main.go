package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/pirjademl/vento-bot/config"
	"github.com/pirjademl/vento-bot/handlers"
	"github.com/pirjademl/vento-bot/persistence"
	"github.com/pirjademl/vento-bot/services"
)

func main() {
	log.SetFlags(log.Lshortfile | log.LstdFlags)

	config.LoadEnv()
	//	apiKey := os.Getenv("GEMINI_API_KEY")
	//client, err := genai.NewClient(ctx, nil)

	//if err != nil {
	//		log.Fatal(err)
	//}

	//result, err := client.Models.GenerateContent(
	//	ctx,
	//	"gemini-3-flash-preview",
	//	genai.Text("Explain how AI works in a few words"),
	//	nil,
	//)
	//if err != nil {
	//	log.Fatal(err)
	//}
	//fmt.Println(result.Text())
	conn, err := config.ConnectDB()
	if err != nil {
		log.Println(err.Error())
		panic(err)
	}
	fmt.Println(conn.IsClosed())
	dbLayer := persistence.NewDatabase(conn)

	clientId := "Iv23lipPTldsNmEpeA7O"
	filename := "vento-bot-private-key.pem"

	contents, err := os.ReadFile(filename)
	if err != nil {
		panic(err)
	}
	vectorService, err := services.NewVectorService("vento_vectors")
	if err != nil {
		log.Fatalf("Vector Service initialization failed: %v", err)
	}
	err = vectorService.InitCollection(context.Background())
	//vectorService.CreateFieldIndex()

	if err != nil {
		log.Fatalf("creating collection err", err)
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
