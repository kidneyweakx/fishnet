package sim

import (
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"

	"fishnet/internal/platform"
)

// ─── Intervention Types ───────────────────────────────────────────────────────

// InterventionEvent represents a human-injected event during simulation.
type InterventionEvent struct {
	Round   int    // inject at this round (0 = ASAP / round 1)
	Type    string // "inject_post" | "trending_topic" | "pause" | "resume" | "agent_directive"
	Content string // for inject_post: post content; for trending_topic: topic
	AgentID string // for agent_directive: which agent
	Message string // for agent_directive: instruction override
}

// InterventionQueue holds pending human interventions.
type InterventionQueue struct {
	mu     sync.Mutex
	events []InterventionEvent
}

// NewInterventionQueue returns an initialized, empty InterventionQueue.
func NewInterventionQueue() *InterventionQueue {
	return &InterventionQueue{}
}

// Add enqueues an InterventionEvent for a future round.
func (q *InterventionQueue) Add(event InterventionEvent) {
	q.mu.Lock()
	q.events = append(q.events, event)
	q.mu.Unlock()
}

// Drain returns and removes all events scheduled for the given round.
// Events with Round == 0 are treated as round 1.
func (q *InterventionQueue) Drain(round int) []InterventionEvent {
	q.mu.Lock()
	defer q.mu.Unlock()

	var matched []InterventionEvent
	remaining := q.events[:0]
	for _, e := range q.events {
		target := e.Round
		if target <= 0 {
			target = 1
		}
		if target == round {
			matched = append(matched, e)
		} else {
			remaining = append(remaining, e)
		}
	}
	q.events = remaining
	return matched
}

// ─── ApplyIntervention ────────────────────────────────────────────────────────

// ApplyIntervention applies a single InterventionEvent to the given platform state.
// projectID is unused in state operations but kept for future persistence hooks.
func ApplyIntervention(state *platform.State, event InterventionEvent, projectID string) {
	_ = projectID // reserved for future persistence
	switch event.Type {
	case "inject_post":
		state.AddPost(&platform.Post{
			ID:         uuid.New().String(),
			Platform:   state.Platform,
			AuthorID:   "human_facilitator",
			AuthorName: "Facilitator",
			Content:    event.Content,
			Timestamp:  time.Now(),
			Tags:       platform.ExtractTags(event.Content),
		})

	case "trending_topic":
		// Force a topic into trending by injecting 3 posts from synthetic agents.
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		syntheticAgents := []struct{ id, name string }{
			{"synth_a", "TrendBot_A"},
			{"synth_b", "TrendBot_B"},
			{"synth_c", "TrendBot_C"},
		}
		for _, agent := range syntheticAgents {
			content := event.Content
			if len(content) == 0 {
				content = "Trending: " + event.Message
			}
			_ = rng
			state.AddPost(&platform.Post{
				ID:         uuid.New().String(),
				Platform:   state.Platform,
				AuthorID:   agent.id,
				AuthorName: agent.name,
				Content:    "#" + platform.SafeUsername(event.Content) + " " + content,
				Timestamp:  time.Now(),
				Tags:       []string{platform.SafeUsername(event.Content)},
			})
		}

	case "pause":
		// pause is handled at the Run() loop level by checking the event type;
		// at the state level there is nothing to apply.

	case "resume":
		// resume is handled at the Run() loop level by clearing the pause flag;
		// at the state level there is nothing to apply.

	case "agent_directive":
		// agent_directive overrides are handled at the Run() loop level.
		// No state mutation needed here.
	}
}
