package com.screwy.igloo.settings

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.i18n.AppLanguageStore
import com.screwy.igloo.testutil.ViewModelTestTracker
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * `SettingsHubViewModel` — owns hub-only prefs and delegates app language to
 * `AppLanguageStore`.
 */
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class SettingsHubViewModelTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var languageStore: AppLanguageStore
    private val viewModels = ViewModelTestTracker()

    @Before fun setUp() {
        Dispatchers.setMain(UnconfinedTestDispatcher())
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })
        val context = ApplicationProvider.getApplicationContext<Context>()
        languageStore = AppLanguageStore(context).also { it.setLanguageTag("") }
    }

    @After fun tearDown() {
        viewModels.clearAll()
        scope.cancel()
        db.close()
        Dispatchers.resetMain()
    }

    private fun newViewModel(): SettingsHubViewModel =
        viewModels.track(SettingsHubViewModel(prefs, languageStore))

    @Test fun setLanguageTag_writesAppWideLanguageStore() {
        val vm = newViewModel()
        vm.setLanguageTag("tr")

        assertEquals("tr", languageStore.languageTagSync())
        assertEquals("tr", vm.languageTag.value)
    }

    @Test fun setShareEmbedFriendlyLinks_writesGenericBoolKey() = runBlocking {
        val vm = newViewModel()
        vm.setShareEmbedFriendlyLinks(true)

        val ok = withTimeoutOrNull(5_000L) {
            while (db.preferenceDao().getValue(PreferencesRepo.Keys.SHARE_EMBED_FRIENDLY_LINKS) != "true") delay(10)
            true
        }
        assertEquals(true, ok)
    }
}
