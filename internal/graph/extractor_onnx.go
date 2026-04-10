package graph

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"

	"fishnet/internal/nlp"
)

// OnnxExtractor uses ONNX multilingual BERT NER for entity extraction.
//
// Compared to LocalExtractor (English-only prose NER):
//   - Native Chinese and English support — no pre-translation step required
//   - ~20–50 ms/chunk vs 3–8 s for LLM calls (~60–160× faster)
//   - Single shared engine instance; thread-safe via internal mutex
type OnnxExtractor struct {
	engine      *nlp.NEREngine
	coOccWindow int // sentence window for co-occurrence (default 2)
	minCoOcc    int // min co-occurrences to create an edge (default 1)
}

// NewOnnxExtractor creates an OnnxExtractor backed by the ONNX NER engine.
// Returns an error if the model files are absent; call nlp.EnsureModels first.
func NewOnnxExtractor() (*OnnxExtractor, error) {
	engine, err := nlp.NewNEREngine()
	if err != nil {
		return nil, fmt.Errorf("onnx extractor: failed to load NER engine: %w", err)
	}
	return &OnnxExtractor{
		engine:      engine,
		coOccWindow: 2,
		minCoOcc:    1,
	}, nil
}

// Close releases the underlying ONNX runtime resources.
func (e *OnnxExtractor) Close() {
	if e.engine != nil {
		_ = e.engine.Close()
	}
}

// labelToType maps ONNX NER span labels to fishnet entity type strings.
var labelToType = map[string]string{
	"PER":  "Person",
	"ORG":  "Organization",
	"LOC":  "Location",
	"MISC": "Concept",
}

// onnxSpanType returns the fishnet entity type for a given NER label.
// Unknown labels fall back to "Concept".
func onnxSpanType(label string) string {
	if t, ok := labelToType[label]; ok {
		return t
	}
	return "Concept"
}

// onnxChunkResult extends chunkResult with ONNX-specific per-pair relation
// types inferred by nlp.InferRelation at extraction time.
type onnxChunkResult struct {
	chunkResult
	coOccRel map[pairKey]string // inferred relation type per entity pair
}

// Extract performs parallel NER + co-occurrence relation extraction across all
// texts and returns an *extractionResult compatible with builder.go's
// storeExtraction — identical format to LocalExtractor.Extract.
func (e *OnnxExtractor) Extract(texts []string) *extractionResult {
	if len(texts) == 0 {
		return &extractionResult{}
	}

	totalTexts := len(texts)
	results := make([]*onnxChunkResult, totalTexts)

	// Bound parallelism to available CPUs.
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

	// ── Merge per-text results ────────────────────────────────────────────────
	entityFreq := make(map[string]int)
	entityMetaMap := make(map[string]entityMetaEntry)
	coOcc := make(map[pairKey]int)
	coOccContext := make(map[pairKey]string)
	coOccRel := make(map[pairKey]string)

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
		for p, rel := range r.coOccRel {
			if _, ok := coOccRel[p]; !ok {
				coOccRel[p] = rel
			}
		}
	}

	// ── TF-IDF weighting ─────────────────────────────────────────────────────
	// weight = log(1 + freq) / log(totalTexts + 1)
	idfDenom := math.Log(float64(totalTexts) + 1)

	result := &extractionResult{}

	// ── Entities ─────────────────────────────────────────────────────────────
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

	// ── Relationships ─────────────────────────────────────────────────────────
	for p, count := range coOcc {
		if count < e.minCoOcc {
			continue
		}
		srcMeta := entityMetaMap[p.a]
		tgtMeta := entityMetaMap[p.b]
		ctx := coOccContext[p]
		// Use nlp.InferRelation result if available, fall back to keyword heuristic.
		relType, ok := coOccRel[p]
		if !ok || relType == "" {
			relType = inferRelationType(ctx)
		}
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

// processText runs ONNX NER on a single text and builds the per-chunk result.
// On engine error it logs gracefully and returns an empty (non-nil) result so
// the caller can continue processing the remaining texts.
func (e *OnnxExtractor) processText(text string) *onnxChunkResult {
	r := &onnxChunkResult{
		chunkResult: chunkResult{
			entityFreq: make(map[string]int),
			entityMeta: make(map[string]entityMetaEntry),
			coOcc:      make(map[pairKey]int),
			coOccCtx:   make(map[pairKey]string),
		},
		coOccRel: make(map[pairKey]string),
	}

	spans, err := e.engine.Extract(text)
	if err != nil {
		// Graceful fallback: skip this chunk, do not propagate the error.
		return r
	}

	lang := nlp.DetectLanguage(text)
	sentences := splitSentences(text)

	// ── Per-sentence entity collection ──────────────────────────────────────
	// sentEnts[i] holds the normalised entity keys found in sentence i.
	sentEnts := make([][]string, len(sentences))

	for si, sent := range sentences {
		sentEnd := sent.start + len(sent.text)

		for _, sp := range spans {
			// A span belongs to this sentence if its start offset falls within it.
			if sp.Start < sent.start || sp.Start >= sentEnd {
				continue
			}
			norm := normalizeEntityName(sp.Text) // reuse helper from extractor_local.go
			if norm == "" || len([]rune(norm)) < 2 {
				continue
			}
			sentEnts[si] = append(sentEnts[si], norm)
			r.entityFreq[norm]++
			if _, exists := r.entityMeta[norm]; !exists {
				r.entityMeta[norm] = entityMetaEntry{
					display: sp.Text,
					label:   onnxSpanType(sp.Label),
				}
			}
		}

		// ── Intra-sentence co-occurrence ─────────────────────────────────────
		ents := sentEnts[si]
		sentRel := nlp.InferRelation(sent.text, lang)
		for ai, a := range ents {
			for _, b := range ents[ai+1:] {
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
					r.coOccCtx[p] = sent.text
					r.coOccRel[p] = sentRel
				}
			}
		}

		// ── Cross-sentence window co-occurrence ───────────────────────────────
		for wi := si + 1; wi <= si+e.coOccWindow && wi < len(sentences); wi++ {
			winEnts := sentEnts[wi]
			winRel := nlp.InferRelation(sentences[wi].text, lang)

			for _, a := range ents {
				for _, b := range winEnts {
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
						r.coOccCtx[p] = sentences[wi].text
						r.coOccRel[p] = winRel
					}
				}
			}
		}
	}

	return r
}

// ── Sentence splitter ─────────────────────────────────────────────────────────

// sentence holds the text of a sentence and its byte offset within the
// original text.
type sentence struct {
	text  string
	start int
}

// splitSentences splits text into sentences on ASCII and CJK sentence-ending
// punctuation (. ! ? 。！？) and newlines.
// Each returned sentence carries the trimmed text and the byte offset of its
// first character in the original text, so that nlp.Span byte offsets can be
// compared directly against sentence.start and sentence.start+len(sentence.text).
func splitSentences(text string) []sentence {
	var sentences []sentence

	segStart := 0 // byte offset where the current segment begins (after prior delimiter)

	flush := func(segEnd int) {
		raw := text[segStart:segEnd]
		// Compute byte offset of the first non-space character.
		trimmed := strings.TrimLeft(raw, " \t\r\n")
		if trimmed == "" {
			return
		}
		offset := segStart + (len(raw) - len(trimmed))
		// Also trim trailing whitespace for the stored text.
		trimmed = strings.TrimRight(trimmed, " \t\r\n")
		sentences = append(sentences, sentence{text: trimmed, start: offset})
	}

	i := 0
	for i < len(text) {
		b := text[i]

		// Detect sentence-ending bytes.
		isSentEnd := false
		advance := 1

		switch b {
		case '.', '!', '?', '\n':
			isSentEnd = true
		default:
			// Check for multi-byte CJK sentence-ending punctuation.
			// 。= E3 80 82, ！= EF BC 81, ？= EF BC 9F
			if i+2 < len(text) {
				three := text[i : i+3]
				if three == "\xe3\x80\x82" || // 。
					three == "\xef\xbc\x81" || // ！
					three == "\xef\xbc\x9f" { // ？
					isSentEnd = true
					advance = 3
				}
			}
		}

		if isSentEnd {
			flush(i + advance)
			i += advance
			segStart = i
			continue
		}

		i++
	}

	// Flush any remaining text that was not terminated by a delimiter.
	if segStart < len(text) {
		flush(len(text))
	}

	return sentences
}
