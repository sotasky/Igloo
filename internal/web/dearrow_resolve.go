package web

// ResolveDearrowTitle picks the title to display based on the user's DeArrow
// mode. Fallback order is casual -> community -> original. Returns original
// when mode is "off" or no DeArrow title is available.
func ResolveDearrowTitle(mode, original string, dearrow, dearrowCasual *string) string {
	if mode == "casual" {
		if dearrowCasual != nil && *dearrowCasual != "" {
			return *dearrowCasual
		}
	}
	if mode == "casual" || mode == "default" {
		if dearrow != nil && *dearrow != "" {
			return *dearrow
		}
	}
	return original
}

// ResolveDearrowDisplayTitles returns server-owned display strings for Android
// sync while preserving Android's local mode switch. displayTitle is the
// default/community title, displayTitleCasual is the casual-mode title.
func ResolveDearrowDisplayTitles(original string, dearrow, dearrowCasual *string) (displayTitle string, displayTitleCasual string) {
	displayTitle = ResolveDearrowTitle("default", original, dearrow, dearrowCasual)
	displayTitleCasual = ResolveDearrowTitle("casual", original, dearrow, dearrowCasual)
	return displayTitle, displayTitleCasual
}

// ResolveDearrowThumbURL selects the canonical DeArrow variant when the mode
// is enabled. The media handler decides availability from assets and falls
// back to the canonical original thumbnail.
func ResolveDearrowThumbURL(mode, videoID string) string {
	base := "/api/media/thumbnail/" + videoID
	if mode == "off" {
		return base
	}
	return base + "?da=1"
}
