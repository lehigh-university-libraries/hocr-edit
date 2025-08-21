package main

import (
	"log/slog"
	"net/http"

	"github.com/joho/godotenv"
	"github.com/lehigh-university-libraries/hocr-edit/internal/handlers"
	"github.com/lehigh-university-libraries/hocr-edit/internal/utils"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		utils.ExitOnError("Error loading .env file", err)
	}

	handler := handlers.New()

	// Set up routes
	http.HandleFunc("/api/sessions", handler.HandleSessions)
	http.HandleFunc("/api/sessions/", handler.HandleSessionDetail)
	http.HandleFunc("/api/upload", handler.HandleUpload)
	http.HandleFunc("/api/hocr/parse", handler.HandleHOCRParse)
	http.HandleFunc("/api/hocr/update", handler.HandleHOCRUpdate)
	http.HandleFunc("/", handler.HandleStatic)

	addr := ":8888"
	slog.Info("hOCR Editor interface available", "addr", addr)

	http.ListenAndServe(addr, nil)
}
