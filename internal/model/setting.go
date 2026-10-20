package model

import "encoding/json"

// Domain system settings the knowledge base applies.
const (
	// SettingTagsFiltrationMode decides how a tag filter combines its tags when
	// the caller does not say.
	SettingTagsFiltrationMode = "tags_filtration_mode"
	// SettingVersionLimit is how many latest versions an article history shows.
	SettingVersionLimit = "knowledge_base_version_limit"
)

// DefaultVersionLimit is the history size of a domain that has not set it.
const DefaultVersionLimit = 10

// TagMatch says how a tag filter combines its tags.
type TagMatch int32

const (
	// TagMatchDefault leaves the choice to the domain setting.
	TagMatchDefault TagMatch = iota
	// TagMatchAll keeps articles carrying every tag.
	TagMatchAll
	// TagMatchAny keeps articles carrying any of the tags.
	TagMatchAny
)

// All reports whether the filter needs every tag; the default needs any.
func (m TagMatch) All() bool {
	return m == TagMatchAll
}

// ParseTagsFiltrationMode reads the stored value of the tags filtration mode
// setting; ok is false for a value that is not a mode.
func ParseTagsFiltrationMode(raw []byte) (TagMatch, bool) {
	var mode string
	if err := json.Unmarshal(raw, &mode); err != nil {
		return TagMatchDefault, false
	}

	switch mode {
	case "AND":
		return TagMatchAll, true
	case "OR":
		return TagMatchAny, true
	default:
		return TagMatchDefault, false
	}
}

// ParseVersionLimit reads the stored value of the version limit setting; ok is
// false for a value that is not a positive whole number.
func ParseVersionLimit(raw []byte) (int, bool) {
	var limit int
	if err := json.Unmarshal(raw, &limit); err != nil || limit < 1 {
		return 0, false
	}

	return limit, true
}
