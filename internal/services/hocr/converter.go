package hocr

import (
	"fmt"
	"html"
	"strings"

	"github.com/lehigh-university-libraries/hocr-edit/internal/models"
)

type Converter struct {
	pageCounter      int
	blockCounter     int
	paragraphCounter int
	lineCounter      int
	wordCounter      int
}

func NewConverter() *Converter {
	return &Converter{
		pageCounter:      1,
		blockCounter:     1,
		paragraphCounter: 1,
		lineCounter:      1,
		wordCounter:      1,
	}
}

func (h *Converter) ConvertToHOCR(gcvResponse models.GCVResponse) (string, error) {
	if len(gcvResponse.Responses) == 0 {
		return "", fmt.Errorf("no responses found in GCV data")
	}

	response := gcvResponse.Responses[0]
	if response.FullTextAnnotation == nil {
		return "", fmt.Errorf("no full text annotation found")
	}

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

	for _, page := range response.FullTextAnnotation.Pages {
		pageHOCR := h.convertPage(page)
		hocr.WriteString(pageHOCR)
	}

	hocr.WriteString("</body>\n")
	hocr.WriteString("</html>\n")

	return hocr.String(), nil
}

func (h *Converter) convertPage(page models.Page) string {
	bbox := fmt.Sprintf("bbox 0 0 %d %d", page.Width, page.Height)

	var pageBuilder strings.Builder
	pageBuilder.WriteString(fmt.Sprintf("<div class='ocr_page' id='page_%d' title='%s'>\n",
		h.pageCounter, bbox))

	for _, block := range page.Blocks {
		if block.BlockType == "TEXT" {
			blockHOCR := h.convertBlock(block)
			pageBuilder.WriteString(blockHOCR)
		}
	}

	pageBuilder.WriteString("</div>\n")
	h.pageCounter++
	return pageBuilder.String()
}

func (h *Converter) convertBlock(block models.Block) string {
	bbox := h.boundingPolyToBBox(block.BoundingBox)

	var blockBuilder strings.Builder
	blockBuilder.WriteString(fmt.Sprintf("<div class='ocr_carea' id='carea_%d' title='%s'>\n",
		h.blockCounter, bbox))

	for _, paragraph := range block.Paragraphs {
		paragraphHOCR := h.convertParagraph(paragraph)
		blockBuilder.WriteString(paragraphHOCR)
	}

	blockBuilder.WriteString("</div>\n")
	h.blockCounter++
	return blockBuilder.String()
}

func (h *Converter) convertParagraph(paragraph models.Paragraph) string {
	bbox := h.boundingPolyToBBox(paragraph.BoundingBox)

	var paragraphBuilder strings.Builder
	paragraphBuilder.WriteString(fmt.Sprintf("<p class='ocr_par' id='par_%d' title='%s'>\n",
		h.paragraphCounter, bbox))

	lines := h.groupWordsIntoLines(paragraph.Words)

	for _, line := range lines {
		lineHOCR := h.convertLine(line)
		paragraphBuilder.WriteString(lineHOCR)
	}

	paragraphBuilder.WriteString("</p>\n")
	h.paragraphCounter++
	return paragraphBuilder.String()
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

func (h *Converter) convertLine(words []models.Word) string {
	if len(words) == 0 {
		return ""
	}

	lineBBox := h.calculateLineBoundingBox(words)

	var lineBuilder strings.Builder
	lineBuilder.WriteString(fmt.Sprintf("<span class='ocr_line' id='line_%d' title='%s'>",
		h.lineCounter, lineBBox))

	for _, word := range words {
		wordHOCR := h.convertWord(word)
		lineBuilder.WriteString(wordHOCR)
	}

	lineBuilder.WriteString("</span>\n")
	h.lineCounter++
	return lineBuilder.String()
}

func (h *Converter) convertWord(word models.Word) string {
	bbox := h.boundingPolyToBBox(word.BoundingBox)

	var text strings.Builder
	for _, symbol := range word.Symbols {
		text.WriteString(symbol.Text)
	}

	confidence := "; x_wconf 95"

	lang := ""
	if word.Property != nil && len(word.Property.DetectedLanguages) > 0 {
		lang = fmt.Sprintf("; x_lang %s", word.Property.DetectedLanguages[0].LanguageCode)
	}

	title := bbox + confidence + lang

	wordHOCR := fmt.Sprintf("<span class='ocrx_word' id='word_%d' title='%s'>%s</span>",
		h.wordCounter, title, html.EscapeString(text.String()))

	if len(word.Symbols) > 0 {
		lastSymbol := word.Symbols[len(word.Symbols)-1]
		if lastSymbol.Property == nil || lastSymbol.Property.DetectedBreak == nil ||
			(lastSymbol.Property.DetectedBreak.Type != "LINE_BREAK" &&
				lastSymbol.Property.DetectedBreak.Type != "EOL_SURE_SPACE") {
			wordHOCR += " "
		}
	}

	h.wordCounter++
	return wordHOCR
}

func (h *Converter) calculateLineBoundingBox(words []models.Word) string {
	if len(words) == 0 {
		return "bbox 0 0 0 0"
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

	return fmt.Sprintf("bbox %d %d %d %d", minX, minY, maxX, maxY)
}

func (h *Converter) boundingPolyToBBox(boundingPoly models.BoundingPoly) string {
	if len(boundingPoly.Vertices) == 0 {
		return "bbox 0 0 0 0"
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

	return fmt.Sprintf("bbox %d %d %d %d", minX, minY, maxX, maxY)
}