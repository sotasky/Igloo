package model

import (
	"regexp"
	"strings"
)

type TwitterMentionSpan struct {
	Start  int
	End    int
	Handle string
}

var (
	twitterMentionPattern = regexp.MustCompile(`@[A-Za-z0-9_]+`)
	twitterURLPattern     = regexp.MustCompile(`https?://[^\s<>"']+`)
	twitterEmailTLD       = regexp.MustCompile(`^\.[A-Za-z]{2,12}\b`)
)

func LinkableTwitterMentions(text string) []TwitterMentionSpan {
	candidates := twitterMentionPattern.FindAllStringIndex(text, -1)
	if len(candidates) == 0 {
		return nil
	}
	urls := twitterURLPattern.FindAllStringIndex(text, -1)
	urlIndex := 0
	var mentions []TwitterMentionSpan
	for _, candidate := range candidates {
		start, end := candidate[0], candidate[1]
		for urlIndex < len(urls) && urls[urlIndex][1] <= start {
			urlIndex++
		}
		if urlIndex < len(urls) && start >= urls[urlIndex][0] && start < urls[urlIndex][1] {
			continue
		}
		if start > 0 && isTwitterMentionWordByte(text[start-1]) {
			continue
		}
		if end < len(text) && (text[end] == '@' || twitterEmailTLD.MatchString(text[end:])) {
			continue
		}
		handle := strings.ToLower(text[start+1 : end])
		if TwitterChannelIDFromHandle(handle) == "" {
			continue
		}
		mentions = append(mentions, TwitterMentionSpan{
			Start:  start,
			End:    end,
			Handle: handle,
		})
	}
	return mentions
}

func isTwitterMentionWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}
