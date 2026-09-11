package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"shopee/backend/services/catalog/internal/domain"
)

// uniqueSlug derives a slug from name and appends a short random suffix if
// it collides, retrying a bounded number of times so a burst of identically
// named items can't loop forever.
func uniqueSlug(ctx context.Context, name string, exists func(ctx context.Context, slug string) (bool, error)) (string, error) {
	base := domain.Slugify(name)
	if base == "" {
		base = "item"
	}

	slug := base
	for attempt := 0; attempt < 5; attempt++ {
		taken, err := exists(ctx, slug)
		if err != nil {
			return "", err
		}
		if !taken {
			return slug, nil
		}

		suffix, err := randomHex(3)
		if err != nil {
			return "", err
		}
		slug = fmt.Sprintf("%s-%s", base, suffix)
	}

	return slug, nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
