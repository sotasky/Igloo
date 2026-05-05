package com.screwy.igloo.net

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.net.auth.NoAuthTokenProvider
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpStatusCode
import io.ktor.utils.io.ByteReadChannel
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
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * End-to-end sanity: HealthApi against a MockEngine returning the standard envelope.
 * Exercises the real igloo HTTP client stack — all interceptors apply — so this also
 * validates that installing everything together doesn't blow up.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class HealthApiTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var hostProvider: IglooHostProvider

    @Before fun setUp() = runBlocking {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 1_000L })
        prefs.setServerUrl("https://igloo.local:8443")
        hostProvider = IglooHostProvider(hostSource = { "igloo.local" })
    }

    @After fun tearDown() {
        scope.cancel()
        db.close()
    }

    @Test fun healthApi_returnsOkAndEnvelopeAdvancesOffset() = runBlocking {
        val body = """{"ok":true,"server_time_ms":5000}"""
        val engine = MockEngine { request ->
            assertEquals("https://igloo.local:8443/api/health", request.url.toString())
            respond(
                content = ByteReadChannel(body),
                status = HttpStatusCode.OK,
                headers = io.ktor.http.headersOf("Content-Type", "application/json"),
            )
        }
        val client = buildIglooClient(
            engine = engine,
            prefsProvider = { prefs },
            tokenProvider = NoAuthTokenProvider,
            hostProvider = hostProvider,
            nowMsProvider = { 1_000L },
        )
        val healthApi = HealthApi(client, baseUrlProvider = { "https://igloo.local:8443" })

        val response = healthApi.health()
        assertEquals(HttpStatusCode.OK, response.status)

        // Envelope parser should update the server-time offset: 5000 - 1000 = 4000.
        withTimeout(3_000L) { while (prefs.serverTimeOffsetMs().first() != 4_000L) delay(10) }
        assertEquals(4_000L, prefs.serverTimeOffsetMs().first())

        client.close()
    }
}
