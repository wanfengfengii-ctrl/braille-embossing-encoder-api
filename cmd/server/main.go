package main

import (
	"log"
	"os"

	"braille-api/internal/api"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := api.NewRouter().Run(":" + port); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
