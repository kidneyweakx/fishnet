package doc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── ReadDir ─────────────────────────────────────────────────────────────────

func TestReadDir_ReadsMarkdown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.md"), []byte("# Hello\nWorld"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	docs, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ReadDir returned %d docs, want 1", len(docs))
	}
	if !strings.Contains(docs[0].Content, "Hello") {
		t.Errorf("doc content = %q, expected to contain 'Hello'", docs[0].Content)
	}
}

func TestReadDir_ReadsTxt(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("plain text content"), 0644)

	docs, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ReadDir txt = %d docs, want 1", len(docs))
	}
}

func TestReadDir_ReadsCSV(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "data.csv"), []byte("name,age\nalice,30"), 0644)

	docs, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ReadDir csv = %d docs, want 1", len(docs))
	}
}

func TestReadDir_ReadsJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"key":"value"}`), 0644)

	docs, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ReadDir json = %d docs, want 1", len(docs))
	}
}

func TestReadDir_ReadsRST(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "doc.rst"), []byte("Title\n=====\nContent"), 0644)

	docs, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ReadDir rst = %d docs, want 1", len(docs))
	}
}

func TestReadDir_SkipsUnsupportedExtensions(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "image.png"), []byte("binary content"), 0644)
	os.WriteFile(filepath.Join(dir, "binary.exe"), []byte("binary"), 0644)
	os.WriteFile(filepath.Join(dir, "doc.md"), []byte("# Hello"), 0644)

	docs, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(docs) != 1 {
		t.Errorf("ReadDir = %d docs, want 1 (only .md)", len(docs))
	}
}

func TestReadDir_SkipsHiddenDirectories(t *testing.T) {
	dir := t.TempDir()
	// Create a hidden directory with a file
	hiddenDir := filepath.Join(dir, ".hidden")
	os.MkdirAll(hiddenDir, 0755)
	os.WriteFile(filepath.Join(hiddenDir, "secret.md"), []byte("hidden content"), 0644)
	// Create a visible file
	os.WriteFile(filepath.Join(dir, "visible.md"), []byte("visible content"), 0644)

	docs, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(docs) != 1 {
		t.Errorf("ReadDir = %d docs, want 1 (hidden dir skipped)", len(docs))
	}
	if docs[0].Name != "visible.md" {
		t.Errorf("ReadDir returned %q, want visible.md", docs[0].Name)
	}
}

func TestReadDir_SkipsEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "empty.md"), []byte("   \n\t"), 0644) // whitespace only
	os.WriteFile(filepath.Join(dir, "real.md"), []byte("# Real content"), 0644)

	docs, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(docs) != 1 {
		t.Errorf("ReadDir = %d docs, want 1 (empty skipped)", len(docs))
	}
}

func TestReadDir_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	docs, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("ReadDir empty dir = %d docs, want 0", len(docs))
	}
}

func TestReadDir_DocumentFieldsPopulated(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "myfile.md"), []byte("# Title\nBody text"), 0644)

	docs, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least 1 doc")
	}

	d := docs[0]
	if d.Name != "myfile.md" {
		t.Errorf("Name = %q, want %q", d.Name, "myfile.md")
	}
	if d.Path == "" {
		t.Error("Path should not be empty")
	}
	if d.Content == "" {
		t.Error("Content should not be empty")
	}
}

func TestReadDir_RecursiveSubdirectory(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")
	os.MkdirAll(subdir, 0755)
	os.WriteFile(filepath.Join(dir, "root.md"), []byte("root doc"), 0644)
	os.WriteFile(filepath.Join(subdir, "sub.md"), []byte("sub doc"), 0644)

	docs, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("ReadDir recursive = %d docs, want 2", len(docs))
	}
}

func TestReadDir_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.md", "b.md", "c.txt"} {
		os.WriteFile(filepath.Join(dir, name), []byte("content "+name), 0644)
	}

	docs, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(docs) != 3 {
		t.Errorf("ReadDir = %d docs, want 3", len(docs))
	}
}

// ─── Chunk ───────────────────────────────────────────────────────────────────

func TestChunk_ShortText_ReturnsOne(t *testing.T) {
	chunks := Chunk("hello world", 100, 10)
	if len(chunks) != 1 {
		t.Errorf("short text chunks = %d, want 1", len(chunks))
	}
	if chunks[0] != "hello world" {
		t.Errorf("chunk = %q, want %q", chunks[0], "hello world")
	}
}

func TestChunk_EmptyText_ReturnsNil(t *testing.T) {
	chunks := Chunk("", 100, 10)
	if len(chunks) != 0 {
		t.Errorf("empty text chunks = %v, want empty", chunks)
	}
}

func TestChunk_WhitespaceOnly_ReturnsNil(t *testing.T) {
	chunks := Chunk("   \n\t", 100, 10)
	if len(chunks) != 0 {
		t.Errorf("whitespace chunks = %v, want empty", chunks)
	}
}

func TestChunk_SplitsLargeText(t *testing.T) {
	text := strings.Repeat("word ", 200) // 1000 runes
	chunks := Chunk(text, 100, 20)
	if len(chunks) <= 1 {
		t.Errorf("large text should produce multiple chunks, got %d", len(chunks))
	}
}

func TestChunk_ChunkSizeRespected(t *testing.T) {
	text := strings.Repeat("a", 500)
	chunkSize := 100
	chunks := Chunk(text, chunkSize, 0)
	for i, c := range chunks {
		runes := []rune(c)
		if len(runes) > chunkSize {
			t.Errorf("chunk[%d] len = %d, exceeds chunkSize %d", i, len(runes), chunkSize)
		}
	}
}

func TestChunk_OverlapIsApplied(t *testing.T) {
	// With overlap, consecutive chunks should share content
	text := "abcdefghijklmnopqrstuvwxyz" // 26 chars
	chunks := Chunk(text, 10, 5)
	if len(chunks) < 2 {
		t.Skip("text too short to have multiple chunks with these parameters")
	}
	// chunk[0] ends at index 10; chunk[1] starts at index 5 (10-5=5)
	// So chunk[0][:5] should equal chunk[1][:5]
	if len(chunks) >= 2 {
		runes0 := []rune(chunks[0])
		runes1 := []rune(chunks[1])
		overlap := 5
		if len(runes0) >= overlap && len(runes1) >= overlap {
			end0 := string(runes0[len(runes0)-overlap:])
			start1 := string(runes1[:overlap])
			if end0 != start1 {
				t.Errorf("overlap mismatch: end of chunk[0]=%q, start of chunk[1]=%q", end0, start1)
			}
		}
	}
}

func TestChunk_ExactSizeText_ReturnsOne(t *testing.T) {
	text := strings.Repeat("x", 100)
	chunks := Chunk(text, 100, 10)
	if len(chunks) != 1 {
		t.Errorf("text exactly chunkSize should give 1 chunk, got %d", len(chunks))
	}
}

func TestChunk_NoOverlap(t *testing.T) {
	text := strings.Repeat("a", 100)
	chunks := Chunk(text, 50, 0)
	if len(chunks) != 2 {
		t.Errorf("100 chars / 50 chunk size / 0 overlap = %d chunks, want 2", len(chunks))
	}
}

// ─── Summary ─────────────────────────────────────────────────────────────────

func TestSummary_ShortContent_Unchanged(t *testing.T) {
	result := Summary("hello world", 100)
	if result != "hello world" {
		t.Errorf("Summary short = %q, want %q", result, "hello world")
	}
}

func TestSummary_LongContent_Truncated(t *testing.T) {
	long := strings.Repeat("a", 200)
	result := Summary(long, 100)
	runes := []rune(result)
	if len(runes) > 104 { // 100 + "..."
		t.Errorf("Summary long len = %d, expected <= 104", len(runes))
	}
	if !strings.HasSuffix(result, "...") {
		t.Errorf("Summary long = %q, should end with '...'", result)
	}
}

func TestSummary_ExactLength(t *testing.T) {
	text := strings.Repeat("b", 50)
	result := Summary(text, 50)
	if result != text {
		t.Errorf("Summary exact length = %q, want unchanged", result)
	}
}

func TestSummary_EmptyInput(t *testing.T) {
	result := Summary("", 100)
	if result != "" {
		t.Errorf("Summary empty = %q, want empty", result)
	}
}

func TestSummary_WhitespaceStripped(t *testing.T) {
	result := Summary("  hello  ", 100)
	if result != "hello" {
		t.Errorf("Summary strips whitespace = %q, want %q", result, "hello")
	}
}
