package domain

import (
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"mime"
	"net/http"
	"net/url"
	"strings"

	_ "golang.org/x/image/webp"
	"golang.org/x/net/html"
	"shopee/backend/pkg/apperror"
)

// ValidateMediaBytes checks the actual content before decoding or storing it.
func ValidateMediaBytes(claimed string, data []byte, imageOnly bool) (MediaKind, string, string, error) {
	contentType, _, err := mime.ParseMediaType(claimed)
	if err != nil {
		return "", "", "", apperror.Validation("Invalid media content type")
	}
	kind, ext, err := ValidateMediaUpload(contentType, int64(len(data)))
	if err != nil {
		return "", "", "", err
	}
	if imageOnly && kind != MediaKindImage {
		return "", "", "", apperror.Validation("A product image must be JPEG, PNG or WebP")
	}
	if http.DetectContentType(data) != contentType {
		return "", "", "", apperror.Validation("Media content does not match its content type")
	}
	if kind == MediaKindImage {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
			return "", "", "", apperror.Validation("Image must be valid, at most 8192 pixels per side and 16 megapixels")
		}
		if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
			return "", "", "", apperror.Validation("Image data is incomplete or invalid")
		}
	}
	return kind, ext, contentType, nil
}

// SafeMediaURL allows absolute HTTP(S) URLs without credentials.
func SafeMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

// SanitizeDescription keeps formatting while stripping executable elements and attributes.
func SanitizeDescription(raw string) string {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return ""
	}
	allowed := map[string]bool{"p": true, "br": true, "b": true, "strong": true, "i": true, "em": true, "u": true, "ul": true, "ol": true, "li": true, "blockquote": true, "h2": true, "h3": true, "h4": true, "pre": true, "code": true}
	blocked := map[string]bool{"script": true, "style": true, "iframe": true, "object": true, "embed": true, "svg": true, "math": true, "template": true, "noscript": true}
	var render func(*html.Node) string
	render = func(n *html.Node) string {
		if n.Type == html.TextNode {
			var out bytes.Buffer
			_ = html.Render(&out, n)
			return out.String()
		}
		if n.Type == html.CommentNode || blocked[n.Data] {
			return ""
		}
		var content strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			content.WriteString(render(c))
		}
		if !allowed[n.Data] {
			return content.String()
		}
		if n.Data == "br" {
			return "<br>"
		}
		return "<" + n.Data + ">" + content.String() + "</" + n.Data + ">"
	}
	return render(doc)
}
