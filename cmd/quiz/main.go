// Квиз на том же WebSocket-стеке, что и чат.
// Чат остаётся в cmd/server. Этот процесс слушает только localhost.
package main

import (
	"log"
	"os"

	"vkquiz/internal/quiz"
)

func main() {
	addr := env("HTTP_ADDR", "127.0.0.1:8093")
	dir := env("STATIC_DIR", "static/quiz")
	if err := quiz.Serve(addr, dir); err != nil {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
