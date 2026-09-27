// Package logsafe strips characters a caller could use to forge extra log
// lines out of values before they're written into a log message.
package logsafe

import (
	"fmt"
	"strings"
)

var replacer = strings.NewReplacer("\n", "\\n", "\r", "\\r")

// S sanitizes a string value for logging.
func S(s string) string {
	return replacer.Replace(s)
}

// V sanitizes an arbitrary value for logging by formatting it first.
func V(v any) string {
	return replacer.Replace(fmt.Sprint(v))
}
