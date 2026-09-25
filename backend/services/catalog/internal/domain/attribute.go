package domain

import (
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

type DataType string

const (
	DataTypeText        DataType = "text"
	DataTypeNumber      DataType = "number"
	DataTypeBoolean     DataType = "boolean"
	DataTypeSelect      DataType = "select"
	DataTypeMultiSelect DataType = "multi_select"
)

func (t DataType) IsChoice() bool {
	return t == DataTypeSelect || t == DataTypeMultiSelect
}

type Attribute struct {
	ID                string
	Code              string
	Name              string
	DataType          DataType
	Unit              *string
	IsActive          bool
	IsVariantDefining bool
	CreatedAt         time.Time
}

type AttributeOption struct {
	ID          string
	AttributeID string
	Value       string
	Position    int
	CreatedAt   time.Time
}

// CategoryAttributeRule is one insert-only, versioned row: editing a rule
// for a (category, attribute) pair means inserting a new row with
// Version = previous + 1, never updating or deleting the old one — so a
// ProductAttributeValue.RuleID reference is never retroactively changed by
// a later rule edit. IsExcluded (attribute removed entirely for this branch)
// is distinct from IsRequired == false (attribute kept, but optional).
type CategoryAttributeRule struct {
	ID          string
	CategoryID  string
	AttributeID string
	Version     int
	IsRequired  bool
	IsExcluded  bool
	Position    int
	CreatedBy   *string
	CreatedAt   time.Time
}

// ProductAttributeValue is one captured value on a product. Exactly one of
// OptionID, ValueText, ValueNumber, ValueBoolean is populated, matching the
// owning Attribute's DataType — enforced by the use case, not the DB.
type ProductAttributeValue struct {
	ID           string
	ProductID    string
	AttributeID  string
	RuleID       *string
	OptionID     *string
	ValueText    *string
	ValueNumber  *float64
	ValueBoolean *bool
	CreatedAt    time.Time
}

// ResolvedAttribute is one entry of a category's effective,
// inheritance-merged attribute template: what ResolveTemplate and the
// attribute-template API return for the frontend to render a form field
// from.
type ResolvedAttribute struct {
	Attribute Attribute
	Options   []AttributeOption
	RuleID    string
	Required  bool
	Position  int
}

func ValidateAttributeCode(code string) error {
	if strings.TrimSpace(code) == "" {
		return apperror.Validation("Attribute code is required")
	}
	return nil
}

func ValidateAttributeName(name string) error {
	if strings.TrimSpace(name) == "" {
		return apperror.Validation("Attribute name is required")
	}
	return nil
}

func ValidateDataType(dataType DataType) error {
	switch dataType {
	case DataTypeText, DataTypeNumber, DataTypeBoolean, DataTypeSelect, DataTypeMultiSelect:
		return nil
	default:
		return apperror.Validation("Invalid attribute data type")
	}
}

func ValidateAttributeOptionValue(value string) error {
	if strings.TrimSpace(value) == "" {
		return apperror.Validation("Option value is required")
	}
	return nil
}
