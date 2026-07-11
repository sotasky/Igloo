package com.screwy.igloo.auth

import com.screwy.igloo.R
import com.screwy.igloo.net.AuthApi
import com.screwy.igloo.ui.UiEffect
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
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.onSubscription
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.Json
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * `AuthRepo` lifecycle across login, logout, refresh, and terminal auth failure.
 */
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class AuthRepoTest {

    private lateinit var storage: InMemoryAuthStorage
    private var logoutStopCount = 0
    private lateinit var uiEffects: UiEffects
    private lateinit var collectedEffects: MutableList<UiEffect>
    private lateinit var effectsScope: CoroutineScope
    private lateinit var scope: CoroutineScope

    @Before fun setUp() = runBlocking {
        Dispatchers.setMain(UnconfinedTestDispatcher())
        storage = InMemoryAuthStorage()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        logoutStopCount = 0

        uiEffects = UiEffects()
        collectedEffects = mutableListOf()
        effectsScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        // SharedFlow(replay=0) drops emissions that land before the first subscriber —
        // onSubscription fires after the subscriber attaches but before the first
        // collect iteration, so awaiting the latch proves the collector is live.
        val subscribed = CompletableDeferred<Unit>()
        effectsScope.launch {
            uiEffects.flow
                .onSubscription { subscribed.complete(Unit) }
                .collect { collectedEffects += it }
        }
        withTimeoutOrNull(1_000L) { subscribed.await() }
        Unit
    }

    @After fun tearDown() {
        effectsScope.cancel()
        scope.cancel()
        Dispatchers.resetMain()
    }

    // ─── login ───────────────────────────────────────────────────────────────

    @Test fun login_success_persistsTokensAndStartsBootstrap() = runBlocking {
        val api = buildAuthApi(
            loginResponder = respondJson(LOGIN_RESPONSE_ALICE),
        )
        val bootstrapFired = BooleanBox()
        val prefsUrls = mutableListOf<String>()
        val repo = buildRepo(
            api,
            prefsUpdater = { url -> prefsUrls += url },
            onBootstrap = { bootstrapFired.value = true },
        )

        val result = repo.login("https://igloo.local:8443/", "alice", "hunter2")

        assertEquals(AuthRepo.LoginResult.Success, result)
        assertEquals("acc-1", repo.accessTokenSync())
        assertEquals("ref-1", repo.refreshTokenSync())
        assertEquals("alice", repo.usernameSync())
        assertTrue(repo.isAdminSync())
        assertEquals("https://igloo.local:8443", repo.serverUrlSync())
        assertTrue(repo.isLoggedInSync())
        assertTrue(repo.hasSessionSync())
        assertTrue(bootstrapFired.value)
        assertEquals("acc-1", storage.getString(AuthKeys.ACCESS_TOKEN))
        assertEquals(2000L, storage.getLong(AuthKeys.ACCESS_EXPIRES_AT_MS))
        // Prefs mirror runs on the app scope; wait briefly for the launch to complete.
        withTimeoutOrNull(1_000L) { while (prefsUrls.isEmpty()) delay(5) }
        assertEquals(listOf("https://igloo.local:8443"), prefsUrls)
    }

    @Test fun login_badCredentials_mapsTo401() = runBlocking {
        val api = buildAuthApi(
            loginResponder = respondJsonStatus(
                status = HttpStatusCode.Unauthorized,
                body = """{"ok":false,"error_code":"invalid_credentials","error_message":"nope"}""",
            ),
        )
        val repo = buildRepo(api)

        val result = repo.login("https://igloo.local", "alice", "bad")

        assertEquals(AuthRepo.LoginResult.BadCredentials, result)
        assertNull(repo.accessTokenSync())
    }

    // ─── logout ──────────────────────────────────────────────────────────────

    @Test fun logout_wipesTokensAndKeepsServerUrl() = runBlocking {
        val api = buildAuthApi(
            loginResponder = respondJson(LOGIN_RESPONSE_BOB),
            logoutResponder = respondJson("""{"ok":true}"""),
        )
        val repo = buildRepo(api)
        repo.login("https://igloo.local", "bob", "pw")
        repo.logout(LogoutReason.UserInitiated)

        assertNull(repo.accessTokenSync())
        assertNull(repo.refreshTokenSync())
        assertNull(repo.usernameSync())
        assertEquals("https://igloo.local", repo.serverUrlSync())
        // user-initiated logout surfaces no toast
        assertTrue(collectedEffects.isEmpty())
        assertEquals(mapOf(AuthKeys.SERVER_URL to "https://igloo.local"), storage.snapshot())
        assertEquals(1, logoutStopCount)
    }

    @Test fun logout_withReason_surfacesToast() = runBlocking {
        val api = buildAuthApi(
            loginResponder = respondJson(LOGIN_RESPONSE_BOB),
            logoutResponder = respondJson("""{"ok":true}"""),
        )
        val repo = buildRepo(api)
        repo.login("https://igloo.local", "bob", "pw")
        collectedEffects.clear()

        repo.logout(LogoutReason.SessionRevoked)

        withTimeoutOrNull(1_000L) { while (collectedEffects.isEmpty()) delay(5) }
        val toast = collectedEffects.filterIsInstance<UiEffect.ToastRes>().firstOrNull()
        assertNotNull("expected a ToastRes effect, got $collectedEffects", toast)
        assertEquals(R.string.auth_signed_out_session_ended, toast!!.resId)
        assertTrue(toast.longDuration)
    }

    // ─── refresh / onAuthExpired ─────────────────────────────────────────────

    @Test fun onAuthExpired_refreshRotatesTokens() = runBlocking {
        val api = buildAuthApi(
            loginResponder = respondJson(LOGIN_RESPONSE_BOB.replace("\"acc\"", "\"acc-old\"").replace("\"ref\"", "\"ref-old\"")),
            refreshResponder = respondJson(
                """{"ok":true,"access_token":"acc-new","refresh_token":"ref-new",""" +
                    """"access_expires_at_ms":999,"refresh_expires_at_ms":8888}""",
            ),
        )
        val repo = buildRepo(api)
        repo.login("https://igloo.local", "bob", "pw")

        val newToken = repo.onAuthExpired(errorCode = "access_token_expired")

        assertEquals("acc-new", newToken)
        assertEquals("acc-new", repo.accessTokenSync())
        assertEquals("ref-new", repo.refreshTokenSync())
        assertTrue(collectedEffects.none { it is UiEffect.RequireLogin })
    }

    @Test fun expiredAccessWithValidRefresh_keepsSession() = runBlocking {
        storage.edit {
            putString(AuthKeys.ACCESS_TOKEN, "acc-old")
            putString(AuthKeys.REFRESH_TOKEN, "ref-valid")
            putLong(AuthKeys.ACCESS_EXPIRES_AT_MS, 1L)
            putLong(AuthKeys.REFRESH_EXPIRES_AT_MS, 10_000L)
            putString(AuthKeys.USERNAME, "bob")
        }
        val repo = buildRepo(
            buildAuthApi(),
            nowMsProvider = { 5_000L },
        )

        assertFalse(repo.isLoggedInSync())
        assertTrue(repo.hasSessionSync())
    }

    @Test fun onAppStart_transientRefreshFailure_keepsLocalSession() = runBlocking {
        storage.edit {
            putString(AuthKeys.ACCESS_TOKEN, "acc-old")
            putString(AuthKeys.REFRESH_TOKEN, "ref-valid")
            putLong(AuthKeys.ACCESS_EXPIRES_AT_MS, 1L)
            putLong(AuthKeys.REFRESH_EXPIRES_AT_MS, 10_000L)
            putString(AuthKeys.USERNAME, "bob")
        }
        val repo = buildRepo(
            buildAuthApi(),
            nowMsProvider = { 5_000L },
        )

        repo.onAppStart()

        assertEquals("acc-old", repo.accessTokenSync())
        assertEquals("ref-valid", repo.refreshTokenSync())
        assertTrue(repo.hasSessionSync())
        assertTrue(collectedEffects.none { it is UiEffect.RequireLogin })
    }

    @Test fun onAuthExpired_terminal_logsOutAndRequiresLogin() = runBlocking {
        val api = buildAuthApi(
            loginResponder = respondJson(LOGIN_RESPONSE_BOB),
            logoutResponder = respondJson("""{"ok":true}"""),
        )
        val repo = buildRepo(api)
        repo.login("https://igloo.local", "bob", "pw")
        collectedEffects.clear()

        val newToken = repo.onAuthExpired(errorCode = "session_revoked")

        assertNull(newToken)
        assertNull(repo.accessTokenSync())
        withTimeoutOrNull(1_000L) {
            while (collectedEffects.none { it is UiEffect.RequireLogin }) delay(5)
        }
        assertTrue(collectedEffects.any { it is UiEffect.RequireLogin })
        assertTrue(collectedEffects.any { it is UiEffect.ToastRes })
    }

    @Test fun onAuthExpired_refreshFailure_firesRequireLogin() = runBlocking {
        val api = buildAuthApi(
            loginResponder = respondJson(LOGIN_RESPONSE_BOB),
            refreshResponder = respondJsonStatus(
                status = HttpStatusCode.Unauthorized,
                body = """{"ok":false,"error_code":"refresh_token_expired"}""",
            ),
            logoutResponder = respondJson("""{"ok":true}"""),
        )
        val repo = buildRepo(api)
        repo.login("https://igloo.local", "bob", "pw")
        collectedEffects.clear()

        val newToken = repo.onAuthExpired(errorCode = "access_token_expired")

        assertNull(newToken)
        withTimeoutOrNull(1_000L) {
            while (collectedEffects.none { it is UiEffect.RequireLogin }) delay(5)
        }
        assertTrue(collectedEffects.any { it is UiEffect.RequireLogin })
        assertNull(repo.accessTokenSync())
    }

    @Test fun onAuthExpired_transientRefreshFailure_keepsLocalSession() = runBlocking {
        val api = buildAuthApi(
            loginResponder = respondJson(LOGIN_RESPONSE_BOB),
            refreshResponder = respondJsonStatus(
                status = HttpStatusCode.InternalServerError,
                body = """{"ok":false,"error_code":"server_error"}""",
            ),
            logoutResponder = respondJson("""{"ok":true}"""),
        )
        val repo = buildRepo(api)
        repo.login("https://igloo.local", "bob", "pw")
        collectedEffects.clear()

        val newToken = repo.onAuthExpired(errorCode = "access_token_expired")

        assertNull(newToken)
        assertEquals("acc", repo.accessTokenSync())
        assertEquals("ref", repo.refreshTokenSync())
        assertEquals("bob", repo.usernameSync())
        assertTrue(collectedEffects.none { it is UiEffect.RequireLogin })
    }

    @Test fun onAuthExpired_refresh401WithErrorEnvelope_logsOutWithExpectSuccessFalse() = runBlocking {
        val api = buildAuthApi(
            loginResponder = respondJson(LOGIN_RESPONSE_BOB),
            refreshResponder = respondJsonStatus(
                status = HttpStatusCode.Unauthorized,
                body = """{"ok":false,"error_code":"refresh_token_expired","error_message":"expired"}""",
            ),
            logoutResponder = respondJson("""{"ok":true}"""),
            expectSuccessfulResponses = false,
        )
        val repo = buildRepo(api)
        assertEquals(AuthRepo.LoginResult.Success, repo.login("https://igloo.local", "bob", "pw"))
        assertEquals("ref", repo.refreshTokenSync())
        collectedEffects.clear()

        val newToken = repo.onAuthExpired(errorCode = "access_token_expired")

        assertNull(newToken)
        withTimeoutOrNull(1_000L) {
            while (collectedEffects.none { it is UiEffect.RequireLogin }) delay(5)
        }
        assertTrue(collectedEffects.any { it is UiEffect.RequireLogin })
        assertNull(repo.accessTokenSync())
        assertNull(repo.refreshTokenSync())
        assertNull(repo.usernameSync())
    }

    // ─── URL normalization ───────────────────────────────────────────────────

    @Test fun normalizeServerUrl_stripsTrailingSlashAndAddsHttpScheme() {
        assertEquals("https://igloo.local:8443", AuthRepo.normalizeServerUrl("https://igloo.local:8443/"))
        assertEquals("http://igloo.local", AuthRepo.normalizeServerUrl("igloo.local"))
        assertEquals("http://igloo.local", AuthRepo.normalizeServerUrl("http://igloo.local"))
        assertEquals("", AuthRepo.normalizeServerUrl(""))
    }

    // ─── helpers ─────────────────────────────────────────────────────────────

    private fun buildRepo(
        api: AuthApi,
        prefsUpdater: suspend (String) -> Unit = {},
        onBootstrap: () -> Unit = {},
        nowMsProvider: () -> Long = { 0L },
    ): AuthRepo = AuthRepo(
        storage = storage,
        uiEffects = uiEffects,
        applicationScope = scope,
        authApiProvider = { api },
        stopReconcilersOnLogout = { logoutStopCount += 1 },
        prefsUpdater = prefsUpdater,
        onPostLoginBootstrap = onBootstrap,
        nowMsProvider = nowMsProvider,
    )

    private fun buildAuthApi(
        loginResponder: Responder? = null,
        refreshResponder: Responder? = null,
        logoutResponder: Responder? = null,
        expectSuccessfulResponses: Boolean = true,
    ): AuthApi {
        val engine = MockEngine { request ->
            val responder = when (request.url.encodedPath) {
                "/api/auth/login" -> loginResponder
                "/api/auth/refresh" -> refreshResponder
                "/api/auth/logout" -> logoutResponder
                else -> null
            } ?: error("no responder for ${request.url.encodedPath}")
            responder.respond(this)
        }
        val client = HttpClient(engine) {
            expectSuccess = expectSuccessfulResponses
            install(ContentNegotiation) { json(Json { ignoreUnknownKeys = true; isLenient = true }) }
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

    companion object {
        private const val LOGIN_RESPONSE_ALICE =
            """{"access_token":"acc-1","refresh_token":"ref-1","access_expires_at_ms":2000,""" +
            """"refresh_expires_at_ms":9000,"username":"alice","role":"admin","is_admin":true,""" +
            """"platforms":["twitter"],"ok":true}"""

        private const val LOGIN_RESPONSE_BOB =
            """{"access_token":"acc","refresh_token":"ref","access_expires_at_ms":1,""" +
            """"refresh_expires_at_ms":2,"username":"bob","role":"user","is_admin":false,""" +
            """"platforms":[],"ok":true}"""
    }
}
