package ocr

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lehigh-university-libraries/hocr-edit/internal/models"
	"github.com/lehigh-university-libraries/hocr-edit/internal/services/hocr"
)

type LLMOCRService struct {
	wordDetectionSvc *WordDetectionService
}

func NewLLMOCR() *LLMOCRService {
	return &LLMOCRService{
		wordDetectionSvc: NewWordDetection(),
	}
}

type OpenAIRequest struct {
	Model       string    `json:"model"`
	Temperature float64   `json:"temperature"`
	Messages    []Message `json:"messages"`
}

type Message struct {
	Role    string    `json:"role"`
	Content []Content `json:"content"`
}

type Content struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (s *LLMOCRService) ProcessImage(imagePath string) (models.GCVResponse, error) {
	// This method is kept for interface compatibility but we primarily use ProcessImageToHOCR
	// For LLM OCR, we do the full processing and return the corrected response

	// First, get boundary boxes using our word detection algorithm
	boundaryBoxResponse, err := s.wordDetectionSvc.ProcessImage(imagePath)
	if err != nil {
		return models.GCVResponse{}, fmt.Errorf("failed to detect boundary boxes: %w", err)
	}

	// Extract word regions and create stitched image
	stitchedImagePath, wordOrder, err := s.createStitchedWordImage(imagePath, boundaryBoxResponse)
	if err != nil {
		slog.Warn("Failed to create stitched image, using boundary box detection only", "error", err)
		return boundaryBoxResponse, nil
	}
	//	defer os.Remove(stitchedImagePath) // Clean up temp file

	slog.Info("Sending stitched image to LLM", "image_path", stitchedImagePath)

	// Get text from LLM using the stitched image
	recognizedText, err := s.getTextFromLLM(stitchedImagePath)
	if err != nil {
		slog.Warn("LLM text recognition failed, using boundary box detection only", "error", err)
		return boundaryBoxResponse, nil
	}

	slog.Info("LLM returned text", "text", recognizedText, "text_length", len(recognizedText))

	// Map the recognized text back to the original boundary boxes
	correctedResponse := s.mapTextToWordBoxes(recognizedText, boundaryBoxResponse, wordOrder)

	slog.Info("Completed LLM OCR processing", "original_words", len(wordOrder), "corrected_response_ready", true)
	return correctedResponse, nil
}

func (s *LLMOCRService) ProcessImageToHOCR(imagePath string) (string, error) {
	// First, get boundary boxes using our word detection algorithm
	boundaryBoxResponse, err := s.wordDetectionSvc.ProcessImage(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to detect boundary boxes: %w", err)
	}

	slog.Info("Detected word boundary boxes", "word_count", s.countWords(boundaryBoxResponse))

	// Extract word regions and create stitched image
	stitchedImagePath, wordOrder, err := s.createStitchedWordImage(imagePath, boundaryBoxResponse)
	if err != nil {
		slog.Warn("Failed to create stitched image, using boundary box detection only", "error", err)
		converter := hocr.NewConverter()
		return converter.ConvertToHOCR(boundaryBoxResponse)
	}
	defer os.Remove(stitchedImagePath) // Clean up temp file

	slog.Info("Created stitched word image", "path", stitchedImagePath, "word_count", len(wordOrder))
	slog.Info("Sending stitched image to LLM for text recognition", "image_path", stitchedImagePath)

	// Get text from LLM using the stitched image
	recognizedText, err := s.getTextFromLLM(stitchedImagePath)
	if err != nil {
		slog.Warn("LLM text recognition failed, using boundary box detection only", "error", err)
		converter := hocr.NewConverter()
		return converter.ConvertToHOCR(boundaryBoxResponse)
	}

	slog.Info("LLM text recognition completed", "text", recognizedText, "text_length", len(recognizedText))

	// Map the recognized text back to the original boundary boxes
	correctedResponse := s.mapTextToWordBoxes(recognizedText, boundaryBoxResponse, wordOrder)

	slog.Info("Text mapped back to word boxes, converting to hOCR", "corrected_words", s.countWords(correctedResponse))

	// Convert to hOCR
	converter := hocr.NewConverter()
	return converter.ConvertToHOCR(correctedResponse)
}

// WordInfo stores information about each word for mapping back to boundaries
type WordInfo struct {
	WordIndex    int
	ParagraphIdx int
	WordIdx      int
	BoundingBox  models.BoundingPoly
	OriginalText string
}

func (s *LLMOCRService) countWords(response models.GCVResponse) int {
	count := 0
	if len(response.Responses) == 0 || response.Responses[0].FullTextAnnotation == nil {
		return count
	}
	for _, page := range response.Responses[0].FullTextAnnotation.Pages {
		for _, block := range page.Blocks {
			for _, paragraph := range block.Paragraphs {
				count += len(paragraph.Words)
			}
		}
	}
	return count
}

func (s *LLMOCRService) createStitchedWordImage(imagePath string, response models.GCVResponse) (string, []WordInfo, error) {
	// Load original image
	originalFile, err := os.Open(imagePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to open original image: %w", err)
	}
	defer originalFile.Close()

	_, _, err = image.Decode(originalFile)
	if err != nil {
		return "", nil, fmt.Errorf("failed to decode original image: %w", err)
	}

	// Collect all words with their positions
	var wordInfos []WordInfo
	wordIndex := 0

	if len(response.Responses) == 0 || response.Responses[0].FullTextAnnotation == nil {
		return "", nil, fmt.Errorf("no text annotation in response")
	}

	for _, page := range response.Responses[0].FullTextAnnotation.Pages {
		for _, block := range page.Blocks {
			for pIdx, paragraph := range block.Paragraphs {
				for wIdx, word := range paragraph.Words {
					wordInfo := WordInfo{
						WordIndex:    wordIndex,
						ParagraphIdx: pIdx,
						WordIdx:      wIdx,
						BoundingBox:  word.BoundingBox,
						OriginalText: fmt.Sprintf("word_%d", wordIndex+1), // Placeholder
					}
					wordInfos = append(wordInfos, wordInfo)
					wordIndex++
				}
			}
		}
	}

	// Sort words by reading order (top to bottom, left to right)
	sort.Slice(wordInfos, func(i, j int) bool {
		// Get Y positions (top of bounding box)
		yI := wordInfos[i].BoundingBox.Vertices[0].Y
		yJ := wordInfos[j].BoundingBox.Vertices[0].Y

		// If on roughly the same line (within some threshold), sort by X
		heightI := wordInfos[i].BoundingBox.Vertices[2].Y - yI
		threshold := heightI / 2
		if abs(yI-yJ) < threshold {
			return wordInfos[i].BoundingBox.Vertices[0].X < wordInfos[j].BoundingBox.Vertices[0].X
		}
		return yI < yJ
	})

	// Create stitched image using ImageMagick
	tempDir := "/tmp"
	baseName := strings.TrimSuffix(filepath.Base(imagePath), filepath.Ext(imagePath))
	stitchedPath := filepath.Join(tempDir, fmt.Sprintf("stitched_%s_%d.png", baseName, time.Now().Unix()))

	err = s.createStitchedImageWithImageMagick(imagePath, wordInfos, stitchedPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create stitched image: %w", err)
	}

	return stitchedPath, wordInfos, nil
}

func (s *LLMOCRService) createStitchedImageWithImageMagick(imagePath string, wordInfos []WordInfo, outputPath string) error {
	// Create individual word images first, then stitch them together
	tempDir := "/tmp"
	var wordImagePaths []string

	for i, wordInfo := range wordInfos {
		bbox := wordInfo.BoundingBox
		if len(bbox.Vertices) < 4 {
			continue // Skip malformed bounding boxes
		}

		// Calculate crop dimensions
		minX := bbox.Vertices[0].X
		minY := bbox.Vertices[0].Y
		maxX := bbox.Vertices[2].X
		maxY := bbox.Vertices[2].Y

		width := maxX - minX
		height := maxY - minY

		if width <= 0 || height <= 0 {
			continue // Skip invalid dimensions
		}

		// Add some padding around each word
		padding := 5
		cropX := max(0, minX-padding)
		cropY := max(0, minY-padding)
		cropWidth := width + 2*padding
		cropHeight := height + 2*padding

		// Create individual word image
		wordImagePath := filepath.Join(tempDir, fmt.Sprintf("word_%d_%d.png", time.Now().Unix(), i))

		cmd := exec.Command("magick", imagePath,
			"-crop", fmt.Sprintf("%dx%d+%d+%d", cropWidth, cropHeight, cropX, cropY),
			"+repage", // Remove the virtual canvas
			wordImagePath)

		if err := cmd.Run(); err != nil {
			slog.Warn("Failed to extract word image", "word_index", i, "error", err)
			continue
		}

		wordImagePaths = append(wordImagePaths, wordImagePath)
	}

	if len(wordImagePaths) == 0 {
		return fmt.Errorf("no valid word images were extracted")
	}

	// Stitch all word images together vertically
	args := []string{}
	args = append(args, wordImagePaths...)
	args = append(args, "-append") // Vertical append
	args = append(args, outputPath)

	cmd := exec.Command("magick", args...)
	err := cmd.Run()

	// Clean up individual word images
	for _, wordImagePath := range wordImagePaths {
		os.Remove(wordImagePath)
	}

	if err != nil {
		return fmt.Errorf("failed to stitch word images: %w", err)
	}

	slog.Info("Successfully created stitched image", "path", outputPath, "word_count", len(wordImagePaths))
	return nil
}

func (s *LLMOCRService) getTextFromLLM(stitchedImagePath string) (string, error) {
	// Get image as base64
	imageBase64, err := s.getImageAsBase64(stitchedImagePath)
	if err != nil {
		return "", fmt.Errorf("failed to encode image: %w", err)
	}

	// Create OpenAI request
	request := OpenAIRequest{
		Model:       s.getModel(),
		Temperature: 0.0,
		Messages: []Message{
			{
				Role: "user",
				Content: []Content{
					{
						Type: "text",
						Text: `Extract all text from this image.
						Each line in the image has one to five word(s).
						In your response, return only the text, one line per line, in the order they appear from top to bottom.
						Do not add any explanations or formatting.
						None of the lines repeat, so if you're going to repeat the same line twice, try harder.
						Though it's possible the same line is repeated later in the document, the likelyhood of two lines being the same is very unlikely. `,
					},
					{
						Type: "image_url",
						ImageURL: &ImageURL{
							URL: fmt.Sprintf("data:image/png;base64,%s", imageBase64),
						},
					},
				},
			},
		},
	}

	slog.Info("Making OpenAI API call", "model", request.Model)

	// Call OpenAI API
	response, err := s.callOpenAI(request)
	if err != nil {
		slog.Error("OpenAI API call failed", "error", err)
		return "", fmt.Errorf("OpenAI API call failed: %w", err)
	}

	cleanResponse := strings.TrimSpace(response)
	slog.Info("OpenAI API call successful", "response_length", len(cleanResponse), "response", cleanResponse)

	return cleanResponse, nil
}

func (s *LLMOCRService) mapTextToWordBoxes(recognizedText string, originalResponse models.GCVResponse, wordOrder []WordInfo) models.GCVResponse {
	// Split recognized text into individual words
	words := strings.Split(strings.TrimSpace(recognizedText), "\n")

	// Clean up words (remove extra whitespace)
	var cleanWords []string
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word != "" {
			cleanWords = append(cleanWords, word)
		}
	}

	slog.Info("Mapping text to word boxes", "recognized_words", len(cleanWords), "original_words", len(wordOrder))

	// Create a new response with the same structure but updated text
	newResponse := originalResponse

	if len(newResponse.Responses) == 0 || newResponse.Responses[0].FullTextAnnotation == nil {
		return originalResponse
	}

	// Map words back to their original positions
	wordIdx := 0
	for pageIdx := range newResponse.Responses[0].FullTextAnnotation.Pages {
		for blockIdx := range newResponse.Responses[0].FullTextAnnotation.Pages[pageIdx].Blocks {
			for paragraphIdx := range newResponse.Responses[0].FullTextAnnotation.Pages[pageIdx].Blocks[blockIdx].Paragraphs {
				for wIdx := range newResponse.Responses[0].FullTextAnnotation.Pages[pageIdx].Blocks[blockIdx].Paragraphs[paragraphIdx].Words {
					if wordIdx < len(cleanWords) {
						// Update the word text in all symbols
						word := &newResponse.Responses[0].FullTextAnnotation.Pages[pageIdx].Blocks[blockIdx].Paragraphs[paragraphIdx].Words[wIdx]
						recognizedWord := cleanWords[wordIdx]

						// Clear existing symbols and create new ones
						word.Symbols = []models.Symbol{
							{
								Text:        recognizedWord,
								BoundingBox: word.BoundingBox, // Use the word's bounding box for the symbol too
							},
						}
						wordIdx++
					}
				}
			}
		}
	}

	// Update the full text annotation
	allText := strings.Join(cleanWords, " ")
	newResponse.Responses[0].FullTextAnnotation.Text = allText

	return newResponse
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *LLMOCRService) getModel() string {
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		return "gpt-4o"
	}
	return model
}

func (s *LLMOCRService) getImageAsBase64(imagePath string) (string, error) {
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(imageData), nil
}

func (s *LLMOCRService) callOpenAI(request OpenAIRequest) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY environment variable not set")
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(requestBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{
		Timeout: 600 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API returned status %d: %s", resp.StatusCode, string(body))
	}

	var openAIResponse OpenAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResponse); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(openAIResponse.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI")
	}

	slog.Info("OpenAI response received", "length", len(openAIResponse.Choices[0].Message.Content))
	return openAIResponse.Choices[0].Message.Content, nil
}

func (s *LLMOCRService) GetDetectionMethod() string {
	return "llm_with_boundary_boxes"
}
