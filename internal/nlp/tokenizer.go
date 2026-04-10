package nlp

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// BertTokenizer holds the vocabulary and provides tokenization.
type BertTokenizer struct {
	vocab    map[string]int // word → token id
	invVocab map[int]string // token id → word (for debugging)
	unkID    int            // [UNK] = 100
	clsID    int            // [CLS] = 101
	sepID    int            // [SEP] = 102
	padID    int            // [PAD] = 0
	maxLen   int            // max sequence length (512 for BERT)
}

// LoadTokenizer loads vocab.txt from path and returns a BertTokenizer.
// vocab.txt has one token per line; line index = token id.
func LoadTokenizer(vocabPath string) (*BertTokenizer, error) {
	f, err := os.Open(vocabPath)
	if err != nil {
		return nil, fmt.Errorf("open vocab file %s: %w", vocabPath, err)
	}
	defer f.Close()

	vocab := make(map[string]int)
	invVocab := make(map[int]string)

	scanner := bufio.NewScanner(f)
	id := 0
	for scanner.Scan() {
		tok := scanner.Text()
		vocab[tok] = id
		invVocab[id] = tok
		id++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read vocab file %s: %w", vocabPath, err)
	}
	if id == 0 {
		return nil, fmt.Errorf("vocab file %s is empty", vocabPath)
	}

	// Resolve special-token IDs; fall back to standard BERT defaults.
	unkID := 100
	clsID := 101
	sepID := 102
	padID := 0

	if v, ok := vocab["[UNK]"]; ok {
		unkID = v
	}
	if v, ok := vocab["[CLS]"]; ok {
		clsID = v
	}
	if v, ok := vocab["[SEP]"]; ok {
		sepID = v
	}
	if v, ok := vocab["[PAD]"]; ok {
		padID = v
	}

	return &BertTokenizer{
		vocab:    vocab,
		invVocab: invVocab,
		unkID:    unkID,
		clsID:    clsID,
		sepID:    sepID,
		padID:    padID,
		maxLen:   512,
	}, nil
}

// isCJK reports whether r is in one of the CJK Unicode blocks that BERT
// treats as individual characters.
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0x2F800 && r <= 0x2FA1F)
}

// basicTokenize pre-processes text:
//  1. Surrounds CJK characters with spaces.
//  2. Surrounds ASCII/Unicode punctuation with spaces.
//  3. Collapses runs of whitespace to a single space.
//  4. Splits on spaces to return a list of word strings, each paired with
//     the byte offset of its first character in the original text.
func basicTokenize(text string) (words []string, wordOffsets []int) {
	// Phase 1: build an expanded rune buffer with spaces around CJK / punct.
	// We also track the original byte offset for each rune position.

	type runeOffset struct {
		r      rune
		offset int // byte offset in original text
		isSep  bool
	}

	expanded := make([]runeOffset, 0, len(text)*2)
	byteIdx := 0
	for _, r := range text {
		rLen := len(string(r))
		switch {
		case isCJK(r):
			expanded = append(expanded,
				runeOffset{' ', -1, true},
				runeOffset{r, byteIdx, false},
				runeOffset{' ', -1, true},
			)
		case unicode.IsPunct(r):
			expanded = append(expanded,
				runeOffset{' ', -1, true},
				runeOffset{r, byteIdx, false},
				runeOffset{' ', -1, true},
			)
		default:
			expanded = append(expanded, runeOffset{r, byteIdx, false})
		}
		byteIdx += rLen
	}

	// Phase 2: split on whitespace runs, collecting word strings and offsets.
	inWord := false
	var wordBuf strings.Builder
	var wordStart int

	flush := func() {
		if wordBuf.Len() > 0 {
			words = append(words, wordBuf.String())
			wordOffsets = append(wordOffsets, wordStart)
			wordBuf.Reset()
		}
		inWord = false
	}

	for _, ro := range expanded {
		if unicode.IsSpace(ro.r) || ro.r == ' ' {
			if inWord {
				flush()
			}
			continue
		}
		if !inWord {
			wordStart = ro.offset
			inWord = true
		}
		wordBuf.WriteRune(ro.r)
	}
	flush()

	return words, wordOffsets
}

// wordpieceTokenize splits a single word into WordPiece subwords.
// Returns the list of subword tokens and the byte offset of each subword
// relative to the word's start (wordOffset is the byte position of the word
// in the original text).
func (t *BertTokenizer) wordpieceTokenize(word string, wordOffset int) (subwords []string, subOffsets []int) {
	// Fast path: whole word is in vocab.
	if _, ok := t.vocab[word]; ok {
		return []string{word}, []int{wordOffset}
	}

	// Convert word to rune slice so we can iterate by character.
	runes := []rune(word)
	if len(runes) == 0 {
		return nil, nil
	}

	// Accumulate byte offsets per rune position within the word.
	runeByteStart := make([]int, len(runes)+1) // runeByteStart[i] = byte offset of runes[i] relative to wordOffset
	bytePos := 0
	for i, r := range runes {
		runeByteStart[i] = bytePos
		bytePos += len(string(r))
	}
	runeByteStart[len(runes)] = bytePos

	start := 0
	first := true
	for start < len(runes) {
		end := len(runes)
		found := false
		for end > start {
			substr := string(runes[start:end])
			candidate := substr
			if !first {
				candidate = "##" + substr
			}
			if _, ok := t.vocab[candidate]; ok {
				subwords = append(subwords, candidate)
				subOffsets = append(subOffsets, wordOffset+runeByteStart[start])
				start = end
				first = false
				found = true
				break
			}
			end--
		}
		if !found {
			// Cannot tokenize — entire word becomes [UNK].
			return []string{"[UNK]"}, []int{wordOffset}
		}
	}
	return subwords, subOffsets
}

// TokenizeForNER tokenizes text and returns:
//   - inputIDs      []int64  (length maxLen, padded with padID)
//   - attentionMask []int64  (1 for real tokens, 0 for padding)
//   - tokenTypeIDs  []int64  (all zeros for single sequence)
//   - tokens        []string (the actual wordpiece tokens, for BIO alignment)
//   - offsets       []int    (byte start offset of each token in original text;
//     -1 for [CLS] and [SEP])
//
// Format: [CLS] t1 t2 ... tN [SEP] [PAD] [PAD] ...
func (t *BertTokenizer) TokenizeForNER(text string) (
	inputIDs, attentionMask, tokenTypeIDs []int64,
	tokens []string,
	offsets []int,
) {
	words, wordOffsets := basicTokenize(text)

	// We have room for (maxLen - 2) content tokens ([CLS] and [SEP] occupy 2 slots).
	maxContent := t.maxLen - 2

	// Build the flat list of subword tokens + their byte offsets.
	var contentTokens []string
	var contentOffsets []int

	for wi, word := range words {
		subs, subOff := t.wordpieceTokenize(word, wordOffsets[wi])
		for si, sub := range subs {
			if len(contentTokens) >= maxContent {
				break
			}
			contentTokens = append(contentTokens, sub)
			contentOffsets = append(contentOffsets, subOff[si])
		}
		if len(contentTokens) >= maxContent {
			break
		}
	}

	// Assemble final token sequence: [CLS] + content + [SEP].
	tokens = make([]string, 0, t.maxLen)
	offsets = make([]int, 0, t.maxLen)

	tokens = append(tokens, "[CLS]")
	offsets = append(offsets, -1)

	tokens = append(tokens, contentTokens...)
	offsets = append(offsets, contentOffsets...)

	tokens = append(tokens, "[SEP]")
	offsets = append(offsets, -1)

	realLen := len(tokens) // includes [CLS] and [SEP]

	// Build int64 slices of length maxLen.
	inputIDs = make([]int64, t.maxLen)
	attentionMask = make([]int64, t.maxLen)
	tokenTypeIDs = make([]int64, t.maxLen)

	for i := 0; i < t.maxLen; i++ {
		if i < realLen {
			tok := tokens[i]
			id, ok := t.vocab[tok]
			if !ok {
				id = t.unkID
			}
			inputIDs[i] = int64(id)
			attentionMask[i] = 1
		} else {
			inputIDs[i] = int64(t.padID)
			attentionMask[i] = 0
		}
		tokenTypeIDs[i] = 0
	}

	// Pad the tokens/offsets slices to maxLen for consistent indexing.
	for len(tokens) < t.maxLen {
		tokens = append(tokens, "[PAD]")
		offsets = append(offsets, -1)
	}

	return inputIDs, attentionMask, tokenTypeIDs, tokens, offsets
}
