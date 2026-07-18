package chat

import (
	"encoding/base64"
	"fmt"
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
	raw, resolved, err := readMediaFile(path, workDir, "pdf", maxPDFBytes)
	if err != nil {
		return llm.ContentPart{}, "", err
	}
	return pdfPartFromBytes(raw, filepath.Base(resolved))
}

// pdfPartFromBytes validates the size cap and wraps raw PDF bytes as a
// base64 "file" ContentPart. If filename is empty (the bytes-based path has
// no path to derive one from), "file.pdf" is synthesized.
func pdfPartFromBytes(data []byte, filename string) (llm.ContentPart, string, error) {
	if len(data) > maxPDFBytes {
		return llm.ContentPart{}, "", mediaTooLarge("pdf", int64(len(data)), maxPDFBytes)
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
