package nlp

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ─── URLs ────────────────────────────────────────────────────────────────────

const (
	hfBase    = "https://huggingface.co/Xenova/bert-base-multilingual-cased-ner-hrl/resolve/main"
	modelURL  = hfBase + "/onnx/model_quantized.onnx"
	tokURL    = hfBase + "/tokenizer.json"
	vocabURL  = hfBase + "/vocab.txt"
	ortVer    = "1.20.1"
	ortBase   = "https://github.com/microsoft/onnxruntime/releases/download/v" + ortVer
)

// ortArchiveURL returns the platform-specific ONNX Runtime archive URL.
func ortArchiveURL() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return ortBase + "/onnxruntime-osx-arm64-" + ortVer + ".tgz", nil
		}
		return ortBase + "/onnxruntime-osx-x86_64-" + ortVer + ".tgz", nil
	case "linux":
		return ortBase + "/onnxruntime-linux-x64-" + ortVer + ".tgz", nil
	case "windows":
		return ortBase + "/onnxruntime-win-x64-" + ortVer + ".zip", nil
	default:
		return "", fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

// ortLibName returns the shared-library filename for the current platform.
func ortLibName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libonnxruntime." + ortVer + ".dylib"
	case "windows":
		return "onnxruntime.dll"
	default:
		return "libonnxruntime.so." + ortVer
	}
}

// ─── Public API ──────────────────────────────────────────────────────────────

// ModelDir returns ~/.fishnet/models/ner/ and creates it if necessary.
func ModelDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".fishnet", "models", "ner")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create model directory %s: %w", dir, err)
	}
	return dir, nil
}

// IsModelReady reports whether all required model files are present.
func IsModelReady() (bool, error) {
	dir, err := ModelDir()
	if err != nil {
		return false, err
	}
	required := []string{
		filepath.Join(dir, "model_quantized.onnx"),
		filepath.Join(dir, "tokenizer.json"),
		filepath.Join(dir, "vocab.txt"),
	}
	for _, p := range required {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return false, nil
		} else if err != nil {
			return false, err
		}
	}
	libPath, err := LibraryPath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(libPath); os.IsNotExist(err) {
		return false, nil
	}
	return true, nil
}

// EnsureModels downloads any missing model files. When verbose is true it
// prints download progress to stdout.
func EnsureModels(verbose bool) error {
	dir, err := ModelDir()
	if err != nil {
		return err
	}

	// Model files from HuggingFace.
	type modelFile struct {
		name string
		url  string
	}
	files := []modelFile{
		{"model_quantized.onnx", modelURL},
		{"tokenizer.json", tokURL},
		{"vocab.txt", vocabURL},
	}
	for _, f := range files {
		dest := filepath.Join(dir, f.name)
		if shouldSkip(dest) {
			if verbose {
				fmt.Printf("[nlp] skipping %s (already exists)\n", f.name)
			}
			continue
		}
		if verbose {
			fmt.Printf("[nlp] downloading %s …\n", f.name)
		}
		if err := downloadFile(f.url, dest, verbose); err != nil {
			return fmt.Errorf("failed to download %s from %s: %w", f.name, f.url, err)
		}
	}

	// ONNX Runtime library.
	libPath, err := LibraryPath()
	if err != nil {
		return err
	}
	if !shouldSkip(libPath) {
		archURL, err := ortArchiveURL()
		if err != nil {
			return err
		}
		ortDir := filepath.Join(dir, "ort")
		if err := os.MkdirAll(ortDir, 0o755); err != nil {
			return fmt.Errorf("cannot create ort directory: %w", err)
		}
		archiveName := filepath.Base(archURL)
		archiveDest := filepath.Join(ortDir, archiveName)
		if verbose {
			fmt.Printf("[nlp] downloading ONNX Runtime from %s …\n", archURL)
		}
		if err := downloadFile(archURL, archiveDest, verbose); err != nil {
			return fmt.Errorf("failed to download ONNX Runtime from %s: %w", archURL, err)
		}
		if strings.HasSuffix(archiveName, ".tgz") || strings.HasSuffix(archiveName, ".tar.gz") {
			if verbose {
				fmt.Printf("[nlp] extracting %s …\n", archiveName)
			}
			if err := extractTGZ(archiveDest, ortDir); err != nil {
				return fmt.Errorf("failed to extract %s: %w", archiveDest, err)
			}
		} else if strings.HasSuffix(archiveName, ".zip") {
			if verbose {
				fmt.Printf("[nlp] extracting %s …\n", archiveName)
			}
			if err := extractZIP(archiveDest, ortDir); err != nil {
				return fmt.Errorf("failed to extract %s: %w", archiveDest, err)
			}
		}
	} else if verbose {
		fmt.Printf("[nlp] skipping ONNX Runtime (already exists at %s)\n", libPath)
	}

	return nil
}

// LibraryPath returns the path to the extracted ONNX Runtime shared library.
func LibraryPath() (string, error) {
	dir, err := ModelDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ort", ortLibName()), nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// shouldSkip returns true when the file already exists and is larger than 1 MB,
// indicating a previous complete download.
func shouldSkip(path string) bool {
	const minSize = 1 << 20 // 1 MB
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Size() > minSize
}

// downloadFile fetches url and atomically writes it to destPath.
// A ".tmp" staging file is used; on failure it is removed.
// When verbose is true, progress is printed every 10 MB.
func downloadFile(url, destPath string, verbose bool) error {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned HTTP %d", url, resp.StatusCode)
	}

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmpPath, err)
	}

	cleanup := func() {
		f.Close()
		os.Remove(tmpPath)
	}

	const chunkSize = 10 << 20 // 10 MB
	buf := make([]byte, 32*1024)
	var written int64
	var nextReport int64 = chunkSize

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				cleanup()
				return fmt.Errorf("write %s: %w", tmpPath, writeErr)
			}
			written += int64(n)
			if verbose && written >= nextReport {
				fmt.Printf("[nlp]   … %.0f MB downloaded\n", float64(written)/(1<<20))
				nextReport += chunkSize
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			cleanup()
			return fmt.Errorf("read from %s: %w", url, readErr)
		}
	}

	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %s → %s: %w", tmpPath, destPath, err)
	}
	return nil
}

// extractTGZ decompresses a .tgz archive into destDir.
func extractTGZ(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip open %s: %w", archivePath, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read %s: %w", archivePath, err)
		}
		target := filepath.Join(destDir, filepath.Base(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFromReader(target, tr); err != nil {
				return err
			}
		}
	}
	return nil
}

// extractZIP decompresses a .zip archive into destDir.
func extractZIP(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("zip open %s: %w", archivePath, err)
	}
	defer r.Close()

	for _, f := range r.File {
		target := filepath.Join(destDir, filepath.Base(f.Name))
		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0o755)
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("zip entry %s: %w", f.Name, err)
		}
		writeErr := writeFromReader(target, rc)
		rc.Close()
		if writeErr != nil {
			return writeErr
		}
	}
	return nil
}

// writeFromReader writes all bytes from r into the file at path.
func writeFromReader(path string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, r); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
