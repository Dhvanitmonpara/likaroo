package agent

import (
	"context"
	"regexp"
	"strings"

	"linkaroo-app/server/pkg/pipeline/extractor"
	"linkaroo-app/server/pkg/pipeline/models"
)

// ContentExtractionTool extracts readable textual content from provided media.
type ContentExtractionTool struct {
	fetcher extractor.HTTPFetcher
}

func NewContentExtractionTool(fetcher extractor.HTTPFetcher) *ContentExtractionTool {
	if fetcher == nil {
		fetcher = extractor.NewMemoryHTTPFetcher()
	}
	return &ContentExtractionTool{fetcher: fetcher}
}

// ExtractContent extracts raw text body content based on raw item and scraped metadata result.
func (t *ContentExtractionTool) ExtractContent(ctx context.Context, item models.RawItem, res *models.NormalizedResult) (string, error) {
	// 1. Text item directly provided
	if strings.TrimSpace(item.Text) != "" {
		return strings.TrimSpace(item.Text), nil
	}

	// 2. Local PDF upload or raw byte payload
	if item.HasPayload() {
		payloadText := string(item.Payload)
		// Clean PDF or raw text streams
		cleaned := t.cleanText(payloadText)
		if len(cleaned) > 0 {
			return cleaned, nil
		}
	}

	// 3. Web URL / Article / YouTube / standard web page
	if item.IsURL() {
		html, err := t.fetcher.FetchHTML(ctx, item.URL, item.Headers)
		if err == nil && len(html) > 0 {
			bodyText := t.extractTextFromHTML(html)
			if len(bodyText) > 0 {
				return bodyText, nil
			}
		}
	}

	// Fallback to synthesized summary from available metadata
	var parts []string
	if res.Title != "" {
		parts = append(parts, res.Title)
	}
	if res.Subtitle != "" {
		parts = append(parts, res.Subtitle)
	}
	if res.Description != "" {
		parts = append(parts, res.Description)
	}

	return strings.Join(parts, "\n\n"), nil
}

func (t *ContentExtractionTool) extractTextFromHTML(html string) string {
	// Strip script and style tags
	reScript := regexp.MustCompile(`(?is)<script.*?>.*?</script>`)
	reStyle := regexp.MustCompile(`(?is)<style.*?>.*?</style>`)
	clean := reScript.ReplaceAllString(html, "")
	clean = reStyle.ReplaceAllString(clean, "")

	// Extract paragraph texts or headings
	reP := regexp.MustCompile(`(?is)<(?:p|h1|h2|h3|li|article|section)[^>]*>(.*?)</(?:p|h1|h2|h3|li|article|section)>`)
	matches := reP.FindAllStringSubmatch(clean, -1)

	var paragraphs []string
	for _, m := range matches {
		if len(m) > 1 {
			txt := t.cleanTags(m[1])
			if len(txt) > 20 { // Filter short boilerplate strings
				paragraphs = append(paragraphs, txt)
			}
		}
	}

	if len(paragraphs) > 0 {
		return strings.Join(paragraphs, "\n\n")
	}

	// Fallback: strip all tags
	return t.cleanTags(clean)
}

func (t *ContentExtractionTool) cleanTags(html string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	text := re.ReplaceAllString(html, " ")
	return t.cleanText(text)
}

func (t *ContentExtractionTool) cleanText(text string) string {
	// Remove unprintable control characters
	reNonPrint := regexp.MustCompile(`[\x00-\x1F\x7F]`)
	text = reNonPrint.ReplaceAllString(text, " ")

	// Normalize spaces
	reSpace := regexp.MustCompile(`\s+`)
	text = reSpace.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}
