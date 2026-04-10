package platform

import (
	"math"
	"sort"
	"strings"
	"time"
)

// FeedWeights controls the 6-factor feed ranking formula.
//
//	Score = w1*Recency + w2*Relevance + w3*Virality + w4*Relationship + w5*CommunityAffinity
//
// Each weight should sum to 1.0, but the algorithm does not enforce this —
// the values serve as relative emphasis.
type FeedWeights struct {
	Recency           float64 // w1: time-decay weight
	Relevance         float64 // w2: topic/interest match weight (blends static + drift)
	Virality          float64 // w3: engagement (likes+reposts+comments) weight
	Relationship      float64 // w4: past-interaction count weight
	CommunityAffinity float64 // w5: same Louvain community affinity weight
	DiversityRate     float64 // fraction of feed slots reserved for out-of-community posts [0,1]
	HalfLifeMin       float64 // recency half-life in real minutes (default: 30)
}

// DefaultFeedWeights mirrors Twitter "For You" emphasis.
// Weights sum to 1.0: 0.25+0.22+0.22+0.16+0.15 = 1.00.
var DefaultFeedWeights = FeedWeights{
	Recency:           0.25,
	Relevance:         0.22,
	Virality:          0.22,
	Relationship:      0.16,
	CommunityAffinity: 0.15,
	DiversityRate:     0.125,
	HalfLifeMin:       30.0,
}

// RankedFeed returns up to limit posts scored by the 5-factor formula,
// excluding posts authored by the agent itself.
//
// influenceByID maps authorID → InfluenceWeight [0-2.0] and is used as a
// soft score multiplier so high-influence authors' content surfaces higher.
// round is the current simulation round (1-based), used to scale interest drift.
// communityByAuthorID maps authorID → CommunityID (from Louvain detection);
// pass nil to disable the CommunityAffinity factor.
func RankedFeed(
	state *State,
	p *Personality,
	limit int,
	influenceByID map[string]float64,
	weights FeedWeights,
	round int,
	communityByAuthorID map[string]int,
) []*Post {
	if weights.HalfLifeMin <= 0 {
		weights.HalfLifeMin = 30.0
	}

	// ── Snapshot under one read-lock ─────────────────────────────────────────
	state.mu.RLock()
	// Snapshot seen posts for this agent
	var seenSnapshot map[string]bool
	if m, ok := state.seenPosts[p.AgentID]; ok && len(m) > 0 {
		seenSnapshot = m // read-only snapshot; safe under RLock
	}
	pool := make([]*Post, 0, len(state.Posts))
	maxEng := 0
	for _, post := range state.Posts {
		if post.AuthorID == p.AgentID {
			continue
		}
		if seenSnapshot[post.ID] {
			continue // skip already-seen posts
		}
		pool = append(pool, post)
		if e := post.Likes + post.Reposts + post.Comments; e > maxEng {
			maxEng = e
		}
	}
	// Snapshot this viewer's interaction counts for scoring (avoids repeated lock).
	var icSnapshot map[string]int
	var maxIC int
	if state.interactions != nil {
		if m, ok := state.interactions[p.AgentID]; ok {
			icSnapshot = make(map[string]int, len(m))
			for k, v := range m {
				icSnapshot[k] = v
				if v > maxIC {
					maxIC = v
				}
			}
		}
	}
	state.mu.RUnlock()

	// snapshot interest drift
	driftSnapshot := state.GetInterestDrift(p.AgentID)

	if len(pool) == 0 {
		return nil
	}

	now := time.Now()
	type scored struct {
		post  *Post
		score float64
	}
	ranked := make([]scored, len(pool))
	for i, post := range pool {
		ranked[i] = scored{
			post:  post,
			score: scorePost(post, p, now, weights, maxEng, maxIC, icSnapshot, influenceByID, driftSnapshot, round, communityByAuthorID),
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	out := make([]*Post, 0, limit)
	for _, s := range ranked {
		if len(out) >= limit {
			break
		}
		out = append(out, s.post)
	}

	// ── Diversity injection ───────────────────────────────────────────────────
	// Replace the last N slots with highest-scoring out-of-community posts,
	// so filter bubbles don't fully close. Only applies when CommunityAffinity
	// is active (communityByAuthorID != nil) and DiversityRate > 0.
	if weights.DiversityRate > 0 && communityByAuthorID != nil && p.CommunityID >= 0 && len(out) > 1 {
		diverseN := int(math.Ceil(float64(len(out)) * weights.DiversityRate))
		if diverseN < 1 {
			diverseN = 1
		}
		if diverseN < len(out) {
			// Build set of posts already in out
			inOut := make(map[string]bool, len(out))
			for _, post := range out {
				inOut[post.ID] = true
			}
			// Collect diverse candidates: different community, not already in out
			var diverse []*Post
			for _, s := range ranked {
				if inOut[s.post.ID] {
					continue
				}
				authorID := s.post.AuthorID
				authorComm, ok := communityByAuthorID[authorID]
				if !ok {
					stripped := strings.TrimSuffix(authorID, "_rd")
					authorComm, ok = communityByAuthorID[stripped]
				}
				if ok && authorComm != p.CommunityID {
					diverse = append(diverse, s.post)
					if len(diverse) >= diverseN {
						break
					}
				}
			}
			// Replace the last diverseN entries of out with diverse posts
			replaceAt := len(out) - diverseN
			for i, dp := range diverse {
				out[replaceAt+i] = dp
			}
		}
	}

	return out
}

// scorePost computes the composite feed score for a single post.
func scorePost(
	post *Post,
	p *Personality,
	now time.Time,
	w FeedWeights,
	maxEng, maxIC int,
	icSnapshot map[string]int,
	influenceByID map[string]float64,
	driftSnapshot map[string]int,
	round int,
	communityByAuthorID map[string]int,
) float64 {
	// ── 1. Recency — exponential half-life decay ──────────────────────────────
	ageMin := now.Sub(post.Timestamp).Minutes()
	if ageMin < 0 {
		ageMin = 0
	}
	recency := math.Exp(-math.Log(2) * ageMin / w.HalfLifeMin)

	// ── 2. Virality — log-normalised engagement ───────────────────────────────
	eng := float64(post.Likes + post.Reposts + post.Comments)
	virality := 0.0
	if maxEng > 0 {
		virality = math.Log1p(eng) / math.Log1p(float64(maxEng))
	}

	// ── 3. Relevance — blend static interests with accumulated drift ──────────
	β := math.Min(float64(round)*0.05, 0.6) // drift weight grows each round, caps at 60%
	staticRelevance := topicRelevance(post.Tags, p.Interests)
	driftRelevance := topicRelevanceDrift(post.Tags, driftSnapshot)
	relevance := (1-β)*staticRelevance + β*driftRelevance

	// ── 4. Relationship — past interaction count with this author ─────────────
	relationship := 0.0
	if maxIC > 0 {
		cnt := icSnapshot[post.AuthorID]
		// Also try stripping "_rd" suffix for cross-platform consistency.
		if cnt == 0 {
			cnt = icSnapshot[strings.TrimSuffix(post.AuthorID, "_rd")]
		}
		relationship = float64(cnt) / float64(maxIC)
	}

	// ── 5. CommunityAffinity — same Louvain community boost ──────────────────
	// Same community → 1.0, adjacent community (±1) → 0.2 for cross-bubble discovery.
	communityAffinity := 0.0
	if communityByAuthorID != nil && p.CommunityID >= 0 {
		authorID := post.AuthorID
		authorCommunity, ok := communityByAuthorID[authorID]
		if !ok {
			stripped := strings.TrimSuffix(authorID, "_rd")
			authorCommunity, ok = communityByAuthorID[stripped]
		}
		if ok && authorCommunity >= 0 {
			if authorCommunity == p.CommunityID {
				communityAffinity = 1.0
			} else if authorCommunity == p.CommunityID+1 || authorCommunity == p.CommunityID-1 {
				communityAffinity = 0.2
			}
		}
	}

	base := w.Recency*recency + w.Relevance*relevance + w.Virality*virality + w.Relationship*relationship + w.CommunityAffinity*communityAffinity

	// ── Influence weight multiplier ───────────────────────────────────────────
	// Maps [0, 2] → [0.5, 1.5] so influence amplifies but never dominates.
	inf := 1.0
	if v, ok := influenceByID[post.AuthorID]; ok && v > 0 {
		inf = v
	} else if stripped := strings.TrimSuffix(post.AuthorID, "_rd"); stripped != post.AuthorID {
		if v, ok := influenceByID[stripped]; ok && v > 0 {
			inf = v
		}
	}
	inf = 0.5 + (inf/2.0)*1.0 // [0,2] → [0.5, 1.5]

	return base * inf
}

// topicRelevance returns a Jaccard-like overlap score between post tags and
// agent interests, in [0, 1].
func topicRelevance(tags, interests []string) float64 {
	if len(tags) == 0 || len(interests) == 0 {
		return 0
	}
	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[normTopic(t)] = true
	}
	matches := 0
	for _, interest := range interests {
		if tagSet[normTopic(interest)] {
			matches++
		}
	}
	if matches == 0 {
		return 0
	}
	union := len(tagSet) + len(interests) - matches
	return float64(matches) / float64(union)
}

// topicRelevanceDrift computes relevance using accumulated interest drift.
// driftMap maps tag → count (frequency of past engagement).
func topicRelevanceDrift(tags []string, driftMap map[string]int) float64 {
	if len(tags) == 0 || len(driftMap) == 0 {
		return 0
	}
	maxCount := 0
	for _, c := range driftMap {
		if c > maxCount {
			maxCount = c
		}
	}
	if maxCount == 0 {
		return 0
	}
	score := 0.0
	for _, tag := range tags {
		if c, ok := driftMap[normTopic(tag)]; ok {
			score += float64(c) / float64(maxCount)
		}
	}
	return math.Min(score/float64(len(tags)), 1.0)
}

func normTopic(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
