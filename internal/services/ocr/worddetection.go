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

type BoundingBox struct {
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

	// Convert to grayscale and process
	wordBoxes, err := s.detectWordBoxes(imagePath, width, height)
	if err != nil {
		return models.GCVResponse{}, fmt.Errorf("failed to detect word boxes: %w", err)
	}

	// Convert to GCV-compatible response format
	return s.convertToGCVResponse(wordBoxes, width, height), nil
}

func (s *WordDetectionService) detectWordBoxes(imagePath string, width, height int) ([]BoundingBox, error) {
	// Use ImageMagick to preprocess the image for text detection
	tempDir := "/tmp"
	baseName := strings.TrimSuffix(filepath.Base(imagePath), filepath.Ext(imagePath))
	processedPath := fmt.Sprintf("%s/processed_%s.jpg", tempDir, strings.ReplaceAll(baseName, "/", "_"))
	defer os.Remove(processedPath)

	// Convert to grayscale, enhance contrast, and apply morphological operations
	cmd := exec.Command("magick", imagePath,
		"-colorspace", "Gray",
		"-normalize",
		"-threshold", "50%",
		"-morphology", "Close", "Rectangle:5x1",
		"-morphology", "Open", "Rectangle:1x2",
		"-morphology", "Close", "Rectangle:2x1",
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

	// Group components into words based on proximity
	wordBoxes := s.groupComponentsIntoWords(components, width, height)

	return wordBoxes, nil
}

func (s *WordDetectionService) findConnectedComponents(img image.Image) []BoundingBox {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Create a visited map
	visited := make([][]bool, height)
	for i := range visited {
		visited[i] = make([]bool, width)
	}

	var components []BoundingBox

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
					components = append(components, BoundingBox{
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

func (s *WordDetectionService) groupComponentsIntoWords(components []BoundingBox, imgWidth, imgHeight int) []BoundingBox {
	if len(components) == 0 {
		return components
	}

	// Sort components by Y coordinate, then by X
	sort.Slice(components, func(i, j int) bool {
		if components[i].Y == components[j].Y {
			return components[i].X < components[j].X
		}
		return components[i].Y < components[j].Y
	})

	var wordBoxes []BoundingBox
	var currentWordComponents []BoundingBox

	for _, component := range components {
		if len(currentWordComponents) == 0 {
			currentWordComponents = append(currentWordComponents, component)
			continue
		}

		// Calculate average height of current components
		avgHeight := 0
		for _, comp := range currentWordComponents {
			avgHeight += comp.Height
		}
		avgHeight /= len(currentWordComponents)

		lastComponent := currentWordComponents[len(currentWordComponents)-1]

		// Check if this component belongs to the same word
		// Criteria: similar Y position, reasonable X gap, similar height
		yDiff := abs(component.Y - lastComponent.Y)
		xGap := component.X - (lastComponent.X + lastComponent.Width)
		heightDiff := abs(component.Height - avgHeight)

		// Group if: on same line (small Y diff), close together (reasonable X gap), similar size
		if yDiff <= avgHeight/2 && xGap <= avgHeight*4 && heightDiff <= avgHeight {
			currentWordComponents = append(currentWordComponents, component)
		} else {
			// Finish current word and start new one
			if len(currentWordComponents) > 0 {
				wordBox := s.mergeComponents(currentWordComponents)
				wordBoxes = append(wordBoxes, wordBox)
			}
			currentWordComponents = []BoundingBox{component}
		}
	}

	// Don't forget the last word
	if len(currentWordComponents) > 0 {
		wordBox := s.mergeComponents(currentWordComponents)
		wordBoxes = append(wordBoxes, wordBox)
	}

	return wordBoxes
}

func (s *WordDetectionService) mergeComponents(components []BoundingBox) BoundingBox {
	if len(components) == 0 {
		return BoundingBox{}
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

	return BoundingBox{
		X:      minX,
		Y:      minY,
		Width:  maxX - minX,
		Height: maxY - minY,
	}
}

func (s *WordDetectionService) convertToGCVResponse(wordBoxes []BoundingBox, width, height int) models.GCVResponse {
	var words []models.Word

	// Convert each word box to GCV format
	for i, box := range wordBoxes {
		word := models.Word{
			BoundingBox: models.BoundingPoly{
				Vertices: []models.Vertex{
					{X: box.X, Y: box.Y},
					{X: box.X + box.Width, Y: box.Y},
					{X: box.X + box.Width, Y: box.Y + box.Height},
					{X: box.X, Y: box.Y + box.Height},
				},
			},
			Symbols: []models.Symbol{
				{
					BoundingBox: models.BoundingPoly{
						Vertices: []models.Vertex{
							{X: box.X, Y: box.Y},
							{X: box.X + box.Width, Y: box.Y},
							{X: box.X + box.Width, Y: box.Y + box.Height},
							{X: box.X, Y: box.Y + box.Height},
						},
					},
					Text: fmt.Sprintf("word_%d", i+1), // Placeholder text
					Property: &models.Property{
						DetectedBreak: &models.DetectedBreak{
							Type: "SPACE",
						},
					},
				},
			},
		}
		words = append(words, word)
	}

	// Group words into paragraphs (simple line-based grouping)
	paragraphs := s.groupWordsIntoParagraphs(words)

	// Create a single block containing all paragraphs
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
					Text:  "Custom word detection - text regions identified",
				},
			},
		},
	}
}

func (s *WordDetectionService) groupWordsIntoParagraphs(words []models.Word) []models.Paragraph {
	if len(words) == 0 {
		return []models.Paragraph{}
	}

	// First, group words into lines based on Y-coordinate overlap
	lines := s.groupWordsIntoLines(words)

	// Then group lines into paragraphs based on spacing
	return s.groupLinesIntoParagraphs(lines)
}

func (s *WordDetectionService) groupWordsIntoLines(words []models.Word) [][]models.Word {
	if len(words) == 0 {
		return [][]models.Word{}
	}

	// Sort words by Y coordinate first, then by X coordinate
	sortedWords := make([]models.Word, len(words))
	copy(sortedWords, words)
	sort.Slice(sortedWords, func(i, j int) bool {
		yI := sortedWords[i].BoundingBox.Vertices[0].Y
		yJ := sortedWords[j].BoundingBox.Vertices[0].Y
		if yI == yJ {
			return sortedWords[i].BoundingBox.Vertices[0].X < sortedWords[j].BoundingBox.Vertices[0].X
		}
		return yI < yJ
	})

	var lines [][]models.Word

	for _, word := range sortedWords {
		wordTop := word.BoundingBox.Vertices[0].Y
		wordBottom := word.BoundingBox.Vertices[2].Y
		wordHeight := wordBottom - wordTop

		// Find if this word belongs to an existing line
		foundLine := false
		for i, line := range lines {
			if len(line) == 0 {
				continue
			}

			// Calculate the Y-range of the current line
			lineTop := line[0].BoundingBox.Vertices[0].Y
			lineBottom := line[0].BoundingBox.Vertices[2].Y

			for _, lineWord := range line {
				wordLineTop := lineWord.BoundingBox.Vertices[0].Y
				wordLineBottom := lineWord.BoundingBox.Vertices[2].Y
				if wordLineTop < lineTop {
					lineTop = wordLineTop
				}
				if wordLineBottom > lineBottom {
					lineBottom = wordLineBottom
				}
			}

			// Check if the word's Y-range overlaps with the line's Y-range
			// Allow some tolerance (half the word height) to account for slight misalignment
			tolerance := wordHeight / 2
			wordOverlapsLine := wordBottom >= lineTop-tolerance && wordTop <= lineBottom+tolerance

			if wordOverlapsLine {
				lines[i] = append(lines[i], word)
				foundLine = true
				break
			}
		}

		if !foundLine {
			// Create a new line for this word
			lines = append(lines, []models.Word{word})
		}
	}

	// Sort words within each line by X coordinate
	for i := range lines {
		sort.Slice(lines[i], func(j, k int) bool {
			return lines[i][j].BoundingBox.Vertices[0].X < lines[i][k].BoundingBox.Vertices[0].X
		})
	}

	return lines
}

func (s *WordDetectionService) groupLinesIntoParagraphs(lines [][]models.Word) []models.Paragraph {
	if len(lines) == 0 {
		return []models.Paragraph{}
	}

	var paragraphs []models.Paragraph
	var currentParagraphLines [][]models.Word

	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		if len(currentParagraphLines) == 0 {
			currentParagraphLines = append(currentParagraphLines, line)
			continue
		}

		// Calculate the vertical gap between the last line and current line
		lastLine := currentParagraphLines[len(currentParagraphLines)-1]
		if len(lastLine) == 0 {
			currentParagraphLines = append(currentParagraphLines, line)
			continue
		}

		// Get the bottom Y of the last line
		lastLineBottom := 0
		for _, word := range lastLine {
			wordBottom := word.BoundingBox.Vertices[2].Y
			if wordBottom > lastLineBottom {
				lastLineBottom = wordBottom
			}
		}

		// Get the top Y of the current line
		currentLineTop := line[0].BoundingBox.Vertices[0].Y
		for _, word := range line {
			wordTop := word.BoundingBox.Vertices[0].Y
			if wordTop < currentLineTop {
				currentLineTop = wordTop
			}
		}

		// Estimate line height from the current line
		lineHeight := 0
		for _, word := range line {
			wordHeight := word.BoundingBox.Vertices[2].Y - word.BoundingBox.Vertices[0].Y
			if wordHeight > lineHeight {
				lineHeight = wordHeight
			}
		}

		verticalGap := currentLineTop - lastLineBottom

		// Start new paragraph if gap is more than 2x line height
		if verticalGap > lineHeight*2 {
			// Finish current paragraph
			paragraphWords := s.flattenLines(currentParagraphLines)
			if len(paragraphWords) > 0 {
				paragraph := s.createParagraphFromWords(paragraphWords)
				paragraphs = append(paragraphs, paragraph)
			}
			currentParagraphLines = [][]models.Word{line}
		} else {
			currentParagraphLines = append(currentParagraphLines, line)
		}
	}

	// Don't forget the last paragraph
	if len(currentParagraphLines) > 0 {
		paragraphWords := s.flattenLines(currentParagraphLines)
		if len(paragraphWords) > 0 {
			paragraph := s.createParagraphFromWords(paragraphWords)
			paragraphs = append(paragraphs, paragraph)
		}
	}

	return paragraphs
}

func (s *WordDetectionService) flattenLines(lines [][]models.Word) []models.Word {
	var words []models.Word
	for _, line := range lines {
		words = append(words, line...)
	}
	return words
}

func (s *WordDetectionService) createParagraphFromWords(words []models.Word) models.Paragraph {
	if len(words) == 0 {
		return models.Paragraph{}
	}

	// Calculate bounding box for the entire paragraph
	minX := words[0].BoundingBox.Vertices[0].X
	minY := words[0].BoundingBox.Vertices[0].Y
	maxX := words[0].BoundingBox.Vertices[2].X
	maxY := words[0].BoundingBox.Vertices[2].Y

	for _, word := range words[1:] {
		if word.BoundingBox.Vertices[0].X < minX {
			minX = word.BoundingBox.Vertices[0].X
		}
		if word.BoundingBox.Vertices[0].Y < minY {
			minY = word.BoundingBox.Vertices[0].Y
		}
		if word.BoundingBox.Vertices[2].X > maxX {
			maxX = word.BoundingBox.Vertices[2].X
		}
		if word.BoundingBox.Vertices[2].Y > maxY {
			maxY = word.BoundingBox.Vertices[2].Y
		}
	}

	return models.Paragraph{
		BoundingBox: models.BoundingPoly{
			Vertices: []models.Vertex{
				{X: minX, Y: minY},
				{X: maxX, Y: minY},
				{X: maxX, Y: maxY},
				{X: minX, Y: maxY},
			},
		},
		Words: words,
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
