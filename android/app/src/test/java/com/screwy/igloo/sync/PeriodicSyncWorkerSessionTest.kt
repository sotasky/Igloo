package com.screwy.igloo.sync

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.screwy.igloo.auth.AuthKeys
import com.screwy.igloo.auth.AuthRepo
import com.screwy.igloo.auth.InMemoryAuthStorage
import com.screwy.igloo.data.DatabaseHolder
import com.screwy.igloo.net.AuthApi
import com.screwy.igloo.ui.UiEffects
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.serialization.kotlinx.json.json
import io.ktor.utils.io.ByteReadChannel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class PeriodicSyncWorkerSessionTest {

    private lateinit var ctx: Context
    private lateinit var storage: InMemoryAuthStorage
    private lateinit var databaseHolder: DatabaseHolder
    private lateinit var scope: CoroutineScope
    private lateinit var client: HttpClient

    @Before fun setUp() {
        ctx = ApplicationProvider.getApplicationContext()
        storage = InMemoryAuthStorage()
        databaseHolder = DatabaseHolder(ctx)
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    }

    @After fun tearDown() {
        if (::client.isInitialized) client.close()
        databaseHolder.username?.let { databaseHolder.closeAndDelete(it) }
        scope.cancel()
    }

    @Test fun periodicSessionOpensWithExpiredAccessAndValidRefresh() = runBlocking {
        val refreshCalls = AtomicInteger(0)
        storage.edit {
            putString(AuthKeys.ACCESS_TOKEN, "access-old")
            putString(AuthKeys.REFRESH_TOKEN, "refresh-valid")
            putLong(AuthKeys.ACCESS_EXPIRES_AT_MS, 1L)
            putLong(AuthKeys.REFRESH_EXPIRES_AT_MS, 20_000L)
            putString(AuthKeys.USERNAME, "alice")
        }
        val repo = buildRepo(
            refreshCalls = refreshCalls,
            nowMsProvider = { 5_000L },
        )

        val prepared = preparePeriodicSyncSession(databaseHolder, repo)

        assertTrue(prepared)
        assertNotNull(databaseHolder.current)
        assertEquals("alice", databaseHolder.username)
        assertEquals("access-new", repo.accessTokenSync())
        assertEquals("refresh-new", repo.refreshTokenSync())
        assertEquals(1, refreshCalls.get())
    }

    private fun buildRepo(
        refreshCalls: AtomicInteger,
        nowMsProvider: () -> Long,
    ): AuthRepo {
        client = HttpClient(
            MockEngine { request ->
                when (request.url.encodedPath) {
                    "/api/auth/refresh" -> {
                        refreshCalls.incrementAndGet()
                        respond(
                            content = ByteReadChannel(
                                """{"ok":true,"access_token":"access-new","refresh_token":"refresh-new","access_expires_at_ms":30000,"refresh_expires_at_ms":40000}""",
                            ),
                            status = HttpStatusCode.OK,
                            headers = headersOf("Content-Type", "application/json"),
                        )
                    }
                    else -> error("unexpected request ${request.url}")
                }
            },
        ) {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true; isLenient = true })
            }
        }
        return AuthRepo(
            storage = storage,
            databaseHolder = databaseHolder,
            uiEffects = UiEffects(),
            applicationScope = scope,
            authApiProvider = { AuthApi(client) { "https://igloo.local" } },
            nowMsProvider = nowMsProvider,
        )
    }
}
