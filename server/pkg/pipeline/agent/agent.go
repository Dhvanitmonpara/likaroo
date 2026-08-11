package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"linkaroo-app/server/pkg/pipeline/extractor"
	"linkaroo-app/server/pkg/pipeline/models"
)

// LLMClient defines the interface for underlying Large Language Model provider invocations.
type LLMClient interface {
	GenerateText(ctx context.Context, prompt string) (string, error)
}

// FallbackLLMClient provides deterministic LLM response generation when external API keys are omitted.
type FallbackLLMClient struct{}

func (f *FallbackLLMClient) GenerateText(ctx context.Context, prompt string) (string, error) {
	return "", fmt.Errorf("no API key configured, relying on fallback synthesis")
}

// GeminiLLMClient provides real Google Gemini API calls via REST endpoint.
type GeminiLLMClient struct {
	apiKey string
	client *http.Client
}

func NewGeminiLLMClient(apiKey string) *GeminiLLMClient {
	return &GeminiLLMClient{
		apiKey: apiKey,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (g *GeminiLLMClient) GenerateText(ctx context.Context, prompt string) (string, error) {
	type endpointConfig struct {
		apiVersion string
		modelName  string
	}

	endpointsToTry := []endpointConfig{
		{apiVersion: "v1", modelName: "gemini-2.5-flash-lite"},
		{apiVersion: "v1beta", modelName: "gemini-2.0-flash-exp"},
	}

	if customModel := strings.TrimSpace(os.Getenv("GEMINI_MODEL")); customModel != "" {
		endpointsToTry = append([]endpointConfig{
			{apiVersion: "v1beta", modelName: customModel},
			{apiVersion: "v1", modelName: customModel},
		}, endpointsToTry...)
	}

	payload := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]any{
					{"text": prompt},
				},
			},
		},
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	var lastErr error
	for _, ep := range endpointsToTry {
		url := fmt.Sprintf("https://generativelanguage.googleapis.com/%s/models/%s:generateContent?key=%s", ep.apiVersion, ep.modelName, g.apiKey)
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBytes))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := g.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("gemini API error (%s/%s, status %d): %s", ep.apiVersion, ep.modelName, resp.StatusCode, string(bodyBytes))
			if resp.StatusCode == 404 {
				// Silently try next candidate endpoint
				continue
			}
			log.Printf("[LangChain Agent] Gemini API returned error: %v", lastErr)
			return "", lastErr
		}

		var geminiResp struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}

		if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
			return "", err
		}

		if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
			resultText := strings.TrimSpace(geminiResp.Candidates[0].Content.Parts[0].Text)
			log.Printf("[LangChain Agent] Successfully generated text using Gemini (%s/%s)", ep.apiVersion, ep.modelName)
			return resultText, nil
		}
	}

	if lastErr != nil {
		log.Printf("[LangChain Agent] Gemini API call failed: %v", lastErr)
	}

	return "", lastErr
}

// OpenAILLMClient provides real OpenAI Chat Completions API calls.
type OpenAILLMClient struct {
	apiKey string
	client *http.Client
}

func NewOpenAILLMClient(apiKey string) *OpenAILLMClient {
	return &OpenAILLMClient{
		apiKey: apiKey,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (o *OpenAILLMClient) GenerateText(ctx context.Context, prompt string) (string, error) {
	url := "https://api.openai.com/v1/chat/completions"

	payload := map[string]any{
		"model": "gpt-4o-mini",
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.3,
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	log.Printf("[LangChain Agent] Sending prompt request to OpenAI LLM...")
	resp, err := o.client.Do(req)
	if err != nil {
		log.Printf("[LangChain Agent] OpenAI API HTTP request error: %v", err)
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[LangChain Agent] OpenAI API error (%d): %s", resp.StatusCode, string(bodyBytes))
		return "", fmt.Errorf("openAI API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var openAIResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(bodyBytes, &openAIResp); err != nil {
		return "", err
	}

	if len(openAIResp.Choices) > 0 {
		resultText := strings.TrimSpace(openAIResp.Choices[0].Message.Content)
		log.Printf("[LangChain Agent] Received OpenAI response: %s", truncateString(resultText, 100))
		return resultText, nil
	}

	return "", fmt.Errorf("empty choices in openAI response")
}

// LangChainContentAgent orchestrates the 3-step LangChain agentic workflow for link media processing.
type LangChainContentAgent struct {
	extractionTool *ContentExtractionTool
	noteChain      *NoteGenerationChain
	tagChain       *TagGenerationChain
}

// NewLangChainContentAgent creates a fully initialized Content Agent.
func NewLangChainContentAgent(fetcher extractor.HTTPFetcher, llm LLMClient) *LangChainContentAgent {
	if fetcher == nil {
		fetcher = extractor.NewMemoryHTTPFetcher()
	}
	if llm == nil {
		llm = createDefaultLLMClient()
	}

	return &LangChainContentAgent{
		extractionTool: NewContentExtractionTool(fetcher),
		noteChain:      NewNoteGenerationChain(llm),
		tagChain:       NewTagGenerationChain(llm),
	}
}

func createDefaultLLMClient() LLMClient {
	openAIKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	geminiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))

	if openAIKey != "" {
		log.Printf("[LangChain Agent] Active LLM Provider: OPENAI_API_KEY (%s)", maskAPIKey(openAIKey))
		return NewOpenAILLMClient(openAIKey)
	}

	if geminiKey != "" {
		log.Printf("[LangChain Agent] Active LLM Provider: GEMINI_API_KEY (%s)", maskAPIKey(geminiKey))
		return NewGeminiLLMClient(geminiKey)
	}

	log.Printf("[LangChain Agent] No API key detected (OPENAI_API_KEY / GEMINI_API_KEY omitted). Using deterministic local agent fallback.")
	return &FallbackLLMClient{}
}

func maskAPIKey(key string) string {
	clean := strings.TrimSpace(key)
	if len(clean) <= 8 {
		return "****"
	}
	return clean[:4] + "..." + clean[len(clean)-4:]
}

// ExecuteWorkflow runs the 3-step LangChain agentic workflow AFTER metadata scraping completes:
// Step 1: Extract content from provided media
// Step 2: Generate notes based on content if not exists
// Step 3: Generate tags from content
func (a *LangChainContentAgent) ExecuteWorkflow(ctx context.Context, item models.RawItem, res *models.NormalizedResult) error {
	if res == nil {
		return fmt.Errorf("cannot execute agent workflow on nil NormalizedResult")
	}

	// -------------------------------------------------------------
	// Step 1: Extract Content from Provided Media
	// -------------------------------------------------------------
	extractedContent, err := a.extractionTool.ExtractContent(ctx, item, res)
	if err != nil {
		res.AddError("agent", "CONTENT_EXTRACTION_FAILED", err.Error())
	} else {
		res.Content = extractedContent
	}

	// -------------------------------------------------------------
	// Step 2: Generate Notes Based on Content if Not Exists
	// -------------------------------------------------------------
	currentNotes := res.Notes
	if currentNotes == "" {
		currentNotes = res.Description
	}

	notes, err := a.noteChain.GenerateNotes(ctx, res.Title, currentNotes, res.Content)
	if err != nil {
		res.AddError("agent", "NOTE_GENERATION_FAILED", err.Error())
	} else {
		res.Notes = notes
		if strings.TrimSpace(res.Description) == "" {
			res.Description = notes
		}
	}

	// -------------------------------------------------------------
	// Step 3: Generate Tags from Content
	// -------------------------------------------------------------
	tags, err := a.tagChain.GenerateTags(ctx, res.Title, res.Notes, res.Content)
	if err != nil {
		res.AddError("agent", "TAG_GENERATION_FAILED", err.Error())
	} else {
		res.Tags = tags
		log.Printf("[LangChain Agent] Generated tags: %v", tags)
	}

	return nil
}
