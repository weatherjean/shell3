package chat

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

// tinyPDF is a minimal (invalid but byte-shaped) PDF; we never decode it, so
// its internal structure doesn't matter for these tests.
var tinyPDF = []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF")

func TestLoadPDFPart(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "doc.pdf")
	writeBytes(t, p, tinyPDF)

	part, desc, err := loadPDFPart(p, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if part.Type != llm.ContentPartTypeFile {
		t.Errorf("type = %q, want file", part.Type)
	}
	if !strings.HasPrefix(part.FileData, "data:application/pdf;base64,") {
		t.Errorf("file data prefix wrong: %.40s", part.FileData)
	}
	if part.FileName != "doc.pdf" {
		t.Errorf("filename = %q, want doc.pdf", part.FileName)
	}
	if !strings.Contains(desc, "pdf doc.pdf") {
		t.Errorf("desc = %q", desc)
	}
}

func TestLoadPDFPart_Missing(t *testing.T) {
	if _, _, err := loadPDFPart("/no/such/doc.pdf", ""); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadPDFPart_RelativePath(t *testing.T) {
	tmp := t.TempDir()
	writeBytes(t, filepath.Join(tmp, "a.pdf"), tinyPDF)
	if _, _, err := loadPDFPart("a.pdf", tmp); err != nil {
		t.Fatalf("relative path with workDir failed: %v", err)
	}
}

func TestPDFPartFromBytes_TooLarge(t *testing.T) {
	if _, _, err := pdfPartFromBytes(make([]byte, maxPDFBytes+1), "big.pdf"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("pdf cap not enforced: %v", err)
	}
}

func TestPDFPartFromBytes_NoFilename(t *testing.T) {
	part, _, err := pdfPartFromBytes(tinyPDF, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if part.FileName != "file.pdf" {
		t.Errorf("filename = %q, want synthesized file.pdf", part.FileName)
	}
}
