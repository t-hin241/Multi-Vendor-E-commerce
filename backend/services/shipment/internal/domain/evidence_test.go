package domain

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestSanitizeEvidenceChecksTheContent(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	if _, typ, ext, err := SanitizeEvidence(buf.Bytes()); err != nil || typ != "image/png" || ext != ".png" {
		t.Fatalf("png: %s %s %v", typ, ext, err)
	}
	mp4 := append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2'}, make([]byte, 64)...)
	if _, typ, _, err := SanitizeEvidence(mp4); err != nil || typ != "video/mp4" {
		t.Fatalf("mp4: %s %v", typ, err)
	}
	for name, data := range map[string][]byte{"empty": nil, "pdf": []byte("%PDF-1.4 fake"), "html": []byte("<html><script>x</script>")} {
		if _, _, _, err := SanitizeEvidence(data); err != ErrEvidenceUnsupported {
			t.Errorf("%s must be refused, got %v", name, err)
		}
	}
	big := append(bytes.Clone(mp4), make([]byte, MaxEvidenceVideoBytes)...)
	if _, _, _, err := SanitizeEvidence(big); err != ErrEvidenceTooLarge {
		t.Errorf("an oversized video is refused, got %v", err)
	}
}
