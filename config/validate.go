package config

import (
	"fmt"
	"github.com/BurntSushi/toml"
	"github.com/agext/levenshtein"
	"reflect"
	"sort"
	"strings"
	"unicode"
)

// UnexpectedKey represents result of validation
type UnexpectedKey struct {
	Name        string
	SuggestName string
}

func normalizeKeyname(f reflect.StructField) string {
	name := string(unicode.ToLower([]rune(f.Name)[0])) + f.Name[1:]
	if s := f.Tag.Get("toml"); s != "" {
		name = s
	}
	return name
}

func addKeynametoSuggestions(f reflect.StructField, suggestions *[]string) *[]string {
	name := normalizeKeyname(f)
	*suggestions = append(*suggestions, name)
	return suggestions
}

func makeSuggestions(t reflect.Type) []string {
	if t.Kind() == reflect.Map || t.Kind() == reflect.Ptr {
		return makeSuggestions(t.Elem())
	}
	var suggestions []string
	fields := reflect.VisibleFields(t)
	for _, f := range fields {
		if s := f.Tag.Get("conf"); s == "ignore" {
			continue
		}
		if s := f.Tag.Get("conf"); s == "parent" {
			addKeynametoSuggestions(f, &suggestions)
			childSuggestions := makeSuggestions(f.Type)
			suggestions = append(suggestions, childSuggestions...)
			continue
		}
		addKeynametoSuggestions(f, &suggestions)
	}

	return suggestions
}

func nameSuggestion(given string, suggestions []string) string {
	for _, suggestion := range suggestions {
		dist := levenshtein.Distance(given, suggestion, nil)
		if dist < 3 {
			return suggestion
		}
	}
	return ""
}

// ValidateConfigFile detect unexpected key in configfile
func ValidateConfigFile(file string) ([]UnexpectedKey, error) {
	config := &Config{}
	md, err := toml.DecodeFile(file, config)
	if err != nil {
		return nil, fmt.Errorf("failed to test config: %s", err)
	}

	var c Config
	suggestions := makeSuggestions(reflect.TypeOf(c))

	var parentKeys []string
	configFields := reflect.VisibleFields(reflect.TypeOf(c))
	for _, f := range configFields {
		if s := f.Tag.Get("conf"); s == "parent" {
			parentKeys = append(parentKeys, normalizeKeyname(f))
		}
	}

	var unexpectedKeys []UnexpectedKey
	for _, v := range md.Undecoded() {
		splitedKey := strings.Split(v.String(), ".")
		key := splitedKey[0]
		if containKeyName(parentKeys, key) {
			/*
					if conffile is following, UnexpectedKey.SuggestName should be `filesystems.use_mountpoint`, not `filesystems`
					```
					[filesystems]
				  use_mntpoint = true
					```
			*/
			suggestName := nameSuggestion(splitedKey[len(splitedKey)-1], suggestions)
			if suggestName == "" {
				unexpectedKeys = append(unexpectedKeys, UnexpectedKey{
					v.String(),
					"",
				})
			} else {
				unexpectedKeys = append(unexpectedKeys, UnexpectedKey{
					v.String(),
					strings.Join(splitedKey[:len(splitedKey)-1], ".") + "." + suggestName,
				})
			}
		} else {
			// don't accept duplicate unexpectedKey
			if !containKey(unexpectedKeys, key) {
				unexpectedKeys = append(unexpectedKeys, UnexpectedKey{
					key,
					nameSuggestion(key, suggestions),
				})
			}
		}
	}

	for k1, v := range config.Plugin {
		/*
			detect [plugin.<unexpected>.<???>]
			default suggestion of [plugin.<unexpected>.<???>] is plugin.metrics.<???>
			don't have to detect critical syntax error about plugin here because error should have occured while loading config
			```
			[plugin.metrics.correct]
			```
			-> A configuration value of `command` should be string or string slice, but <nil>
			```
			[plugin.metrics]
			command = "test command"
			```
			-> type mismatch for config.PluginConfig: expected table but found string
		*/
		if k1 != "metrics" && k1 != "checks" && k1 != "metadata" {
			suggestName := nameSuggestion(k1, []string{"metrics", "checks", "metadata"})
			for k2 := range v {
				if suggestName == "" {
					unexpectedKeys = append(unexpectedKeys, UnexpectedKey{
						fmt.Sprintf("plugin.%s.%s", k1, k2),
						fmt.Sprintf("plugin.metrics.%s", k2),
					})
				} else {
					unexpectedKeys = append(unexpectedKeys, UnexpectedKey{
						fmt.Sprintf("plugin.%s.%s", k1, k2),
						fmt.Sprintf("plugin.%s.%s", suggestName, k2),
					})
				}
			}
		}
	}

	sort.Slice(unexpectedKeys, func(i, j int) bool {
		return unexpectedKeys[i].Name < unexpectedKeys[j].Name
	})

	return unexpectedKeys, nil
}

func containKey(target []UnexpectedKey, want string) bool {
	for _, v := range target {
		if v.Name == want {
			return true
		}
	}
	return false
}

func containKeyName(target []string, want string) bool {
	for _, v := range target {
		if v == want {
			return true
		}
	}
	return false
}
