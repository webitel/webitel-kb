package model

import "golang.org/x/text/language"

// TextSearchFallback is the dictionary of a language Postgres has no stemmer for.
const TextSearchFallback = "simple"

// snowballByLanguage names the built-in Postgres stemmer of a base language.
var snowballByLanguage = map[string]string{
	"ar": "arabic", "hy": "armenian", "eu": "basque", "ca": "catalan",
	"da": "danish", "nl": "dutch", "en": "english", "fi": "finnish",
	"fr": "french", "de": "german", "el": "greek", "hi": "hindi",
	"hu": "hungarian", "id": "indonesian", "ga": "irish", "it": "italian",
	"lt": "lithuanian", "ne": "nepali", "nb": "norwegian", "nn": "norwegian",
	"no": "norwegian", "pt": "portuguese", "ro": "romanian", "ru": "russian",
	"sr": "serbian", "es": "spanish", "sv": "swedish", "ta": "tamil",
	"tr": "turkish", "yi": "yiddish",
}

// CanonicalLanguage validates a BCP 47 tag and returns its canonical form.
func CanonicalLanguage(tag string) (string, bool) {
	parsed, err := language.Parse(tag)
	if err != nil || parsed == language.Und {
		return "", false
	}

	return parsed.String(), true
}

// TextSearchDictionary names the stemmer a space language builds its search
// vectors with.
func TextSearchDictionary(tag string) string {
	parsed, err := language.Parse(tag)
	if err != nil {
		return TextSearchFallback
	}

	base, _ := parsed.Base()
	if name, ok := snowballByLanguage[base.String()]; ok {
		return name
	}

	return TextSearchFallback
}
