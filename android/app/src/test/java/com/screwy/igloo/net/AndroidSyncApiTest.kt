package com.screwy.igloo.net

import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Test

class AndroidSyncApiTest {
    @Test
    fun bootstrapAndChangesOptInToFullYoutubeMetadata() = runBlocking {
        val requestedMetadataModes = mutableListOf<Pair<String, String?>>()
        val client =
            HttpClient(
                MockEngine { request ->
                    requestedMetadataModes +=
                        request.url.encodedPath to request.url.parameters["full_youtube_metadata"]
                    respond(
                        "{\"changes\":[],\"next_cursor\":\"cursor\",\"end_of_stream\":true}",
                        HttpStatusCode.OK,
                        headersOf("Content-Type", ContentType.Application.Json.toString()),
                    )
                },
            )
        try {
            val api = AndroidSyncApi(client) { "https://igloo.example" }
            val retention = AndroidSyncRetentionRequest(7, 14, 7, 48)

            api.bootstrap(retention, after = null)
            api.changes(retention, after = "cursor")

            assertEquals(
                listOf(
                    "/api/android/sync/bootstrap" to "1",
                    "/api/android/sync/changes" to "1",
                ),
                requestedMetadataModes,
            )
        } finally {
            client.close()
        }
    }
}
