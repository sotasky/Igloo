package com.screwy.igloo.net

import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.request.get
import io.ktor.client.statement.HttpResponse
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.utils.io.ByteReadChannel
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Verifies `HttpResponse.classify()` covers the 03-outbox.md §9 error-classification
 * matrix: 2xx → null, 401 → AuthRefreshRequired, 408/429/5xx → Transient, other 4xx → Dead.
 */
class ErrorClassifierTest {

    private fun clientFor(status: HttpStatusCode, body: String): HttpClient =
        HttpClient(MockEngine { _ ->
            respond(
                content = ByteReadChannel(body),
                status = status,
                headers = headersOf("Content-Type", ContentType.Application.Json.toString()),
            )
        }) {
            expectSuccess = false
        }

    private fun callClassify(status: HttpStatusCode, body: String = "{}"): IglooError? = runBlocking {
        val client = clientFor(status, body)
        try {
            val response: HttpResponse = client.get("/anything")
            response.classify()
        } finally {
            client.close()
        }
    }

    @Test fun success2xx_returnsNull() {
        assertNull(callClassify(HttpStatusCode.OK))
        assertNull(callClassify(HttpStatusCode.Created))
        assertNull(callClassify(HttpStatusCode.NoContent, body = ""))
    }

    @Test fun unauthorized_mapsToAuthRefreshRequired() {
        val body = """{"ok":false,"error_code":"access_token_expired","error_message":"token expired","server_time_ms":1}"""
        val err = callClassify(HttpStatusCode.Unauthorized, body)
        assertNotNull(err)
        assertTrue("expected AuthRefreshRequired, got ${err!!::class.simpleName}", err is IglooError.AuthRefreshRequired)
        assertTrue(err.requiresRefresh)
        assertEquals("access_token_expired", err.errorCode)
        assertEquals("token expired", err.errorMessage)
    }

    @Test fun serverErrors_mapToTransient() {
        listOf(
            HttpStatusCode.InternalServerError,
            HttpStatusCode.BadGateway,
            HttpStatusCode.ServiceUnavailable,
            HttpStatusCode.GatewayTimeout,
        ).forEach { status ->
            val err = callClassify(status)
            assertTrue("5xx $status should be Transient, got ${err?.let { it::class.simpleName }}", err is IglooError.Transient)
            assertTrue(err!!.isTransient)
        }
    }

    @Test fun timeoutAndRateLimited_mapToTransient() {
        assertTrue(callClassify(HttpStatusCode.RequestTimeout) is IglooError.Transient)
        assertTrue(callClassify(HttpStatusCode.TooManyRequests) is IglooError.Transient)
    }

    @Test fun otherClientErrors_mapToDead() {
        listOf(
            HttpStatusCode.BadRequest,
            HttpStatusCode.Forbidden,
            HttpStatusCode.NotFound,
            HttpStatusCode.Conflict,
            HttpStatusCode.UnprocessableEntity,
        ).forEach { status ->
            val err = callClassify(status)
            assertTrue("4xx $status should be Dead, got ${err?.let { it::class.simpleName }}", err is IglooError.Dead)
            assertTrue(err!!.isDead)
        }
    }

    @Test fun iglooErrorFlags_areMutuallyExclusive() {
        val transient = IglooError.Transient(503, null, null)
        assertTrue(transient.isTransient)
        assertTrue(!transient.isDead)
        assertTrue(!transient.requiresRefresh)

        val dead = IglooError.Dead(404, null, null)
        assertTrue(dead.isDead)
        assertTrue(!dead.isTransient)

        val refresh = IglooError.AuthRefreshRequired(401, null, null)
        assertTrue(refresh.requiresRefresh)
    }
}
