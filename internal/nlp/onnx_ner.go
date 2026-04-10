package nlp

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// nerLabels maps label index → label string for bert-base-multilingual-cased-ner-hrl.
var nerLabels = []string{
	"O",      // 0
	"B-PER",  // 1
	"I-PER",  // 2
	"B-ORG",  // 3
	"I-ORG",  // 4
	"B-LOC",  // 5
	"I-LOC",  // 6
	"B-MISC", // 7
	"I-MISC", // 8
}

// Span represents an extracted named entity.
type Span struct {
	Text  string // original text of the entity (from input)
	Label string // "PER", "ORG", "LOC", "MISC"
	Start int    // byte start offset in input text
	End   int    // byte end offset in input text
}

// NEREngine is a thread-safe ONNX NER model.
type NEREngine struct {
	session   *ort.DynamicAdvancedSession
	tokenizer *BertTokenizer
	mu        sync.Mutex // protects session calls
	labels    []string   // label index → label string
}

// NewNEREngine creates a NEREngine:
//  1. Calls EnsureModels(false) to verify files are present.
//  2. Sets ONNX Runtime shared library path via ort.SetSharedLibraryPath().
//  3. Calls ort.InitializeEnvironment().
//  4. Loads the model from ModelDir()/model_quantized.onnx.
//  5. Loads the tokenizer from ModelDir()/vocab.txt.
func NewNEREngine() (*NEREngine, error) {
	if err := EnsureModels(false); err != nil {
		return nil, fmt.Errorf(
			"NER model files not ready (run 'fishnet model download' first): %w", err,
		)
	}

	libPath, err := LibraryPath()
	if err != nil {
		return nil, fmt.Errorf("resolve ONNX Runtime library path: %w", err)
	}

	ort.SetSharedLibraryPath(libPath)

	// InitializeEnvironment is idempotent; if it returns "already initialized"
	// we treat that as a non-fatal condition.
	if err := ort.InitializeEnvironment(); err != nil {
		if !strings.Contains(err.Error(), "already been initialized") {
			return nil, fmt.Errorf("initialize ONNX Runtime environment: %w", err)
		}
	}

	dir, err := ModelDir()
	if err != nil {
		return nil, fmt.Errorf("resolve model directory: %w", err)
	}

	modelPath := filepath.Join(dir, "model_quantized.onnx")
	vocabPath := filepath.Join(dir, "vocab.txt")

	inputNames := []string{"input_ids", "attention_mask", "token_type_ids"}
	outputNames := []string{"logits"}

	session, err := ort.NewDynamicAdvancedSession(modelPath, inputNames, outputNames, nil)
	if err != nil {
		return nil, fmt.Errorf("create ONNX session from %s: %w", modelPath, err)
	}

	tokenizer, err := LoadTokenizer(vocabPath)
	if err != nil {
		_ = session.Destroy()
		return nil, fmt.Errorf("load tokenizer from %s: %w", vocabPath, err)
	}

	return &NEREngine{
		session:   session,
		tokenizer: tokenizer,
		labels:    nerLabels,
	}, nil
}

// Extract runs NER on text and returns entity spans.
// Steps:
//  1. Tokenize text.
//  2. Run ONNX inference (inputs: input_ids, attention_mask, token_type_ids).
//  3. Decode BIO tags from logits.
//  4. Map token spans back to character offsets.
//  5. Return []Span.
func (n *NEREngine) Extract(text string) ([]Span, error) {
	inputIDsData, attMaskData, ttIDsData, tokens, offsets :=
		n.tokenizer.TokenizeForNER(text)

	seqLen := int64(len(inputIDsData))
	numLabels := int64(len(n.labels))

	// Build input tensors (int64).
	inputIDsTensor, err := ort.NewTensor(ort.NewShape(1, seqLen), inputIDsData)
	if err != nil {
		return nil, fmt.Errorf("create input_ids tensor: %w", err)
	}
	defer inputIDsTensor.Destroy()

	attMaskTensor, err := ort.NewTensor(ort.NewShape(1, seqLen), attMaskData)
	if err != nil {
		return nil, fmt.Errorf("create attention_mask tensor: %w", err)
	}
	defer attMaskTensor.Destroy()

	ttIDsTensor, err := ort.NewTensor(ort.NewShape(1, seqLen), ttIDsData)
	if err != nil {
		return nil, fmt.Errorf("create token_type_ids tensor: %w", err)
	}
	defer ttIDsTensor.Destroy()

	// Output tensor: shape [1, seqLen, numLabels] (float32).
	outputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, seqLen, numLabels))
	if err != nil {
		return nil, fmt.Errorf("create output logits tensor: %w", err)
	}
	defer outputTensor.Destroy()

	// Run inference — session is not goroutine-safe, protect with mutex.
	n.mu.Lock()
	err = n.session.Run(
		[]ort.Value{inputIDsTensor, attMaskTensor, ttIDsTensor},
		[]ort.Value{outputTensor},
	)
	n.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("ONNX session run: %w", err)
	}

	// Reshape flat []float32 → [seqLen][numLabels].
	flat := outputTensor.GetData()
	logits := make([][]float32, seqLen)
	for i := int64(0); i < seqLen; i++ {
		logits[i] = flat[i*numLabels : (i+1)*numLabels]
	}

	return decodeBIO(logits, tokens, offsets, text, n.labels), nil
}

// Close releases ONNX session resources.
func (n *NEREngine) Close() error {
	if n.session != nil {
		if err := n.session.Destroy(); err != nil {
			return fmt.Errorf("destroy ONNX session: %w", err)
		}
		n.session = nil
	}
	return nil
}

// ─── BIO decoding ────────────────────────────────────────────────────────────

// decodeBIO converts per-token logits to entity spans.
//
//   - logits:  shape [seq_len][num_labels]
//   - tokens:  wordpiece tokens (length seq_len, padded to maxLen)
//   - offsets: byte start offsets of each token in original text
//     (-1 for [CLS], [SEP], and padding tokens)
//   - text:    original input text
//   - labels:  label-index → label string (e.g. "B-PER", "I-ORG", "O")
func decodeBIO(logits [][]float32, tokens []string, offsets []int, text string, labels []string) []Span {
	var spans []Span

	inEntity := false
	var curLabel string // e.g. "PER"
	var curStart int    // byte start of entity in text
	var curEnd int      // byte end (exclusive) of entity in text

	flushEntity := func() {
		if inEntity && curEnd > curStart {
			spans = append(spans, Span{
				Text:  text[curStart:curEnd],
				Label: curLabel,
				Start: curStart,
				End:   curEnd,
			})
		}
		inEntity = false
		curLabel = ""
	}

	for i, row := range logits {
		// Skip special / padding tokens (offset == -1).
		if i >= len(offsets) || offsets[i] == -1 {
			continue
		}

		// argmax over label dimension.
		best := 0
		for j := 1; j < len(row); j++ {
			if row[j] > row[best] {
				best = j
			}
		}
		if best >= len(labels) {
			flushEntity()
			continue
		}
		labelStr := labels[best]

		switch {
		case labelStr == "O":
			flushEntity()

		case strings.HasPrefix(labelStr, "B-"):
			// Start of a new entity — close any open entity first.
			flushEntity()
			entityType := labelStr[2:] // strip "B-"
			tokenStart := offsets[i]
			tokenEnd := tokenEndOffset(tokens[i], tokenStart, text)
			inEntity = true
			curLabel = entityType
			curStart = tokenStart
			curEnd = tokenEnd

		case strings.HasPrefix(labelStr, "I-"):
			entityType := labelStr[2:] // strip "I-"
			if inEntity && curLabel == entityType {
				// Extend the current entity span to include this token.
				tokenEnd := tokenEndOffset(tokens[i], offsets[i], text)
				// Also include any whitespace between previous token end and
				// this token start so the extracted text is contiguous.
				if offsets[i] < curEnd {
					// Overlapping / adjacent subword — just extend the end.
					if tokenEnd > curEnd {
						curEnd = tokenEnd
					}
				} else {
					// There may be whitespace between the tokens; use the
					// current token's end as the new entity end so that
					// text[curStart:curEnd] encompasses the whole phrase.
					curEnd = tokenEnd
				}
			} else {
				// I- tag without a matching B- (or label mismatch) —
				// treat it as the start of a new entity.
				flushEntity()
				tokenStart := offsets[i]
				tokenEnd := tokenEndOffset(tokens[i], tokenStart, text)
				inEntity = true
				curLabel = entityType
				curStart = tokenStart
				curEnd = tokenEnd
			}
		}
	}
	flushEntity()

	return spans
}

// tokenEndOffset estimates the exclusive byte-end offset of a token in the
// original text.
//
// For a WordPiece subword "##ing" the actual characters are "ing" (3 bytes).
// For a whole-word token "play" the actual characters are "play" (4 bytes).
// We advance tokenStart by the byte length of the surface characters.
func tokenEndOffset(token string, tokenStart int, text string) int {
	// Strip "##" prefix to get the actual surface characters.
	chars := token
	if strings.HasPrefix(chars, "##") {
		chars = chars[2:]
	}
	end := tokenStart + len(chars)
	if end > len(text) {
		end = len(text)
	}
	return end
}
