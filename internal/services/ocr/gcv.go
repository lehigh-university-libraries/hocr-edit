package ocr

import (
	"context"
	"os"

	vision "cloud.google.com/go/vision/apiv1"
	"cloud.google.com/go/vision/v2/apiv1/visionpb"
	"github.com/lehigh-university-libraries/hocr-edit/internal/models"
)

type Service struct{}

func New() *Service {
	return &Service{}
}

func (s *Service) ProcessImage(imagePath string) (models.GCVResponse, error) {
	ctx := context.Background()

	client, err := vision.NewImageAnnotatorClient(ctx)
	if err != nil {
		return models.GCVResponse{}, err
	}
	defer client.Close()

	f, err := os.Open(imagePath)
	if err != nil {
		return models.GCVResponse{}, err
	}
	defer f.Close()

	image, err := vision.NewImageFromReader(f)
	if err != nil {
		return models.GCVResponse{}, err
	}

	annotation, err := client.DetectDocumentText(ctx, image, nil)
	if err != nil {
		return models.GCVResponse{}, err
	}

	return convertVisionResponseToGCV(annotation), nil
}

func convertVisionResponseToGCV(annotation *visionpb.TextAnnotation) models.GCVResponse {
	if annotation == nil {
		return models.GCVResponse{}
	}

	var pages []models.Page
	for _, page := range annotation.Pages {
		convertedPage := models.Page{
			Width:  int(page.Width),
			Height: int(page.Height),
		}

		for _, block := range page.Blocks {
			convertedBlock := models.Block{
				BoundingBox: convertBoundingPoly(block.BoundingBox),
				BlockType:   "TEXT",
			}

			for _, paragraph := range block.Paragraphs {
				convertedParagraph := models.Paragraph{
					BoundingBox: convertBoundingPoly(paragraph.BoundingBox),
				}

				for _, word := range paragraph.Words {
					convertedWord := models.Word{
						BoundingBox: convertBoundingPoly(word.BoundingBox),
					}

					for _, symbol := range word.Symbols {
						convertedSymbol := models.Symbol{
							BoundingBox: convertBoundingPoly(symbol.BoundingBox),
							Text:        symbol.Text,
						}

						if symbol.Property != nil && symbol.Property.DetectedBreak != nil {
							convertedSymbol.Property = &models.Property{
								DetectedBreak: &models.DetectedBreak{
									Type: symbol.Property.DetectedBreak.Type.String(),
								},
							}
						}

						convertedWord.Symbols = append(convertedWord.Symbols, convertedSymbol)
					}

					convertedParagraph.Words = append(convertedParagraph.Words, convertedWord)
				}

				convertedBlock.Paragraphs = append(convertedBlock.Paragraphs, convertedParagraph)
			}

			convertedPage.Blocks = append(convertedPage.Blocks, convertedBlock)
		}

		pages = append(pages, convertedPage)
	}

	return models.GCVResponse{
		Responses: []models.Response{
			{
				FullTextAnnotation: &models.FullTextAnnotation{
					Pages: pages,
					Text:  annotation.Text,
				},
			},
		},
	}
}

func convertBoundingPoly(poly *visionpb.BoundingPoly) models.BoundingPoly {
	if poly == nil {
		return models.BoundingPoly{}
	}

	var vertices []models.Vertex
	for _, vertex := range poly.Vertices {
		vertices = append(vertices, models.Vertex{
			X: int(vertex.X),
			Y: int(vertex.Y),
		})
	}

	return models.BoundingPoly{Vertices: vertices}
}
