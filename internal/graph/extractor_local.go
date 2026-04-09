package graph

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"

	prose "github.com/jdkato/prose/v2"
)

// proseModel is the shared NLP model, initialized once on first use.
// Reusing the model avoids the ~150ms deserialization cost per document,
// bringing per-document inference down to ~11ms.
var (
	proseModelOnce sync.Once
	proseModelInst *prose.Model
)

// getProseModel returns the lazily-initialized shared prose model.
func getProseModel() *prose.Model {
	proseModelOnce.Do(func() {
		// Initialize by parsing a short seed sentence; prose stores the model
		// on the Document and we capture it for reuse.
		doc, _ := prose.NewDocument("Init.")
		if doc != nil {
			proseModelInst = doc.Model
		}
	})
	return proseModelInst
}

// LocalExtractor performs NER using prose/v2 (English) without any LLM API calls.
// For 157 chunks of 600 chars each running concurrently, it completes in <1 second.
type LocalExtractor struct {
	coOccWindow int // sentence window for co-occurrence (default 2)
	minCoOcc    int // min co-occurrences to create edge (default 1)
}

func NewLocalExtractor() *LocalExtractor {
	// Warm the model eagerly so the first Extract call is fast.
	getProseModel()
	return &LocalExtractor{coOccWindow: 2, minCoOcc: 1}
}

// chunkResult holds the per-text NER output before merging.
type chunkResult struct {
	entityFreq map[string]int
	entityMeta map[string]entityMetaEntry
	coOcc      map[pairKey]int
	coOccCtx   map[pairKey]string
}

type entityMetaEntry struct {
	display string
	label   string
}

type pairKey struct{ a, b string }

// Extract runs NER + co-occurrence on one or more text sections concurrently.
// Returns the same extractionResult type used by the LLM extractor (same package).
func (e *LocalExtractor) Extract(texts []string) *extractionResult {
	if len(texts) == 0 {
		return &extractionResult{}
	}

	totalTexts := len(texts)
	results := make([]*chunkResult, totalTexts)

	// Process each text in parallel, bounded by available CPUs.
	workers := runtime.NumCPU()
	if workers > totalTexts {
		workers = totalTexts
	}

	type job struct {
		idx  int
		text string
	}
	jobs := make(chan job, totalTexts)
	for i, t := range texts {
		jobs <- job{i, t}
	}
	close(jobs)

	var wg sync.WaitGroup
	wg.Add(workers)
	for range make([]struct{}, workers) {
		go func() {
			defer wg.Done()
			for j := range jobs {
				results[j.idx] = e.processText(j.text)
			}
		}()
	}
	wg.Wait()

	// Merge all per-text results.
	entityFreq := make(map[string]int)
	entityMetaMap := make(map[string]entityMetaEntry)
	coOcc := make(map[pairKey]int)
	coOccContext := make(map[pairKey]string)

	for _, r := range results {
		if r == nil {
			continue
		}
		for k, v := range r.entityFreq {
			entityFreq[k] += v
		}
		for k, v := range r.entityMeta {
			if _, exists := entityMetaMap[k]; !exists {
				entityMetaMap[k] = v
			}
		}
		for p, cnt := range r.coOcc {
			coOcc[p] += cnt
		}
		for p, ctx := range r.coOccCtx {
			if _, ok := coOccContext[p]; !ok {
				coOccContext[p] = ctx
			}
		}
	}

	// Build TF-IDF weight: log(1 + freq) / log(totalTexts+1)
	idfDenom := math.Log(float64(totalTexts) + 1)

	result := &extractionResult{}

	// Add entities
	for norm, meta := range entityMetaMap {
		freq := entityFreq[norm]
		var tfidf float64
		if idfDenom > 0 {
			tfidf = math.Log(1+float64(freq)) / idfDenom
		}
		summary := meta.label + " entity"
		if tfidf > 0.5 {
			summary = "prominent " + summary
		}
		result.Entities = append(result.Entities, struct {
			Name       string            `json:"name"`
			Type       string            `json:"type"`
			Summary    string            `json:"summary"`
			Attributes map[string]string `json:"attributes"`
		}{
			Name:    meta.display,
			Type:    meta.label,
			Summary: summary,
			Attributes: map[string]string{
				"freq":  fmt.Sprintf("%d", freq),
				"tfidf": fmt.Sprintf("%.3f", tfidf),
			},
		})
	}

	// Add co-occurrence relationships
	for p, count := range coOcc {
		if count < e.minCoOcc {
			continue
		}
		srcMeta := entityMetaMap[p.a]
		tgtMeta := entityMetaMap[p.b]
		ctx := coOccContext[p]
		relType := inferRelationType(ctx)
		fact := fmt.Sprintf("Co-occurs %d times: %s", count, ctx)
		if len(fact) > 200 {
			fact = fact[:200]
		}
		result.Relationships = append(result.Relationships, struct {
			Source string `json:"source"`
			Target string `json:"target"`
			Type   string `json:"type"`
			Fact   string `json:"fact"`
		}{
			Source: srcMeta.display,
			Target: tgtMeta.display,
			Type:   relType,
			Fact:   fact,
		})
	}

	return result
}

// processText runs prose NER on a single text and returns the per-text result.
func (e *LocalExtractor) processText(text string) *chunkResult {
	r := &chunkResult{
		entityFreq: make(map[string]int),
		entityMeta: make(map[string]entityMetaEntry),
		coOcc:      make(map[pairKey]int),
		coOccCtx:   make(map[pairKey]string),
	}

	model := getProseModel()
	var doc *prose.Document
	var err error
	if model != nil {
		doc, err = prose.NewDocument(text, prose.UsingModel(model))
	} else {
		doc, err = prose.NewDocument(text)
	}
	if err != nil {
		return r
	}

	sentences := doc.Sentences()
	allEnts := doc.Entities()

	for si, sent := range sentences {
		// Collect entities in current sentence
		var sentEntities []string
		for _, ent := range allEnts {
			if strings.Contains(sent.Text, ent.Text) {
				norm := normalizeEntityName(ent.Text)
				if norm == "" || len(norm) < 2 {
					continue
				}
				sentEntities = append(sentEntities, norm)
				r.entityFreq[norm]++
				if _, exists := r.entityMeta[norm]; !exists {
					r.entityMeta[norm] = entityMetaEntry{
						display: ent.Text,
						label:   proseToEntityType(ent.Label),
					}
				}
			}
		}

		// Co-occurrence within the same sentence
		for _, a := range sentEntities {
			for _, b := range sentEntities {
				if a >= b {
					continue
				}
				p := pairKey{a, b}
				r.coOcc[p]++
				if _, ok := r.coOccCtx[p]; !ok {
					r.coOccCtx[p] = sent.Text
				}
			}
		}

		// Co-occurrence across sentence window
		for wi := si + 1; wi <= si+e.coOccWindow && wi < len(sentences); wi++ {
			var winEntities []string
			for _, ent := range allEnts {
				if strings.Contains(sentences[wi].Text, ent.Text) {
					norm := normalizeEntityName(ent.Text)
					if norm != "" && len(norm) >= 2 {
						winEntities = append(winEntities, norm)
					}
				}
			}
			for _, a := range sentEntities {
				for _, b := range winEntities {
					if a == b {
						continue
					}
					ka, kb := a, b
					if ka > kb {
						ka, kb = kb, ka
					}
					p := pairKey{ka, kb}
					r.coOcc[p]++
					if _, ok := r.coOccCtx[p]; !ok {
						r.coOccCtx[p] = sentences[wi].Text
					}
				}
			}
		}
	}

	return r
}

// normalizeEntityName lowercases and trims for deduplication.
func normalizeEntityName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

// proseToEntityType maps prose NER labels to fishnet entity types.
func proseToEntityType(label string) string {
	switch label {
	case "PERSON":
		return "Person"
	case "ORG":
		return "Organization"
	case "GPE":
		return "Location"
	default:
		return "Concept"
	}
}

// inferRelationType infers a relationship type from surrounding sentence context.
func inferRelationType(sentence string) string {
	s := strings.ToLower(sentence)
	switch {
	case containsAny(s, "work", "employ", "hire", "staff"):
		return "works_for"
	case containsAny(s, "found", "creat", "start", "establish"):
		return "founded"
	case containsAny(s, "oppos", "against", "critic", "attack"):
		return "opposes"
	case containsAny(s, "support", "back", "endors", "approv"):
		return "supports"
	case containsAny(s, "part of", "member", "belong", "affili"):
		return "part_of"
	case containsAny(s, "compet", "rival", "vs", "versus"):
		return "competes_with"
	case containsAny(s, "acquir", "buy", "purchas", "merge"):
		return "acquired"
	default:
		return "related_to"
	}
}

func containsAny(s string, substrings ...string) bool {
	for _, sub := range substrings {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
