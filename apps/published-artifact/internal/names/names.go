package names

import (
	"regexp"
	"strings"
)

var nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)

var adjectives = []string{"amber", "brisk", "calm", "bright", "gentle", "lucky", "quiet", "swift"}
var nouns = []string{"badger", "falcon", "harbor", "maple", "otter", "summit", "willow", "wren"}

func Normalize(supplied string) string {
	name := strings.ToLower(supplied)
	name = nonAlphanumeric.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

func Generate(taken map[string]bool) string {
	prefix := ""
	for depth := 0; ; depth++ {
		for _, adjective := range adjectives {
			for _, noun := range nouns {
				candidate := prefix + adjective + "-" + noun
				if !taken[candidate] {
					return candidate
				}
			}
		}
		prefix += adjectives[depth%len(adjectives)] + "-"
	}
}
