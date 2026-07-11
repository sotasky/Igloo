package com.screwy.igloo.net

import io.ktor.http.encodeURLPathPart

fun androidSyncAssetPath(assetId: String, revision: Long): String =
    "/api/android/sync/assets/${assetId.encodeURLPathPart()}/file?revision=$revision"
