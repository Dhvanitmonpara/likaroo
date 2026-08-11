package test

import (
	"context"
	"testing"

	"linkaroo-app/server/pkg/pipeline/agent"
	"linkaroo-app/server/pkg/pipeline/extractor"
	"linkaroo-app/server/pkg/pipeline/models"
)

func TestLangChainContentAgent(t *testing.T) {
	fetcher := extractor.NewMemoryHTTPFetcher()
	fetcher.HTMLMap["https://example.com/article"] = `
		<html>
			<head><title>Understanding Go Concurrency</title></head>
			<body>
				<article>
					<p>Go provides lightweight goroutines and channels to implement concurrent programming cleanly.</p>
					<p>Concurrency is not parallelism, but it enables flexible software design.</p>
				</article>
			</body>
		</html>
	`

	contentAgent := agent.NewLangChainContentAgent(fetcher, nil)
	ctx := context.Background()

	t.Run("Step 1: Extract Content from Article URL", func(t *testing.T) {
		item := models.RawItem{URL: "https://example.com/article"}
		res := &models.NormalizedResult{
			Title: "Understanding Go Concurrency",
		}

		err := contentAgent.ExecuteWorkflow(ctx, item, res)
		if err != nil {
			t.Fatalf("ExecuteWorkflow returned unexpected error: %v", err)
		}

		if res.Content == "" {
			t.Errorf("Expected extracted content to be non-empty")
		}

		if res.Notes == "" {
			t.Errorf("Expected generated notes to be non-empty")
		}

		if len(res.Tags) == 0 {
			t.Errorf("Expected generated tags slice to be non-empty")
		}
	})

	t.Run("Step 2: Generate Notes If Not Exists for Plain Text", func(t *testing.T) {
		item := models.RawItem{Text: "Quick reminder: Review PR #42 for database indexing optimizations before release."}
		res := &models.NormalizedResult{
			Title: "Quick Reminder",
		}

		err := contentAgent.ExecuteWorkflow(ctx, item, res)
		if err != nil {
			t.Fatalf("ExecuteWorkflow failed: %v", err)
		}

		if res.Content != item.Text {
			t.Errorf("Content = %q, want %q", res.Content, item.Text)
		}

		if res.Notes == "" {
			t.Errorf("Notes should be generated when description is missing")
		}
	})

	t.Run("Step 3: Tag Generation From Content", func(t *testing.T) {
		item := models.RawItem{Text: "Tutorial on Python machine learning and data science models."}
		res := &models.NormalizedResult{
			Title: "Python ML Tutorial",
		}

		err := contentAgent.ExecuteWorkflow(ctx, item, res)
		if err != nil {
			t.Fatalf("ExecuteWorkflow failed: %v", err)
		}

		hasExpectedTag := false
		for _, tag := range res.Tags {
			if tag == "python" || tag == "tutorial" || tag == "programming" {
				hasExpectedTag = true
				break
			}
		}

		if !hasExpectedTag {
			t.Errorf("Tags = %v, expected at least one relevant tag like python/tutorial", res.Tags)
		}
	})
}
