package parser_test

import (
	"testing"

	"github.com/lehigh-university-libraries/hocr-edit/pkg/hocr/parser"
)

func TestParseHOCRWords(t *testing.T) {
	// Test hOCR XML with line structure
	testXML := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en" lang="en">
<head><title></title></head>
<body>
<div class='ocr_page' id='page_1' title='bbox 0 0 1120 1368'>
<span class='ocr_line' id='line_1' title='bbox 161 80 435 129'>
<span class='ocrx_word' id='word_1' title='bbox 161 84 300 129; x_wconf 95'>Dear</span> 
<span class='ocrx_word' id='word_2' title='bbox 324 80 417 123; x_wconf 95'>Sir</span>
</span>
<span class='ocr_line' id='line_2' title='bbox 599 41 674 69'>
<span class='ocrx_word' id='word_3' title='bbox 599 41 674 69; x_wconf 95'>ALS</span>
</span>
</div>
</body>
</html>`

	words, err := parser.ParseHOCRWords(testXML)
	if err != nil {
		t.Fatalf("Error parsing hOCR: %v", err)
	}

	// Test basic parsing
	if len(words) != 3 {
		t.Errorf("Expected 3 words, got %d", len(words))
	}

	// Test first word
	if words[0].Text != "Dear" {
		t.Errorf("Expected first word to be 'Dear', got '%s'", words[0].Text)
	}
	if words[0].ID != "word_1" {
		t.Errorf("Expected first word ID to be 'word_1', got '%s'", words[0].ID)
	}
	if words[0].LineID != "line_1" {
		t.Errorf("Expected first word LineID to be 'line_1', got '%s'", words[0].LineID)
	}
	if words[0].Confidence != 95.0 {
		t.Errorf("Expected first word confidence to be 95.0, got %.1f", words[0].Confidence)
	}

	// Test second word (same line)
	if words[1].Text != "Sir" {
		t.Errorf("Expected second word to be 'Sir', got '%s'", words[1].Text)
	}
	if words[1].LineID != "line_1" {
		t.Errorf("Expected second word LineID to be 'line_1', got '%s'", words[1].LineID)
	}

	// Test third word (different line)
	if words[2].Text != "ALS" {
		t.Errorf("Expected third word to be 'ALS', got '%s'", words[2].Text)
	}
	if words[2].LineID != "line_2" {
		t.Errorf("Expected third word LineID to be 'line_2', got '%s'", words[2].LineID)
	}

	// Test bounding boxes
	if words[0].BBox.X1 != 161 || words[0].BBox.Y1 != 84 || words[0].BBox.X2 != 300 || words[0].BBox.Y2 != 129 {
		t.Errorf("Expected first word bbox to be [161, 84, 300, 129], got [%d, %d, %d, %d]",
			words[0].BBox.X1, words[0].BBox.Y1, words[0].BBox.X2, words[0].BBox.Y2)
	}
}

func TestParseHOCRLines(t *testing.T) {
	// Test hOCR XML with line structure
	testXML := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en" lang="en">
<head><title></title></head>
<body>
<div class='ocr_page' id='page_1' title='bbox 0 0 1120 1368'>
<span class='ocr_line' id='line_1' title='bbox 161 80 435 129'>
<span class='ocrx_word' id='word_1' title='bbox 161 84 300 129; x_wconf 95'>Dear</span> 
<span class='ocrx_word' id='word_2' title='bbox 324 80 417 123; x_wconf 95'>Sir</span>
</span>
<span class='ocr_line' id='line_2' title='bbox 599 41 674 69'>
<span class='ocrx_word' id='word_3' title='bbox 599 41 674 69; x_wconf 95'>ALS</span>
</span>
</div>
</body>
</html>`

	lines, err := parser.ParseHOCRLines(testXML)
	if err != nil {
		t.Fatalf("Error parsing hOCR lines: %v", err)
	}

	// Test basic parsing
	if len(lines) != 2 {
		t.Errorf("Expected 2 lines, got %d", len(lines))
	}

	// Test first line
	if lines[0].ID != "line_1" {
		t.Errorf("Expected first line ID to be 'line_1', got '%s'", lines[0].ID)
	}
	if len(lines[0].Words) != 2 {
		t.Errorf("Expected first line to have 2 words, got %d", len(lines[0].Words))
	}
	if lines[0].BBox.X1 != 161 || lines[0].BBox.Y1 != 80 || lines[0].BBox.X2 != 435 || lines[0].BBox.Y2 != 129 {
		t.Errorf("Expected first line bbox to be [161, 80, 435, 129], got [%d, %d, %d, %d]",
			lines[0].BBox.X1, lines[0].BBox.Y1, lines[0].BBox.X2, lines[0].BBox.Y2)
	}

	// Test second line
	if lines[1].ID != "line_2" {
		t.Errorf("Expected second line ID to be 'line_2', got '%s'", lines[1].ID)
	}
	if len(lines[1].Words) != 1 {
		t.Errorf("Expected second line to have 1 word, got %d", len(lines[1].Words))
	}

	// Test that words in lines have correct LineID
	if lines[0].Words[0].LineID != "line_1" {
		t.Errorf("Expected first word in first line to have LineID 'line_1', got '%s'", lines[0].Words[0].LineID)
	}
}
