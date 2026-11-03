package protocol

import "regexp"

var placeholder = regexp.MustCompile(`\{([^{}]+)\}`)

func TemplateInputs(template string) []string {
	var names []string
	for _, match := range placeholder.FindAllStringSubmatch(template, -1) {
		names = append(names, match[1])
	}
	return names
}

func Expand(template string, lookup func(string) (string, bool)) (string, bool) {
	complete := true
	expanded := placeholder.ReplaceAllStringFunc(template, func(match string) string {
		value, present := lookup(match[1 : len(match)-1])
		complete = complete && present
		return value
	})
	return expanded, complete
}
