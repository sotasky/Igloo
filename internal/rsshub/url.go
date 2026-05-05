package rsshub

import "net/url"

// BuildEnrichedURL constructs the RSSHub URL for a Twitter/X handle with all
// enrichment parameters that the enrich.go parser depends on (author blocks,
// avatar URLs, quote tweet sections, timestamps, etc.).
func BuildEnrichedURL(base, handle string) string {
	routeParams := url.PathEscape(
		"readable=1&authorNameBold=1&showAuthorInDesc=1" +
			"&showAuthorAvatarInDesc=1&showQuotedAuthorAvatarInDesc=1" +
			"&showEmojiForRetweetAndReply=1&showTimestampInDescription=1" +
			"&addLinkForPics=1&include_replies=1&include_rts=1",
	)
	return base + "/twitter/user/" + handle + "/" + routeParams
}
