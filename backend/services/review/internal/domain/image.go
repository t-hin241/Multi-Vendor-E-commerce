package domain

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"mime"
	"net/http"

	_ "golang.org/x/image/webp" // decoder for uploaded WebP photos

	"shopee/backend/pkg/apperror"
)

const (
	MaxImageBytes  = 5 * 1024 * 1024
	maxImageSide   = 8192
	maxImagePixels = 16_000_000
)

// PreparedImage is what is stored for an uploaded photo: decoded and encoded
// again, so camera metadata (GPS location, device, owner) never becomes
// public, with the photo turned upright as its EXIF orientation says.
type PreparedImage struct {
	Data        []byte
	ContentType string
	Ext         string
}

// PrepareImage checks an uploaded photo (declared type, real content,
// size, dimensions, complete data) and re-encodes it: PNG or transparent
// images as PNG, others as JPEG.
func PrepareImage(claimed string, data []byte) (*PreparedImage, error) {
	contentType, _, err := mime.ParseMediaType(claimed)
	if err != nil || (contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp") {
		return nil, apperror.Validation("Image must be JPEG, PNG or WebP")
	}
	if len(data) == 0 || len(data) > MaxImageBytes {
		return nil, apperror.Validation("Image must be no larger than 5MB")
	}
	if http.DetectContentType(data) != contentType {
		return nil, apperror.Validation("Image content does not match its declared type")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxImageSide || cfg.Height > maxImageSide ||
		int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return nil, apperror.Validation("Image must be valid, at most 8192 pixels per side and 16 megapixels")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, apperror.Validation("Image data is incomplete or invalid")
	}
	orientation := 1
	if contentType == "image/jpeg" {
		orientation = jpegOrientation(data)
	}
	upright := orient(img, orientation)
	var out bytes.Buffer
	if contentType == "image/png" || !upright.Opaque() {
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, upright); err != nil {
			return nil, apperror.Validation("Image could not be processed")
		}
		return &PreparedImage{Data: out.Bytes(), ContentType: "image/png", Ext: ".png"}, nil
	}
	if err := jpeg.Encode(&out, upright, &jpeg.Options{Quality: 88}); err != nil {
		return nil, apperror.Validation("Image could not be processed")
	}
	return &PreparedImage{Data: out.Bytes(), ContentType: "image/jpeg", Ext: ".jpg"}, nil
}

// orient copies img into an NRGBA image turned as EXIF orientation o
// (1-8) asks: 2 mirror, 3 rotate 180, 4 flip, 5 transpose, 6 rotate 90
// clockwise, 7 transverse, 8 rotate 90 counter-clockwise.
func orient(img image.Image, o int) *image.NRGBA {
	b := img.Bounds()
	src := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)
	if o < 2 || o > 8 {
		return src
	}
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			si, di := src.PixOffset(sx, sy), dst.PixOffset(x, y)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

// jpegOrientation reads the EXIF orientation tag (1 when absent or
// unreadable) from a JPEG's APP1 segment.
func jpegOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		switch {
		case marker == 0xFF:
			i++
			continue
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			i += 2
			continue
		case marker == 0xDA || marker == 0xD9:
			return 1
		}
		size := int(b[i+2])<<8 | int(b[i+3])
		if size < 2 || i+2+size > len(b) {
			return 1
		}
		if seg := b[i+4 : i+2+size]; marker == 0xE1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	off := int(order.Uint32(t[4:8]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	entries := int(order.Uint16(t[off:]))
	for k := 0; k < entries; k++ {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 1
		}
		if order.Uint16(t[e:]) == 0x0112 {
			if v := int(order.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
