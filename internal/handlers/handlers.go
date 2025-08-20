package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/hocr-edit/internal/models"
	"github.com/lehigh-university-libraries/hocr-edit/internal/services/hocr"
	"github.com/lehigh-university-libraries/hocr-edit/internal/services/ocr"
	"github.com/lehigh-university-libraries/hocr-edit/internal/storage"
	"github.com/lehigh-university-libraries/hocr-edit/internal/utils"
	"github.com/lehigh-university-libraries/hocr-edit/pkg/metrics"
	"github.com/lehigh-university-libraries/hocr-edit/pkg/hocr/parser"
)

type Handler struct {
	sessionStore *storage.SessionStore
	ocrService   *ocr.Service
}

func New() *Handler {
	return &Handler{
		sessionStore: storage.New(),
		ocrService:   ocr.New(),
	}
}

func (h *Handler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	http.ServeFile(w, r, "static/index.html")
}

func (h *Handler) HandleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		sessions := h.sessionStore.GetAll()
		sessionList := make([]*models.CorrectionSession, 0, len(sessions))
		for _, session := range sessions {
			sessionList = append(sessionList, session)
		}
		json.NewEncoder(w).Encode(sessionList)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) HandleSessionDetail(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/sessions/")

	if strings.HasSuffix(sessionID, "/metrics") {
		sessionID = strings.TrimSuffix(sessionID, "/metrics")
		if r.Method == "POST" {
			h.handleMetrics(w, r, sessionID)
			return
		}
	}

	session, exists := h.sessionStore.Get(sessionID)
	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case "GET":
		json.NewEncoder(w).Encode(session)
	case "PUT":
		var updatedSession models.CorrectionSession
		if err := json.NewDecoder(r.Body).Decode(&updatedSession); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		h.sessionStore.Set(sessionID, &updatedSession)
		json.NewEncoder(w).Encode(updatedSession)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request, _ string) {
	var request struct {
		Original  string `json:"original"`
		Corrected string `json:"corrected"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	metricsResult := metrics.CalculateAccuracyMetrics(request.Original, request.Corrected)
	json.NewEncoder(w).Encode(metricsResult)
}

func (h *Handler) HandleHOCRUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		SessionID string `json:"session_id"`
		ImageID   string `json:"image_id"`
		HOCR      string `json:"hocr"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	session, exists := h.sessionStore.Get(request.SessionID)
	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	for i, image := range session.Images {
		if image.ID == request.ImageID {
			session.Images[i].CorrectedHOCR = request.HOCR
			session.Images[i].Completed = true
			break
		}
	}

	h.sessionStore.Set(request.SessionID, session)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (h *Handler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	file, header, err := r.FormFile("files")
	if err != nil {
		file, header, err = r.FormFile("file")
		if err != nil {
			utils.RespondWithError(w, "Failed to read file: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	defer file.Close()

	sessionID := fmt.Sprintf("session_%d", time.Now().Unix())
	session := &models.CorrectionSession{
		ID:        sessionID,
		Images:    []models.ImageItem{},
		Current:   0,
		CreatedAt: time.Now(),
		Config: models.EvalConfig{
			Model:       "google_cloud_vision",
			Prompt:      "Google Cloud Vision OCR with hOCR conversion",
			Temperature: 0.0,
			Timestamp:   time.Now().Format("2006-01-02_15-04-05"),
		},
	}

	uploadsDir := "uploads"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		utils.RespondWithError(w, "Failed to create uploads directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	fileData, err := io.ReadAll(file)
	if err != nil {
		utils.RespondWithError(w, "Failed to read file contents: "+err.Error(), http.StatusInternalServerError)
		return
	}

	md5Hash := utils.CalculateDataMD5(fileData)

	ext := filepath.Ext(header.Filename)

	imageFilename := md5Hash + ext
	hocrFilename := md5Hash + ".xml"

	imageFilePath := filepath.Join(uploadsDir, imageFilename)
	hocrFilePath := filepath.Join(uploadsDir, hocrFilename)

	if err := os.WriteFile(imageFilePath, fileData, 0644); err != nil {
		utils.RespondWithError(w, "Failed to save file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Info("Image saved", "filename", imageFilename, "md5", md5Hash)

	width, height := utils.GetImageDimensions(imageFilePath)

	var hocrXML string

	if _, err := os.Stat(hocrFilePath); err == nil {
		hocrData, err := os.ReadFile(hocrFilePath)
		if err != nil {
			slog.Warn("Failed to read existing hOCR file", "error", err, "path", hocrFilePath)
			hocrXML, err = h.getOCRForImage(imageFilePath)
			if err != nil {
				slog.Warn("Failed to get hOCR from Google Cloud Vision", "error", err)
				utils.RespondWithError(w, "Failed to process image", http.StatusInternalServerError)
				return
			}
			if err := os.WriteFile(hocrFilePath, []byte(hocrXML), 0644); err != nil {
				slog.Warn("Failed to save hOCR file", "error", err)
			}
		} else {
			hocrXML = string(hocrData)
			slog.Info("Using cached hOCR", "filename", hocrFilename)
		}
	} else {
		slog.Info("Generating new hOCR via Google Cloud Vision", "filename", imageFilename)
		hocrXML, err = h.getOCRForImage(imageFilePath)
		if err != nil {
			slog.Warn("Failed to get hOCR from Google Cloud Vision", "error", err)
			utils.RespondWithError(w, "Failed to process image", http.StatusInternalServerError)
			return
		}

		if err := os.WriteFile(hocrFilePath, []byte(hocrXML), 0644); err != nil {
			slog.Warn("Failed to save hOCR file", "error", err)
		} else {
			slog.Info("hOCR cached", "filename", hocrFilename)
		}
	}

	imageItem := models.ImageItem{
		ID:            "img_1",
		ImagePath:     imageFilename,
		ImageURL:      "/static/uploads/" + imageFilename,
		OriginalHOCR:  hocrXML,
		CorrectedHOCR: "",
		Completed:     false,
		ImageWidth:    width,
		ImageHeight:   height,
	}

	session.Images = []models.ImageItem{imageItem}
	h.sessionStore.Set(sessionID, session)

	_, cacheErr := os.Stat(hocrFilePath)
	cacheUsed := cacheErr == nil

	response := map[string]any{
		"session_id": sessionID,
		"message":    "Successfully processed 1 file",
		"images":     1,
		"cache_used": cacheUsed,
		"md5_hash":   md5Hash,
	}

	json.NewEncoder(w).Encode(response)
}

func (h *Handler) getOCRForImage(imagePath string) (string, error) {
	gcvResponse, err := h.ocrService.ProcessImage(imagePath)
	if err != nil {
		return "", err
	}

	converter := hocr.NewConverter()
	hocr, err := converter.ConvertToHOCR(gcvResponse)
	if err != nil {
		return "", fmt.Errorf("failed to convert to hOCR: %w", err)
	}

	return hocr, nil
}

func (h *Handler) HandleStatic(w http.ResponseWriter, r *http.Request) {
	filepath := strings.TrimPrefix(r.URL.Path, "/static/")
	http.ServeFile(w, r, filepath)
}

func (h *Handler) HandleHOCRParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		HOCR string `json:"hocr"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	words, err := parser.ParseHOCRWords(request.HOCR)
	if err != nil {
		http.Error(w, "Failed to parse hOCR", http.StatusBadRequest)
		return
	}

	response := struct {
		Words []models.HOCRWord `json:"words"`
	}{
		Words: words,
	}

	json.NewEncoder(w).Encode(response)
}