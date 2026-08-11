package pipeline

import (
	"context"
	"fmt"

	"linkaroo-app/server/pkg/pipeline/agent"
	"linkaroo-app/server/pkg/pipeline/detector"
	"linkaroo-app/server/pkg/pipeline/extractor"
	"linkaroo-app/server/pkg/pipeline/mapper"
	"linkaroo-app/server/pkg/pipeline/models"
)

// Pipeline is the core media detection and extraction engine for Linkaroo.
type Pipeline struct {
	detectorRegistry  *detector.DetectorRegistry
	extractorRegistry *extractor.ExtractorRegistry
	canonicalMapper   mapper.CanonicalMapper
	agent             *agent.LangChainContentAgent
}

// NewPipeline creates a fully initialized Media Detection and Extraction Pipeline with default plugins registered.
func NewPipeline(fetcher extractor.HTTPFetcher) *Pipeline {
	if fetcher == nil {
		fetcher = extractor.NewMemoryHTTPFetcher()
	}

	// 1. Initialize Detector Registry
	detReg := detector.NewDetectorRegistry()
	detReg.Register(detector.NewURLDomainStrategy(100))
	detReg.Register(detector.NewMIMETypeStrategy(90))
	detReg.Register(detector.NewPlainTextStrategy(80))

	// 2. Initialize Extractor Registry
	extReg := extractor.NewExtractorRegistry()
	extReg.Register(extractor.NewAmazonExtractor(fetcher))
	extReg.Register(extractor.NewYouTubeExtractor(fetcher))
	extReg.Register(extractor.NewIMDbExtractor(fetcher))
	extReg.Register(extractor.NewGoodreadsExtractor(fetcher))
	extReg.Register(extractor.NewImageExtractor())
	extReg.Register(extractor.NewPDFExtractor())
	extReg.Register(extractor.NewTextExtractor())
	extReg.SetFallback(extractor.NewArticleExtractor(fetcher))

	// 3. Initialize Mapper
	cnMapper := mapper.NewDefaultCanonicalMapper()

	// 4. Initialize Agent
	contentAgent := agent.NewLangChainContentAgent(fetcher, nil)

	return &Pipeline{
		detectorRegistry:  detReg,
		extractorRegistry: extReg,
		canonicalMapper:   cnMapper,
		agent:             contentAgent,
	}
}

// CustomPipeline allows assembling a pipeline with custom registries (e.g. for testing).
func CustomPipeline(detReg *detector.DetectorRegistry, extReg *extractor.ExtractorRegistry, cnMapper mapper.CanonicalMapper) *Pipeline {
	fetcher := extractor.NewMemoryHTTPFetcher()
	contentAgent := agent.NewLangChainContentAgent(fetcher, nil)

	return &Pipeline{
		detectorRegistry:  detReg,
		extractorRegistry: extReg,
		canonicalMapper:   cnMapper,
		agent:             contentAgent,
	}
}

// SetAgent allows setting a custom LangChainContentAgent instance.
func (p *Pipeline) SetAgent(ag *agent.LangChainContentAgent) {
	p.agent = ag
}

// RegisterExtractor enables dynamic plugin extension without modifying pipeline core logic.
func (p *Pipeline) RegisterExtractor(e extractor.Extractor) {
	p.extractorRegistry.Register(e)
}

// RegisterDetectionStrategy enables adding new source detection rules dynamically.
func (p *Pipeline) RegisterDetectionStrategy(strategy detector.DetectionStrategy) {
	p.detectorRegistry.Register(strategy)
}

// Process accepts a RawItem and executes the complete Detection -> Extraction -> Mapping pipeline.
func (p *Pipeline) Process(ctx context.Context, item models.RawItem) (*models.NormalizedResult, error) {
	// Step 1: Detect Source
	sourceType, detectConf := p.detectorRegistry.DetectSource(ctx, item)

	// Step 2: Resolve Extractor
	ext, found := p.extractorRegistry.GetExtractor(ctx, sourceType, item)
	if !found {
		// Fallback to unknown source normalization without crashing
		result, err := p.canonicalMapper.MapToCanonical(ctx, item, models.SourceUnknown, nil)
		if result != nil {
			result.Confidence = detectConf
			result.AddError("extractor", "NO_EXTRACTOR", fmt.Sprintf("No extractor found for source %s", sourceType))
			// Run agent workflow even on fallback if result exists
			_ = p.agent.ExecuteWorkflow(ctx, item, result)
		}
		return result, err
	}

	// Step 3: Extract Metadata (Metadata Scraping Stage)
	extractedData, err := ext.Extract(ctx, item)
	if err != nil {
		// Graceful degradation: map fallback item with recorded error
		result, mapErr := p.canonicalMapper.MapToCanonical(ctx, item, sourceType, nil)
		if result != nil {
			result.AddError("extractor", "EXTRACTION_FAILED", err.Error())
			_ = p.agent.ExecuteWorkflow(ctx, item, result)
		}
		return result, mapErr
	}

	// Step 4: Map to Canonical Normalized Result (Metadata Scraping Ends Here)
	result, mapErr := p.canonicalMapper.MapToCanonical(ctx, item, sourceType, extractedData)
	if mapErr != nil {
		if result == nil {
			result = &models.NormalizedResult{ItemID: item.ID, Source: sourceType, CanonicalType: models.MediaTypeUnknown}
		}
		result.AddError("mapper", "MAPPING_FAILED", mapErr.Error())
	}

	// Step 5: Execute LangChain Agentic Workflow (Content Extraction -> Note Generation -> Tag Generation)
	if agentErr := p.agent.ExecuteWorkflow(ctx, item, result); agentErr != nil {
		result.AddError("agent", "WORKFLOW_FAILED", agentErr.Error())
	}

	return result, nil
}
