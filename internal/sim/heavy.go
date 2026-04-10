package sim

import (
	"context"
	"fmt"
	"strings"

	"fishnet/internal/platform"
)

// ─── Heavy Mode Decision ──────────────────────────────────────────────────────
//
// In heavy mode each agent makes one LLM call per round that simultaneously
// decides WHAT to do AND generates the content.  This produces far more
// authentic, stance-aware behaviour than the math-only decide + batch-content
// approach — at the cost of N*rounds LLM calls instead of ~1*rounds.

type heavyActionRaw struct {
	Type    string `json:"type"`     // LIKE_POST | REPOST | CREATE_POST | QUOTE_POST | CREATE_COMMENT | DO_NOTHING
	PostID  string `json:"post_id"`  // target post (for reactions)
	Content string `json:"content"`  // generated text (CREATE_POST / QUOTE_POST / CREATE_COMMENT)
	Reason  string `json:"reason"`   // LLM reasoning — surfaced in progress logs
}

type heavyDecisionRaw struct {
	Actions []heavyActionRaw `json:"actions"`
}

// HeavyDecideResult is one resolved agent decision for heavy mode.
type HeavyDecideResult struct {
	Action  platform.PlannedAction
	Content string // pre-filled for content-generating actions
	Reason  string
}

// heavyDecide asks the LLM to decide what the agent does this round and
// returns fully resolved actions with content already embedded.
// The returned slice has at most 3 items; DO_NOTHING is returned on LLM error.
func (ps *PlatformSim) heavyDecide(
	ctx context.Context,
	p *platform.Personality,
	timeline []*platform.Post,
	scenario string,
	round int,
	platName string,
) []HeavyDecideResult {
	isReddit := platName == "reddit"

	// ── Build tiered timeline description ────────────────────────────────────
	// Top posts get full text; lower-ranked posts are progressively compressed.
	// Position is used as a proxy for relevance (RankedFeed returns highest first).
	var tl strings.Builder
	for i, post := range timeline {
		if i >= 8 {
			break
		}
		engagement := fmt.Sprintf("(%d❤ %d🔁 %d💬)", post.Likes, post.Reposts, post.Comments)

		var entry string
		switch {
		case i < 3:
			// Tier 1: full content (top 3 most relevant)
			entry = fmt.Sprintf("%d. [%s] %s  %s  post_id:%q\n",
				i+1, post.AuthorName, post.Content, engagement, post.ID)
		case i < 6:
			// Tier 2: truncated content (middle 3)
			entry = fmt.Sprintf("%d. [%s] %s  %s  post_id:%q\n",
				i+1, post.AuthorName, clip(post.Content, 60), engagement, post.ID)
		default:
			// Tier 3: topic summary only (bottom 2)
			topic := "general"
			if len(post.Tags) > 0 {
				topic = post.Tags[0]
			}
			entry = fmt.Sprintf("%d. [%s] topic:%s  %s  post_id:%q\n",
				i+1, post.AuthorName, topic, engagement, post.ID)
		}
		tl.WriteString(entry)
	}

	// ── Choose platform-appropriate action vocabulary ─────────────────────────
	var actionVocab string
	if isReddit {
		actionVocab = "LIKE_POST, REPOST, CREATE_POST, CREATE_COMMENT, DO_NOTHING"
	} else {
		actionVocab = "LIKE_POST, REPOST, CREATE_POST, QUOTE_POST, DO_NOTHING"
	}

	// Use compressed fingerprint if available, otherwise fall back to full persona
	var personaBlock string
	if p.Fingerprint != "" {
		personaBlock = p.Fingerprint
	} else {
		bio := p.Bio
		if len([]rune(bio)) > 200 {
			bio = string([]rune(bio)[:200]) + "…"
		}
		personaBlock = fmt.Sprintf("%s | %s | stance:%s | style:%s | bias:%.1f\nBio: %s\nInterests: %s",
			p.Name, p.NodeType, p.Stance, p.PostStyle, p.SentimentBias,
			bio, strings.Join(p.Interests, ", "))
	}
	catchphrases := ""
	if len(p.Catchphrases) > 0 {
		catchphrases = "\nCatchphrases you use: " + strings.Join(p.Catchphrases[:min3(len(p.Catchphrases), 3)], " | ")
	}

	system := fmt.Sprintf(
		`You are this agent: %s%s

Decide your social media actions for round %d on %s. Be authentic to your persona.

Stance guidance:
- "supportive": LIKE_POST or REPOST content you agree with; CREATE_POST to amplify the narrative.
- "opposing": QUOTE_POST or CREATE_POST to rebut; express disagreement clearly.
- "observer": minimal engagement; occasional thoughtful CREATE_POST with nuanced take.
- "neutral": balanced — like diverse content, post occasionally.

Rules:
- Max 3 actions total.
- For LIKE_POST and REPOST: provide post_id from the timeline; content must be empty.
- For CREATE_POST, QUOTE_POST, CREATE_COMMENT: write realistic "content" in your voice.
  Twitter: under 280 chars. Reddit: 2-4 sentences, analytical or community-style.
- QUOTE_POST (Twitter only): include a brief comment + quote the post_id.
- CREATE_COMMENT (Reddit only): reply to a specific post_id.
- If nothing on the timeline interests you, return DO_NOTHING.

Available action types: %s

Return ONLY valid JSON:
{"actions":[{"type":"...","post_id":"...","content":"...","reason":"one-line reasoning"}]}`,
		personaBlock, catchphrases,
		round, platName,
		actionVocab,
	)

	timelineStr := tl.String()
	if timelineStr == "" {
		timelineStr = "(no posts yet — start the conversation)"
	}
	user := fmt.Sprintf("Scenario: %s\n\nTimeline:\n%s", truncScenario(scenario), timelineStr)

	var raw heavyDecisionRaw
	if err := ps.llm.JSON(ctx, system, user, &raw); err != nil {
		return []HeavyDecideResult{{
			Action:  platform.PlannedAction{Type: platform.ActDoNothing},
			Reason:  "llm error: " + err.Error(),
		}}
	}

	// ── Validate and convert ──────────────────────────────────────────────────
	results := make([]HeavyDecideResult, 0, 3)
	for _, ha := range raw.Actions {
		if len(results) >= 3 {
			break
		}
		switch ha.Type {
		case platform.ActLikePost, platform.ActDislikePost:
			if ha.PostID == "" {
				continue
			}
			results = append(results, HeavyDecideResult{
				Action:  platform.PlannedAction{Type: ha.Type, PostID: ha.PostID},
				Reason:  ha.Reason,
			})

		case platform.ActRepost:
			if ha.PostID == "" {
				continue
			}
			results = append(results, HeavyDecideResult{
				Action:  platform.PlannedAction{Type: ha.Type, PostID: ha.PostID},
				Reason:  ha.Reason,
			})

		case platform.ActQuotePost:
			if isReddit || ha.PostID == "" || ha.Content == "" {
				continue
			}
			results = append(results, HeavyDecideResult{
				Action:  platform.PlannedAction{Type: ha.Type, PostID: ha.PostID, NeedLLM: false},
				Content: ha.Content,
				Reason:  ha.Reason,
			})

		case platform.ActCreateComment:
			if !isReddit || ha.Content == "" {
				continue
			}
			results = append(results, HeavyDecideResult{
				Action:  platform.PlannedAction{Type: ha.Type, PostID: ha.PostID, NeedLLM: false},
				Content: ha.Content,
				Reason:  ha.Reason,
			})

		case platform.ActCreatePost:
			if ha.Content == "" {
				continue
			}
			results = append(results, HeavyDecideResult{
				Action:  platform.PlannedAction{Type: ha.Type, NeedLLM: false},
				Content: ha.Content,
				Reason:  ha.Reason,
			})

		case platform.ActDoNothing:
			results = append(results, HeavyDecideResult{
				Action: platform.PlannedAction{Type: platform.ActDoNothing},
				Reason: ha.Reason,
			})

		default:
			// Unknown action type — skip
		}
	}

	if len(results) == 0 {
		return []HeavyDecideResult{{Action: platform.PlannedAction{Type: platform.ActDoNothing}}}
	}
	return results
}

func min3(a, b int) int {
	if a < b {
		return a
	}
	return b
}
