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
	Schema *OntologySchema // optional; if set, guides entity extraction
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
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, chunk := range chunks {
		select {
		case <-ctx.Done():
			return p, ctx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(c db.Chunk) {
			defer func() {
				<-sem
				wg.Done()
			}()

			extracted, err := b.extractFromChunk(ctx, c.Content)
			if err != nil {
				atomic.AddInt64(&p.Errors, 1)
				atomic.AddInt64(&p.Done, 1)
				if onProgress != nil {
					onProgress(p)
				}
				return
			}

			mu.Lock()
			nodes, edges := b.storeExtraction(projectID, extracted)
			mu.Unlock()

			atomic.AddInt64(&p.NodesAdded, int64(nodes))
			atomic.AddInt64(&p.EdgesAdded, int64(edges))
			atomic.AddInt64(&p.Done, 1)

			_ = b.db.MarkChunkDone(c.ID)
			if onProgress != nil {
				onProgress(p)
			}
		}(chunk)
	}

	wg.Wait()
	return p, nil
}

// extractFromChunk calls the LLM to extract entities/relationships from a text
// chunk. It retries up to maxJSONRetries times on JSON parse failure, since
// LLMs occasionally return malformed JSON.
const maxJSONRetries = 3

func (b *Builder) extractFromChunk(ctx context.Context, text string) (*extractionResult, error) {
	prompt := systemPrompt
	if b.config.Schema != nil {
		prompt += b.config.Schema.ToPromptHint()
	}
	userMsg := fmt.Sprintf("Extract entities and relationships from this text:\n\n%s", text)

	var result extractionResult
	var lastErr error
	for attempt := 0; attempt < maxJSONRetries; attempt++ {
		lastErr = b.llm.JSON(ctx, prompt, userMsg, &result)
		if lastErr == nil {
			return &result, nil
		}
		// Only retry on JSON parse errors, not on context cancellation or API errors.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Reset result for next attempt
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
