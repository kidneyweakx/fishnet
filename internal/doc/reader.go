package doc

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Document struct {
	Path    string
	Name    string
	Content string
}

var supportedExts = map[string]bool{
	".txt":  true,
	".md":   true,
	".rst":  true,
	".csv":  true,
	".json": true,
	".docx": true,
	".html": true,
	".htm":  true,
}

// ReadDir recursively reads all supported documents from dir.
func ReadDir(dir string) ([]Document, error) {
	var docs []Document
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable paths
		}
		if info.IsDir() {
			// Skip hidden dirs
			if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !supportedExts[ext] {
			return nil
		}

		content, err := readFile(path, ext)
		if err != nil {
			fmt.Printf("  skip %s: %v\n", path, err)
			return nil
		}
		if !utf8.ValidString(content) {
			fmt.Printf("  skip %s: not valid UTF-8\n", path)
			return nil
		}
		if strings.TrimSpace(content) == "" {
			return nil
		}
		docs = append(docs, Document{
			Path:    path,
			Name:    info.Name(),
			Content: content,
		})
		return nil
	})
	return docs, err
}

// readFile reads and returns the text content of a file based on its extension.
func readFile(path, ext string) (string, error) {
	switch ext {
	case ".docx":
		return readDOCX(path)
	case ".html", ".htm":
		return readHTML(path)
	default:
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

// readDOCX extracts plain text from a .docx file.
// A .docx is a ZIP archive containing word/document.xml.
func readDOCX(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open document.xml: %w", err)
		}
		defer rc.Close()

		data, err := io.ReadAll(rc)
		if err != nil {
			return "", fmt.Errorf("read document.xml: %w", err)
		}

		// Strip XML tags
		xmlTagRe := regexp.MustCompile(`<[^>]+>`)
		text := xmlTagRe.ReplaceAllString(string(data), " ")

		// Collapse whitespace
		spaceRe := regexp.MustCompile(`\s+`)
		text = strings.TrimSpace(spaceRe.ReplaceAllString(text, " "))

		return text, nil
	}
	return "", fmt.Errorf("no word/document.xml found in %s", path)
}

// readHTML extracts plain text from an HTML file by stripping tags.
func readHTML(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	// Strip HTML tags
	tagRe := regexp.MustCompile(`<[^>]+>`)
	text := tagRe.ReplaceAllString(string(data), " ")
	// Collapse whitespace
	spaceRe := regexp.MustCompile(`\s+`)
	text = strings.TrimSpace(spaceRe.ReplaceAllString(text, " "))
	return text, nil
}

// Chunk splits text into overlapping chunks using paragraph-aware boundaries.
//
// Strategy (mirroring MiroFish text_processor.py):
//  1. Split the text into paragraphs (double-newline boundaries).
//  2. Accumulate paragraphs into a chunk until it would exceed `size` runes.
//  3. When a chunk is full, emit it and start the next chunk with the last
//     paragraph as context (sentence-level overlap).
//
// If `overlap` > 0, the last `overlap` runes of the previous chunk are
// prepended to the next chunk as context (capped at the last paragraph).
func Chunk(text string, size, overlap int) []string {
	text = strings.TrimSpace(text)
	if len(text) == 0 {
		return nil
	}
	runes := []rune(text)
	if len(runes) <= size {
		return []string{text}
	}

	// Split into paragraphs
	paragraphs := splitParagraphs(text)
	if len(paragraphs) == 0 {
		return []string{text}
	}

	var chunks []string
	var current strings.Builder
	var lastPara string // last paragraph added, used for overlap

	flush := func() {
		s := strings.TrimSpace(current.String())
		if s != "" {
			chunks = append(chunks, s)
		}
		current.Reset()
	}

	for _, para := range paragraphs {
		paraRunes := []rune(para)

		// If a single paragraph exceeds size, split it by size with overlap
		if len(paraRunes) > size {
			// Flush whatever we have first
			flush()
			// Hard-chunk the oversized paragraph
			subChunks := chunkBySize(para, size, overlap)
			chunks = append(chunks, subChunks...)
			if len(subChunks) > 0 {
				lastPara = subChunks[len(subChunks)-1]
			}
			continue
		}

		// Would adding this paragraph exceed the size limit?
		candidateLen := len([]rune(current.String())) + len(paraRunes)
		if current.Len() > 0 {
			candidateLen += 2 // account for "\n\n" separator
		}

		if candidateLen > size && current.Len() > 0 {
			flush()

			// Begin next chunk with overlap: use last paragraph as context
			if overlap > 0 && lastPara != "" {
				overlapText := lastParagraphContext(lastPara, overlap)
				if overlapText != "" {
					current.WriteString(overlapText)
				}
			}
		}

		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
		lastPara = para
	}
	flush()

	return chunks
}

// splitParagraphs splits text on blank lines, returning non-empty paragraphs.
func splitParagraphs(text string) []string {
	// Normalize line endings
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	rawParas := strings.Split(text, "\n\n")
	var result []string
	for _, p := range rawParas {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// lastParagraphContext returns up to `overlap` runes from the end of text,
// trimmed to a sentence boundary when possible.
func lastParagraphContext(text string, overlap int) string {
	runes := []rune(text)
	if len(runes) <= overlap {
		return text
	}
	// Take trailing `overlap` runes
	tail := string(runes[len(runes)-overlap:])
	// Try to find a sentence start within tail
	for _, sep := range []string{". ", "! ", "? ", "。", "！", "？"} {
		idx := strings.Index(tail, sep)
		if idx >= 0 && idx+len(sep) < len(tail) {
			return strings.TrimSpace(tail[idx+len(sep):])
		}
	}
	return strings.TrimSpace(tail)
}

// chunkBySize is the naive size-based chunker used for oversized paragraphs.
func chunkBySize(text string, size, overlap int) []string {
	var chunks []string
	runes := []rune(text)
	n := len(runes)

	step := size - overlap
	if step <= 0 {
		step = size
	}

	for start := 0; start < n; start += step {
		end := start + size
		if end > n {
			end = n
		}
		chunks = append(chunks, string(runes[start:end]))
		if end == n {
			break
		}
	}
	return chunks
}

// Summary returns a brief preview of a document.
func Summary(content string, maxLen int) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) <= maxLen {
		return content
	}
	return string(runes[:maxLen]) + "..."
}
