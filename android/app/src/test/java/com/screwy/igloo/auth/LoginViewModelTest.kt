package com.screwy.igloo.auth

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.screwy.igloo.R
import com.screwy.igloo.data.DatabaseHolder
import com.screwy.igloo.net.AuthApi
import com.screwy.igloo.testutil.ViewModelTestTracker
import com.screwy.igloo.ui.UiEffects
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.HttpResponseData
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.serialization.kotlinx.json.json
import io.ktor.utils.io.ByteReadChannel
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
import kotlinx.serialization.json.Json
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * `LoginViewModel` — input editing, submit gating, state transitions on every
 * `AuthRepo.LoginResult` branch. Mirrors the plan's "LoginRoute composable test"
 * at the ViewModel layer (Compose-UI tests run on device via androidTest).
 */
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class LoginViewModelTest {

    private lateinit var ctx: Context
    private lateinit var scope: CoroutineScope
    private lateinit var storage: InMemoryAuthStorage
    private lateinit var databaseHolder: DatabaseHolder
    private val viewModels = ViewModelTestTracker()

    @Before fun setUp() {
        Dispatchers.setMain(UnconfinedTestDispatcher())
        ctx = ApplicationProvider.getApplicationContext()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        storage = InMemoryAuthStorage()
        databaseHolder = DatabaseHolder(ctx)
    }

    @After fun tearDown() {
        viewModels.clearAll()
        databaseHolder.username?.let { databaseHolder.closeAndDelete(it) }
        scope.cancel()
        Dispatchers.resetMain()
    }

    private fun newViewModel(repo: AuthRepo, onLoginSuccess: () -> Unit = {}): LoginViewModel =
        viewModels.track(LoginViewModel(repo, onLoginSuccess))

    @Test fun initialState_prefillsServerUrlFromAuthStorage() {
        storage.edit { putString(AuthKeys.SERVER_URL, "https://custom.server:9443") }
        val repo = buildRepo(buildAuthApi())
        val vm = newViewModel(repo)

        assertEquals("https://custom.server:9443", vm.state.value.serverUrl)
        assertTrue(vm.state.value.status is LoginViewModel.Status.Idle)
    }

    @Test fun submitEnabled_requiresAllThreeFields() {
        val vm = newViewModel(buildRepo(buildAuthApi()))
        assertTrue(!vm.state.value.submitEnabled)
        vm.onUsernameChange("alice")
        assertTrue(!vm.state.value.submitEnabled)
        vm.onPasswordChange("pw")
        assertTrue(!vm.state.value.submitEnabled)
        vm.onServerUrlChange("https://igloo.local")
        assertTrue(vm.state.value.submitEnabled)
    }

    @Test fun plainHttpUrlAllowsSubmitWhenComplete() {
        val vm = newViewModel(buildRepo(buildAuthApi()))
        vm.onServerUrlChange("http://100.64.0.20:5001")
        vm.onUsernameChange("alice")
        vm.onPasswordChange("pw")

        assertTrue(vm.state.value.submitEnabled)
    }

    @Test fun submit_success_firesNavigateCallback() = runBlocking {
        val api = buildAuthApi(
            loginResponder = respondJson(
                """{"access_token":"acc","refresh_token":"ref","access_expires_at_ms":1,""" +
                    """"refresh_expires_at_ms":2,"username":"alice","role":"user","is_admin":false,""" +
                    """"platforms":[],"ok":true}""",
            ),
        )
        val navigated = BooleanBox()
        val vm = newViewModel(buildRepo(api), onLoginSuccess = { navigated.value = true })
        vm.onServerUrlChange("https://igloo.local")
        vm.onUsernameChange("alice")
        vm.onPasswordChange("pw")
        vm.onSubmit()

        withTimeoutOrNull(1_500L) { while (!navigated.value) delay(5) }
        assertTrue("expected onLoginSuccess to fire", navigated.value)
        assertEquals(LoginViewModel.Status.Idle, vm.state.value.status)
        assertEquals("", vm.state.value.password)
    }

    @Test fun submit_badCredentials_surfacesInlineError() = runBlocking {
        val api = buildAuthApi(
            loginResponder = respondJsonStatus(
                HttpStatusCode.Unauthorized,
                """{"ok":false,"error_code":"invalid_credentials"}""",
            ),
        )
        val vm = newViewModel(buildRepo(api))
        vm.onServerUrlChange("https://igloo.local")
        vm.onUsernameChange("alice")
        vm.onPasswordChange("bad")
        vm.onSubmit()

        withTimeoutOrNull(1_500L) {
            while (vm.state.value.status !is LoginViewModel.Status.Error) delay(5)
        }
        val err = vm.state.value.status as LoginViewModel.Status.Error
        assertEquals(R.string.login_error_invalid_credentials, err.resId)
    }

    @Test fun typing_clearsExistingError() = runBlocking {
        val api = buildAuthApi(
            loginResponder = respondJsonStatus(
                HttpStatusCode.Unauthorized,
                """{"ok":false,"error_code":"invalid_credentials"}""",
            ),
        )
        val vm = newViewModel(buildRepo(api))
        vm.onServerUrlChange("https://igloo.local")
        vm.onUsernameChange("alice")
        vm.onPasswordChange("bad")
        vm.onSubmit()
        withTimeoutOrNull(1_500L) {
            while (vm.state.value.status !is LoginViewModel.Status.Error) delay(5)
        }

        vm.onUsernameChange("alice2")
        assertEquals(LoginViewModel.Status.Idle, vm.state.value.status)
    }

    // ─── helpers ─────────────────────────────────────────────────────────────

    private fun buildRepo(api: AuthApi): AuthRepo = AuthRepo(
        context = ctx,
        storage = storage,
        databaseHolder = databaseHolder,
        uiEffects = UiEffects(),
        applicationScope = scope,
        authApiProvider = { api },
        nowMsProvider = { 0L },
    )

    private fun buildAuthApi(
        loginResponder: Responder? = null,
    ): AuthApi {
        val engine = MockEngine { request ->
            val responder = when (request.url.encodedPath) {
                "/api/auth/login" -> loginResponder
                else -> null
            } ?: error("no responder for ${request.url.encodedPath}")
            responder.respond(this)
        }
        val client = HttpClient(engine) {
            install(ContentNegotiation) { json(Json { ignoreUnknownKeys = true; isLenient = true }) }
            expectSuccess = true
        }
        return AuthApi(client) { "https://igloo.local" }
    }

    private fun interface Responder {
        suspend fun respond(scope: MockRequestHandleScope): HttpResponseData
    }

    private fun respondJson(body: String): Responder = Responder { scope ->
        scope.respond(
            content = ByteReadChannel(body),
            status = HttpStatusCode.OK,
            headers = headersOf("Content-Type", "application/json"),
        )
    }

    private fun respondJsonStatus(status: HttpStatusCode, body: String): Responder = Responder { scope ->
        scope.respond(
            content = ByteReadChannel(body),
            status = status,
            headers = headersOf("Content-Type", "application/json"),
        )
    }

    private class BooleanBox(var value: Boolean = false)
}
