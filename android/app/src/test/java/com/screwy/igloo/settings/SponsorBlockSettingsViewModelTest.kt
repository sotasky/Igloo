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
class SponsorBlockSettingsViewModelTest {

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

    private fun newViewModel(): SponsorBlockSettingsViewModel =
        viewModels.track(SponsorBlockSettingsViewModel(prefs))

    @Test fun sponsorBlock_defaultsMatchCategoryPolicy() = runBlocking {
        val vm = newViewModel()

        assertEquals(SponsorBlockSettings.SB_SILENT, vm.sbSponsor.value)
        assertEquals(SponsorBlockSettings.SB_SILENT, vm.sbSelfPromo.value)
        assertEquals(SponsorBlockSettings.SB_SILENT, vm.sbInteraction.value)
        assertEquals(SponsorBlockSettings.SB_ASK, vm.sbIntro.value)
        assertEquals(SponsorBlockSettings.SB_ASK, vm.sbOutro.value)
        assertEquals(SponsorBlockSettings.SB_ASK, vm.sbPreview.value)
        assertEquals(SponsorBlockSettings.SB_ASK, vm.sbFiller.value)
        assertEquals(SponsorBlockSettings.SB_ASK, vm.sbMusicOffTopic.value)
    }

    @Test fun setSponsorBlock_writesThroughToPrefs() = runBlocking {
        val vm = newViewModel()
        vm.setSponsorBlock(PreferencesRepo.Keys.SB_INTRO, SponsorBlockSettings.SB_OFF)

        val ok = withTimeoutOrNull(5_000L) {
            while (db.preferenceDao().getValue(PreferencesRepo.Keys.SB_INTRO) != SponsorBlockSettings.SB_OFF) delay(10)
            true
        }
        assertEquals(true, ok)
    }

    @Test fun dearrowMode_defaultsToOff() = runBlocking {
        val vm = newViewModel()
        assertEquals("off", vm.dearrowMode.value)
    }

    @Test fun setDearrowMode_writesThroughToPrefs() = runBlocking {
        val vm = newViewModel()
        vm.setDearrowMode("casual")

        val ok = withTimeoutOrNull(5_000L) {
            while (db.preferenceDao().getValue(PreferencesRepo.Keys.DEARROW_MODE) != "casual") delay(10)
            true
        }
        assertEquals(true, ok)
    }

    @Test fun setDearrowMode_invalidValueNormalizes() = runBlocking {
        val vm = newViewModel()
        vm.setDearrowMode("banana")

        val ok = withTimeoutOrNull(5_000L) {
            while (db.preferenceDao().getValue(PreferencesRepo.Keys.DEARROW_MODE) != "off") delay(10)
            true
        }
        assertEquals(true, ok)
    }
}
