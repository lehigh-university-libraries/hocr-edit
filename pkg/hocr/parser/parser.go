package parser

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/lehigh-university-libraries/hocr-edit/internal/models"
)

type XMLElement struct {
	XMLName  xml.Name
	Attrs    []xml.Attr   `xml:",any,attr"`
	Content  string       `xml:",chardata"`
	Children []XMLElement `xml:",any"`
}

func ParseHOCRLines(hocrXML string) ([]models.HOCRLine, error) {
	var doc XMLElement

	decoder := xml.NewDecoder(strings.NewReader(hocrXML))
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("failed to parse XML: %w", err)
	}

	var lines []models.HOCRLine

	traverseLinesElements(doc, &lines)

	return lines, nil
}

func ParseHOCRWords(hocrXML string) ([]models.HOCRWord, error) {
	var doc XMLElement

	decoder := xml.NewDecoder(strings.NewReader(hocrXML))
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("failed to parse XML: %w", err)
	}

	var words []models.HOCRWord

	traverseElementsWithLineContext(doc, &words, "")

	return words, nil
}

func traverseLinesElements(element XMLElement, lines *[]models.HOCRLine) {
	if isLineElement(element) {
		line, err := parseLineElement(element)
		if err == nil && line.ID != "" {
			*lines = append(*lines, line)
		}
	}

	for _, child := range element.Children {
		traverseLinesElements(child, lines)
	}
}

func traverseElementsWithLineContext(element XMLElement, words *[]models.HOCRWord, currentLineID string) {
	// Update line ID if this element is a line element
	if isLineElement(element) {
		for _, attr := range element.Attrs {
			if attr.Name.Local == "id" {
				currentLineID = attr.Value
				break
			}
		}
	}

	// Parse word elements with line context
	if isWordElement(element) {
		word, err := parseWordElement(element)
		if err == nil && word.ID != "" {
			word.LineID = currentLineID
			*words = append(*words, word)
		}
	}

	// Recursively traverse children with current line context
	for _, child := range element.Children {
		traverseElementsWithLineContext(child, words, currentLineID)
	}
}

func isLineElement(element XMLElement) bool {
	for _, attr := range element.Attrs {
		if attr.Name.Local == "class" && strings.Contains(attr.Value, "ocr_line") {
			return true
		}
	}
	return false
}

func isWordElement(element XMLElement) bool {
	for _, attr := range element.Attrs {
		if attr.Name.Local == "class" && strings.Contains(attr.Value, "ocrx_word") {
			return true
		}
	}
	return false
}

func parseLineElement(element XMLElement) (models.HOCRLine, error) {
	line := models.HOCRLine{}

	for _, attr := range element.Attrs {
		switch attr.Name.Local {
		case "id":
			line.ID = attr.Value
		case "title":
			if err := parseLineTitleAttribute(attr.Value, &line); err != nil {
				return line, fmt.Errorf("failed to parse title attribute: %w", err)
			}
		}
	}

	var words []models.HOCRWord
	traverseWordsInLine(element, &words, line.ID)
	line.Words = words

	return line, nil
}

func traverseWordsInLine(element XMLElement, words *[]models.HOCRWord, lineID string) {
	if isWordElement(element) {
		word, err := parseWordElement(element)
		if err == nil && word.ID != "" {
			word.LineID = lineID
			*words = append(*words, word)
		}
	}

	for _, child := range element.Children {
		traverseWordsInLine(child, words, lineID)
	}
}

func parseLineTitleAttribute(title string, line *models.HOCRLine) error {
	bboxRegex := regexp.MustCompile(`bbox\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)`)
	if matches := bboxRegex.FindStringSubmatch(title); len(matches) == 5 {
		var err error
		if line.BBox.X1, err = strconv.Atoi(matches[1]); err != nil {
			return fmt.Errorf("invalid bbox x1: %w", err)
		}
		if line.BBox.Y1, err = strconv.Atoi(matches[2]); err != nil {
			return fmt.Errorf("invalid bbox y1: %w", err)
		}
		if line.BBox.X2, err = strconv.Atoi(matches[3]); err != nil {
			return fmt.Errorf("invalid bbox x2: %w", err)
		}
		if line.BBox.Y2, err = strconv.Atoi(matches[4]); err != nil {
			return fmt.Errorf("invalid bbox y2: %w", err)
		}
	}

	return nil
}

func parseWordElement(element XMLElement) (models.HOCRWord, error) {
	word := models.HOCRWord{}

	for _, attr := range element.Attrs {
		switch attr.Name.Local {
		case "id":
			word.ID = attr.Value
		case "title":
			if err := parseTitleAttribute(attr.Value, &word); err != nil {
				return word, fmt.Errorf("failed to parse title attribute: %w", err)
			}
		}
	}

	word.Text = strings.TrimSpace(element.Content)

	return word, nil
}

func parseTitleAttribute(title string, word *models.HOCRWord) error {
	bboxRegex := regexp.MustCompile(`bbox\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)`)
	if matches := bboxRegex.FindStringSubmatch(title); len(matches) == 5 {
		var err error
		if word.BBox.X1, err = strconv.Atoi(matches[1]); err != nil {
			return fmt.Errorf("invalid bbox x1: %w", err)
		}
		if word.BBox.Y1, err = strconv.Atoi(matches[2]); err != nil {
			return fmt.Errorf("invalid bbox y1: %w", err)
		}
		if word.BBox.X2, err = strconv.Atoi(matches[3]); err != nil {
			return fmt.Errorf("invalid bbox x2: %w", err)
		}
		if word.BBox.Y2, err = strconv.Atoi(matches[4]); err != nil {
			return fmt.Errorf("invalid bbox y2: %w", err)
		}
	}

	confRegex := regexp.MustCompile(`x_wconf\s+(\d+(?:\.\d+)?)`)
	if matches := confRegex.FindStringSubmatch(title); len(matches) == 2 {
		var err error
		if word.Confidence, err = strconv.ParseFloat(matches[1], 64); err != nil {
			return fmt.Errorf("invalid confidence: %w", err)
		}
	}

	return nil
}
