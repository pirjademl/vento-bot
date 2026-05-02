package config

import (
	"bufio"
	"log"
	"os"
	"strings"
)

func LoadEnv() {
	log.Println("scanning .env file")
	file, err := os.Open(".env")
	if err != nil {
		log.Fatal(err.Error())
		return
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			log.Println("skipping invalid env variable")
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if err := os.Setenv(key, value); err != nil {
			log.Println("failed to set an environment variable")
		}
		if err := scanner.Err(); err != nil {
			log.Fatal("Error reading .env file:", err)
		}

	}
	println("successfully executed this function")

}
