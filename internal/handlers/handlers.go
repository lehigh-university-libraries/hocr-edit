package handlers

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
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
	"github.com/lehigh-university-libraries/hocr-edit/pkg/hocr/parser"
	"github.com/lehigh-university-libraries/hocr-edit/pkg/metrics"
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

func (h *Handler) HandleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		sessions := h.sessionStore.GetAll()
		sessionList := make([]*models.CorrectionSession, 0, len(sessions))
		for _, session := range sessions {
			sessionList = append(sessionList, session)
		}
		if err := json.NewEncoder(w).Encode(sessionList); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
			return
		}
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
		if err := json.NewEncoder(w).Encode(session); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
			return
		}
	case "PUT":
		var updatedSession models.CorrectionSession
		if err := json.NewDecoder(r.Body).Decode(&updatedSession); err != nil {
			slog.Error("Unable to decode session data", "err", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		h.sessionStore.Set(sessionID, &updatedSession)
		if err := json.NewEncoder(w).Encode(updatedSession); err != nil {
			slog.Error("Unable to encode session data", "err", err)
			http.Error(w, "Invalid JSON", http.StatusInternalServerError)
			return
		}
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
		slog.Error("Unable to decode metrics data", "err", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	metricsResult := metrics.CalculateAccuracyMetrics(request.Original, request.Corrected)
	if err := json.NewEncoder(w).Encode(metricsResult); err != nil {
		slog.Error("Unable to encode metrics data", "err", err)
		http.Error(w, "Invalid JSON", http.StatusInternalServerError)
	}
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
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); err != nil {
		slog.Error("Unable to encode success", "err", err)
		http.Error(w, "Invalid JSON", http.StatusInternalServerError)
	}
}

func (h *Handler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Check if this is a JSON request with image URL
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var request struct {
			ImageURL string `json:"image_url"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			utils.RespondWithError(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		if request.ImageURL == "" {
			utils.RespondWithError(w, "image_url is required", http.StatusBadRequest)
			return
		}

		sessionID, err := h.createSessionFromURL(request.ImageURL)
		if err != nil {
			utils.RespondWithError(w, "Failed to process image URL: "+err.Error(), http.StatusBadRequest)
			return
		}

		response := map[string]any{
			"session_id": sessionID,
			"message":    "Successfully processed image from URL",
			"images":     1,
			"cache_used": false,
			"source":     "url",
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			slog.Error("Unable to encode response data", "err", err)
			http.Error(w, "Invalid JSON", http.StatusInternalServerError)
		}
		return
	}

	// Handle file upload (existing logic)
	file, header, err := r.FormFile("files")
	if err != nil {
		file, header, err = r.FormFile("file")
		if err != nil {
			utils.RespondWithError(w, "Failed to read file: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	defer file.Close()

	// Use filename (without extension) as session name, with timestamp for uniqueness
	baseFilename := strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	sessionID := fmt.Sprintf("%s_%d", baseFilename, time.Now().Unix())
	session := &models.CorrectionSession{
		ID:        sessionID,
		Images:    []models.ImageItem{},
		Current:   0,
		CreatedAt: time.Now(),
		Config: models.EvalConfig{
			Model:       h.ocrService.GetDetectionMethod(),
			Prompt:      fmt.Sprintf("%s OCR with hOCR conversion", h.ocrService.GetDetectionMethod()),
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

	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Unable to encode response data", "err", err)
		http.Error(w, "Invalid JSON", http.StatusInternalServerError)
	}
}

func (h *Handler) createSessionFromURL(imageURL string) (string, error) {
	// Download image from URL
	resp, err := http.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download image: HTTP %d", resp.StatusCode)
	}

	// Read image data
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	// Get content type from response
	contentType := resp.Header.Get("Content-Type")

	// Convert JP2/TIFF images using Houdini if needed
	originalImageData := imageData
	if needsHoudiniConversion(contentType, imageURL) {
		slog.Info("Image requires Houdini conversion", "content_type", contentType, "url", imageURL)
		convertedData, err := h.convertImageViaHoudini(imageData, contentType)
		if err != nil {
			return "", fmt.Errorf("failed to convert image via Houdini: %w", err)
		}
		imageData = convertedData
		contentType = "image/jpeg"
	}

	// Calculate MD5 hash of the original image data for consistent caching
	md5Hash := utils.CalculateDataMD5(originalImageData)

	// Extract filename from URL or use md5 hash
	filename := md5Hash
	if urlParts := strings.Split(imageURL, "/"); len(urlParts) > 0 {
		lastPart := urlParts[len(urlParts)-1]
		if lastPart != "" && strings.Contains(lastPart, ".") {
			filename = strings.TrimSuffix(lastPart, filepath.Ext(lastPart))
		}
	}

	// Create session ID using filename and timestamp
	sessionID := fmt.Sprintf("%s_%d", filename, time.Now().Unix())

	// Determine file extension from content type (which may have been updated by Houdini conversion)
	ext := ".jpg" // default
	switch contentType {
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	default:
		// Try to get extension from URL
		if urlExt := filepath.Ext(imageURL); urlExt != "" {
			ext = urlExt
		}
	}

	uploadsDir := "uploads"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create uploads directory: %w", err)
	}

	imageFilename := md5Hash + ext
	hocrFilename := md5Hash + ".xml"
	imageFilePath := filepath.Join(uploadsDir, imageFilename)
	hocrFilePath := filepath.Join(uploadsDir, hocrFilename)

	// Save image file
	if err := os.WriteFile(imageFilePath, imageData, 0644); err != nil {
		return "", fmt.Errorf("failed to save image: %w", err)
	}

	slog.Info("Image downloaded and saved", "filename", imageFilename, "md5", md5Hash, "url", imageURL)

	// Get image dimensions
	width, height := utils.GetImageDimensions(imageFilePath)

	// Process hOCR (check cache first)
	var hocrXML string
	if _, err := os.Stat(hocrFilePath); err == nil {
		hocrData, err := os.ReadFile(hocrFilePath)
		if err != nil {
			slog.Warn("Failed to read existing hOCR file", "error", err, "path", hocrFilePath)
			hocrXML, err = h.getOCRForImage(imageFilePath)
			if err != nil {
				return "", fmt.Errorf("failed to process image with OCR: %w", err)
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
			return "", fmt.Errorf("failed to process image with OCR: %w", err)
		}

		if err := os.WriteFile(hocrFilePath, []byte(hocrXML), 0644); err != nil {
			slog.Warn("Failed to save hOCR file", "error", err)
		} else {
			slog.Info("hOCR cached", "filename", hocrFilename)
		}
	}

	// Create session
	session := &models.CorrectionSession{
		ID:        sessionID,
		Images:    []models.ImageItem{},
		Current:   0,
		CreatedAt: time.Now(),
		Config: models.EvalConfig{
			Model:       h.ocrService.GetDetectionMethod(),
			Prompt:      fmt.Sprintf("%s OCR with hOCR conversion", h.ocrService.GetDetectionMethod()),
			Temperature: 0.0,
			Timestamp:   time.Now().Format("2006-01-02_15-04-05"),
		},
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

	slog.Info("Session created from URL", "session_id", sessionID, "url", imageURL)
	return sessionID, nil
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

	if strings.HasPrefix(filepath, "uploads/") {
		http.ServeFile(w, r, filepath)
		return
	}

	// Extract the file path after /static/
	if filepath == "" {
		filepath = "index.html"
	}

	// Check if image URL parameter is provided
	imageURL := r.URL.Query().Get("image")
	if imageURL != "" {
		// Create session from image URL
		sessionID, err := h.createSessionFromURL(imageURL)
		if err != nil {
			slog.Error("Failed to create session from URL", "url", imageURL, "error", err)
			http.Error(w, "Failed to process image URL: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Redirect to the session
		http.Redirect(w, r, "/hocr/?session="+sessionID, http.StatusFound)
		return
	}

	// Check if Drupal node ID parameter is provided
	nid := r.URL.Query().Get("nid")
	if nid != "" {
		// Create session from Drupal node
		sessionID, err := h.createSessionFromDrupalNode(nid)
		if err != nil {
			slog.Error("Failed to create session from Drupal node", "nid", nid, "error", err)
			http.Error(w, "Failed to process Drupal node: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Redirect to the session
		http.Redirect(w, r, "/hocr/?session="+sessionID, http.StatusFound)
		return
	}

	// Prevent directory traversal attacks
	if strings.Contains(filepath, "..") {
		http.Error(w, "Invalid file path", http.StatusBadRequest)
		return
	}

	// Set appropriate content type based on file extension
	switch {
	case strings.HasSuffix(filepath, ".css"):
		w.Header().Set("Content-Type", "text/css")
	case strings.HasSuffix(filepath, ".js"):
		w.Header().Set("Content-Type", "application/javascript")
	case strings.HasSuffix(filepath, ".html"):
		w.Header().Set("Content-Type", "text/html")
	}

	// Serve files from the static directory
	fullPath := "static/" + filepath
	http.ServeFile(w, r, fullPath)
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
		slog.Error("Unable to parse hocr", "hocr", request.HOCR, "err", err)
		http.Error(w, "Failed to parse hOCR", http.StatusBadRequest)
		return
	}

	response := struct {
		Words []models.HOCRWord `json:"words"`
	}{
		Words: words,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Unable to encode response data", "err", err)
		http.Error(w, "Invalid JSON", http.StatusInternalServerError)
	}
}

// convertImageViaHoudini converts JP2/TIFF images to JPG using Houdini service
func (h *Handler) convertImageViaHoudini(imageData []byte, contentType string) ([]byte, error) {
	houdiniURL := os.Getenv("HOUDINI_URL")
	if houdiniURL == "" {
		return nil, fmt.Errorf("HOUDINI_URL environment variable not set")
	}

	if contentType == "application/octet-stream" {
		contentType = "image/jp2"
	}

	// Create cache key based on image data hash
	hash := md5.Sum(imageData)
	cacheKey := hex.EncodeToString(hash[:])
	cacheFilename := cacheKey + "_converted.jpg"
	cacheDir := "cache/houdini"
	cachePath := filepath.Join(cacheDir, cacheFilename)

	// Check cache first
	if cachedData, err := os.ReadFile(cachePath); err == nil {
		slog.Info("Using cached Houdini conversion", "cache_key", cacheKey)
		return cachedData, nil
	}

	// Create cache directory
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		slog.Warn("Failed to create Houdini cache directory", "error", err)
	}

	slog.Info("Converting image via Houdini", "content_type", contentType, "size", len(imageData))

	// Make request to Houdini
	req, err := http.NewRequest("POST", houdiniURL, bytes.NewReader(imageData))
	if err != nil {
		return nil, fmt.Errorf("failed to create Houdini request: %w", err)
	}

	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "image/jpeg")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("houdini request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("houdini returned HTTP %d", resp.StatusCode)
	}

	// Read converted image
	convertedData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Houdini response: %w", err)
	}

	// Cache the converted image
	if err := os.WriteFile(cachePath, convertedData, 0644); err != nil {
		slog.Warn("Failed to cache Houdini conversion", "error", err)
	} else {
		slog.Info("Cached Houdini conversion", "cache_key", cacheKey, "size", len(convertedData))
	}

	return convertedData, nil
}

// needsHoudiniConversion checks if the image format requires Houdini conversion
func needsHoudiniConversion(contentType, url string) bool {
	// Check content type first
	switch contentType {
	case "image/jp2", "image/jpeg2000", "image/tiff", "image/tif":
		return true
	}

	// Check file extension from URL as fallback
	ext := strings.ToLower(filepath.Ext(url))
	switch ext {
	case ".jp2", ".jpx", ".j2k", ".tiff", ".tif":
		return true
	}

	return false
}

// DrupalFileObject represents a single file object from Drupal
type DrupalFileObject struct {
	URI      string `json:"uri"`
	TermName string `json:"term_name"`
	TID      string `json:"tid"`
	NID      string `json:"nid"`
	ViewNode string `json:"view_node"`
}

// DrupalHOCRData represents the JSON response from Drupal HOCR endpoint (array of file objects)
type DrupalHOCRData []DrupalFileObject

// createSessionFromDrupalNode creates a session from a Drupal node ID
func (h *Handler) createSessionFromDrupalNode(nid string) (string, error) {
	drupalURL := os.Getenv("DRUPAL_HOCR_URL")
	if drupalURL == "" {
		return "", fmt.Errorf("DRUPAL_HOCR_URL environment variable not set")
	}

	// Format the URL with the node ID
	requestURL := fmt.Sprintf(drupalURL, nid)
	slog.Info("Fetching Drupal HOCR data", "nid", nid, "url", requestURL)

	// Make request to Drupal
	resp, err := http.Get(requestURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch Drupal data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("drupal returned HTTP %d", resp.StatusCode)
	}

	// Parse JSON response
	var drupalData DrupalHOCRData
	if err := json.NewDecoder(resp.Body).Decode(&drupalData); err != nil {
		return "", fmt.Errorf("failed to parse Drupal JSON: %w", err)
	}

	if len(drupalData) == 0 {
		return "", fmt.Errorf("no file objects provided by Drupal")
	}

	// Find service file and hOCR file
	var serviceFile, hocrFile *DrupalFileObject
	for i, fileObj := range drupalData {
		switch fileObj.TermName {
		case "Service File":
			serviceFile = &drupalData[i]
		case "hOCR":
			hocrFile = &drupalData[i]
		}
	}

	if serviceFile == nil {
		return "", fmt.Errorf("no Service File found in Drupal response")
	}

	if hocrFile == nil {
		return "", fmt.Errorf("no hOCR file found in Drupal response")
	}

	baseUrl := strings.Replace(drupalURL, "/node/%s/hocr", "", 1)
	// Construct image URL from service file
	imageURL := baseUrl + serviceFile.ViewNode + serviceFile.URI

	// Construct hOCR upload URL
	hocrUploadURL := fmt.Sprintf("%s/node/%s%s/media/file/%s", baseUrl, nid, serviceFile.ViewNode, hocrFile.TID)

	slog.Info("Retrieved Drupal data", "nid", nid, "image_url", imageURL, "hocr_upload", hocrUploadURL)

	// Check if we should use existing hOCR (if URI contains "gcloud") or generate new hOCR
	var sessionID string
	var sessionErr error

	if strings.Contains(hocrFile.URI, "gcloud") {
		// Download and use existing hOCR instead of calling Google Cloud Vision
		slog.Info("Using existing hOCR from Drupal", "nid", nid, "hocr_uri", hocrFile.URI)
		sessionID, sessionErr = h.createSessionFromDrupalWithExistingHOCR(imageURL, hocrFile.ViewNode+hocrFile.URI, nid)
	} else {
		// Generate new hOCR using Google Cloud Vision API (same as normal image upload)
		slog.Info("Generating new hOCR via Google Cloud Vision", "nid", nid, "hocr_uri", hocrFile.URI)
		sessionID, sessionErr = h.createSessionFromDrupalWithNewHOCR(imageURL, nid)
	}

	if sessionErr != nil {
		return "", fmt.Errorf("failed to create session from Drupal: %w", sessionErr)
	}

	// Store the Drupal upload URL in the session for later use
	session, exists := h.sessionStore.Get(sessionID)
	if exists {
		// Add Drupal metadata to session
		session.Config.Prompt = fmt.Sprintf("Drupal Node %s - %s", nid, session.Config.Prompt)

		// Store upload URL in the new dedicated field
		if len(session.Images) > 0 {
			session.Images[0].DrupalUploadURL = hocrUploadURL
			session.Images[0].DrupalNid = nid
		}

		h.sessionStore.Set(sessionID, session)
	}

	return sessionID, nil
}

// createSessionFromDrupalWithExistingHOCR creates a session using existing hOCR from Drupal
func (h *Handler) createSessionFromDrupalWithExistingHOCR(imageURL, hocrURL, nid string) (string, error) {
	// Download image from URL (similar to createSessionFromURL but use existing hOCR)
	resp, err := http.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download image: HTTP %d", resp.StatusCode)
	}

	// Read image data
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	// Get content type from response
	contentType := resp.Header.Get("Content-Type")

	// Convert JP2/TIFF images using Houdini if needed
	originalImageData := imageData
	if needsHoudiniConversion(contentType, imageURL) {
		slog.Info("Image requires Houdini conversion", "content_type", contentType, "url", imageURL)
		convertedData, err := h.convertImageViaHoudini(imageData, contentType)
		if err != nil {
			return "", fmt.Errorf("failed to convert image via Houdini: %w", err)
		}
		imageData = convertedData
		contentType = "image/jpeg" // Houdini converts to JPEG
	}

	// Calculate MD5 hash of the original image data for consistent caching
	md5Hash := utils.CalculateDataMD5(originalImageData)

	// Extract filename from URL or use md5 hash
	filename := md5Hash
	if urlParts := strings.Split(imageURL, "/"); len(urlParts) > 0 {
		lastPart := urlParts[len(urlParts)-1]
		if lastPart != "" && strings.Contains(lastPart, ".") {
			filename = strings.TrimSuffix(lastPart, filepath.Ext(lastPart))
		}
	}

	// Use NID in session name for Drupal sessions
	sessionID := fmt.Sprintf("drupal_%s_%s_%d", nid, filename, time.Now().Unix())

	// Determine file extension from content type (which may have been updated by Houdini conversion)
	ext := ".jpg" // default
	switch contentType {
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	default:
		// Try to get extension from URL
		if urlExt := filepath.Ext(imageURL); urlExt != "" {
			ext = urlExt
		}
	}

	uploadsDir := "uploads"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create uploads directory: %w", err)
	}

	imageFilename := md5Hash + ext
	imageFilePath := filepath.Join(uploadsDir, imageFilename)

	// Save image file
	if err := os.WriteFile(imageFilePath, imageData, 0644); err != nil {
		return "", fmt.Errorf("failed to save image: %w", err)
	}

	slog.Info("Image downloaded and saved", "filename", imageFilename, "md5", md5Hash, "url", imageURL)

	// Get image dimensions
	width, height := utils.GetImageDimensions(imageFilePath)

	// Download existing hOCR
	hocrResp, err := http.Get(hocrURL)
	if err != nil {
		return "", fmt.Errorf("failed to download existing hOCR: %w", err)
	}
	defer hocrResp.Body.Close()

	if hocrResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download hOCR: HTTP %d", hocrResp.StatusCode)
	}

	hocrData, err := io.ReadAll(hocrResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read hOCR data: %w", err)
	}

	hocrXML := string(hocrData)
	slog.Info("Using existing hOCR from Drupal", "nid", nid, "hocr_url", hocrURL)

	// Create session
	session := &models.CorrectionSession{
		ID:        sessionID,
		Images:    []models.ImageItem{},
		Current:   0,
		CreatedAt: time.Now(),
		Config: models.EvalConfig{
			Model:       "drupal_existing_hocr",
			Prompt:      "Using existing hOCR from Drupal",
			Temperature: 0.0,
			Timestamp:   time.Now().Format("2006-01-02_15-04-05"),
		},
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

	slog.Info("Session created from Drupal with existing hOCR", "session_id", sessionID, "nid", nid)
	return sessionID, nil
}

// createSessionFromDrupalWithNewHOCR creates a session and generates new hOCR via Google Cloud Vision
func (h *Handler) createSessionFromDrupalWithNewHOCR(imageURL, nid string) (string, error) {
	// Download image from URL (similar to createSessionFromURL)
	resp, err := http.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download image: HTTP %d", resp.StatusCode)
	}

	// Read image data
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	// Get content type from response
	contentType := resp.Header.Get("Content-Type")

	// Convert JP2/TIFF images using Houdini if needed
	originalImageData := imageData
	if needsHoudiniConversion(contentType, imageURL) {
		slog.Info("Image requires Houdini conversion", "content_type", contentType, "url", imageURL)
		convertedData, err := h.convertImageViaHoudini(imageData, contentType)
		if err != nil {
			return "", fmt.Errorf("failed to convert image via Houdini: %w", err)
		}
		imageData = convertedData
		contentType = "image/jpeg" // Houdini converts to JPEG
	}

	// Calculate MD5 hash of the original image data for consistent caching
	md5Hash := utils.CalculateDataMD5(originalImageData)

	// Extract filename from URL or use md5 hash
	filename := md5Hash
	if urlParts := strings.Split(imageURL, "/"); len(urlParts) > 0 {
		lastPart := urlParts[len(urlParts)-1]
		if lastPart != "" && strings.Contains(lastPart, ".") {
			filename = strings.TrimSuffix(lastPart, filepath.Ext(lastPart))
		}
	}

	// Use NID in session name for Drupal sessions
	sessionID := fmt.Sprintf("drupal_%s_%s_%d", nid, filename, time.Now().Unix())

	// Determine file extension from content type (which may have been updated by Houdini conversion)
	ext := ".jpg" // default
	switch contentType {
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	default:
		// Try to get extension from URL
		if urlExt := filepath.Ext(imageURL); urlExt != "" {
			ext = urlExt
		}
	}

	uploadsDir := "uploads"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create uploads directory: %w", err)
	}

	imageFilename := md5Hash + ext
	hocrFilename := md5Hash + ".xml"
	imageFilePath := filepath.Join(uploadsDir, imageFilename)
	hocrFilePath := filepath.Join(uploadsDir, hocrFilename)

	// Save image file
	if err := os.WriteFile(imageFilePath, imageData, 0644); err != nil {
		return "", fmt.Errorf("failed to save image: %w", err)
	}

	slog.Info("Image downloaded and saved", "filename", imageFilename, "md5", md5Hash, "url", imageURL)

	// Get image dimensions
	width, height := utils.GetImageDimensions(imageFilePath)

	// Process hOCR (check cache first, then generate via Google Cloud Vision)
	var hocrXML string
	if _, err := os.Stat(hocrFilePath); err == nil {
		hocrData, err := os.ReadFile(hocrFilePath)
		if err != nil {
			slog.Warn("Failed to read existing hOCR file", "error", err, "path", hocrFilePath)
			hocrXML, err = h.getOCRForImage(imageFilePath)
			if err != nil {
				return "", fmt.Errorf("failed to process image with OCR: %w", err)
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
			return "", fmt.Errorf("failed to process image with OCR: %w", err)
		}

		if err := os.WriteFile(hocrFilePath, []byte(hocrXML), 0644); err != nil {
			slog.Warn("Failed to save hOCR file", "error", err)
		} else {
			slog.Info("hOCR cached", "filename", hocrFilename)
		}
	}

	// Create session
	session := &models.CorrectionSession{
		ID:        sessionID,
		Images:    []models.ImageItem{},
		Current:   0,
		CreatedAt: time.Now(),
		Config: models.EvalConfig{
			Model:       "google_cloud_vision",
			Prompt:      "Google Cloud Vision OCR with hOCR conversion for Drupal",
			Temperature: 0.0,
			Timestamp:   time.Now().Format("2006-01-02_15-04-05"),
		},
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

	slog.Info("Session created from Drupal with new hOCR", "session_id", sessionID, "nid", nid)
	return sessionID, nil
}
