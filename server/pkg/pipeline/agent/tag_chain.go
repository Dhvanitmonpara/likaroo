package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// TagGenerationChain executes Step 3 of the agentic workflow: generating tags from content.
type TagGenerationChain struct {
	llm LLMClient
}

func NewTagGenerationChain(llm LLMClient) *TagGenerationChain {
	return &TagGenerationChain{llm: llm}
}

// GenerateTags generates 3-5 concise tags based on title, notes, and extracted content.
func (c *TagGenerationChain) GenerateTags(ctx context.Context, title, notes, content string) ([]string, error) {
	textSource := strings.TrimSpace(title + " " + notes + " " + content)
	if textSource == "" {
		return []string{"uncategorized"}, nil
	}

	prompt := fmt.Sprintf(
		"System: You are an AI categorization agent.\n"+
			"Task: Generate 3 to 5 relevant, lowercase tag keywords for indexing this content.\n\n"+
			"Title: %s\n"+
			"Notes: %s\n"+
			"Content Excerpt: %s\n\n"+
			"Instructions: Output ONLY comma-separated lowercase tags without spaces or numbers. Example: golang,web-dev,tutorial,api",
		title, notes, truncateString(content, 1000),
	)

	resp, err := c.llm.GenerateText(ctx, prompt)
	var tags []string

	if err == nil && strings.TrimSpace(resp) != "" {
		tags = c.parseTagResponse(resp)
	}

	if len(tags) == 0 {
		tags = c.synthesizeFallbackTags(title, notes, content)
	}

	return tags, nil
}

func (c *TagGenerationChain) parseTagResponse(resp string) []string {
	rawTags := strings.Split(resp, ",")
	seen := make(map[string]bool)
	var result []string

	reClean := regexp.MustCompile(`[^a-z0-9-.]`)

	for _, tag := range rawTags {
		clean := strings.ToLower(strings.TrimSpace(tag))
		clean = reClean.ReplaceAllString(clean, "")
		clean = strings.Trim(clean, "-.")

		if len(clean) >= 2 && len(clean) <= 25 && !seen[clean] {
			seen[clean] = true
			result = append(result, clean)
		}
	}

	return result
}

func (c *TagGenerationChain) synthesizeFallbackTags(title, notes, content string) []string {
	combined := strings.ToLower(title + " " + notes + " " + content)

	candidateKeywords := []struct {
		keyword string
		tag     string
	}{
		{"youtube", "video"},
		{"video", "video"},
		{"tutorial", "tutorial"},
		{"guide", "guide"},
		{"golang", "go"},
		{"go ", "go"},
		{"python", "python"},
		{"javascript", "javascript"},
		{"react", "react"},
		{"book", "reading"},
		{"isbn", "book"},
		{"author", "literature"},
		{"movie", "film"},
		{"imdb", "entertainment"},
		{"amazon", "shopping"},
		{"product", "product"},
		{"pdf", "document"},
		{"paper", "research"},
		{"article", "article"},
		{"blog", "article"},
		{"news", "news"},
		{"api", "api"},
		{"code", "programming"},
		{"software", "software"},
	}

	seen := make(map[string]bool)
	var result []string

	for _, item := range candidateKeywords {
		if strings.Contains(combined, item.keyword) && !seen[item.tag] {
			seen[item.tag] = true
			result = append(result, item.tag)
			if len(result) >= 4 {
				break
			}
		}
	}

	if len(result) == 0 {
		result = append(result, "general")
	}

	return result
}
