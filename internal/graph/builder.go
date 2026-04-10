package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"fishnet/internal/db"
	"fishnet/internal/llm"
)

// ─── LLM Extraction Types ────────────────────────────────────────────────────

type extractionResult struct {
	Entities []struct {
		Name       string            `json:"name"`
		Type       string            `json:"type"`
		Summary    string            `json:"summary"`
		Attributes map[string]string `json:"attributes"`
	} `json:"entities"`
	Relationships []struct {
		Source string `json:"source"`
		Target string `json:"target"`
		Type   string `json:"type"`
		Fact   string `json:"fact"`
	} `json:"relationships"`
}

const systemPrompt = `You are a knowledge graph extractor. Extract entities and relationships from text.
Return ONLY raw JSON with this structure (no markdown, no explanation):
{
  "entities": [
    {
      "name": "EntityName",
      "type": "EntityType",
      "summary": "one-line description",
      "attributes": {"key": "value"}
    }
  ],
  "relationships": [
    {"source": "EntityName1", "target": "EntityName2", "type": "RelationType", "fact": "natural language description"}
  ]
}
Rules:
- Entity types: Person, Company, Product, Technology, Concept, Event, Location, Policy, Topic
- Relationship types: works_for, competes_with, uses, related_to, supports, opposes, part_of, mentioned_in
- Only extract entities explicitly mentioned in the text
- Names should be exact as they appear in text
- Keep summaries under 20 words
- attributes is optional; use it for key properties like title, role, location`

// ─── Builder ─────────────────────────────────────────────────────────────────

// Config holds optional configuration for the Builder.
type Config struct {
	Schema         *OntologySchema // optional; if set, guides entity extraction
	BatchSize      int             // chunks per LLM call (default 3; reduces API calls ~3x)
	ExtractionMode string          // "local" | "llm" | "hybrid" | "onnx" (default: "local")
}

type Builder struct {
	db     *db.DB
	llm    *llm.Client
	config Config
}

func NewBuilder(database *db.DB, client *llm.Client) *Builder {
	return &Builder{db: database, llm: client}
}

// NewBuilderWithConfig creates a Builder with the given Config.
func NewBuilderWithConfig(database *db.DB, client *llm.Client, cfg Config) *Builder {
	return &Builder{db: database, llm: client, config: cfg}
}

type Progress struct {
	Total     int
	Done      int64
	Errors    int64
	NodesAdded int64
	EdgesAdded int64
}

// BuildFromChunks processes chunks concurrently, extracting entities into the graph.
// Chunks are grouped into batches (config.BatchSize, default 3) so each LLM call
// covers multiple chunks, reducing total API round-trips ~3x.
func (b *Builder) BuildFromChunks(
	ctx context.Context,
	projectID string,
	chunks []db.Chunk,
	concurrency int,
	onProgress func(p Progress),
) (Progress, error) {

	p := Progress{Total: len(chunks)}

	if concurrency <= 0 {
		concurrency = 4
	}
	batchSize := b.config.BatchSize
	if batchSize <= 0 {
		batchSize = 3
	}

	// Group chunks into batches.
	type batch struct{ chunks []db.Chunk }
	var batches []batch
	for i := 0; i < len(chunks); i += batchSize {
		end := i + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		batches = append(batches, batch{chunks: chunks[i:end]})
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, bt := range batches {
		select {
		case <-ctx.Done():
			return p, ctx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(bt batch) {
			defer func() {
				<-sem
				wg.Done()
			}()

			texts := make([]string, len(bt.chunks))
			for i, c := range bt.chunks {
				texts[i] = c.Content
			}
			var extracted *extractionResult
			var err error
			if b.config.ExtractionMode == "llm" {
				extracted, err = b.extractFromChunks(ctx, texts)
				if err != nil {
					atomic.AddInt64(&p.Errors, int64(len(bt.chunks)))
					atomic.AddInt64(&p.Done, int64(len(bt.chunks)))
					if onProgress != nil {
						onProgress(p)
					}
					return
				}
			} else if b.config.ExtractionMode == "onnx" {
				// ONNX multilingual BERT NER — no LLM needed, 100x faster.
				// Supports Chinese + English natively without translation.
				onnxEx, onnxErr := NewOnnxExtractor()
				if onnxErr != nil {
					// Fallback to local NER if ONNX not available (model not downloaded).
					localEx := NewLocalExtractor()
					extracted = localEx.Extract(translateCJKBatch(ctx, b.llm, texts))
				} else {
					defer onnxEx.Close()
					extracted = onnxEx.Extract(texts)
				}
			} else {
				// "local" / "hybrid" / "" — prose NER.
				// Auto-translate CJK-heavy chunks to English first so prose NER works.
				localEx := NewLocalExtractor()
				extracted = localEx.Extract(translateCJKBatch(ctx, b.llm, texts))
			}

			mu.Lock()
			nodes, edges := b.storeExtraction(projectID, extracted)
			mu.Unlock()

			atomic.AddInt64(&p.NodesAdded, int64(nodes))
			atomic.AddInt64(&p.EdgesAdded, int64(edges))
			atomic.AddInt64(&p.Done, int64(len(bt.chunks)))

			for _, c := range bt.chunks {
				_ = b.db.MarkChunkDone(c.ID)
			}
			if onProgress != nil {
				onProgress(p)
			}
		}(bt)
	}

	wg.Wait()
	return p, nil
}

// extractFromChunks calls the LLM to extract entities/relationships from one or
// more text chunks in a single API call. Sending multiple chunks at once
// (~3 by default) reduces total round-trips ~3x with no quality loss.
// It retries up to maxJSONRetries times on JSON parse failure.
const maxJSONRetries = 3

func (b *Builder) extractFromChunks(ctx context.Context, texts []string) (*extractionResult, error) {
	prompt := systemPrompt
	if b.config.Schema != nil {
		prompt += b.config.Schema.ToPromptHint()
	}

	var userMsg string
	if len(texts) == 1 {
		userMsg = fmt.Sprintf("Extract entities and relationships from this text:\n\n%s", texts[0])
	} else {
		var sb strings.Builder
		sb.WriteString("Extract entities and relationships from ALL of the following text sections. Merge entities that refer to the same real-world actor.\n\n")
		for i, t := range texts {
			sb.WriteString(fmt.Sprintf("=== SECTION %d ===\n%s\n\n", i+1, t))
		}
		userMsg = sb.String()
	}

	var result extractionResult
	var lastErr error
	for attempt := 0; attempt < maxJSONRetries; attempt++ {
		lastErr = b.llm.JSON(ctx, prompt, userMsg, &result)
		if lastErr == nil {
			return &result, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		result = extractionResult{}
	}
	return nil, fmt.Errorf("extraction failed after %d attempts: %w", maxJSONRetries, lastErr)
}

func (b *Builder) storeExtraction(projectID string, ex *extractionResult) (nodes, edges int) {
	nameToID := make(map[string]string)

	for _, e := range ex.Entities {
		name := strings.TrimSpace(e.Name)
		typ := strings.TrimSpace(e.Type)
		if name == "" || typ == "" {
			continue
		}
		// Merge extracted attributes with a sentinel marker.
		attrMap := make(map[string]string)
		attrMap["extracted"] = "true"
		for k, v := range e.Attributes {
			attrMap[k] = v
		}
		attrs, _ := json.Marshal(attrMap)
		id, err := b.db.UpsertNode(projectID, name, typ, e.Summary, string(attrs))
		if err == nil {
			nameToID[name] = id
			nodes++
		}
	}

	for _, r := range ex.Relationships {
		srcID, ok1 := nameToID[r.Source]
		tgtID, ok2 := nameToID[r.Target]
		if !ok1 || !ok2 {
			continue
		}
		relType := strings.TrimSpace(r.Type)
		if relType == "" {
			relType = "related_to"
		}
		if err := b.db.UpsertEdge(projectID, srcID, tgtID, relType, r.Fact); err == nil {
			edges++
		}
	}
	return
}

// ─── CJK Translation ─────────────────────────────────────────────────────────

// isCJKHeavy returns true when more than 15% of runes in the text are
// CJK Unified Ideographs (U+4E00–U+9FFF). Used to decide whether to
// translate before prose NER (which is English-only).
func isCJKHeavy(text string) bool {
	var total, cjk int
	for _, r := range text {
		total++
		if r >= 0x4E00 && r <= 0x9FFF {
			cjk++
		}
	}
	return total > 0 && float64(cjk)/float64(total) > 0.15
}

// translateCJKBatch checks each text for CJK content. If any are CJK-heavy,
// it sends them all in one LLM call for translation, then returns the
// (possibly translated) texts. Falls back to the originals on any error.
func translateCJKBatch(ctx context.Context, client *llm.Client, texts []string) []string {
	if client == nil {
		return texts
	}

	// Find which indices need translation.
	var needsTranslation []int
	for i, t := range texts {
		if isCJKHeavy(t) {
			needsTranslation = append(needsTranslation, i)
		}
	}
	if len(needsTranslation) == 0 {
		return texts // fast path: all English
	}

	// Build a single prompt with all CJK sections.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Translate each of the following %d sections to English. ", len(needsTranslation)))
	sb.WriteString("Return a JSON array of strings in the same order. No explanation.\n\n")
	for order, idx := range needsTranslation {
		sb.WriteString(fmt.Sprintf("=== SECTION %d ===\n%s\n\n", order+1, texts[idx]))
	}

	var translated []string
	if err := client.JSON(ctx,
		"You are a professional translator. Translate each section to English exactly, preserving names and technical terms.",
		sb.String(),
		&translated,
	); err != nil || len(translated) != len(needsTranslation) {
		return texts // graceful fallback: use originals
	}

	// Splice translated texts back into the result slice.
	out := make([]string, len(texts))
	copy(out, texts)
	for order, idx := range needsTranslation {
		out[idx] = translated[order]
	}
	return out
}
