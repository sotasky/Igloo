package com.screwy.igloo.settings

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.media.CacheOps
import com.screwy.igloo.sync.Scheduler
import com.screwy.igloo.testutil.ViewModelTestTracker
import io.mockk.Runs
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.just
import io.mockk.mockk
import io.mockk.verify
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class StorageViewModelTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var cacheOps: CacheOps
    private lateinit var scheduler: Scheduler
    private val viewModels = ViewModelTestTracker()

    @Before fun setUp() {
        Dispatchers.setMain(UnconfinedTestDispatcher())
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })
        cacheOps = mockk()
        scheduler = mockk(relaxed = true)
        coEvery { cacheOps.stats() } returns emptyList()
        coEvery { cacheOps.clearCache(any()) } just Runs
        coEvery { cacheOps.clearCache(null) } just Runs
    }

    @After fun tearDown() {
        viewModels.clearAll()
        scope.cancel()
        db.close()
        Dispatchers.resetMain()
    }

    private fun newViewModel(): StorageViewModel =
        viewModels.track(StorageViewModel(cacheOps, prefs, scheduler))

    private suspend fun eventuallyVerify(timeoutMs: Long = 5_000L, assertion: () -> Unit): Boolean =
        withTimeoutOrNull(timeoutMs) {
            while (true) {
                try {
                    assertion()
                    return@withTimeoutOrNull true
                } catch (_: AssertionError) {
                    delay(10)
                }
            }
        } == true

    @Test fun setSyncPrefs_writeThroughToPrefs() = runBlocking {
        val vm = newViewModel()
        vm.setSyncEnabled(false)
        vm.setSyncWifiOnly(true)
        vm.setSyncIntervalMinutes(120)

        val ok = withTimeoutOrNull(5_000L) {
            while (
                db.preferenceDao().getValue(PreferencesRepo.Keys.SYNC_ENABLED) != "false" ||
                db.preferenceDao().getValue(PreferencesRepo.Keys.SYNC_WIFI_ONLY) != "true" ||
                db.preferenceDao().getValue(PreferencesRepo.Keys.SYNC_INTERVAL_MINUTES) != "120"
            ) {
                delay(10)
            }
            true
        }
        assertEquals(true, ok)
    }

    @Test fun setRetentionPrefs_writeThroughToPrefs() = runBlocking {
        val vm = newViewModel()
        vm.setRetentionDaysYoutube(7)
        vm.setRetentionDaysMoments(30)
        vm.setRetentionDaysFeed(14)
        vm.setStoriesWindowHours(72)

        val ok = withTimeoutOrNull(5_000L) {
            while (
                db.preferenceDao().getValue(PreferencesRepo.Keys.RETENTION_DAYS_YOUTUBE) != "7" ||
                db.preferenceDao().getValue(PreferencesRepo.Keys.RETENTION_DAYS_MOMENTS) != "30" ||
                db.preferenceDao().getValue(PreferencesRepo.Keys.RETENTION_DAYS_FEED) != "14" ||
                db.preferenceDao().getValue(PreferencesRepo.Keys.STORIES_WINDOW_HOURS) != "72"
            ) {
                delay(10)
            }
            true
        }
        assertEquals(true, ok)
    }

    @Test fun clearCache_forwardsBucketToCacheOps() = runBlocking {
        val vm = newViewModel()
        vm.clearCache("youtube_videos")

        val ok = eventuallyVerify {
            coVerify { cacheOps.clearCache("youtube_videos") }
        }
        assertEquals(true, ok)
    }

    @Test fun clearCache_allBucketsWhenNull() = runBlocking {
        val vm = newViewModel()
        vm.clearCache(null)

        val ok = eventuallyVerify {
            coVerify { cacheOps.clearCache(null) }
        }
        assertEquals(true, ok)
    }

    @Test fun triggerSyncNow_forwardsToScheduler() {
        val vm = newViewModel()
        vm.triggerSyncNow()

        verify { scheduler.triggerAll() }
    }
}
