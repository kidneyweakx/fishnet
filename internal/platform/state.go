// Package platform implements in-memory Twitter + Reddit simulation state.
// Thread-safe. No LLM per round — LLM only for content generation.
package platform

import (
	"encoding/json"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// ─── Core Types ───────────────────────────────────────────────────────────────

type Post struct {
	ID        string    `json:"id"`
	Platform  string    `json:"platform"` // "twitter" | "reddit"
	AuthorID  string    `json:"author_id"`
	AuthorName string   `json:"author_name"`
	Content   string    `json:"content"`
	ParentID  string    `json:"parent_id,omitempty"` // reply/quote target
	Subreddit string    `json:"subreddit,omitempty"` // reddit only
	Timestamp time.Time `json:"timestamp"`
	Likes     int       `json:"likes"`
	Reposts   int       `json:"reposts"`
	Comments  int       `json:"comments"`
	Tags      []string  `json:"tags,omitempty"`
}

type User struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Platform    string   `json:"platform"`
	Bio         string   `json:"bio"`
	Followers   []string `json:"followers"`
	Following   []string `json:"following"`
	Posts       []string `json:"posts"` // post IDs
	Karma       int      `json:"karma"` // reddit
	FollowerCnt int      `json:"follower_count"`
}

// Action is a single agent action event — written to actions.jsonl
type Action struct {
	Round      int       `json:"round"`
	Timestamp  time.Time `json:"timestamp"`
	Platform   string    `json:"platform"`
	AgentID    string    `json:"agent_id"`
	AgentName  string    `json:"agent_name"`
	Type       string    `json:"type"` // CREATE_POST|LIKE_POST|REPOST|QUOTE_POST|COMMENT|FOLLOW|SEARCH
	PostID     string    `json:"post_id,omitempty"`
	Content    string    `json:"content,omitempty"`
	Subreddit  string    `json:"subreddit,omitempty"`
	Success    bool      `json:"success"`
}

func (a Action) MarshalLine() []byte {
	b, _ := json.Marshal(a)
	return append(b, '\n')
}

// Stats is a snapshot of platform activity
type Stats struct {
	Posts    int `json:"posts"`
	Likes    int `json:"likes"`
	Reposts  int `json:"reposts"`
	Comments int `json:"comments"`
	Users    int `json:"users"`
}

// ─── State ────────────────────────────────────────────────────────────────────

// State is a thread-safe in-memory platform.
type State struct {
	mu        sync.RWMutex
	Platform  string
	Posts     map[string]*Post
	Users     map[string]*User
	postOrder []string // chronological
	trending  []string
}

func NewState(platform string) *State {
	return &State{
		Platform: platform,
		Posts:    make(map[string]*Post),
		Users:    make(map[string]*User),
	}
}

// RegisterUser adds a user to the platform.
func (s *State) RegisterUser(u *User) {
	s.mu.Lock()
	s.Users[u.ID] = u
	s.mu.Unlock()
}

// AddPost stores a post and updates parent comment count.
func (s *State) AddPost(p *Post) {
	s.mu.Lock()
	s.Posts[p.ID] = p
	s.postOrder = append(s.postOrder, p.ID)
	if p.ParentID != "" {
		if parent, ok := s.Posts[p.ParentID]; ok {
			parent.Comments++
		}
	}
	if u, ok := s.Users[p.AuthorID]; ok {
		u.Posts = append(u.Posts, p.ID)
		if s.Platform == "reddit" {
			u.Karma += 1
		}
	}
	s.mu.Unlock()
}

func (s *State) LikePost(postID string) {
	s.mu.Lock()
	if p, ok := s.Posts[postID]; ok {
		p.Likes++
	}
	s.mu.Unlock()
}

func (s *State) Repost(postID string) {
	s.mu.Lock()
	if p, ok := s.Posts[postID]; ok {
		p.Reposts++
	}
	s.mu.Unlock()
}

func (s *State) Follow(followerID, followeeID string) {
	if followerID == followeeID {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.Users[followerID]
	ee := s.Users[followeeID]
	if f == nil || ee == nil {
		return
	}
	for _, id := range f.Following {
		if id == followeeID {
			return
		}
	}
	f.Following = append(f.Following, followeeID)
	ee.Followers = append(ee.Followers, followerID)
	ee.FollowerCnt++
}

// Timeline returns up to limit recent posts not authored by agentID.
func (s *State) Timeline(agentID string, limit int) []*Post {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Post
	for i := len(s.postOrder) - 1; i >= 0 && len(out) < limit; i-- {
		p := s.Posts[s.postOrder[i]]
		if p != nil && p.AuthorID != agentID {
			out = append(out, p)
		}
	}
	return out
}

// RecentPosts returns n most recent posts (any author).
func (s *State) RecentPosts(n int) []*Post {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Post
	for i := len(s.postOrder) - 1; i >= 0 && len(out) < n; i-- {
		if p := s.Posts[s.postOrder[i]]; p != nil {
			out = append(out, p)
		}
	}
	return out
}

// RecentPostsExcluding returns up to n most recent posts not authored by agentID.
func (s *State) RecentPostsExcluding(agentID string, n int) []*Post {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Post
	for i := len(s.postOrder) - 1; i >= 0 && len(out) < n; i-- {
		p := s.Posts[s.postOrder[i]]
		if p != nil && p.AuthorID != agentID {
			out = append(out, p)
		}
	}
	return out
}

// RandomPost picks a recent post weighted toward recency.
func (s *State) RandomPost(rng *rand.Rand) *Post {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := len(s.postOrder)
	if n == 0 {
		return nil
	}
	start := n - 30
	if start < 0 {
		start = 0
	}
	idx := start + rng.Intn(n-start)
	return s.Posts[s.postOrder[idx]]
}

// UpdateTrending recomputes top trending tags from recent posts.
func (s *State) UpdateTrending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	freq := make(map[string]int)
	recent := s.postOrder
	if len(recent) > 200 {
		recent = recent[len(recent)-200:]
	}
	for _, pid := range recent {
		p := s.Posts[pid]
		if p == nil {
			continue
		}
		for _, t := range p.Tags {
			freq[t]++
		}
	}
	s.trending = topN(freq, 5)
}

func (s *State) Trending() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string{}, s.trending...)
}

func (s *State) GetStats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Stats{Posts: len(s.Posts), Users: len(s.Users)}
	for _, p := range s.Posts {
		st.Likes += p.Likes
		st.Reposts += p.Reposts
		st.Comments += p.Comments
	}
	return st
}

func (s *State) UserByID(id string) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Users[id]
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func topN(freq map[string]int, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		best := ""
		bestN := 0
		for t, c := range freq {
			if c > bestN {
				best, bestN = t, c
			}
		}
		if best == "" {
			break
		}
		out = append(out, best)
		delete(freq, best)
	}
	return out
}

// ExtractTags finds #hashtags in content.
func ExtractTags(content string) []string {
	var tags []string
	for _, word := range strings.Fields(content) {
		if strings.HasPrefix(word, "#") && len(word) > 1 {
			tag := strings.Trim(word[1:], ".,!?;:")
			if tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	return tags
}

// SafeUsername converts an entity name to a valid platform handle.
func SafeUsername(name string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			sb.WriteRune(r)
		} else if r == ' ' || r == '-' {
			sb.WriteRune('_')
		}
	}
	s := sb.String()
	if len(s) > 15 {
		s = s[:15]
	}
	if s == "" {
		s = "user"
	}
	return s
}
