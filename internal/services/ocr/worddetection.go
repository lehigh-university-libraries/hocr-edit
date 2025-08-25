package ocr

import (
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lehigh-university-libraries/hocr-edit/internal/models"
)

type WordDetectionService struct{}

func NewWordDetection() *WordDetectionService {
	return &WordDetectionService{}
}

type LineBoundingBox struct {
	X, Y, Width, Height int
}

func (s *WordDetectionService) ProcessImage(imagePath string) (models.GCVResponse, error) {
	// Load image
	file, err := os.Open(imagePath)
	if err != nil {
		return models.GCVResponse{}, fmt.Errorf("failed to open image: %w", err)
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return models.GCVResponse{}, fmt.Errorf("failed to decode image: %w", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Detect text lines in the image
	lineBoxes, err := s.detectTextLines(imagePath, width, height)
	if err != nil {
		return models.GCVResponse{}, fmt.Errorf("failed to detect text lines: %w", err)
	}

	// Convert to GCV-compatible response format
	return s.convertLinesToGCVResponse(lineBoxes, width, height), nil
}

func (s *WordDetectionService) detectTextLines(imagePath string, width, height int) ([]LineBoundingBox, error) {
	// Use ImageMagick to preprocess the image for line detection
	tempDir := "/tmp"
	baseName := strings.TrimSuffix(filepath.Base(imagePath), filepath.Ext(imagePath))
	processedPath := fmt.Sprintf("%s/processed_%s.jpg", tempDir, strings.ReplaceAll(baseName, "/", "_"))
//	defer os.Remove(processedPath)

	// Convert to grayscale and gently enhance for both printed and handwritten text
	cmd := exec.Command("magick", imagePath,
		"-colorspace", "Gray",
		"-contrast-stretch", "0.15x0.05%",
		"-sharpen", "0x1",
		"-threshold", "75%",
		processedPath)
	slog.Info("Converting image", "cmd", cmd.String())
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("imagemagick preprocessing failed: %w", err)
	}

	// Load processed image
	file, err := os.Open(processedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open processed image: %w", err)
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("failed to decode processed image: %w", err)
	}

	// Find connected components (text regions)
	components := s.findConnectedComponents(img)

	// Group components into text lines based on Y-coordinate
	lineBoxes := s.groupComponentsIntoLines(components, width, height)

	return lineBoxes, nil
}

func (s *WordDetectionService) findConnectedComponents(img image.Image) []LineBoundingBox {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Create a visited map
	visited := make([][]bool, height)
	for i := range visited {
		visited[i] = make([]bool, width)
	}

	var components []LineBoundingBox

	// Find all connected components using flood fill
	for y := range height {
		for x := range width {
			if !visited[y][x] && s.isTextPixel(img.At(x, y)) {
				// Start flood fill to find component bounds
				minX, minY := x, y
				maxX, maxY := x, y
				s.floodFill(img, visited, x, y, &minX, &minY, &maxX, &maxY)

				// Only keep components of reasonable size (filter noise)
				w := maxX - minX + 1
				h := maxY - minY + 1
				if w >= 5 && h >= 8 && w <= width/2 && h <= height/4 {
					components = append(components, LineBoundingBox{
						X:      minX,
						Y:      minY,
						Width:  w,
						Height: h,
					})
				}
			}
		}
	}

	return components
}

func (s *WordDetectionService) floodFill(img image.Image, visited [][]bool, x, y int, minX, minY, maxX, maxY *int) {
	bounds := img.Bounds()
	if x < 0 || x >= bounds.Dx() || y < 0 || y >= bounds.Dy() || visited[y][x] || !s.isTextPixel(img.At(x, y)) {
		return
	}

	visited[y][x] = true

	// Update bounding box
	if x < *minX {
		*minX = x
	}
	if x > *maxX {
		*maxX = x
	}
	if y < *minY {
		*minY = y
	}
	if y > *maxY {
		*maxY = y
	}

	// Recursively check 8 neighbors
	directions := [][]int{{-1, -1}, {-1, 0}, {-1, 1}, {0, -1}, {0, 1}, {1, -1}, {1, 0}, {1, 1}}
	for _, dir := range directions {
		s.floodFill(img, visited, x+dir[0], y+dir[1], minX, minY, maxX, maxY)
	}
}

func (s *WordDetectionService) isTextPixel(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	// Convert to grayscale (16-bit values)
	gray := (r + g + b) / 3
	// Consider dark pixels as text (threshold at 50% gray)
	return gray < 32768
}

func (s *WordDetectionService) groupComponentsIntoLines(components []LineBoundingBox, imgWidth, imgHeight int) []LineBoundingBox {
	if len(components) == 0 {
		return components
	}

	// Sort components by Y coordinate first for line grouping
	sort.Slice(components, func(i, j int) bool {
		return components[i].Y < components[j].Y
	})

	var lineBoxes []LineBoundingBox
	var currentLineComponents []LineBoundingBox

	for _, component := range components {
		if len(currentLineComponents) == 0 {
			currentLineComponents = append(currentLineComponents, component)
			continue
		}

		// Calculate average height of current line components
		avgHeight := 0
		for _, comp := range currentLineComponents {
			avgHeight += comp.Height
		}
		avgHeight /= len(currentLineComponents)

		// Check if this component belongs to the same line
		// Components belong to same line if they have similar Y positions
		yOverlap := false
		for _, lineComp := range currentLineComponents {
			// Check for Y-coordinate overlap (allowing some tolerance)
			tolerance := avgHeight / 2
			if !(component.Y+component.Height < lineComp.Y-tolerance || 
			     component.Y > lineComp.Y+lineComp.Height+tolerance) {
				yOverlap = true
				break
			}
		}

		if yOverlap {
			currentLineComponents = append(currentLineComponents, component)
		} else {
			// Finish current line and start new one
			if len(currentLineComponents) > 0 {
				lineBox := s.mergeLineComponents(currentLineComponents)
				lineBoxes = append(lineBoxes, lineBox)
			}
			currentLineComponents = []LineBoundingBox{component}
		}
	}

	// Don't forget the last line
	if len(currentLineComponents) > 0 {
		lineBox := s.mergeLineComponents(currentLineComponents)
		lineBoxes = append(lineBoxes, lineBox)
	}

	return lineBoxes
}

func (s *WordDetectionService) mergeLineComponents(components []LineBoundingBox) LineBoundingBox {
	if len(components) == 0 {
		return LineBoundingBox{}
	}

	minX := components[0].X
	minY := components[0].Y
	maxX := components[0].X + components[0].Width
	maxY := components[0].Y + components[0].Height

	for _, comp := range components[1:] {
		if comp.X < minX {
			minX = comp.X
		}
		if comp.Y < minY {
			minY = comp.Y
		}
		if comp.X+comp.Width > maxX {
			maxX = comp.X + comp.Width
		}
		if comp.Y+comp.Height > maxY {
			maxY = comp.Y + comp.Height
		}
	}

	return LineBoundingBox{
		X:      minX,
		Y:      minY,
		Width:  maxX - minX,
		Height: maxY - minY,
	}
}

func (s *WordDetectionService) convertLinesToGCVResponse(lineBoxes []LineBoundingBox, width, height int) models.GCVResponse {
	var paragraphs []models.Paragraph

	// Convert each line to a paragraph containing a single word
	for i, lineBox := range lineBoxes {
		// Create a single word that represents the entire line
		word := models.Word{
			BoundingBox: models.BoundingPoly{
				Vertices: []models.Vertex{
					{X: lineBox.X, Y: lineBox.Y},
					{X: lineBox.X + lineBox.Width, Y: lineBox.Y},
					{X: lineBox.X + lineBox.Width, Y: lineBox.Y + lineBox.Height},
					{X: lineBox.X, Y: lineBox.Y + lineBox.Height},
				},
			},
			Symbols: []models.Symbol{
				{
					BoundingBox: models.BoundingPoly{
						Vertices: []models.Vertex{
							{X: lineBox.X, Y: lineBox.Y},
							{X: lineBox.X + lineBox.Width, Y: lineBox.Y},
							{X: lineBox.X + lineBox.Width, Y: lineBox.Y + lineBox.Height},
							{X: lineBox.X, Y: lineBox.Y + lineBox.Height},
						},
					},
					Text: fmt.Sprintf("line_%d", i+1), // Placeholder text for the line
					Property: &models.Property{
						DetectedBreak: &models.DetectedBreak{
							Type: "LINE_BREAK",
						},
					},
				},
			},
		}

		// Create a paragraph for this line
		paragraph := models.Paragraph{
			BoundingBox: models.BoundingPoly{
				Vertices: []models.Vertex{
					{X: lineBox.X, Y: lineBox.Y},
					{X: lineBox.X + lineBox.Width, Y: lineBox.Y},
					{X: lineBox.X + lineBox.Width, Y: lineBox.Y + lineBox.Height},
					{X: lineBox.X, Y: lineBox.Y + lineBox.Height},
				},
			},
			Words: []models.Word{word},
		}
		paragraphs = append(paragraphs, paragraph)
	}

	// Create a single block containing all paragraphs (lines)
	block := models.Block{
		BoundingBox: models.BoundingPoly{
			Vertices: []models.Vertex{
				{X: 0, Y: 0},
				{X: width, Y: 0},
				{X: width, Y: height},
				{X: 0, Y: height},
			},
		},
		BlockType:  "TEXT",
		Paragraphs: paragraphs,
	}

	// Create page
	page := models.Page{
		Width:  width,
		Height: height,
		Blocks: []models.Block{block},
	}

	return models.GCVResponse{
		Responses: []models.Response{
			{
				FullTextAnnotation: &models.FullTextAnnotation{
					Pages: []models.Page{page},
					Text:  "Custom line detection - text lines identified",
				},
			},
		},
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}