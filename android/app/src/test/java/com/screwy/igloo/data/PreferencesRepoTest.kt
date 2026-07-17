package com.screwy.igloo.data

import com.screwy.igloo.ui.theme.DefaultThemeAccentHex
import com.screwy.igloo.ui.theme.DefaultThemeId
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * PreferencesRepo behavior — default-on-miss reads, typed setters, and the two sync caches
 * (debugMode, serverTimeOffsetMs) staying in sync with the Flow updater.
 *
 * Note: the sync-cache tests use a real `Dispatchers.Default` scope + poll-with-timeout because
 * Room's Flow emits on its own internal dispatcher; TestScope/testScheduler can't advance it
 * deterministically.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class PreferencesRepoTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope

    @Before
    fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    }

    @After
    fun tearDown() {
        scope.cancel()
        db.close()
    }

    private suspend fun waitFor(
        description: String,
        timeoutMs: Long = 5_000L,
        predicate: () -> Boolean,
    ) {
        withTimeout(timeoutMs) { while (!predicate()) delay(10) }
    }

    @Test
    fun defaults_surfaceWhenMissing() = runBlocking {
        val repo = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })
        assertEquals(PreferencesRepo.Defaults.SERVER_URL, repo.serverUrl().first())
        assertTrue(
            repo.serverUrl().first().startsWith("http://") ||
                repo.serverUrl().first().startsWith("https://")
        )
        assertEquals(PreferencesRepo.Defaults.SYNC_ENABLED, repo.syncEnabled().first())
        assertEquals(
            PreferencesRepo.Defaults.SYNC_INTERVAL_MINUTES,
            repo.syncIntervalMinutes().first(),
        )
        assertEquals(PreferencesRepo.Defaults.DEBUG_MODE, repo.debugMode().first())
        assertEquals(PreferencesRepo.Defaults.THEME_ID, repo.themeId().first())
        assertEquals(PreferencesRepo.Defaults.THEME_ACCENT_HEX, repo.themeAccentHex().first())
        assertEquals(DefaultThemeId, PreferencesRepo.Defaults.THEME_ID)
        assertEquals(DefaultThemeAccentHex, PreferencesRepo.Defaults.THEME_ACCENT_HEX)
        assertEquals(PreferencesRepo.Defaults.THEME_CUSTOM_CSS, repo.themeCustomCss().first())
        assertEquals(
            PreferencesRepo.Defaults.RETENTION_DAYS_MOMENTS,
            repo.retentionDaysMoments().first(),
        )
        assertEquals(PreferencesRepo.Defaults.RETENTION_DAYS_FEED, repo.retentionDaysFeed().first())
        assertEquals(
            PreferencesRepo.Defaults.RETENTION_DAYS_YOUTUBE,
            repo.retentionDaysYoutube().first(),
        )
        assertEquals(
            PreferencesRepo.Defaults.SHARE_EMBED_FRIENDLY_LINKS,
            repo.shareEmbedFriendlyLinks().first(),
        )
        assertEquals(7, repo.retentionDaysMoments().first())
        assertEquals(2, repo.retentionDaysFeed().first())
        assertEquals(3, repo.retentionDaysYoutube().first())
        assertEquals(
            PreferencesRepo.Defaults.SB_SPONSOR,
            repo
                .flowString(
                    PreferencesRepo.Keys.SB_SPONSOR,
                    default =
                        PreferencesRepo.Defaults.sponsorBlockCategory(
                            PreferencesRepo.Keys.SB_SPONSOR
                        ),
                )
                .first(),
        )
        assertEquals(
            PreferencesRepo.Defaults.SB_INTRO,
            repo
                .flowString(
                    PreferencesRepo.Keys.SB_INTRO,
                    default =
                        PreferencesRepo.Defaults.sponsorBlockCategory(PreferencesRepo.Keys.SB_INTRO),
                )
                .first(),
        )
    }

    @Test
    fun typedSetters_writeBackAsStrings() = runBlocking {
        val repo = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 42L })

        repo.setServerUrl("https://example.com")
        repo.setSyncEnabled(false)
        repo.setSyncIntervalMinutes(15)
        repo.setThemeId("github-light")
        repo.setThemeAccentHex("#0969da")
        repo.setThemeCustomCss(".feed-card { border-radius: 0; }")
        repo.setServerTimeOffsetMs(-2000L)
        repo.setShareEmbedFriendlyLinks(true)

        assertEquals("https://example.com", repo.serverUrl().first())
        assertFalse(repo.syncEnabled().first())
        assertEquals(15, repo.syncIntervalMinutes().first())
        assertEquals("github-light", repo.themeId().first())
        assertEquals("#0969da", repo.themeAccentHex().first())
        assertEquals(".feed-card { border-radius: 0; }", repo.themeCustomCss().first())
        assertEquals(-2000L, repo.serverTimeOffsetMs().first())
        assertTrue(repo.shareEmbedFriendlyLinks().first())
    }

    @Test
    fun bookmarkSheetPrefs_roundTrip() = runBlocking {
        val repo = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 42L })

        repo.setLastBookmarkCategoryId(17L)
        repo.setBookmarkAccountPrefs("Creator_Handle", listOf("Alpha", "@beta"))

        assertEquals(17L, repo.getLastBookmarkCategoryId())
        assertEquals(listOf("alpha", "beta"), repo.getBookmarkAccountPrefs("creator_handle"))
    }

    @Test
    fun themeCustomCss_isCappedBeforeStorage() = runBlocking {
        val repo = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 42L })
        val oversized = "a".repeat(PreferencesRepo.Defaults.THEME_CUSTOM_CSS_MAX_BYTES + 10)

        repo.setThemeCustomCss(oversized)

        assertEquals(
            PreferencesRepo.Defaults.THEME_CUSTOM_CSS_MAX_BYTES,
            repo.themeCustomCss().first().length,
        )
    }

    @Test
    fun syncCaches_trackDebugMode() = runBlocking {
        val repo = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })

        // Initial value is the default
        waitFor("initial default") { repo.debugModeSync() == PreferencesRepo.Defaults.DEBUG_MODE }
        assertEquals(PreferencesRepo.Defaults.DEBUG_MODE, repo.debugModeSync())

        repo.setDebugMode(false)
        waitFor("toggle off") { !repo.debugModeSync() }
        assertFalse(repo.debugModeSync())

        repo.setDebugMode(true)
        waitFor("toggle on") { repo.debugModeSync() }
        assertTrue(repo.debugModeSync())
    }

    @Test
    fun syncCaches_trackServerTimeOffset() = runBlocking {
        val repo = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })

        waitFor("initial default") { repo.serverTimeOffsetMsSync() == 0L }

        repo.setServerTimeOffsetMs(12345L)
        waitFor("cache reflects offset") { repo.serverTimeOffsetMsSync() == 12345L }
        assertEquals(12345L, repo.serverTimeOffsetMsSync())
    }

    @Test
    fun dearrowMode_defaultsToOff() = runBlocking {
        val repo = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })
        assertEquals("off", repo.dearrowMode().first())
    }

    @Test
    fun dearrowMode_setAndRoundTripCasual() = runBlocking {
        val repo = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })
        repo.putString(PreferencesRepo.Keys.DEARROW_MODE, "casual")
        assertEquals("casual", repo.dearrowMode().first())
    }

    @Test
    fun dearrowMode_invalidValueNormalizesToOff() = runBlocking {
        val repo = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })
        repo.putString(PreferencesRepo.Keys.DEARROW_MODE, "banana")
        assertEquals("off", repo.dearrowMode().first())
    }

    @Test
    fun deleteAll_resetsReadsToDefaults() = runBlocking {
        val repo = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })
        repo.setSyncEnabled(false)
        assertFalse(repo.syncEnabled().first())
        repo.deleteAll()
        assertEquals(PreferencesRepo.Defaults.SYNC_ENABLED, repo.syncEnabled().first())
    }
}
