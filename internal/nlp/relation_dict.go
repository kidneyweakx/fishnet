package nlp

import (
	"strings"
)

// ─── Pattern tables ──────────────────────────────────────────────────────────

// enPatterns maps fishnet relation types to English prefix/substring patterns.
// Matching is done case-insensitively against the lowercased sentence.
var enPatterns = []struct {
	rel      string
	patterns []string
}{
	{"works_for", []string{
		"work", "employ", "hire", "staff", "serve",
		"report to", "head of", "ceo of", "cto of", "director of",
	}},
	{"founded", []string{
		"found", "co-found", "start", "establish", "creat", "launch", "build",
	}},
	{"opposes", []string{
		"oppos", "against", "critic", "attack", "fight",
		"protest", "reject", "condemn",
	}},
	{"supports", []string{
		"support", "back", "endors", "approv", "fund",
		"invest in", "partner with",
	}},
	{"part_of", []string{
		"part of", "member", "belong", "affili",
		"subsidiary", "division", "branch",
	}},
	{"competes_with", []string{
		"compet", "rival", "vs", "versus",
		"challenge", "compete against",
	}},
	{"acquired", []string{
		"acquir", "buy", "purchas", "merge", "take over", "absorb",
	}},
}

// zhPatterns maps fishnet relation types to Chinese keyword patterns.
// Matching uses exact substring search (strings.Contains).
var zhPatterns = []struct {
	rel      string
	patterns []string
}{
	{"works_for", []string{
		"任職", "加入", "擔任", "就職", "受雇", "服務於", "任職於", "入職",
	}},
	{"founded", []string{
		"創辦", "創立", "成立", "創建", "建立", "創設",
	}},
	{"opposes", []string{
		"反對", "批評", "抨擊", "譴責", "攻擊", "抵制", "反抗",
	}},
	{"supports", []string{
		"支持", "支援", "背書", "贊助", "資助", "投資", "合作",
	}},
	{"part_of", []string{
		"隸屬", "附屬", "旗下", "子公司", "部門", "成員",
	}},
	{"competes_with", []string{
		"競爭", "競對", "對抗", "挑戰", "對壘",
	}},
	{"acquired", []string{
		"收購", "併購", "買下", "合并", "整合",
	}},
}

// ─── Public API ──────────────────────────────────────────────────────────────

// InferRelation returns the most likely fishnet relation type for the given
// sentence. lang should be one of "zh", "en", "mixed", or "other".
//
// Strategy:
//   - "zh" or "mixed": check Chinese patterns first, then English patterns.
//   - "en" or anything else: check English patterns only.
//   - No match: returns "related_to".
func InferRelation(sentence, lang string) string {
	lower := strings.ToLower(sentence)

	if lang == "zh" || lang == "mixed" {
		if rel := matchZH(sentence); rel != "" {
			return rel
		}
		if rel := matchEN(lower); rel != "" {
			return rel
		}
		return "related_to"
	}

	// "en", "other", or any unknown lang — English-only.
	if rel := matchEN(lower); rel != "" {
		return rel
	}
	return "related_to"
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// matchZH checks zhPatterns against the original (case-preserved) sentence.
func matchZH(sentence string) string {
	for _, entry := range zhPatterns {
		for _, p := range entry.patterns {
			if strings.Contains(sentence, p) {
				return entry.rel
			}
		}
	}
	return ""
}

// matchEN checks enPatterns against the already-lowercased sentence using
// containsAnyPrefix (prefix/substring match).
func matchEN(lower string) string {
	for _, entry := range enPatterns {
		if containsAnyPrefix(lower, entry.patterns...) {
			return entry.rel
		}
	}
	return ""
}

// containsAnyPrefix returns true when s contains at least one of the given
// substrings (each treated as a literal substring / prefix fragment).
func containsAnyPrefix(s string, patterns ...string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
