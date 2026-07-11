package feed

import (
	"regexp"
	"strings"

	"github.com/screwys/igloo/internal/model"
)

var hashtagRe = regexp.MustCompile(`#[A-Za-z0-9_]+`)

// ExtractInterestTokens extracts hashtags and @mentions from tweet text.
// Returns lowercase, deduplicated tokens.
func ExtractInterestTokens(text string) []string {
	if text == "" {
		return nil
	}
	seen := make(map[string]bool)
	var tokens []string

	for _, match := range hashtagRe.FindAllString(text, -1) {
		tok := strings.ToLower(match)
		if !seen[tok] {
			seen[tok] = true
			tokens = append(tokens, tok)
		}
	}
	for _, match := range model.LinkableTwitterMentions(text) {
		tok := "@" + match.Handle
		if !seen[tok] {
			seen[tok] = true
			tokens = append(tokens, tok)
		}
	}
	if len(tokens) == 0 {
		return nil
	}
	return tokens
}
