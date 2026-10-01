package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"shopee/backend/pkg/apperror"
)

type ReviewStatus string

const (
	ReviewPublished ReviewStatus = "published"
	ReviewHidden    ReviewStatus = "hidden"
	maxCommentRunes              = 2000
	maxNoteRunes                 = 1000
	maxReviewImages              = 5
)

type Review struct {
	ID, BuyerID, VendorID, ProductID, OrderItemID, VendorOrderID string
	Rating                                                       int
	Comment                                                      string
	Status                                                       ReviewStatus
	// VerifiedPurchase is set only when Order confirmed the purchase at
	// creation; imported or seeded rows are never verified.
	VerifiedPurchase bool
	// AuthorLabel is the masked public name ("N***A") snapshotted when the
	// review was written; nil shows the generic label.
	AuthorLabel              *string
	HiddenReasonID, HiddenBy *string
	HiddenNote               *string
	HiddenAt                 *time.Time
	CreatedAt, UpdatedAt     time.Time
}

type Image struct {
	ID, ReviewID, ObjectKey, URL, ContentType string
	SizeBytes                                 int64
	Position                                  int
	CreatedAt                                 time.Time
}

type Reply struct {
	ReviewID, VendorID, Message string
	CreatedAt, UpdatedAt        time.Time
}

type Reason struct {
	ID, Code, Label string
	Description     *string
	IsActive        bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ReportStatus string

const (
	ReportOpen     ReportStatus = "open"
	ReportResolved ReportStatus = "resolved"
)

type ReportDecision string

const (
	DecisionKeep ReportDecision = "keep"
	DecisionHide ReportDecision = "hide"
)

type Report struct {
	ID, ReviewID, ReportingVendorID, ReasonID, ReasonCode, ReasonLabel string
	Note                                                               *string
	Status                                                             ReportStatus
	Decision                                                           *ReportDecision
	ResolutionReasonID, ResolvedBy                                     *string
	ResolvedAt                                                         *time.Time
	ResolutionNote                                                     *string
	CreatedAt, UpdatedAt                                               time.Time
}

type Summary struct {
	RatingAverage float64
	RatingCount   int64
	Distribution  [5]int64
}

// CleanText normalizes user text before it is stored or shown: line breaks
// become "\n", other control characters and bidirectional overrides (used
// to disguise text) are removed, runs of blank lines are shortened and the
// ends trimmed. Rendering stays plain text; nothing is interpreted as HTML.
func CleanText(raw string) string {
	raw = strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	var b strings.Builder
	newlines := 0
	for _, r := range raw {
		switch {
		case r == '\n':
			newlines++
			if newlines > 2 {
				continue
			}
		case r == '\t':
			r = ' '
			newlines = 0
		case unicode.IsControl(r), r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200E, r == 0x200F, r == 0xFEFF:
			continue
		default:
			newlines = 0
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func ValidateReview(rating int, comment string) (string, error) {
	comment = CleanText(comment)
	if rating < 1 || rating > 5 {
		return "", apperror.Validation("Rating must be between 1 and 5")
	}
	if comment == "" {
		return "", apperror.Validation("Review comment is required")
	}
	if len([]rune(comment)) > maxCommentRunes {
		return "", apperror.Validation("Review comment must be at most 2000 characters")
	}
	return comment, nil
}

func ValidateReply(message string) (string, error) {
	message = CleanText(message)
	if message == "" || len([]rune(message)) > maxCommentRunes {
		return "", apperror.Validation("Reply must contain at most 2000 characters")
	}
	return message, nil
}

// ValidateNote cleans an optional note (report, moderation decision); an
// empty note is nil. required refuses an empty one.
func ValidateNote(note *string, required bool) (*string, error) {
	var clean string
	if note != nil {
		clean = CleanText(*note)
	}
	if clean == "" {
		if required {
			return nil, apperror.Validation("A note explaining the decision is required")
		}
		return nil, nil
	}
	if len([]rune(clean)) > maxNoteRunes {
		return nil, apperror.Validation("A note must be at most 1000 characters")
	}
	return &clean, nil
}

var reasonCode = regexp.MustCompile(`^[a-z0-9_]{2,40}$`)

// ValidateReason checks a moderation reason's code, label and description.
func ValidateReason(code, label string, description *string) (string, string, *string, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	label = CleanText(label)
	if !reasonCode.MatchString(code) {
		return "", "", nil, apperror.Validation("Reason code must be 2-40 lowercase letters, digits or underscores")
	}
	if label == "" || len([]rune(label)) > 100 {
		return "", "", nil, apperror.Validation("Reason label must be 1-100 characters")
	}
	desc, err := ValidateNote(description, false)
	if err != nil {
		return "", "", nil, apperror.Validation("Reason description must be at most 1000 characters")
	}
	return code, label, desc, nil
}

// MaskName turns a full name into the public author label: its first and
// last letters around asterisks ("Nguyen Van An" -> "N***n"), so a review
// never shows who wrote it in full. An empty name gives "".
func MaskName(full string) string {
	letters := make([]rune, 0, len(full))
	for _, r := range CleanText(full) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letters = append(letters, r)
		}
	}
	switch len(letters) {
	case 0:
		return ""
	case 1:
		return string(letters[0]) + "***"
	}
	return string(letters[0]) + "***" + string(letters[len(letters)-1])
}

func MaxImages() int { return maxReviewImages }
