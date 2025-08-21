package models

import "time"

type EvalConfig struct {
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	Temperature float64 `json:"temperature"`
	CSVPath     string  `json:"csv_path"`
	TestRows    []int   `json:"rows"`
	Timestamp   string  `json:"timestamp"`
}

type EvalResult struct {
	Identifier            string  `json:"identifier"`
	ImagePath             string  `json:"image_path"`
	TranscriptPath        string  `json:"transcript_path"`
	Public                bool    `json:"public"`
	OpenAIResponse        string  `json:"openai_response"`
	CharacterSimilarity   float64 `json:"character_similarity"`
	WordSimilarity        float64 `json:"word_similarity"`
	WordAccuracy          float64 `json:"word_accuracy"`
	WordErrorRate         float64 `json:"word_error_rate"`
	TotalWordsOriginal    int     `json:"total_words_original"`
	TotalWordsTranscribed int     `json:"total_words_transcribed"`
	CorrectWords          int     `json:"correct_words"`
	Substitutions         int     `json:"substitutions"`
	Deletions             int     `json:"deletions"`
	Insertions            int     `json:"insertions"`
}

type EvalSummary struct {
	Config  EvalConfig   `json:"config"`
	Results []EvalResult `json:"results"`
}

type TemplateData struct {
	Model       string
	Prompt      string
	Temperature float64
	ImageBase64 string
	MimeType    string
}

type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type CorrectionSession struct {
	ID        string       `json:"id"`
	Images    []ImageItem  `json:"images"`
	Current   int          `json:"current"`
	Results   []EvalResult `json:"results"`
	Config    EvalConfig   `json:"config"`
	CreatedAt time.Time    `json:"created_at"`
}

type ImageItem struct {
	ID            string `json:"id"`
	ImagePath     string `json:"image_path"`
	ImageURL      string `json:"image_url"`
	OriginalHOCR  string `json:"original_hocr"`
	CorrectedHOCR string `json:"corrected_hocr"`
	GroundTruth   string `json:"ground_truth"`
	Completed     bool   `json:"completed"`
	ImageWidth    int    `json:"image_width"`
	ImageHeight   int    `json:"image_height"`
}

type HOCRLine struct {
	ID    string     `json:"id"`
	BBox  BBox       `json:"bbox"`
	Words []HOCRWord `json:"words"`
}

type HOCRWord struct {
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	BBox       BBox    `json:"bbox"`
	Confidence float64 `json:"confidence"`
	LineID     string  `json:"line_id"`
}

type BBox struct {
	X1 int `json:"x1"`
	Y1 int `json:"y1"`
	X2 int `json:"x2"`
	Y2 int `json:"y2"`
}
