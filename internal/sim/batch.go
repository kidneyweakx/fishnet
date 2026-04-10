package sim

import (
	"context"
	"encoding/json"
	"fmt"

	"fishnet/internal/platform"
)

// batchGenContent sends a single LLM call for all content-generation requests
// in a round and returns the generated strings in the same order as items.
// Fallback to genContentTemplate per item if the LLM fails or returns empty.
func (ps *PlatformSim) batchGenContent(ctx context.Context, items []contentItem, round int) ([]string, error) {
	type reqItem struct {
		ID           int      `json:"id"`
		Agent        string   `json:"agent"`
		NodeType     string   `json:"node_type"`
		Profession   string   `json:"profession,omitempty"`
		Fingerprint  string   `json:"fingerprint,omitempty"`
		Bio          string   `json:"bio,omitempty"`
		Interests    []string `json:"interests,omitempty"`
		Catchphrases []string `json:"catchphrases,omitempty"`
		Type         string   `json:"type"`
		Platform     string   `json:"platform"`
		Stance       string   `json:"stance"`
		Style        string   `json:"style"`
		Topic        string   `json:"topic"`
	}
	type respItem struct {
		ID      int    `json:"id"`
		Content string `json:"content"`
	}

	reqs := make([]reqItem, len(items))
	for i, it := range items {
		style := "1-2 sentences"
		if it.planned.Type == platform.ActCreatePost && it.pers.Verbosity > 0.6 {
			style = "2-3 sentences"
		}
		bio := it.pers.Bio
		if len([]rune(bio)) > 150 {
			bio = string([]rune(bio)[:150]) + "…"
		}
		fingerprint := it.pers.Fingerprint
		// If fingerprint is set, truncate bio since fingerprint covers it
		if fingerprint != "" && len([]rune(bio)) > 80 {
			bio = string([]rune(bio)[:80]) + "…"
		}
		reqs[i] = reqItem{
			ID:           i,
			Agent:        it.pers.Name,
			NodeType:     it.pers.NodeType,
			Profession:   it.pers.Profession,
			Fingerprint:  fingerprint,
			Bio:          bio,
			Interests:    it.pers.Interests,
			Catchphrases: it.pers.Catchphrases,
			Type:         it.planned.Type,
			Platform:     it.state.Platform,
			Stance:       it.pers.Stance,
			Style:        style,
			Topic:        it.planned.Topic,
		}
	}

	reqJSON, err := json.Marshal(reqs)
	if err != nil {
		return nil, fmt.Errorf("batch marshal: %w", err)
	}

	const sys = `You generate authentic social media posts for a simulation. Each agent has a unique persona.
Input: JSON array of content requests with agent persona details.
Output: JSON array of {"id": <number>, "content": "<post text>"} objects.

Rules:
- If 'fingerprint' is provided, use it as the primary persona description instead of bio
- Write in the agent's voice — use their bio, profession, and interests to shape the post
- If catchphrases are provided, occasionally weave one in naturally
- Match stance (supportive/opposing/observer/neutral) and sentiment
- Twitter: concise, under 280 chars, conversational
- Reddit: 2-4 sentences, community-aware, can be more analytical
- DO NOT include hashtags unless the agent's style clearly calls for them
- Output ONLY the JSON array, no other text`

	var responses []respItem
	if err := ps.llm.JSON(ctx, sys, string(reqJSON), &responses); err != nil {
		return nil, fmt.Errorf("batch llm: %w", err)
	}

	// Map by ID
	byID := make(map[int]string, len(responses))
	for _, r := range responses {
		byID[r.ID] = r.Content
	}

	result := make([]string, len(items))
	for i, it := range items {
		c := byID[i]
		if c == "" {
			// Fallback: local template for this item
			c = genContentTemplate(it.pers, it.planned.Topic, it.planned.Type, it.state.Platform, round)
		}
		result[i] = c
	}
	return result, nil
}
