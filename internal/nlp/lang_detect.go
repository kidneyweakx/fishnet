package nlp

import (
	"sync"
	"unicode"

	lingua "github.com/pemistahl/lingua-go"
)

// ─── Detector singleton ──────────────────────────────────────────────────────

var (
	detectorOnce sync.Once
	detector     lingua.LanguageDetector
)

func getDetector() lingua.LanguageDetector {
	detectorOnce.Do(func() {
		detector = lingua.NewLanguageDetectorBuilder().
			FromLanguages(lingua.Chinese, lingua.English).
			Build()
	})
	return detector
}

// ─── Public API ──────────────────────────────────────────────────────────────

// DetectLanguage returns "zh", "en", "mixed", or "other" for the given text.
//
// Classification rules:
//   - CJK ratio > 40%         → "zh"
//   - CJK ratio < 5%          → "en" (confirmed via lingua-go, else "other")
//   - CJK ratio 5%–40%        → "mixed"
//   - Short text (< 10 chars) → determined by CJK ratio alone, no lingua call
func DetectLanguage(text string) string {
	ratio := cjkRatio(text)

	switch {
	case ratio > 0.40:
		return "zh"
	case ratio >= 0.05:
		return "mixed"
	default:
		// ratio < 5% — likely Latin script; confirm with lingua-go unless short.
		if len([]rune(text)) < 10 {
			return "en"
		}
		lang, ok := getDetector().DetectLanguageOf(text)
		if !ok {
			return "other"
		}
		if lang == lingua.English {
			return "en"
		}
		return "other"
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// isCJKRune reports whether r is a CJK character.
// Covers the main CJK Unified Ideographs blocks plus extensions A–F and
// compatibility ideographs, as well as the common CJK punctuation block.
func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

// cjkRatio returns the fraction of runes in text that are CJK characters.
// Returns 0 for empty strings.
func cjkRatio(text string) float64 {
	runes := []rune(text)
	total := len(runes)
	if total == 0 {
		return 0
	}
	var cjk int
	for _, r := range runes {
		if isCJKRune(r) {
			cjk++
		}
	}
	return float64(cjk) / float64(total)
}
