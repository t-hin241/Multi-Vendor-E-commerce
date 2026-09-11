package domain

import (
	"regexp"
	"strings"
)

var slugInvalidChars = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify derives a URL-safe slug from a display name. Callers are
// responsible for resolving collisions (e.g. by appending a short suffix)
// since uniqueness is a persistence concern, not a domain rule.
func Slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugInvalidChars.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
