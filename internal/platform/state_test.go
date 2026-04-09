package platform

import (
	"math/rand"
	"testing"
)

// ─── NewState ─────────────────────────────────────────────────────────────────

func TestNewState_PlatformSet(t *testing.T) {
	s := NewState("twitter")
	if s.Platform != "twitter" {
		t.Errorf("Platform = %q, want %q", s.Platform, "twitter")
	}
}

func TestNewState_EmptyMaps(t *testing.T) {
	s := NewState("twitter")
	if len(s.Posts) != 0 {
		t.Errorf("expected empty Posts map, got %d entries", len(s.Posts))
	}
	if len(s.Users) != 0 {
		t.Errorf("expected empty Users map, got %d entries", len(s.Users))
	}
}

// ─── AddPost ─────────────────────────────────────────────────────────────────

func TestState_AddPost_Stored(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "hello"})

	if _, ok := s.Posts["p1"]; !ok {
		t.Error("post p1 should be stored in Posts map")
	}
}

func TestState_AddPost_UpdatesParentCommentCount(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "parent"})
	s.AddPost(&Post{ID: "p2", AuthorID: "a2", Content: "reply", ParentID: "p1"})

	if s.Posts["p1"].Comments != 1 {
		t.Errorf("parent Comments = %d, want 1", s.Posts["p1"].Comments)
	}
}

func TestState_AddPost_UpdatesUserPostList(t *testing.T) {
	s := NewState("twitter")
	s.RegisterUser(&User{ID: "a1", Name: "Alice", Platform: "twitter"})
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "hello"})

	if len(s.Users["a1"].Posts) != 1 {
		t.Errorf("user Posts = %d, want 1", len(s.Users["a1"].Posts))
	}
}

func TestState_AddPost_RedditKarma(t *testing.T) {
	s := NewState("reddit")
	s.RegisterUser(&User{ID: "a1", Name: "Alice", Platform: "reddit"})
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "reddit post"})

	if s.Users["a1"].Karma != 1 {
		t.Errorf("reddit karma = %d, want 1", s.Users["a1"].Karma)
	}
}

func TestState_AddPost_TwitterNoKarma(t *testing.T) {
	s := NewState("twitter")
	s.RegisterUser(&User{ID: "a1", Name: "Alice", Platform: "twitter"})
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "tweet"})

	if s.Users["a1"].Karma != 0 {
		t.Errorf("twitter karma = %d, want 0 (no karma on twitter)", s.Users["a1"].Karma)
	}
}

// ─── LikePost ─────────────────────────────────────────────────────────────────

func TestState_LikePost_IncrementsLikes(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "hello"})
	s.LikePost("p1")
	s.LikePost("p1")

	if s.Posts["p1"].Likes != 2 {
		t.Errorf("Likes = %d, want 2", s.Posts["p1"].Likes)
	}
}

func TestState_LikePost_MissingPost(t *testing.T) {
	s := NewState("twitter")
	// Should not panic on missing post
	s.LikePost("nonexistent")
}

// ─── Repost ───────────────────────────────────────────────────────────────────

func TestState_Repost_IncrementsReposts(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "hello"})
	s.Repost("p1")

	if s.Posts["p1"].Reposts != 1 {
		t.Errorf("Reposts = %d, want 1", s.Posts["p1"].Reposts)
	}
}

// ─── Follow ───────────────────────────────────────────────────────────────────

func TestState_Follow_UpdatesFollowLists(t *testing.T) {
	s := NewState("twitter")
	s.RegisterUser(&User{ID: "a1", Name: "Alice"})
	s.RegisterUser(&User{ID: "a2", Name: "Bob"})

	s.Follow("a1", "a2")

	alice := s.Users["a1"]
	bob := s.Users["a2"]

	if len(alice.Following) != 1 || alice.Following[0] != "a2" {
		t.Errorf("Alice.Following = %v, want [a2]", alice.Following)
	}
	if len(bob.Followers) != 1 || bob.Followers[0] != "a1" {
		t.Errorf("Bob.Followers = %v, want [a1]", bob.Followers)
	}
	if bob.FollowerCnt != 1 {
		t.Errorf("Bob.FollowerCnt = %d, want 1", bob.FollowerCnt)
	}
}

func TestState_Follow_NoSelfFollow(t *testing.T) {
	s := NewState("twitter")
	s.RegisterUser(&User{ID: "a1", Name: "Alice"})

	s.Follow("a1", "a1")

	if len(s.Users["a1"].Following) != 0 {
		t.Error("self-follow should be ignored")
	}
}

func TestState_Follow_NoDuplicates(t *testing.T) {
	s := NewState("twitter")
	s.RegisterUser(&User{ID: "a1", Name: "Alice"})
	s.RegisterUser(&User{ID: "a2", Name: "Bob"})

	s.Follow("a1", "a2")
	s.Follow("a1", "a2") // duplicate

	if len(s.Users["a1"].Following) != 1 {
		t.Errorf("Following should deduplicate: len = %d, want 1", len(s.Users["a1"].Following))
	}
}

func TestState_Follow_MissingUser(t *testing.T) {
	s := NewState("twitter")
	s.RegisterUser(&User{ID: "a1", Name: "Alice"})

	// Should not panic when followee doesn't exist
	s.Follow("a1", "nonexistent")
}

// ─── Timeline ─────────────────────────────────────────────────────────────────

func TestState_Timeline_ExcludesOwnPosts(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "p1", AuthorID: "alice", Content: "post1"})
	s.AddPost(&Post{ID: "p2", AuthorID: "bob", Content: "post2"})
	s.AddPost(&Post{ID: "p3", AuthorID: "alice", Content: "post3"})

	timeline := s.Timeline("alice", 10)
	for _, p := range timeline {
		if p.AuthorID == "alice" {
			t.Errorf("Timeline should exclude own posts, found post by alice: %q", p.ID)
		}
	}
}

func TestState_Timeline_LimitRespected(t *testing.T) {
	s := NewState("twitter")
	for i := 0; i < 20; i++ {
		id := "p" + string(rune('a'+i%26))
		s.AddPost(&Post{ID: id, AuthorID: "other", Content: "post"})
	}

	timeline := s.Timeline("me", 5)
	if len(timeline) > 5 {
		t.Errorf("Timeline limit not respected: got %d, want <= 5", len(timeline))
	}
}

func TestState_Timeline_RecentFirst(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "old", AuthorID: "other", Content: "old post"})
	s.AddPost(&Post{ID: "new", AuthorID: "other", Content: "new post"})

	timeline := s.Timeline("me", 10)
	if len(timeline) < 2 {
		t.Fatal("expected at least 2 posts in timeline")
	}
	// Timeline is most-recent first
	if timeline[0].ID != "new" {
		t.Errorf("Timeline[0] = %q, want %q (most recent first)", timeline[0].ID, "new")
	}
}

func TestState_Timeline_Empty(t *testing.T) {
	s := NewState("twitter")
	timeline := s.Timeline("alice", 10)
	if len(timeline) != 0 {
		t.Errorf("expected empty timeline, got %d posts", len(timeline))
	}
}

// ─── RecentPosts ──────────────────────────────────────────────────────────────

func TestState_RecentPosts_Count(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "post1"})
	s.AddPost(&Post{ID: "p2", AuthorID: "a2", Content: "post2"})
	s.AddPost(&Post{ID: "p3", AuthorID: "a3", Content: "post3"})

	recent := s.RecentPosts(2)
	if len(recent) != 2 {
		t.Errorf("RecentPosts(2) = %d, want 2", len(recent))
	}
}

func TestState_RecentPostsExcluding(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "p1", AuthorID: "alice", Content: "post1"})
	s.AddPost(&Post{ID: "p2", AuthorID: "bob", Content: "post2"})

	recent := s.RecentPostsExcluding("alice", 10)
	for _, p := range recent {
		if p.AuthorID == "alice" {
			t.Error("RecentPostsExcluding should not include alice's posts")
		}
	}
}

// ─── GetStats ─────────────────────────────────────────────────────────────────

func TestState_GetStats_PostCount(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "hello"})
	s.AddPost(&Post{ID: "p2", AuthorID: "a2", Content: "world"})

	stats := s.GetStats()
	if stats.Posts != 2 {
		t.Errorf("Stats.Posts = %d, want 2", stats.Posts)
	}
}

func TestState_GetStats_LikesAndReposts(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "hello"})
	s.LikePost("p1")
	s.LikePost("p1")
	s.Repost("p1")

	stats := s.GetStats()
	if stats.Likes != 2 {
		t.Errorf("Stats.Likes = %d, want 2", stats.Likes)
	}
	if stats.Reposts != 1 {
		t.Errorf("Stats.Reposts = %d, want 1", stats.Reposts)
	}
}

func TestState_GetStats_UserCount(t *testing.T) {
	s := NewState("twitter")
	s.RegisterUser(&User{ID: "a1", Name: "Alice"})
	s.RegisterUser(&User{ID: "a2", Name: "Bob"})

	stats := s.GetStats()
	if stats.Users != 2 {
		t.Errorf("Stats.Users = %d, want 2", stats.Users)
	}
}

func TestState_GetStats_Comments(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "hello"})
	s.AddPost(&Post{ID: "p2", AuthorID: "a2", Content: "reply", ParentID: "p1"})

	stats := s.GetStats()
	if stats.Comments != 1 {
		t.Errorf("Stats.Comments = %d, want 1", stats.Comments)
	}
}

// ─── Trending ─────────────────────────────────────────────────────────────────

func TestState_Trending_Empty(t *testing.T) {
	s := NewState("twitter")
	trending := s.Trending()
	if len(trending) != 0 {
		t.Errorf("initial trending = %v, want empty", trending)
	}
}

func TestState_UpdateTrending_TopTags(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "hello #ai", Tags: []string{"ai"}})
	s.AddPost(&Post{ID: "p2", AuthorID: "a2", Content: "#ai rocks", Tags: []string{"ai"}})
	s.AddPost(&Post{ID: "p3", AuthorID: "a3", Content: "#blockchain", Tags: []string{"blockchain"}})

	s.UpdateTrending()
	trending := s.Trending()

	if len(trending) == 0 {
		t.Fatal("UpdateTrending should produce trending tags")
	}
	if trending[0] != "ai" {
		t.Errorf("top trend = %q, want %q", trending[0], "ai")
	}
}

// ─── ExtractTags ──────────────────────────────────────────────────────────────

func TestExtractTags_Basic(t *testing.T) {
	tags := ExtractTags("Hello #world #ai this is a post")
	if len(tags) != 2 {
		t.Errorf("ExtractTags = %v, want 2 tags", tags)
	}
}

func TestExtractTags_NoTags(t *testing.T) {
	tags := ExtractTags("no hashtags here")
	if len(tags) != 0 {
		t.Errorf("ExtractTags with no # = %v, want empty", tags)
	}
}

func TestExtractTags_HashOnly(t *testing.T) {
	tags := ExtractTags("# alone hash")
	if len(tags) != 0 {
		t.Errorf("# alone should not be a tag: %v", tags)
	}
}

func TestExtractTags_StripsTrailingPunctuation(t *testing.T) {
	tags := ExtractTags("#hello! #world.")
	for _, tag := range tags {
		if len(tag) == 0 {
			t.Error("tag should not be empty string")
			continue
		}
		last := tag[len(tag)-1]
		if last == '!' || last == '.' || last == ',' || last == '?' {
			t.Errorf("tag %q has trailing punctuation", tag)
		}
	}
}

// ─── SafeUsername ─────────────────────────────────────────────────────────────

func TestSafeUsername_Basic(t *testing.T) {
	u := SafeUsername("Alice Chen")
	if u == "" {
		t.Fatal("SafeUsername should not return empty string")
	}
	// Should not contain spaces
	for _, r := range u {
		if r == ' ' {
			t.Errorf("SafeUsername has space: %q", u)
		}
	}
}

func TestSafeUsername_MaxLength(t *testing.T) {
	u := SafeUsername("A Very Long Name That Exceeds The Limit")
	if len(u) > 15 {
		t.Errorf("SafeUsername too long: %q (len=%d)", u, len(u))
	}
}

func TestSafeUsername_EmptyInput(t *testing.T) {
	u := SafeUsername("")
	if u == "" {
		t.Error("SafeUsername('') should return non-empty fallback")
	}
}

func TestSafeUsername_SpacesBecomesUnderscore(t *testing.T) {
	u := SafeUsername("Alice Bob")
	found := false
	for _, r := range u {
		if r == '_' {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("SafeUsername('Alice Bob') = %q, expected underscore", u)
	}
}

// ─── RegisterUser ─────────────────────────────────────────────────────────────

func TestState_RegisterUser(t *testing.T) {
	s := NewState("twitter")
	u := &User{ID: "u1", Name: "Alice", Platform: "twitter"}
	s.RegisterUser(u)

	if s.UserByID("u1") == nil {
		t.Error("registered user not found by ID")
	}
}

// ─── RandomPost ───────────────────────────────────────────────────────────────

func TestState_RandomPost_Nil_When_Empty(t *testing.T) {
	s := NewState("twitter")
	rng := rand.New(rand.NewSource(42))
	post := s.RandomPost(rng)
	if post != nil {
		t.Errorf("expected nil RandomPost on empty state, got %v", post)
	}
}

func TestState_RandomPost_Returns_Post(t *testing.T) {
	s := NewState("twitter")
	s.AddPost(&Post{ID: "p1", AuthorID: "a1", Content: "test"})
	rng := rand.New(rand.NewSource(42))
	post := s.RandomPost(rng)
	if post == nil {
		t.Error("expected non-nil RandomPost when posts exist")
	}
}

// ─── Action.MarshalLine ───────────────────────────────────────────────────────

func TestAction_MarshalLine_EndsWithNewline(t *testing.T) {
	a := Action{Round: 1, AgentID: "a1", Type: "CREATE_POST", Success: true}
	line := a.MarshalLine()
	if len(line) == 0 {
		t.Fatal("MarshalLine returned empty bytes")
	}
	if line[len(line)-1] != '\n' {
		t.Error("MarshalLine should end with newline")
	}
}

func TestAction_MarshalLine_ValidJSON(t *testing.T) {
	a := Action{Round: 2, AgentID: "a2", AgentName: "Bob", Type: "LIKE_POST", PostID: "p1", Success: true}
	line := a.MarshalLine()
	// Strip the trailing newline and verify it's parseable JSON
	if len(line) < 2 {
		t.Fatal("MarshalLine too short")
	}
	jsonBytes := line[:len(line)-1] // remove trailing newline
	// Basic check: starts with '{' and ends with '}'
	if jsonBytes[0] != '{' || jsonBytes[len(jsonBytes)-1] != '}' {
		t.Errorf("MarshalLine not valid JSON object: %q", string(jsonBytes))
	}
}

// ─── topN helper ──────────────────────────────────────────────────────────────

func TestTopN_Basic(t *testing.T) {
	freq := map[string]int{"a": 3, "b": 5, "c": 1}
	top := topN(freq, 2)
	if len(top) != 2 {
		t.Fatalf("topN(2) = %v, want 2 items", top)
	}
	if top[0] != "b" {
		t.Errorf("top[0] = %q, want %q (most frequent)", top[0], "b")
	}
}

func TestTopN_Empty(t *testing.T) {
	freq := map[string]int{}
	top := topN(freq, 5)
	if len(top) != 0 {
		t.Errorf("topN empty freq = %v, want empty", top)
	}
}

func TestTopN_RequestMoreThanAvailable(t *testing.T) {
	freq := map[string]int{"only": 1}
	top := topN(freq, 5)
	if len(top) != 1 {
		t.Errorf("topN(5) with 1 item = %v, want 1 item", top)
	}
}
