# Security Policy

I'm actively looking for
possible security issues, especially around local data access, authentication,
 media downloads, cookie handling, and external cli tools (ffmpeg, yt-dlp etc) and CodeQL scanning is also activated in the repo. I aim to keep defaults convervative, and keep trust assumptions (or threat model) the same as running yt-dlp with cookies, or running ffmpeg locally (common in media servers like Jellyfin for example). If you find something is amiss, please report it via Github Private Reporting!
