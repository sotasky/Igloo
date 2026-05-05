# Privacy

Igloo is a self-hosted personal software, all your data stays on your machine. Logs, including debug logs are local only, there is no external telemetry.

Server itself contacts external services to fetch media, those being:

- YouTube, TikTok, X (and fxtwitter), Instagram, SponsorBlock, Unavatar (only for avatars in youtube search page, since yt-dlp doesn't expose avatar urls there) and maybe some I missed. You can also enable auto-translate for X posts, and select a provider (or connect to your self-hosted node).

Realistically, you should expect that your download activity is not private, as even with cookies not configured, you can be fingerprinted by the platform with the subscription list at least. Your activity, and how you interact with the content however, stays private. Platforms simply can't tell when/for how long you watched a video, or which channels you spend more time on, which I believe is a privacy-win you would not get in other similar services, where you don't self-host.
