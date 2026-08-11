package models

import (
	"time"
)

// Author represents an entity responsible for creating the content.
type Author struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
	URL  string `json:"url,omitempty"`
}

// ImageRef represents image asset references linked to extracted items.
type ImageRef struct {
	URL     string `json:"url"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	Type    string `json:"type,omitempty"` // e.g. "thumbnail", "cover", "banner"
	Caption string `json:"caption,omitempty"`
}

// ExtractionError captures non-fatal errors during detection or extraction.
type ExtractionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Stage   string `json:"stage"` // "detection", "extraction", "mapping"
}

// BookDetails provides specialized attributes for books and audiobooks.
type BookDetails struct {
	ISBN        string `json:"isbn,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	Pages       int    `json:"pages,omitempty"`
	Format      string `json:"format,omitempty"` // e.g. "Hardcover", "Kindle Edition", "Audiobook"
	Rating      string `json:"rating,omitempty"`
	ReviewCount int    `json:"review_count,omitempty"`
}

// ProductDetails provides specialized attributes for e-commerce products.
type ProductDetails struct {
	Price        string `json:"price,omitempty"`
	Currency     string `json:"currency,omitempty"`
	Brand        string `json:"brand,omitempty"`
	Category     string `json:"category,omitempty"` // e.g. "Electronics", "Fashion", "Furniture"
	InStock      bool   `json:"in_stock,omitempty"`
	Rating       string `json:"rating,omitempty"`
	ReviewCount  int    `json:"review_count,omitempty"`
	ModelNumber  string `json:"model_number,omitempty"`
}

// VideoDetails provides specialized attributes for video/media streams.
type VideoDetails struct {
	DurationSeconds int64  `json:"duration_seconds,omitempty"`
	ChannelName     string `json:"channel_name,omitempty"`
	ChannelID       string `json:"channel_id,omitempty"`
	ViewCount       int64  `json:"view_count,omitempty"`
	IsLive          bool   `json:"is_live,omitempty"`
}

// PDFDetails provides specialized attributes for PDF documents.
type PDFDetails struct {
	PageCount int    `json:"page_count,omitempty"`
	Producer  string `json:"producer,omitempty"`
	Encrypted bool   `json:"encrypted,omitempty"`
	Version   string `json:"version,omitempty"`
}

// ImageDetails provides specialized attributes for images and screenshots.
type ImageDetails struct {
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	ColorSpace string `json:"color_space,omitempty"`
	Format    string `json:"format,omitempty"`
}

// NormalizedResult is the unified canonical object returned by the Media Detection and Extraction Pipeline.
type NormalizedResult struct {
	ItemID        string                 `json:"item_id"`
	CanonicalType MediaType              `json:"canonical_type"`
	Source        SourceType             `json:"source"`
	Title         string                 `json:"title"`
	Subtitle      string                 `json:"subtitle,omitempty"`
	Description   string                 `json:"description,omitempty"`
	Authors       []Author               `json:"authors,omitempty"`
	Images        []ImageRef             `json:"images,omitempty"`
	Thumbnail     *ImageRef              `json:"thumbnail,omitempty"`
	Metadata      map[string]any         `json:"metadata,omitempty"`
	OriginalURL   string                 `json:"original_url,omitempty"`
	Language      string                 `json:"language,omitempty"`
	PublishDate   *time.Time             `json:"publish_date,omitempty"`
	CreatedTime   time.Time              `json:"created_time"`
	RawMetadata   map[string]any         `json:"raw_metadata,omitempty"`
	Confidence    float64                `json:"confidence"` // Range: 0.0 to 1.0

	// Typed Domain Sub-structs (Optional, populated when available)
	BookDetails    *BookDetails    `json:"book_details,omitempty"`
	ProductDetails *ProductDetails `json:"product_details,omitempty"`
	VideoDetails   *VideoDetails   `json:"video_details,omitempty"`
	PDFDetails     *PDFDetails     `json:"pdf_details,omitempty"`
	ImageDetails   *ImageDetails   `json:"image_details,omitempty"`

	// Agentic Processing Outputs
	Content string   `json:"content,omitempty"`
	Notes   string   `json:"notes,omitempty"`
	Tags    []string `json:"tags,omitempty"`

	// Non-fatal processing warnings/errors
	Errors []ExtractionError `json:"errors,omitempty"`
}

// AddError appends a non-fatal extraction warning/error to the normalized result.
func (n *NormalizedResult) AddError(stage, code, msg string) {
	n.Errors = append(n.Errors, ExtractionError{
		Stage:   stage,
		Code:    code,
		Message: msg,
	})
}
