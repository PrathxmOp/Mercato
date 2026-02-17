package i18n

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
)

type contextKey string

const langKey contextKey = "lang"

var (
	translations = make(map[string]map[string]interface{})
	mu           sync.RWMutex
)

// Init loads all JSON files from the provided filesystem
func Init(f fs.FS) error {
	mu.Lock()
	defer mu.Unlock()

	// Clear existing translations if any
	translations = make(map[string]map[string]interface{})

	files, err := fs.ReadDir(f, ".")
	if err != nil {
		return err
	}

	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".json" {
			lang := strings.TrimSuffix(file.Name(), ".json")
			data, err := fs.ReadFile(f, file.Name())
			if err != nil {
				return err
			}

			var t map[string]interface{}
			if err := json.Unmarshal(data, &t); err != nil {
				return err
			}
			translations[lang] = t
		}
	}

	return nil
}

// T returns the translated string for a key. Key can be "nested.key"
func T(lang, key string, args ...interface{}) string {
	mu.RLock()
	defer mu.RUnlock()

	if lang == "" {
		lang = "en"
	}

	langTranslations, ok := translations[lang]
	if !ok {
		langTranslations = translations["en"]
	}

	if langTranslations == nil {
		return key
	}

	parts := strings.Split(key, ".")
	var current interface{} = langTranslations

	for _, part := range parts {
		if m, ok := current.(map[string]interface{}); ok {
			current = m[part]
		} else {
			return key
		}
	}

	if s, ok := current.(string); ok {
		if len(args) > 0 {
			return fmt.Sprintf(s, args...)
		}
		return s
	}

	return key
}

// GetLang returns the language from context
func GetLang(ctx context.Context) string {
	if lang, ok := ctx.Value(langKey).(string); ok {
		return lang
	}
	return "en"
}

// ContextWithLang returns a new context with the language set
func ContextWithLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, langKey, lang)
}
