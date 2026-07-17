package chat

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

const maxPDFBytes = 20 << 20 // 20 MB

var supportedPDFExts = map[string]bool{".pdf": true}

// loadPDFPart resolves path against workDir (~ expands), validates size,
// reads the raw bytes, and returns an OpenAI-compatible "file" ContentPart
// carrying a base64 data-URI (file_data) plus a human-readable description.
// The PDF is never parsed or decoded — only its bytes are attached.
func loadPDFPart(path, workDir string) (llm.ContentPart, string, error) {
	path = resolvePath(path, workDir)

	info, err := os.Stat(path)
	if err != nil {
		return llm.ContentPart{}, "", fmt.Errorf("cannot read %q: %w", path, err)
	}
	if info.Size() > maxPDFBytes {
		return llm.ContentPart{}, "", fmt.Errorf("pdf too large (%d MB, max 20 MB)", info.Size()>>20)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return llm.ContentPart{}, "", fmt.Errorf("cannot read %q: %w", path, err)
	}

	return pdfPartFromBytes(raw, filepath.Base(path))
}

// pdfPartFromBytes validates the size cap and wraps raw PDF bytes as a
// base64 "file" ContentPart. If filename is empty (the bytes-based path has
// no path to derive one from), "file.pdf" is synthesized.
func pdfPartFromBytes(data []byte, filename string) (llm.ContentPart, string, error) {
	if len(data) > maxPDFBytes {
		return llm.ContentPart{}, "", fmt.Errorf("pdf too large (%d MB, max 20 MB)", len(data)>>20)
	}
	if strings.TrimSpace(filename) == "" {
		filename = "file.pdf"
	}
	desc := fmt.Sprintf("pdf %s (%d KB)", filename, len(data)>>10)
	return llm.ContentPart{
		Type:     llm.ContentPartTypeFile,
		FileName: filename,
		FileData: "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data),
	}, desc, nil
}
