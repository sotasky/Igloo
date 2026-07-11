package com.screwy.igloo.net

import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.plugins.ClientRequestException
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.client.statement.HttpResponse
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import kotlinx.serialization.Serializable

/**
 * `/api/auth/{login,refresh,logout}`.
 */
class AuthApi(
    private val client: HttpClient,
    private val baseUrlProvider: () -> String,
) {

    suspend fun login(username: String, password: String): LoginResponse {
        val response = client.post(baseUrlProvider() + "/api/auth/login") {
            contentType(ContentType.Application.Json)
            setBody(LoginRequest(username, password))
        }
        return response.body()
    }

    suspend fun refresh(refreshToken: String): RefreshResponse {
        val response = client.post(baseUrlProvider() + "/api/auth/refresh") {
            contentType(ContentType.Application.Json)
            setBody(RefreshRequest(refreshToken))
        }
        if (response.status == HttpStatusCode.Unauthorized) {
            throw ClientRequestException(response, "unauthorized")
        }
        return response.body()
    }

    suspend fun logout(refreshToken: String): HttpResponse =
        client.post(baseUrlProvider() + "/api/auth/logout") {
            contentType(ContentType.Application.Json)
            setBody(RefreshRequest(refreshToken))
        }
}

@Serializable
data class LoginRequest(val username: String, val password: String)

@Serializable
data class RefreshRequest(val refresh_token: String)

@Serializable
data class LoginResponse(
    val access_token: String,
    val refresh_token: String,
    val access_expires_at_ms: Long,
    val refresh_expires_at_ms: Long,
    val username: String,
    val role: String,
    val platforms: List<String> = emptyList(),
    val is_admin: Boolean = false,
    val ok: Boolean = true,
    val server_time_ms: Long? = null,
)

@Serializable
data class RefreshResponse(
    val access_token: String,
    val refresh_token: String,
    val access_expires_at_ms: Long,
    val refresh_expires_at_ms: Long,
    val ok: Boolean = true,
    val server_time_ms: Long? = null,
)
