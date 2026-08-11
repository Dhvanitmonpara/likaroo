package agent

import (
	"context"
	"fmt"
	"strings"
)

// NoteGenerationChain executes Step 2 of the agentic workflow: generating notes if missing.
type NoteGenerationChain struct {
	llm LLMClient
}

func NewNoteGenerationChain(llm LLMClient) *NoteGenerationChain {
	return &NoteGenerationChain{llm: llm}
}

// GenerateNotes generates notes from extracted content if existing notes/description are missing.
func (c *NoteGenerationChain) GenerateNotes(ctx context.Context, title, currentNotes, content string) (string, error) {
	// If notes/description already exist and are sufficiently descriptive, preserve them
	trimmedCurrent := strings.TrimSpace(currentNotes)
	if len(trimmedCurrent) > 30 {
		return trimmedCurrent, nil
	}

	if strings.TrimSpace(content) == "" && trimmedCurrent != "" {
		return trimmedCurrent, nil
	}

	if strings.TrimSpace(content) == "" && title == "" {
		return "No content available to generate notes.", nil
	}

	prompt := fmt.Sprintf(
		"System: You are an intelligent note-taking AI agent.\n"+
			"Task: Generate clear, structured summary notes based on the provided media content.\n\n"+
			"Title: %s\n"+
			"Content:\n%s\n\n"+
			"Instructions: Write a concise summary highlighting the main points, key concepts, and takeaways in 2-4 sentences or bullet points.",
		title, truncateString(content, 2000),
	)

	generatedNotes, err := c.llm.GenerateText(ctx, prompt)
	if err != nil || strings.TrimSpace(generatedNotes) == "" {
		// Fallback deterministic note synthesis
		return synthesizeFallbackNotes(title, content), nil
	}

	return strings.TrimSpace(generatedNotes), nil
}

func synthesizeFallbackNotes(title, content string) string {
	cleanContent := strings.TrimSpace(content)
	if len(cleanContent) == 0 {
		if title != "" {
			return fmt.Sprintf("Notes on %s.", title)
		}
		return "Summary notes unavailable."
	}

	sentences := strings.Split(cleanContent, ". ")
	if len(sentences) > 3 {
		return strings.Join(sentences[:3], ". ") + "."
	}
	if len(cleanContent) > 300 {
		return cleanContent[:300] + "..."
	}
	return cleanContent
}

func truncateString(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
