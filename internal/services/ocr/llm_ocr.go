package ocr

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
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
	Temperature float64   `json:"temperature,omitempty"`
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

	// Create overlay image with detected text clearly visible
	overlayImagePath, err := s.createTextOverlayImage(imagePath, boundaryBoxResponse)
	if err != nil {
		slog.Warn("Failed to create overlay image, using boundary box detection only", "error", err)
		return boundaryBoxResponse, nil
	}
	// defer os.Remove(overlayImagePath) // Clean up temp file

	slog.Info("Sending overlay image to LLM", "image_path", overlayImagePath)

	// Get text from LLM by reading the overlaid text
	recognizedText, err := s.getTextFromLLM(overlayImagePath)
	if err != nil {
		slog.Warn("LLM text reading failed, using boundary box detection only", "error", err)
		return boundaryBoxResponse, nil
	}

	slog.Info("LLM returned text", "text", recognizedText, "text_length", len(recognizedText))

	// For ProcessImage, we still need to return a GCVResponse structure
	// Fall back to original boundary box response since this method expects that format
	slog.Info("Completed LLM text reading processing, returning original boundary box response")
	return boundaryBoxResponse, nil
}

func (s *LLMOCRService) ProcessImageToHOCR(imagePath string) (string, error) {
	// First, get boundary boxes using our word detection algorithm
	boundaryBoxResponse, err := s.wordDetectionSvc.ProcessImage(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to detect boundary boxes: %w", err)
	}

	slog.Info("Detected word boundary boxes", "word_count", s.countWords(boundaryBoxResponse))

	// Create overlay image with detected text clearly visible
	overlayImagePath, err := s.createTextOverlayImage(imagePath, boundaryBoxResponse)
	if err != nil {
		slog.Warn("Failed to create overlay image, using boundary box detection only", "error", err)
		converter := hocr.NewConverter()
		return converter.ConvertToHOCR(boundaryBoxResponse)
	}
	// defer os.Remove(overlayImagePath) // Clean up temp file

	slog.Info("Created text overlay image", "path", overlayImagePath)
	slog.Info("Sending overlay image to LLM for text reading", "image_path", overlayImagePath)

	// Get text from LLM by reading the overlaid text
	recognizedText, err := s.getTextFromLLM(overlayImagePath)
	if err != nil {
		slog.Warn("LLM text reading failed, using boundary box detection only", "error", err)
		converter := hocr.NewConverter()
		return converter.ConvertToHOCR(boundaryBoxResponse)
	}

	slog.Info("LLM text reading completed", "text", recognizedText, "text_length", len(recognizedText))

	// Fix common CSS class name issues in ChatGPT's response
	recognizedText = strings.ReplaceAll(recognizedText, "ocrx line", "ocrx_line")
	recognizedText = strings.ReplaceAll(recognizedText, "ocr line", "ocrx_line")

	// The LLM output is already complete hOCR markup, just wrap it in basic hOCR structure
	hocrDocument := fmt.Sprintf(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en" lang="en">
<head>
<title></title>
<meta http-equiv="Content-Type" content="text/html;charset=utf-8" />
<meta name='ocr-system' content='hocr-edit with LLM OCR' />
</head>
<body>
<div class='ocr_page' id='page_1'>
%s
</div>
</body>
</html>`, recognizedText)

	return hocrDocument, nil
}

// LineInfo stores information about each line for mapping back to boundaries
type LineInfo struct {
	LineIndex   int
	BoundingBox models.BoundingPoly
	OriginalText string
}

// groupWordsByLine groups words by their Y position to detect actual text lines
func (s *LLMOCRService) groupWordsByLine(words []models.Word) [][]models.Word {
	if len(words) == 0 {
		return [][]models.Word{}
	}
	
	// Sort words by Y position first
	sort.Slice(words, func(i, j int) bool {
		yI := words[i].BoundingBox.Vertices[0].Y
		yJ := words[j].BoundingBox.Vertices[0].Y
		return yI < yJ
	})
	
	var lines [][]models.Word
	currentLine := []models.Word{words[0]}
	
	// Group words that have similar Y positions into the same line
	// Use a tolerance based on average word height
	tolerance := s.calculateLineHeightTolerance(words)
	
	for i := 1; i < len(words); i++ {
		currentY := words[i].BoundingBox.Vertices[0].Y
		previousY := words[i-1].BoundingBox.Vertices[0].Y
		
		if abs(currentY-previousY) <= tolerance {
			// Same line
			currentLine = append(currentLine, words[i])
		} else {
			// New line
			lines = append(lines, currentLine)
			currentLine = []models.Word{words[i]}
		}
	}
	
	// Don't forget the last line
	if len(currentLine) > 0 {
		lines = append(lines, currentLine)
	}
	
	// Sort words within each line by X position (left to right)
	for i := range lines {
		sort.Slice(lines[i], func(j, k int) bool {
			xJ := lines[i][j].BoundingBox.Vertices[0].X
			xK := lines[i][k].BoundingBox.Vertices[0].X
			return xJ < xK
		})
	}
	
	return lines
}

// calculateLineHeightTolerance calculates a tolerance for grouping words into lines
func (s *LLMOCRService) calculateLineHeightTolerance(words []models.Word) int {
	if len(words) == 0 {
		return 10 // Default tolerance
	}
	
	var heights []int
	for _, word := range words {
		if len(word.BoundingBox.Vertices) >= 4 {
			height := word.BoundingBox.Vertices[2].Y - word.BoundingBox.Vertices[0].Y
			heights = append(heights, height)
		}
	}
	
	if len(heights) == 0 {
		return 10 // Default tolerance
	}
	
	// Calculate average height
	sum := 0
	for _, h := range heights {
		sum += h
	}
	avgHeight := sum / len(heights)
	
	// Use half the average height as tolerance
	tolerance := avgHeight / 2
	if tolerance < 5 {
		tolerance = 5 // Minimum tolerance
	}
	
	return tolerance
}

// calculateLineBoundingBox creates a bounding box that encompasses all words in a line
func (s *LLMOCRService) calculateLineBoundingBox(words []models.Word) models.BoundingPoly {
	if len(words) == 0 {
		return models.BoundingPoly{}
	}
	
	minX := words[0].BoundingBox.Vertices[0].X
	minY := words[0].BoundingBox.Vertices[0].Y
	maxX := words[0].BoundingBox.Vertices[2].X
	maxY := words[0].BoundingBox.Vertices[2].Y
	
	// Find the overall bounding box for all words in the line
	for _, word := range words {
		if len(word.BoundingBox.Vertices) >= 4 {
			wordMinX := word.BoundingBox.Vertices[0].X
			wordMinY := word.BoundingBox.Vertices[0].Y
			wordMaxX := word.BoundingBox.Vertices[2].X
			wordMaxY := word.BoundingBox.Vertices[2].Y
			
			if wordMinX < minX {
				minX = wordMinX
			}
			if wordMinY < minY {
				minY = wordMinY
			}
			if wordMaxX > maxX {
				maxX = wordMaxX
			}
			if wordMaxY > maxY {
				maxY = wordMaxY
			}
		}
	}
	
	return models.BoundingPoly{
		Vertices: []models.Vertex{
			{X: minX, Y: minY},
			{X: maxX, Y: minY},
			{X: maxX, Y: maxY},
			{X: minX, Y: maxY},
		},
	}
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

func (s *LLMOCRService) createTextOverlayImage(imagePath string, response models.GCVResponse) (string, error) {
	// Create a stitched image with hOCR tags and line images
	tempDir := "/tmp"
	baseName := strings.TrimSuffix(filepath.Base(imagePath), filepath.Ext(imagePath))
	stitchedPath := filepath.Join(tempDir, fmt.Sprintf("stitched_%s_%d.png", baseName, time.Now().Unix()))

	if len(response.Responses) == 0 || response.Responses[0].FullTextAnnotation == nil {
		return "", fmt.Errorf("no text annotation in response")
	}

	// Create individual components for stitching
	var componentPaths []string
	
	// Process each detected line
	lineNumber := 1
	for _, page := range response.Responses[0].FullTextAnnotation.Pages {
		for _, block := range page.Blocks {
			for _, paragraph := range block.Paragraphs {
				// Group words by their Y position to detect actual text lines
				lines := s.groupWordsByLine(paragraph.Words)
				
				for _, wordLine := range lines {
					if len(wordLine) == 0 {
						continue
					}
					
					// Get line bounding box
					lineBBox := s.calculateLineBoundingBox(wordLine)
					if len(lineBBox.Vertices) < 4 {
						continue
					}
					
					// Create opening span tag image
					openingTag := fmt.Sprintf(`<span class='ocrx_line' title='bbox %d %d %d %d'>
					    <span class='ocrx_word' id='word_1' title='bbox %d %d %d %d'>`, 
						lineBBox.Vertices[0].X, lineBBox.Vertices[0].Y, 
						lineBBox.Vertices[2].X, lineBBox.Vertices[2].Y,
  					lineBBox.Vertices[0].X, lineBBox.Vertices[0].Y,
						lineBBox.Vertices[2].X, lineBBox.Vertices[2].Y)
					openingImagePath, err := s.createTextImage(openingTag, tempDir, fmt.Sprintf("opening_%d", lineNumber))
					if err == nil {
						componentPaths = append(componentPaths, openingImagePath)
					}
					
					// Extract line image from original
					lineImagePath, err := s.extractLineImage(imagePath, lineBBox, tempDir, fmt.Sprintf("line_%d", lineNumber))
					if err == nil {
						componentPaths = append(componentPaths, lineImagePath)
					}
					
					// Create closing span tag image
					closingImagePath, err := s.createTextImage("</span></span>", tempDir, fmt.Sprintf("closing_%d", lineNumber))
					if err == nil {
						componentPaths = append(componentPaths, closingImagePath)
					}
					
					lineNumber++
				}
			}
		}
	}

	if len(componentPaths) == 0 {
		return "", fmt.Errorf("no valid components were created")
	}

	// Stitch all components together vertically
	args := []string{}
	args = append(args, componentPaths...)
	args = append(args, "-append") // Vertical append
	args = append(args, stitchedPath)

	cmd := exec.Command("magick", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	// Clean up component images
	for _, componentPath := range componentPaths {
		os.Remove(componentPath)
	}

	if err != nil {
		slog.Error("Failed to stitch components", "error", err, "stderr", stderr.String(), "cmd", cmd.String())
		return "", fmt.Errorf("failed to stitch components: %w", err)
	}

	slog.Info("Successfully created stitched hOCR image", "path", stitchedPath, "components", len(componentPaths))
	return stitchedPath, nil
}

// createTextImage creates a simple image with just the text
func (s *LLMOCRService) createTextImage(text, tempDir, filename string) (string, error) {
	outputPath := filepath.Join(tempDir, fmt.Sprintf("%s_%d.png", filename, time.Now().Unix()))
	
	cmd := exec.Command("magick",
		"-size", "2000x200",
		"xc:white",
		"-fill", "black",
		"-font", "DejaVu-Sans-Mono",
		"-pointsize", "50",
		"-draw", fmt.Sprintf(`text 10,70 "%s"`, text),
		outputPath)
	
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	
	if err := cmd.Run(); err != nil {
		slog.Error("Failed to create text image", "error", err, "stderr", stderr.String(), "cmd", cmd.String())
		return "", fmt.Errorf("failed to create text image: %w", err)
	}
	
	return outputPath, nil
}

// extractLineImage extracts a line region from the original image
func (s *LLMOCRService) extractLineImage(imagePath string, bbox models.BoundingPoly, tempDir, filename string) (string, error) {
	if len(bbox.Vertices) < 4 {
		return "", fmt.Errorf("invalid bounding box")
	}
	
	outputPath := filepath.Join(tempDir, fmt.Sprintf("%s_%d.png", filename, time.Now().Unix()))
	
	minX := bbox.Vertices[0].X
	minY := bbox.Vertices[0].Y
	maxX := bbox.Vertices[2].X
	maxY := bbox.Vertices[2].Y
	
	width := maxX - minX
	height := maxY - minY
	
	if width <= 0 || height <= 0 {
		return "", fmt.Errorf("invalid dimensions")
	}
	
	// Add some padding
	padding := 2
	cropX := max(0, minX-padding)
	cropY := max(0, minY-padding)
	cropWidth := width + 2*padding
	cropHeight := height + 2*padding
	
	cmd := exec.Command("magick", imagePath,
		"-crop", fmt.Sprintf("%dx%d+%d+%d", cropWidth, cropHeight, cropX, cropY),
		"+repage",
		outputPath)
	
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	
	if err := cmd.Run(); err != nil {
		slog.Error("Failed to extract line image", "error", err, "stderr", stderr.String(), "cmd", cmd.String())
		return "", fmt.Errorf("failed to extract line image: %w", err)
	}
	
	return outputPath, nil
}


func (s *LLMOCRService) getTextFromLLM(stitchedImagePath string) (string, error) {
	// Preprocess the stitched image for better LLM recognition
	enhancedImagePath, err := s.enhanceImageForLLM(stitchedImagePath)
	if err != nil {
		slog.Warn("Failed to enhance image for LLM, using original", "error", err)
		enhancedImagePath = stitchedImagePath
	} else {
		// defer os.Remove(enhancedImagePath) // Clean up enhanced image
	}

	// Get enhanced image as base64
	imageBase64, err := s.getImageAsBase64(enhancedImagePath)
	if err != nil {
		return "", fmt.Errorf("failed to encode image: %w", err)
	}

	// Create OpenAI request
	request := OpenAIRequest{
		Model: s.getModel(),
		Messages: []Message{
			{
				Role: "user",
				Content: []Content{
					{
						Type: "text",
						Text: `Read and transcribe all the hOCR markup that has been overlaid on this image.
						The image contains complete hOCR markup overlaid in a clear, readable font.
						Each line shows hOCR tags with text content like:
						<span class='ocrx_line' id='line_1' title='bbox=x y w h'>
					    <span class='ocrx_word' id='word_1' title='bbox x y w h'>
						followed by an image that needs transcribed.
						When you print you span tags, the span must always start with
						"<span class='ocrx_line'" OR "<span class='ocrx_word'"
					being sure to include the underscore.
						After the image that is transcribed, a </span></span> tag is shown
						You must transcribe BOTH the hOCR tags AND the text content inside them.
						In your response, return the complete hOCR markup exactly as you see it overlaid on the image, including:
						- The opening tags with all attributes (class, bbox, etc.)
						- The actual text content between the opening and closing tags
						- The closing tags
						Read each line of markup in order from top to bottom.
						If the image inside a <span> tag has no legible text, do not print the <span> at all. We do not want blank text boxes.
						Do not add any explanations, formatting, or modifications.
						Simply transcribe the complete hOCR markup (tags + content) you see overlaid on the image exactly as it appears.`,
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

func (s *LLMOCRService) mapTextToOriginalStructure(recognizedText string, originalResponse models.GCVResponse) models.GCVResponse {
	// Parse the recognized text to extract line content
	lines := strings.Split(strings.TrimSpace(recognizedText), "\n")
	
	var extractedLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Extract text after "LINE X: " prefix
		if strings.Contains(line, ": ") {
			parts := strings.SplitN(line, ": ", 2)
			if len(parts) == 2 {
				extractedLines = append(extractedLines, strings.TrimSpace(parts[1]))
			}
		} else if line != "" {
			extractedLines = append(extractedLines, line)
		}
	}

	slog.Info("Mapping extracted text to original structure", "extracted_lines", len(extractedLines))

	// Create a new response with the same structure but updated text
	newResponse := originalResponse

	if len(newResponse.Responses) == 0 || newResponse.Responses[0].FullTextAnnotation == nil {
		return originalResponse
	}

	// Map extracted lines back to the original structure
	lineIdx := 0
	for pageIdx := range newResponse.Responses[0].FullTextAnnotation.Pages {
		for blockIdx := range newResponse.Responses[0].FullTextAnnotation.Pages[pageIdx].Blocks {
			for paragraphIdx := range newResponse.Responses[0].FullTextAnnotation.Pages[pageIdx].Blocks[blockIdx].Paragraphs {
				paragraph := &newResponse.Responses[0].FullTextAnnotation.Pages[pageIdx].Blocks[blockIdx].Paragraphs[paragraphIdx]
				
				// Group words by line and assign extracted text
				lines := s.groupWordsByLine(paragraph.Words)
				for _, wordLine := range lines {
					if lineIdx < len(extractedLines) && len(wordLine) > 0 {
						extractedText := extractedLines[lineIdx]
						
						// Replace the first word's content with the extracted line text
						word := &wordLine[0]
						word.Symbols = []models.Symbol{
							{
								Text:        extractedText,
								BoundingBox: word.BoundingBox,
								Property: &models.Property{
									DetectedBreak: &models.DetectedBreak{
										Type: "LINE_BREAK",
									},
								},
							},
						}
						lineIdx++
					}
				}
			}
		}
	}

	// Update the full text annotation
	allText := strings.Join(extractedLines, "\n")
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
		return "gpt-5"
	}
	return model
}

// enhanceImageForLLM applies the same preprocessing as word detection for better text recognition
func (s *LLMOCRService) enhanceImageForLLM(imagePath string) (string, error) {
	tempDir := "/tmp"
	baseName := strings.TrimSuffix(filepath.Base(imagePath), filepath.Ext(imagePath))
	enhancedPath := fmt.Sprintf("%s/enhanced_llm_%s.jpg", tempDir, strings.ReplaceAll(baseName, "/", "_"))

	// Use the same gentle enhancement as word detection
	cmd := exec.Command("magick", imagePath,
		"-colorspace", "Gray",
		"-contrast-stretch", "0.15x0.05%",
		"-sharpen", "0x1",
		"-threshold", "75%",
		enhancedPath)
	
	slog.Info("Enhancing stitched image for LLM", "input", imagePath, "output", enhancedPath)
	
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("imagemagick enhancement failed: %w", err)
	}

	return enhancedPath, nil
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

	const maxRetries = 5
	const baseDelay = 1 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
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
			if attempt == maxRetries {
				return "", fmt.Errorf("failed to make request after %d attempts: %w", maxRetries+1, err)
			}
			delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay
			slog.Warn("Request failed, retrying", "attempt", attempt+1, "delay", delay, "error", err)
			time.Sleep(delay)
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr != nil {
			if attempt == maxRetries {
				return "", fmt.Errorf("failed to read response body after %d attempts: %w", maxRetries+1, readErr)
			}
			delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay
			slog.Warn("Failed to read response body, retrying", "attempt", attempt+1, "delay", delay, "error", readErr)
			time.Sleep(delay)
			continue
		}

		if resp.StatusCode == http.StatusBadGateway {
			if attempt == maxRetries {
				return "", fmt.Errorf("OpenAI API returned 502 Bad Gateway after %d attempts: %s", maxRetries+1, string(body))
			}
			delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay
			slog.Warn("Received 502 Bad Gateway, retrying with exponential backoff", "attempt", attempt+1, "delay", delay)
			time.Sleep(delay)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("OpenAI API returned status %d: %s", resp.StatusCode, string(body))
		}

		var openAIResponse OpenAIResponse
		if err := json.Unmarshal(body, &openAIResponse); err != nil {
			return "", fmt.Errorf("failed to decode response: %w", err)
		}

		if len(openAIResponse.Choices) == 0 {
			return "", fmt.Errorf("no response from OpenAI")
		}

		slog.Info("OpenAI response received", "length", len(openAIResponse.Choices[0].Message.Content), "attempt", attempt+1)
		return openAIResponse.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("unreachable code")
}

func (s *LLMOCRService) GetDetectionMethod() string {
	return "llm_with_boundary_boxes"
}
