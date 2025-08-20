package models

// Google Cloud Vision structures
type GCVResponse struct {
	Responses []Response `json:"responses"`
}

type Response struct {
	TextAnnotations    []TextAnnotation    `json:"textAnnotations"`
	FullTextAnnotation *FullTextAnnotation `json:"fullTextAnnotation"`
}

type TextAnnotation struct {
	Locale       string       `json:"locale"`
	Description  string       `json:"description"`
	BoundingPoly BoundingPoly `json:"boundingPoly"`
}

type FullTextAnnotation struct {
	Pages []Page `json:"pages"`
	Text  string `json:"text"`
}

type Page struct {
	Property *Property `json:"property"`
	Width    int       `json:"width"`
	Height   int       `json:"height"`
	Blocks   []Block   `json:"blocks"`
}

type Block struct {
	BoundingBox BoundingPoly `json:"boundingBox"`
	Paragraphs  []Paragraph  `json:"paragraphs"`
	BlockType   string       `json:"blockType"`
}

type Paragraph struct {
	BoundingBox BoundingPoly `json:"boundingBox"`
	Words       []Word       `json:"words"`
}

type Word struct {
	Property    *Property    `json:"property"`
	BoundingBox BoundingPoly `json:"boundingBox"`
	Symbols     []Symbol     `json:"symbols"`
}

type Symbol struct {
	Property    *Property    `json:"property"`
	BoundingBox BoundingPoly `json:"boundingBox"`
	Text        string       `json:"text"`
}

type Property struct {
	DetectedLanguages []DetectedLanguage `json:"detectedLanguages"`
	DetectedBreak     *DetectedBreak     `json:"detectedBreak"`
}

type DetectedLanguage struct {
	LanguageCode string  `json:"languageCode"`
	Confidence   float64 `json:"confidence"`
}

type DetectedBreak struct {
	Type string `json:"type"`
}

type BoundingPoly struct {
	Vertices []Vertex `json:"vertices"`
}

type Vertex struct {
	X int `json:"x"`
	Y int `json:"y"`
}