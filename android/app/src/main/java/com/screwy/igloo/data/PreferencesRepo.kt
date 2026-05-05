package com.screwy.igloo.data

import com.screwy.igloo.BuildConfig
import com.screwy.igloo.data.dao.PreferenceDao
import kotlinx.serialization.json.Json
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch

/**
 * Typed reactive wrapper over the `preferences` key-value DAO.
 *
 *  - Reactive reads return `Flow<T>`; a missing row emits the schema default.
 *  - Suspend writers upsert the row with a fresh `updated_at`.
 *  - Two sync caches (`debugModeSync`, `serverTimeOffsetMsSync`) power hot-path gating
 *    — Logger.debug short-circuits before allocating its payload, and outbox enqueue
 *    reads the server-time offset without a Room query. Both stay in sync with the
 *    underlying Flow via a long-lived collector on the supplied scope.
 *  - No `runBlocking`. If a caller can't collect a Flow, it must use one of the
 *    narrow sync caches. New sync caches need a design-doc justification.
 */
class PreferencesRepo(
    private val dao: PreferenceDao,
    private val scope: CoroutineScope,
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
) {

    // ─── Catalogue keys ──────────────────────────────────────────────────────

    @Suppress("MemberVisibilityCanBePrivate")
    object Keys {
        // Server
        const val SERVER_URL                 = "server_url"

        // Sync
        const val SYNC_ENABLED               = "sync_enabled"
        const val SYNC_INTERVAL_MINUTES      = "sync_interval_minutes"
        const val SYNC_WIFI_ONLY             = "sync_wifi_only"

        // Theme
        const val THEME_ID                   = "theme_id"
        const val THEME_ACCENT_HEX           = "theme_accent_hex"
        const val THEME_CUSTOM_CSS           = "theme_custom_css"

        // Playback
        const val AUTOPLAY                   = "autoplay"
        const val MUTE_DEFAULT               = "mute_default"
        const val PLAYBACK_SPEED_DEFAULT     = "playback_speed_default"

        // Feed
        const val INCLUDE_REPOSTS_DEFAULT    = "include_reposts_default"
        const val MEDIA_ONLY_DEFAULT         = "media_only_default"

        // Display
        const val STARTING_PAGE              = "starting_page"
        const val SHARE_EMBED_FRIENDLY_LINKS = "share_embed_friendly_links"

        // Debug
        const val DEBUG_MODE                 = "debug_mode"
        const val SERVER_TIME_OFFSET_MS      = "server_time_offset_ms"

        // Retention
        const val RETENTION_DAYS_MOMENTS     = "retention_days_moments"
        const val RETENTION_DAYS_FEED        = "retention_days_feed"
        const val RETENTION_DAYS_YOUTUBE     = "retention_days_youtube"
        const val STORIES_WINDOW_HOURS       = "stories_window_hours"

        // SponsorBlock — client-side categories (off / silent / ask).
        // One key per category so a future server sync doesn't need a rename.
        const val SB_SPONSOR                 = "sb_sponsor"
        const val SB_SELF_PROMO              = "sb_selfpromo"
        const val SB_INTERACTION             = "sb_interaction"
        const val SB_INTRO                   = "sb_intro"
        const val SB_OUTRO                   = "sb_outro"
        const val SB_PREVIEW                 = "sb_preview"
        const val SB_FILLER                  = "sb_filler"
        const val SB_MUSIC_OFFTOPIC          = "sb_music_offtopic"

        // DeArrow — client-side mode (off / default / casual). Title + thumbnail
        // substitution for YouTube. Separate key so the user can flip without a
        // server round-trip; server emits dearrow_mode as a simple enum too.
        const val DEARROW_MODE               = "dearrow_mode"

        // Moments resume cursor (written by outbox ACK for moments_cursor kind)
        const val MOMENTS_DEFAULT_TAB         = "moments_default_tab"
        const val MOMENTS_INCLUDE_REPOSTS_DEFAULT = "moments_include_reposts_default"
        const val INSTAGRAM_INCLUDE_TAGGED_DEFAULT = "instagram_include_tagged_default"
        const val MOMENTS_RESUME_VIDEO_ID    = "moments_resume_video_id"
        const val MOMENTS_RESUME_POSITION_MS = "moments_resume_position_ms"
        const val MOMENTS_RESUME_VIDEO_ID_ALL = "moments_resume_video_id_all"
        const val MOMENTS_RESUME_POSITION_MS_ALL = "moments_resume_position_ms_all"
        const val MOMENTS_RESUME_VIDEO_ID_FOLLOWING = "moments_resume_video_id_following"
        const val MOMENTS_RESUME_POSITION_MS_FOLLOWING = "moments_resume_position_ms_following"
        const val LAST_BOOKMARK_CATEGORY_ID  = "last_bookmark_category_id"
        const val BOOKMARK_ACCOUNT_PREFS     = "bookmark_account_prefs"

    }

    object Defaults {
        val SERVER_URL                       = BuildConfig.DEFAULT_SERVER_URL
        const val SYNC_ENABLED               = true
        const val SYNC_INTERVAL_MINUTES      = 30
        const val SYNC_WIFI_ONLY             = false
        const val THEME_ID                   = "system"
        const val THEME_ACCENT_HEX           = "#f38ba8"
        const val THEME_CUSTOM_CSS           = ""
        const val THEME_CUSTOM_CSS_MAX_BYTES = 64 * 1024
        // Auto-swipe off by default: letting shorts silently self-advance was
        // reported as "keeps turning itself on" because ChannelRoute /
        // BookmarksRoute overlays ignored the user's saved choice and reverted
        // to the old `true` default on every reopen. Off is the safer surprise-
        // minimising default now that all three overlays read from here.
        const val AUTOPLAY                   = false
        // Mute off by default: app-wide single source of truth, respected
        // across Moments / Channel / Bookmarks.
        const val MUTE_DEFAULT               = false
        const val PLAYBACK_SPEED_DEFAULT     = "1.0"
        const val DEBUG_MODE                 = false
        const val SERVER_TIME_OFFSET_MS      = 0L
        const val RETENTION_DAYS_MOMENTS     = 7
        const val RETENTION_DAYS_FEED        = 7
        const val RETENTION_DAYS_YOUTUBE     = 7
        const val STORIES_WINDOW_HOURS       = 48
        const val STARTING_PAGE              = "videos"
        const val SHARE_EMBED_FRIENDLY_LINKS = false

        /**
         * Valid start destinations for [AppNavHost]. Keep in sync with the
         * composables registered under MainScaffold in AppNavHost.kt and the
         * options exposed in SettingsHubRoute. If the stored pref is outside this
         * set (e.g. dropped from a future build), NavHost falls back to "feed".
         */
        val VALID_STARTING_PAGES = setOf("feed", "videos", "moments", "bookmarks", "liked")

        const val SB_SPONSOR                 = "silent"
        const val SB_SELF_PROMO              = "silent"
        const val SB_INTERACTION             = "silent"
        const val SB_INTRO                   = "ask"
        const val SB_OUTRO                   = "ask"
        const val SB_PREVIEW                 = "ask"
        const val SB_FILLER                  = "ask"
        const val SB_MUSIC_OFFTOPIC          = "ask"

        const val DEARROW_MODE               = "off"
        val VALID_DEARROW_MODES = setOf("off", "default", "casual")
        const val MOMENTS_DEFAULT_TAB         = "all"
        const val MOMENTS_INCLUDE_REPOSTS_DEFAULT = false
        const val INSTAGRAM_INCLUDE_TAGGED_DEFAULT = false
        val VALID_MOMENTS_TABS = setOf("all", "following", "stories")

        fun normalizeDearrowMode(v: String?): String =
            if (v in VALID_DEARROW_MODES) v!! else DEARROW_MODE

        fun normalizeMomentsTab(v: String?): String =
            if (v in VALID_MOMENTS_TABS) v!! else MOMENTS_DEFAULT_TAB

        fun normalizeStoriesWindowHours(v: Int): Int =
            v.coerceIn(1, 168)

        fun sponsorBlockCategory(key: String): String = when (key) {
            Keys.SB_SPONSOR        -> SB_SPONSOR
            Keys.SB_SELF_PROMO     -> SB_SELF_PROMO
            Keys.SB_INTERACTION    -> SB_INTERACTION
            Keys.SB_INTRO          -> SB_INTRO
            Keys.SB_OUTRO          -> SB_OUTRO
            Keys.SB_PREVIEW        -> SB_PREVIEW
            Keys.SB_FILLER         -> SB_FILLER
            Keys.SB_MUSIC_OFFTOPIC -> SB_MUSIC_OFFTOPIC
            else                   -> "ask"
        }

        fun normalizeThemeCustomCss(value: String): String {
            val bytes = value.toByteArray(Charsets.UTF_8)
            if (bytes.size <= THEME_CUSTOM_CSS_MAX_BYTES) return value
            var end = THEME_CUSTOM_CSS_MAX_BYTES
            while (end > 0 && (bytes[end].toInt() and 0xC0) == 0x80) {
                end--
            }
            return bytes.copyOf(end).toString(Charsets.UTF_8)
        }
    }

    // ─── Reactive reads ──────────────────────────────────────────────────────

    fun serverUrl(): Flow<String> =
        flowString(Keys.SERVER_URL, default = Defaults.SERVER_URL)

    fun syncEnabled(): Flow<Boolean> = flowBool(Keys.SYNC_ENABLED, default = Defaults.SYNC_ENABLED)
    fun syncIntervalMinutes(): Flow<Int> =
        flowInt(Keys.SYNC_INTERVAL_MINUTES, default = Defaults.SYNC_INTERVAL_MINUTES)
    fun syncWifiOnly(): Flow<Boolean> =
        flowBool(Keys.SYNC_WIFI_ONLY, default = Defaults.SYNC_WIFI_ONLY)

    fun themeId(): Flow<String> = flowString(Keys.THEME_ID, default = Defaults.THEME_ID)
    fun themeAccentHex(): Flow<String> =
        flowString(Keys.THEME_ACCENT_HEX, default = Defaults.THEME_ACCENT_HEX)
    fun themeCustomCss(): Flow<String> =
        flowString(Keys.THEME_CUSTOM_CSS, default = Defaults.THEME_CUSTOM_CSS)

    fun autoplay(): Flow<Boolean> = flowBool(Keys.AUTOPLAY, default = Defaults.AUTOPLAY)
    fun muteDefault(): Flow<Boolean> = flowBool(Keys.MUTE_DEFAULT, default = Defaults.MUTE_DEFAULT)
    fun playbackSpeedDefault(): Flow<String> =
        flowString(Keys.PLAYBACK_SPEED_DEFAULT, default = Defaults.PLAYBACK_SPEED_DEFAULT)

    fun debugMode(): Flow<Boolean> = flowBool(Keys.DEBUG_MODE, default = Defaults.DEBUG_MODE)
    fun serverTimeOffsetMs(): Flow<Long> =
        flowLong(Keys.SERVER_TIME_OFFSET_MS, default = Defaults.SERVER_TIME_OFFSET_MS)

    fun retentionDaysMoments(): Flow<Int> =
        flowInt(Keys.RETENTION_DAYS_MOMENTS, default = Defaults.RETENTION_DAYS_MOMENTS)
    fun retentionDaysFeed(): Flow<Int> =
        flowInt(Keys.RETENTION_DAYS_FEED, default = Defaults.RETENTION_DAYS_FEED)
    fun retentionDaysYoutube(): Flow<Int> =
        flowInt(Keys.RETENTION_DAYS_YOUTUBE, default = Defaults.RETENTION_DAYS_YOUTUBE)
    fun storiesWindowHours(): Flow<Int> =
        flowInt(Keys.STORIES_WINDOW_HOURS, default = Defaults.STORIES_WINDOW_HOURS)
            .map { Defaults.normalizeStoriesWindowHours(it) }

    fun startingPage(): Flow<String> =
        flowString(Keys.STARTING_PAGE, default = Defaults.STARTING_PAGE)

    fun shareEmbedFriendlyLinks(): Flow<Boolean> =
        flowBool(Keys.SHARE_EMBED_FRIENDLY_LINKS, default = Defaults.SHARE_EMBED_FRIENDLY_LINKS)

    fun dearrowMode(): Flow<String> =
        flowString(Keys.DEARROW_MODE, default = Defaults.DEARROW_MODE)
            .map { Defaults.normalizeDearrowMode(it) }

    fun momentsDefaultTab(): Flow<String> =
        flowString(Keys.MOMENTS_DEFAULT_TAB, default = Defaults.MOMENTS_DEFAULT_TAB)
            .map { Defaults.normalizeMomentsTab(it) }

    fun momentsIncludeRepostsDefault(): Flow<Boolean> =
        flowBool(Keys.MOMENTS_INCLUDE_REPOSTS_DEFAULT, default = Defaults.MOMENTS_INCLUDE_REPOSTS_DEFAULT)

    fun instagramIncludeTaggedDefault(): Flow<Boolean> =
        flowBool(Keys.INSTAGRAM_INCLUDE_TAGGED_DEFAULT, default = Defaults.INSTAGRAM_INCLUDE_TAGGED_DEFAULT)

    fun momentsResumeVideoId(scope: String): Flow<String?> {
        val normalized = Defaults.normalizeMomentsTab(scope)
        val key = momentsResumeVideoIdKey(normalized)
        if (normalized != "all") {
            return dao.flowByKey(key).map { it?.value }
        }
        return combine(
            dao.flowByKey(key),
            dao.flowByKey(Keys.MOMENTS_RESUME_VIDEO_ID),
        ) { scoped, legacy -> scoped?.value ?: legacy?.value }
    }

    fun momentsResumePositionMs(scope: String): Flow<Long> {
        val normalized = Defaults.normalizeMomentsTab(scope)
        val key = momentsResumePositionMsKey(normalized)
        if (normalized != "all") {
            return flowLong(key, default = 0L)
        }
        return combine(
            dao.flowByKey(key),
            dao.flowByKey(Keys.MOMENTS_RESUME_POSITION_MS),
        ) { scoped, legacy ->
            scoped?.value?.toLongOrNull() ?: legacy?.value?.toLongOrNull() ?: 0L
        }
    }

    // ─── Sync caches for hot paths ───────────────────────────────────────────
    //
    // Narrow by design: only the two prefs that gate high-frequency code paths
    // (Logger.debug gating, outbox enqueue server-time offset) get sync reads.
    // Both stay in sync with the Flow via the collector below.

    private val debugModeCache = MutableStateFlow(Defaults.DEBUG_MODE)
    fun debugModeSync(): Boolean = debugModeCache.value

    private val serverTimeOffsetMsCache = MutableStateFlow(Defaults.SERVER_TIME_OFFSET_MS)
    fun serverTimeOffsetMsSync(): Long = serverTimeOffsetMsCache.value

    // AppNavHost reads this synchronously at first composition to pick the start
    // destination. Initialized to the schema default; the collector below swaps
    // in the persisted value once Room returns. The window between app launch
    // and the first flow emission is small — in the worst case the very first
    // composition after a fresh process start sees "feed" regardless of the
    // user's preference, which resolves on next launch.
    private val startingPageCache = MutableStateFlow(Defaults.STARTING_PAGE)
    fun startingPageSync(): String = startingPageCache.value

    init {
        scope.launch {
            debugMode().distinctUntilChanged().collect { debugModeCache.value = it }
        }
        scope.launch {
            serverTimeOffsetMs().distinctUntilChanged()
                .collect { serverTimeOffsetMsCache.value = it }
        }
        scope.launch {
            startingPage().distinctUntilChanged().collect { startingPageCache.value = it }
        }
    }

    // ─── Writers ─────────────────────────────────────────────────────────────

    suspend fun setServerUrl(value: String) = putString(Keys.SERVER_URL, value)

    suspend fun setSyncEnabled(value: Boolean) = putBool(Keys.SYNC_ENABLED, value)
    suspend fun setSyncIntervalMinutes(value: Int) = putInt(Keys.SYNC_INTERVAL_MINUTES, value)
    suspend fun setSyncWifiOnly(value: Boolean) = putBool(Keys.SYNC_WIFI_ONLY, value)

    suspend fun setThemeId(value: String) = putString(Keys.THEME_ID, value)
    suspend fun setThemeAccentHex(value: String) = putString(Keys.THEME_ACCENT_HEX, value)
    suspend fun setThemeCustomCss(value: String) =
        putString(Keys.THEME_CUSTOM_CSS, Defaults.normalizeThemeCustomCss(value))

    suspend fun setAutoplay(value: Boolean) = putBool(Keys.AUTOPLAY, value)
    suspend fun setMuteDefault(value: Boolean) = putBool(Keys.MUTE_DEFAULT, value)
    suspend fun setPlaybackSpeedDefault(value: String) = putString(Keys.PLAYBACK_SPEED_DEFAULT, value)

    suspend fun setDebugMode(value: Boolean) = putBool(Keys.DEBUG_MODE, value)
    suspend fun setServerTimeOffsetMs(offsetMs: Long) = putLong(Keys.SERVER_TIME_OFFSET_MS, offsetMs)

    suspend fun setRetentionDaysMoments(days: Int) = putInt(Keys.RETENTION_DAYS_MOMENTS, days)
    suspend fun setRetentionDaysFeed(days: Int) = putInt(Keys.RETENTION_DAYS_FEED, days)
    suspend fun setRetentionDaysYoutube(days: Int) = putInt(Keys.RETENTION_DAYS_YOUTUBE, days)
    suspend fun setStoriesWindowHours(hours: Int) =
        putInt(Keys.STORIES_WINDOW_HOURS, Defaults.normalizeStoriesWindowHours(hours))

    suspend fun setStartingPage(value: String) = putString(Keys.STARTING_PAGE, value)
    suspend fun setShareEmbedFriendlyLinks(value: Boolean) =
        putBool(Keys.SHARE_EMBED_FRIENDLY_LINKS, value)

    suspend fun setMomentsDefaultTab(value: String) =
        putString(Keys.MOMENTS_DEFAULT_TAB, Defaults.normalizeMomentsTab(value))
    suspend fun setMomentsIncludeRepostsDefault(value: Boolean) =
        putBool(Keys.MOMENTS_INCLUDE_REPOSTS_DEFAULT, value)
    suspend fun setInstagramIncludeTaggedDefault(value: Boolean) =
        putBool(Keys.INSTAGRAM_INCLUDE_TAGGED_DEFAULT, value)

    suspend fun setMomentsResumeVideoId(videoId: String?, scope: String = "all") {
        val normalized = Defaults.normalizeMomentsTab(scope)
        putString(momentsResumeVideoIdKey(normalized), videoId)
        if (normalized == "all") putString(Keys.MOMENTS_RESUME_VIDEO_ID, videoId)
    }
    suspend fun setMomentsResumePositionMs(positionMs: Long, scope: String = "all") {
        val normalized = Defaults.normalizeMomentsTab(scope)
        putLong(momentsResumePositionMsKey(normalized), positionMs)
        if (normalized == "all") putLong(Keys.MOMENTS_RESUME_POSITION_MS, positionMs)
    }
    suspend fun setLastBookmarkCategoryId(categoryId: Long?) =
        putString(Keys.LAST_BOOKMARK_CATEGORY_ID, categoryId?.toString())

    /** Suspend read so OutboxWriter can compare before overwriting the cursor. */
    suspend fun getMomentsResumeVideoId(scope: String): String? {
        val normalized = Defaults.normalizeMomentsTab(scope)
        return dao.getValue(momentsResumeVideoIdKey(normalized))
            ?: if (normalized == "all") dao.getValue(Keys.MOMENTS_RESUME_VIDEO_ID) else null
    }
    suspend fun getLastBookmarkCategoryId(): Long? =
        dao.getValue(Keys.LAST_BOOKMARK_CATEGORY_ID)?.toLongOrNull()
    suspend fun getBookmarkAccountPrefs(channelKey: String): List<String>? {
        val raw = dao.getValue(Keys.BOOKMARK_ACCOUNT_PREFS) ?: return null
        val normalizedKey = channelKey.trim().lowercase()
        if (normalizedKey.isBlank()) return null
        return runCatching {
            Json.decodeFromString<Map<String, List<String>>>(raw)[normalizedKey]
        }.getOrNull()
    }
    suspend fun setBookmarkAccountPrefs(channelKey: String, handles: List<String>) {
        val normalizedKey = channelKey.trim().lowercase()
        if (normalizedKey.isBlank()) return
        val existing = runCatching {
            Json.decodeFromString<Map<String, List<String>>>(
                dao.getValue(Keys.BOOKMARK_ACCOUNT_PREFS).orEmpty(),
            )
        }.getOrDefault(emptyMap()).toMutableMap()
        existing[normalizedKey] = handles
            .map(String::trim)
            .filter(String::isNotBlank)
            .map { it.removePrefix("@") }
            .map(String::lowercase)
            .distinct()
        putString(Keys.BOOKMARK_ACCOUNT_PREFS, Json.encodeToString(existing))
    }

    // Generic accessors are exposed so adding a new key without a typed wrapper
    // stays possible, but the typed wrappers above are the preferred surface.

    fun flowString(key: String, default: String): Flow<String> =
        dao.flowByKey(key).map { it?.value ?: default }

    fun flowBool(key: String, default: Boolean): Flow<Boolean> =
        dao.flowByKey(key).map { it?.value?.toBooleanStrictOrNull() ?: default }

    fun flowInt(key: String, default: Int): Flow<Int> =
        dao.flowByKey(key).map { it?.value?.toIntOrNull() ?: default }

    fun flowLong(key: String, default: Long): Flow<Long> =
        dao.flowByKey(key).map { it?.value?.toLongOrNull() ?: default }

    suspend fun putString(key: String, value: String?) =
        dao.put(key = key, value = value, nowMs = nowMsProvider())

    suspend fun putBool(key: String, value: Boolean) = putString(key, value.toString())
    suspend fun putInt(key: String, value: Int) = putString(key, value.toString())
    suspend fun putLong(key: String, value: Long) = putString(key, value.toString())

    private fun momentsResumeVideoIdKey(scope: String): String =
        if (Defaults.normalizeMomentsTab(scope) == "following") {
            Keys.MOMENTS_RESUME_VIDEO_ID_FOLLOWING
        } else {
            Keys.MOMENTS_RESUME_VIDEO_ID_ALL
        }

    private fun momentsResumePositionMsKey(scope: String): String =
        if (Defaults.normalizeMomentsTab(scope) == "following") {
            Keys.MOMENTS_RESUME_POSITION_MS_FOLLOWING
        } else {
            Keys.MOMENTS_RESUME_POSITION_MS_ALL
        }

    // ─── Test / dev hook ─────────────────────────────────────────────────────
    //
    // Wipes every pref — used by logout when the DB survives (shouldn't happen in
    // single-user today, but keeps the shape honest for multi-user).

    suspend fun deleteAll() { dao.deleteAll() }
}
