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

func ParseHOCRWords(hocrXML string) ([]models.HOCRWord, error) {
	var doc XMLElement

	decoder := xml.NewDecoder(strings.NewReader(hocrXML))
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("failed to parse XML: %w", err)
	}

	var words []models.HOCRWord

	traverseElements(doc, &words)

	return words, nil
}

func traverseElements(element XMLElement, words *[]models.HOCRWord) {
	if isWordElement(element) {
		word, err := parseWordElement(element)
		if err == nil && word.ID != "" {
			*words = append(*words, word)
		}
	}

	for _, child := range element.Children {
		traverseElements(child, words)
	}
}

func isWordElement(element XMLElement) bool {
	for _, attr := range element.Attrs {
		if attr.Name.Local == "class" && strings.Contains(attr.Value, "ocrx_word") {
			return true
		}
	}
	return false
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