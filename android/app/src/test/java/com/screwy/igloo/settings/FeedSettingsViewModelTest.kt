package com.screwy.igloo.settings

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.testutil.ViewModelTestTracker
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
class FeedSettingsViewModelTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private val viewModels = ViewModelTestTracker()

    @Before fun setUp() {
        Dispatchers.setMain(UnconfinedTestDispatcher())
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })
    }

    @After fun tearDown() {
        viewModels.clearAll()
        scope.cancel()
        db.close()
        Dispatchers.resetMain()
    }

    private fun newViewModel(): FeedSettingsViewModel =
        viewModels.track(FeedSettingsViewModel(prefs))

    @Test fun setIncludeReposts_writesGenericBoolKey() = runBlocking {
        val vm = newViewModel()
        vm.setIncludeReposts(false)

        val ok = withTimeoutOrNull(5_000L) {
            while (db.preferenceDao().getValue(PreferencesRepo.Keys.INCLUDE_REPOSTS_DEFAULT) != "false") delay(10)
            true
        }
        assertEquals(true, ok)
    }

    @Test fun setMediaOnly_writesGenericBoolKey() = runBlocking {
        val vm = newViewModel()
        vm.setMediaOnly(true)

        val ok = withTimeoutOrNull(5_000L) {
            while (db.preferenceDao().getValue(PreferencesRepo.Keys.MEDIA_ONLY_DEFAULT) != "true") delay(10)
            true
        }
        assertEquals(true, ok)
    }
}
