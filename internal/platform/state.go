// Package platform implements in-memory Twitter + Reddit simulation state.
// Thread-safe. No LLM per round — LLM only for content generation.
package platform

import (
	"encoding/json"
	"fmt"
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
	Dislikes  int       `json:"dislikes"`  // reddit downvotes
	Reposts   int       `json:"reposts"`
	Comments  int       `json:"comments"`
	Tags      []string  `json:"tags,omitempty"`

	// CommentLikes / CommentDislikes track aggregate vote counts on comments
	// that are children of this post (reddit-style).
	CommentLikes    int `json:"comment_likes,omitempty"`
	CommentDislikes int `json:"comment_dislikes,omitempty"`
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
	Muted       []string `json:"muted,omitempty"` // user IDs this user has muted
}

// Action type constants — mirrors OASIS action vocabulary.
//
// Shared (Twitter + Reddit):
//
//	CREATE_POST, LIKE_POST, REPOST, QUOTE_POST, FOLLOW, DO_NOTHING, TREND, REFRESH
//
// Reddit-only:
//
//	CREATE_COMMENT, DISLIKE_POST, LIKE_COMMENT, DISLIKE_COMMENT,
//	SEARCH_POSTS, SEARCH_USER, MUTE
const (
	ActCreatePost     = "CREATE_POST"
	ActCreateComment  = "CREATE_COMMENT"
	ActLikePost       = "LIKE_POST"
	ActDislikePost    = "DISLIKE_POST"
	ActLikeComment    = "LIKE_COMMENT"
	ActDislikeComment = "DISLIKE_COMMENT"
	ActRepost         = "REPOST"
	ActQuotePost      = "QUOTE_POST"
	ActFollow         = "FOLLOW"
	ActSearchPosts    = "SEARCH_POSTS"
	ActSearchUser     = "SEARCH_USER"
	ActTrend          = "TREND"
	ActRefresh        = "REFRESH"
	ActMute           = "MUTE"
	ActDoNothing      = "DO_NOTHING"
)

// TwitterActions are the actions available on the Twitter platform.
var TwitterActions = []string{
	ActCreatePost, ActLikePost, ActRepost, ActQuotePost,
	ActFollow, ActTrend, ActRefresh, ActDoNothing,
}

// RedditActions are the actions available on the Reddit platform.
var RedditActions = []string{
	ActCreatePost, ActCreateComment,
	ActLikePost, ActDislikePost,
	ActLikeComment, ActDislikeComment,
	ActSearchPosts, ActSearchUser,
	ActTrend, ActRefresh,
	ActFollow, ActMute, ActDoNothing,
}

// Action is a single agent action event — written to actions.jsonl
type Action struct {
	Round      int       `json:"round"`
	Timestamp  time.Time `json:"timestamp"`
	Platform   string    `json:"platform"`
	AgentID    string    `json:"agent_id"`
	AgentName  string    `json:"agent_name"`
	Type       string    `json:"type"`
	PostID     string    `json:"post_id,omitempty"`
	TargetID   string    `json:"target_id,omitempty"`   // target user for FOLLOW/MUTE/SEARCH_USER
	Content    string    `json:"content,omitempty"`
	Query      string    `json:"query,omitempty"`        // search query for SEARCH_POSTS/SEARCH_USER
	Subreddit  string    `json:"subreddit,omitempty"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"` // non-empty when Success==false
}

// Description returns a human-readable memory description for graph updates.
func (a Action) Description() string {
	switch a.Type {
	case ActCreatePost:
		return fmt.Sprintf("%s published a post about: %s", a.AgentName, clipStr(a.Content, 80))
	case ActCreateComment:
		return fmt.Sprintf("%s commented on post %s: %s", a.AgentName, a.PostID, clipStr(a.Content, 60))
	case ActLikePost:
		return fmt.Sprintf("%s liked post %s", a.AgentName, a.PostID)
	case ActDislikePost:
		return fmt.Sprintf("%s disliked post %s", a.AgentName, a.PostID)
	case ActLikeComment:
		return fmt.Sprintf("%s upvoted a comment on post %s", a.AgentName, a.PostID)
	case ActDislikeComment:
		return fmt.Sprintf("%s downvoted a comment on post %s", a.AgentName, a.PostID)
	case ActRepost:
		return fmt.Sprintf("%s reposted post %s", a.AgentName, a.PostID)
	case ActQuotePost:
		return fmt.Sprintf("%s quote-posted %s: %s", a.AgentName, a.PostID, clipStr(a.Content, 60))
	case ActFollow:
		return fmt.Sprintf("%s followed user %s", a.AgentName, a.TargetID)
	case ActSearchPosts:
		return fmt.Sprintf("%s searched for posts: %q", a.AgentName, a.Query)
	case ActSearchUser:
		return fmt.Sprintf("%s searched for user: %q", a.AgentName, a.Query)
	case ActTrend:
		return fmt.Sprintf("%s browsed trending topics", a.AgentName)
	case ActRefresh:
		return fmt.Sprintf("%s refreshed their feed", a.AgentName)
	case ActMute:
		return fmt.Sprintf("%s muted user %s", a.AgentName, a.TargetID)
	case ActDoNothing:
		return fmt.Sprintf("%s chose to do nothing this round", a.AgentName)
	default:
		return fmt.Sprintf("%s performed %s", a.AgentName, a.Type)
	}
}

func clipStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
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

	// interactions tracks how many times a viewer has engaged with each author.
	// interactions[viewerID][authorID] = count
	// Used by the 4-factor feed ranking to compute the Relationship signal.
	interactions map[string]map[string]int

	// interestDrift tracks tag frequency accumulated from liked posts per agent.
	// interestDrift[agentID][tag] = count of times agent liked content with this tag
	interestDrift map[string]map[string]int

	// seenPosts tracks which post IDs each agent has already seen in their feed.
	// seenPosts[agentID][postID] = true
	seenPosts map[string]map[string]bool
}

func NewState(platform string) *State {
	return &State{
		Platform:      platform,
		Posts:         make(map[string]*Post),
		Users:         make(map[string]*User),
		interactions:  make(map[string]map[string]int),
		interestDrift: make(map[string]map[string]int),
		seenPosts:     make(map[string]map[string]bool),
	}
}

// RecordInteraction notes that viewerID engaged with content by authorID.
// Called after LIKE_POST, REPOST, CREATE_COMMENT, and QUOTE_POST actions.
func (s *State) RecordInteraction(viewerID, authorID string) {
	if viewerID == "" || authorID == "" || viewerID == authorID {
		return
	}
	s.mu.Lock()
	if s.interactions[viewerID] == nil {
		s.interactions[viewerID] = make(map[string]int)
	}
	s.interactions[viewerID][authorID]++
	s.mu.Unlock()
}

// GetInteractionCount returns how many times viewerID has engaged with authorID's content.
func (s *State) GetInteractionCount(viewerID, authorID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m, ok := s.interactions[viewerID]; ok {
		return m[authorID]
	}
	return 0
}

// RecordLikedTags records the tags from a post that agentID liked.
// Called after LIKE_POST and REPOST actions to drive interest drift.
func (s *State) RecordLikedTags(agentID string, tags []string) {
	if agentID == "" || len(tags) == 0 {
		return
	}
	s.mu.Lock()
	if s.interestDrift[agentID] == nil {
		s.interestDrift[agentID] = make(map[string]int)
	}
	for _, tag := range tags {
		if tag != "" {
			s.interestDrift[agentID][normTag(tag)]++
		}
	}
	s.mu.Unlock()
}

// GetInterestDrift returns a copy of the interest drift map for agentID.
func (s *State) GetInterestDrift(agentID string) map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.interestDrift[agentID]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// MarkSeen records that agentID has seen the given post IDs in their feed.
// Subsequent calls to RankedFeed will exclude these posts from the pool.
func (s *State) MarkSeen(agentID string, postIDs []string) {
	if agentID == "" || len(postIDs) == 0 {
		return
	}
	s.mu.Lock()
	if s.seenPosts[agentID] == nil {
		s.seenPosts[agentID] = make(map[string]bool, len(postIDs))
	}
	for _, id := range postIDs {
		s.seenPosts[agentID][id] = true
	}
	s.mu.Unlock()
}

// PostTags returns the tags for a post, or nil if the post is not found.
func (s *State) PostTags(postID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.Posts[postID]; ok {
		return append([]string{}, p.Tags...)
	}
	return nil
}

// normTag normalises a tag string to lowercase trimmed form.
func normTag(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// PostAuthorID returns the AuthorID of a post, or "" if the post is not found.
func (s *State) PostAuthorID(postID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.Posts[postID]; ok {
		return p.AuthorID
	}
	return ""
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

// DislikePost increments the dislike/downvote counter (Reddit).
func (s *State) DislikePost(postID string) {
	s.mu.Lock()
	if p, ok := s.Posts[postID]; ok {
		p.Dislikes++
	}
	s.mu.Unlock()
}

// LikeComment increments the aggregate comment-likes on a post's comment tree.
func (s *State) LikeComment(postID string) {
	s.mu.Lock()
	if p, ok := s.Posts[postID]; ok {
		p.CommentLikes++
	}
	s.mu.Unlock()
}

// DislikeComment increments the aggregate comment-dislikes on a post's comment tree.
func (s *State) DislikeComment(postID string) {
	s.mu.Lock()
	if p, ok := s.Posts[postID]; ok {
		p.CommentDislikes++
	}
	s.mu.Unlock()
}

// Mute adds targetID to the muterID's muted list.
func (s *State) Mute(muterID, targetID string) {
	if muterID == targetID {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.Users[muterID]
	if u == nil {
		return
	}
	for _, id := range u.Muted {
		if id == targetID {
			return
		}
	}
	u.Muted = append(u.Muted, targetID)
}

// SearchPosts returns posts matching a simple keyword query.
func (s *State) SearchPosts(query string, limit int) []*Post {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q := strings.ToLower(query)
	var out []*Post
	for i := len(s.postOrder) - 1; i >= 0 && len(out) < limit; i-- {
		p := s.Posts[s.postOrder[i]]
		if p != nil && strings.Contains(strings.ToLower(p.Content), q) {
			out = append(out, p)
		}
	}
	return out
}

// SearchUsers returns users whose name contains query.
func (s *State) SearchUsers(query string, limit int) []*User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q := strings.ToLower(query)
	var out []*User
	for _, u := range s.Users {
		if len(out) >= limit {
			break
		}
		if strings.Contains(strings.ToLower(u.Name), q) {
			out = append(out, u)
		}
	}
	return out
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

// PostTagsOrFallback returns the tags for the given content. If ExtractTags
// returns nothing, it falls back to the first min(2, len(interests)) agent
// interest strings — ensuring every post has at least some topic signal.
func PostTagsOrFallback(content string, interests []string) []string {
	tags := ExtractTags(content)
	if len(tags) == 0 && len(interests) > 0 {
		n := 2
		if len(interests) < n {
			n = len(interests)
		}
		tags = make([]string, n)
		copy(tags, interests[:n])
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
