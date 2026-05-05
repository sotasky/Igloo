package com.screwy.igloo.settings

import com.screwy.igloo.auth.AuthRepo
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.testutil.ViewModelTestTracker
import io.mockk.coVerify
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
class AccountSettingsViewModelTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var authRepo: AuthRepo
    private val viewModels = ViewModelTestTracker()

    @Before fun setUp() {
        Dispatchers.setMain(UnconfinedTestDispatcher())
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })
        authRepo = mockk(relaxed = true)
    }

    @After fun tearDown() {
        viewModels.clearAll()
        scope.cancel()
        db.close()
        Dispatchers.resetMain()
    }

    private fun newViewModel(): AccountSettingsViewModel =
        viewModels.track(AccountSettingsViewModel(prefs, authRepo))

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

    @Test fun setServerUrl_writesPrefsAndUpdatesAuthRepo() = runBlocking {
        val vm = newViewModel()
        vm.setServerUrl("https://new.example.com")

        val prefsOk = withTimeoutOrNull(5_000L) {
            while (db.preferenceDao().getValue(PreferencesRepo.Keys.SERVER_URL) != "https://new.example.com") delay(10)
            true
        }
        assertEquals(true, prefsOk)

        val authOk = eventuallyVerify {
            verify { authRepo.updateServerUrl("https://new.example.com") }
        }
        assertEquals(true, authOk)
    }

    @Test fun logout_forwardsToAuthRepo() = runBlocking {
        val vm = newViewModel()
        vm.logout()

        val ok = eventuallyVerify {
            coVerify { authRepo.logout() }
        }
        assertEquals(true, ok)
    }
}
