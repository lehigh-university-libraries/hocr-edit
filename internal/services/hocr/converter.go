package hocr

import (
	"fmt"
	"html"
	"strings"

	"github.com/lehigh-university-libraries/hocr-edit/internal/models"
)

type Converter struct {
	lineCounter int
	wordCounter int
}

func NewConverter() *Converter {
	return &Converter{
		lineCounter: 1,
		wordCounter: 1,
	}
}

func (h *Converter) ConvertToHOCRLines(gcvResponse models.GCVResponse) ([]models.HOCRLine, error) {
	if len(gcvResponse.Responses) == 0 {
		return nil, fmt.Errorf("no responses found in GCV data")
	}

	response := gcvResponse.Responses[0]
	if response.FullTextAnnotation == nil {
		return nil, fmt.Errorf("no full text annotation found")
	}

	var allLines []models.HOCRLine

	for _, page := range response.FullTextAnnotation.Pages {
		pageLines := h.convertPageToLines(page)
		allLines = append(allLines, pageLines...)
	}

	return allLines, nil
}

func (h *Converter) ConvertHOCRLinesToXML(lines []models.HOCRLine, pageWidth, pageHeight int) string {
	var hocr strings.Builder

	hocr.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	hocr.WriteString("<!DOCTYPE html PUBLIC \"-//W3C//DTD XHTML 1.0 Transitional//EN\"\n")
	hocr.WriteString("    \"http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd\">\n")
	hocr.WriteString("<html xmlns=\"http://www.w3.org/1999/xhtml\" xml:lang=\"en\" lang=\"en\">\n")
	hocr.WriteString("<head>\n")
	hocr.WriteString("<title></title>\n")
	hocr.WriteString("<meta http-equiv=\"Content-Type\" content=\"text/html; charset=utf-8\" />\n")
	hocr.WriteString("<meta name='ocr-system' content='google-cloud-vision' />\n")
	hocr.WriteString("<meta name='ocr-capabilities' content='ocr_page ocr_carea ocr_par ocr_line ocrx_word' />\n")
	hocr.WriteString("</head>\n")
	hocr.WriteString("<body>\n")

	bbox := fmt.Sprintf("bbox 0 0 %d %d", pageWidth, pageHeight)
	hocr.WriteString(fmt.Sprintf("<div class='ocr_page' id='page_1' title='%s'>\n", bbox))

	for _, line := range lines {
		hocr.WriteString(h.convertHOCRLineToXML(line))
	}

	hocr.WriteString("</div>\n")
	hocr.WriteString("</body>\n")
	hocr.WriteString("</html>\n")

	return hocr.String()
}

func (h *Converter) convertHOCRLineToXML(line models.HOCRLine) string {
	bbox := fmt.Sprintf("bbox %d %d %d %d", line.BBox.X1, line.BBox.Y1, line.BBox.X2, line.BBox.Y2)

	var lineBuilder strings.Builder
	lineBuilder.WriteString(fmt.Sprintf("<span class='ocr_line' id='%s' title='%s'>", line.ID, bbox))

	for _, word := range line.Words {
		wordXML := h.convertHOCRWordToXML(word)
		lineBuilder.WriteString(wordXML)
	}

	lineBuilder.WriteString("</span>\n")
	return lineBuilder.String()
}

func (h *Converter) convertHOCRWordToXML(word models.HOCRWord) string {
	bbox := fmt.Sprintf("bbox %d %d %d %d", word.BBox.X1, word.BBox.Y1, word.BBox.X2, word.BBox.Y2)
	confidence := fmt.Sprintf("; x_wconf %.0f", word.Confidence)
	title := bbox + confidence

	return fmt.Sprintf("<span class='ocrx_word' id='%s' title='%s'>%s</span> ",
		word.ID, title, html.EscapeString(word.Text))
}

func (h *Converter) ConvertToHOCR(gcvResponse models.GCVResponse) (string, error) {
	lines, err := h.ConvertToHOCRLines(gcvResponse)
	if err != nil {
		return "", err
	}

	if len(gcvResponse.Responses) == 0 || gcvResponse.Responses[0].FullTextAnnotation == nil || len(gcvResponse.Responses[0].FullTextAnnotation.Pages) == 0 {
		return "", fmt.Errorf("no page data found")
	}

	page := gcvResponse.Responses[0].FullTextAnnotation.Pages[0]
	return h.ConvertHOCRLinesToXML(lines, page.Width, page.Height), nil
}

func (h *Converter) convertPageToLines(page models.Page) []models.HOCRLine {
	var allLines []models.HOCRLine

	for _, block := range page.Blocks {
		if block.BlockType == "TEXT" {
			blockLines := h.convertBlockToLines(block)
			allLines = append(allLines, blockLines...)
		}
	}

	return allLines
}

func (h *Converter) convertBlockToLines(block models.Block) []models.HOCRLine {
	var allLines []models.HOCRLine

	for _, paragraph := range block.Paragraphs {
		paragraphLines := h.convertParagraphToLines(paragraph)
		allLines = append(allLines, paragraphLines...)
	}

	return allLines
}

func (h *Converter) convertParagraphToLines(paragraph models.Paragraph) []models.HOCRLine {
	wordsGroups := h.groupWordsIntoLines(paragraph.Words)
	var lines []models.HOCRLine

	for _, wordsGroup := range wordsGroups {
		if len(wordsGroup) == 0 {
			continue
		}

		lineID := fmt.Sprintf("line_%d", h.lineCounter)
		lineBBox := h.calculateLineBBoxStruct(wordsGroup)

		var hocrWords []models.HOCRWord
		for _, gcvWord := range wordsGroup {
			hocrWord := h.convertGCVWordToHOCRWord(gcvWord, lineID)
			hocrWords = append(hocrWords, hocrWord)
		}

		line := models.HOCRLine{
			ID:    lineID,
			BBox:  lineBBox,
			Words: hocrWords,
		}

		lines = append(lines, line)
		h.lineCounter++
	}

	return lines
}

func (h *Converter) groupWordsIntoLines(words []models.Word) [][]models.Word {
	if len(words) == 0 {
		return nil
	}

	var lines [][]models.Word
	var currentLine []models.Word

	for i, word := range words {
		currentLine = append(currentLine, word)

		shouldEndLine := false

		if len(word.Symbols) > 0 {
			lastSymbol := word.Symbols[len(word.Symbols)-1]
			if lastSymbol.Property != nil && lastSymbol.Property.DetectedBreak != nil {
				breakType := lastSymbol.Property.DetectedBreak.Type
				if breakType == "LINE_BREAK" || breakType == "EOL_SURE_SPACE" {
					shouldEndLine = true
				}
			}
		}

		if i == len(words)-1 {
			shouldEndLine = true
		}

		if shouldEndLine {
			lines = append(lines, currentLine)
			currentLine = nil
		}
	}

	return lines
}

func (h *Converter) calculateLineBBoxStruct(words []models.Word) models.BBox {
	if len(words) == 0 {
		return models.BBox{X1: 0, Y1: 0, X2: 0, Y2: 0}
	}

	minX, minY := int(^uint(0)>>1), int(^uint(0)>>1)
	maxX, maxY := 0, 0

	for _, word := range words {
		for _, vertex := range word.BoundingBox.Vertices {
			if vertex.X < minX {
				minX = vertex.X
			}
			if vertex.X > maxX {
				maxX = vertex.X
			}
			if vertex.Y < minY {
				minY = vertex.Y
			}
			if vertex.Y > maxY {
				maxY = vertex.Y
			}
		}
	}

	return models.BBox{X1: minX, Y1: minY, X2: maxX, Y2: maxY}
}

func (h *Converter) convertGCVWordToHOCRWord(gcvWord models.Word, lineID string) models.HOCRWord {
	var text strings.Builder
	for _, symbol := range gcvWord.Symbols {
		text.WriteString(symbol.Text)
	}

	bbox := h.boundingPolyToBBoxStruct(gcvWord.BoundingBox)

	confidence := 95.0
	if gcvWord.Property != nil && len(gcvWord.Property.DetectedLanguages) > 0 {
		confidence = gcvWord.Property.DetectedLanguages[0].Confidence * 100
	}

	wordID := fmt.Sprintf("word_%d", h.wordCounter)
	h.wordCounter++

	return models.HOCRWord{
		ID:         wordID,
		Text:       text.String(),
		BBox:       bbox,
		Confidence: confidence,
		LineID:     lineID,
	}
}

func (h *Converter) boundingPolyToBBoxStruct(boundingPoly models.BoundingPoly) models.BBox {
	if len(boundingPoly.Vertices) == 0 {
		return models.BBox{X1: 0, Y1: 0, X2: 0, Y2: 0}
	}

	minX, minY := int(^uint(0)>>1), int(^uint(0)>>1)
	maxX, maxY := 0, 0

	for _, vertex := range boundingPoly.Vertices {
		if vertex.X < minX {
			minX = vertex.X
		}
		if vertex.X > maxX {
			maxX = vertex.X
		}
		if vertex.Y < minY {
			minY = vertex.Y
		}
		if vertex.Y > maxY {
			maxY = vertex.Y
		}
	}

	return models.BBox{X1: minX, Y1: minY, X2: maxX, Y2: maxY}
}
