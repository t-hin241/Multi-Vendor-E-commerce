package domain

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestValidateReview(t *testing.T) {
	t.Run("normalizes a valid comment", func(t *testing.T) {
		comment, err := ValidateReview(5, "  useful product  ")
		if err != nil || comment != "useful product" {
			t.Fatalf("ValidateReview() = %q, %v", comment, err)
		}
	})
	for _, tc := range []struct {
		name    string
		rating  int
		comment string
	}{
		{"rating below range", 0, "comment"},
		{"rating above range", 6, "comment"},
		{"empty comment", 4, "  "},
		{"only control characters", 4, "\u0000\u202e\u200f"},
		{"comment is too long", 4, strings.Repeat("a", 2001)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateReview(tc.rating, tc.comment); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCleanTextKeepsPlainTextOnly(t *testing.T) {
	got := CleanText("Tốt\r\n\r\n\r\n\r\nlắm\u0007\t<b>x</b> \u202eevil\u202c ")
	if got != "Tốt\n\nlắm <b>x</b> evil" {
		t.Fatalf("CleanText() = %q", got)
	}
}

func TestMaskNameRevealsTwoLettersAtMost(t *testing.T) {
	for in, want := range map[string]string{
		"Nguyễn Văn An":    "N***n",
		"  Ann ":           "A***n",
		"B":                "B***",
		"":                 "",
		"   \u0000":        "",
		"buyer@example.vn": "b***n",
	} {
		if got := MaskName(in); got != want {
			t.Errorf("MaskName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateNoteAndReason(t *testing.T) {
	if n, err := ValidateNote(nil, false); err != nil || n != nil {
		t.Fatal("an absent optional note is nil")
	}
	blank := "  "
	if _, err := ValidateNote(&blank, true); err == nil {
		t.Fatal("a required note cannot be blank")
	}
	long := strings.Repeat("x", 1001)
	if _, err := ValidateNote(&long, false); err == nil {
		t.Fatal("a note is at most 1000 characters")
	}
	if code, label, _, err := ValidateReason(" Spam_Link ", " Spam ", nil); err != nil || code != "spam_link" || label != "Spam" {
		t.Fatalf("ValidateReason() = %q %q %v", code, label, err)
	}
	for _, code := range []string{"x", "has space", "dấu", strings.Repeat("a", 41)} {
		if _, _, _, err := ValidateReason(code, "Label", nil); err == nil {
			t.Errorf("code %q must be refused", code)
		}
	}
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x * 10), uint8(y * 10), 100, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withExif inserts an APP1 Exif segment (orientation plus a fake GPS
// marker string) after the JPEG's SOI marker.
func withExif(data []byte, orientation uint16) []byte {
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0}
	entry := make([]byte, 12)
	binary.LittleEndian.PutUint16(entry[0:], 0x0112)
	binary.LittleEndian.PutUint16(entry[2:], 3)
	binary.LittleEndian.PutUint32(entry[4:], 1)
	binary.LittleEndian.PutUint16(entry[8:], orientation)
	tiff = append(tiff, entry...)
	tiff = append(tiff, 0, 0, 0, 0)
	tiff = append(tiff, []byte("GPS 21.0285N 105.8542E")...)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	seg = append(seg, payload...)
	return append(append([]byte{0xFF, 0xD8}, seg...), data[2:]...)
}

func TestPrepareImageStripsMetadataAndTurnsUpright(t *testing.T) {
	raw := withExif(encodeJPEG(t, 8, 4), 6)
	if jpegOrientation(raw) != 6 {
		t.Fatal("test image must carry orientation 6")
	}
	out, err := PrepareImage("image/jpeg", raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.ContentType != "image/jpeg" || out.Ext != ".jpg" || bytes.Contains(out.Data, []byte("Exif")) || bytes.Contains(out.Data, []byte("GPS")) {
		t.Fatal("the stored image must not keep camera metadata")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out.Data))
	if err != nil || cfg.Width != 4 || cfg.Height != 8 {
		t.Fatalf("expected the photo turned upright (4x8), got %dx%d %v", cfg.Width, cfg.Height, err)
	}
}

func TestOrientMapsEveryCorner(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	src.Set(0, 0, color.NRGBA{255, 0, 0, 255}) // top-left marker
	for o, want := range map[int]image.Point{1: {0, 0}, 2: {2, 0}, 3: {2, 1}, 4: {0, 1}, 5: {0, 0}, 6: {1, 0}, 7: {1, 2}, 8: {0, 2}} {
		got := orient(src, o)
		if got.NRGBAAt(want.X, want.Y).R != 255 {
			t.Errorf("orientation %d: top-left pixel not at %v", o, want)
		}
	}
}

func TestPrepareImageRefusesBadInput(t *testing.T) {
	jpg := encodeJPEG(t, 4, 4)
	var wide bytes.Buffer
	_ = png.Encode(&wide, image.NewGray(image.Rect(0, 0, 9000, 1)))
	for name, tc := range map[string]struct {
		claimed string
		data    []byte
	}{
		"unsupported type":        {"image/gif", []byte("GIF89a")},
		"type does not match":     {"image/png", jpg},
		"empty":                   {"image/jpeg", nil},
		"too large":               {"image/jpeg", append(jpg, make([]byte, MaxImageBytes)...)},
		"too wide":                {"image/png", wide.Bytes()},
		"truncated":               {"image/jpeg", jpg[:len(jpg)/2]},
		"script disguised as jpg": {"image/jpeg", []byte("<script>alert(1)</script>")},
	} {
		if _, err := PrepareImage(tc.claimed, tc.data); err == nil {
			t.Errorf("%s: expected refusal", name)
		}
	}
}

func TestPrepareImageKeepsPNGAsPNGWithoutTextChunks(t *testing.T) {
	var b bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{1, 2, 3, 128})
	_ = png.Encode(&b, img)
	data := b.Bytes()
	// Add a tEXt chunk after IHDR (8-byte signature + 25-byte IHDR chunk).
	text := []byte("Comment\x00owner phone 0900000000")
	chunk := make([]byte, 8)
	binary.BigEndian.PutUint32(chunk, uint32(len(text)))
	copy(chunk[4:], "tEXt")
	chunk = append(chunk, text...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	data = append(append(append([]byte{}, data[:33]...), chunk...), data[33:]...)
	out, err := PrepareImage("image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	if out.ContentType != "image/png" || bytes.Contains(out.Data, []byte("owner phone")) {
		t.Fatal("PNG text metadata must be dropped")
	}
}
