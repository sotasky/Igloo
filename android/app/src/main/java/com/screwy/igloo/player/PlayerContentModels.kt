package com.screwy.igloo.player

import com.screwy.igloo.data.entity.VideoCommentEntity
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull

internal data class VideoMetadataCounts(
    val viewCount: Long? = null,
    val viewCountLabel: String? = null,
    val likeCount: Long? = null,
    val likeCountLabel: String? = null,
)

internal fun parseVideoMetadataCounts(metadataJson: String?): VideoMetadataCounts {
    if (metadataJson.isNullOrBlank()) return VideoMetadataCounts()
    val obj = runCatching { Json.parseToJsonElement(metadataJson).jsonObject }.getOrNull()
        ?: return VideoMetadataCounts()
    return VideoMetadataCounts(
        viewCount = obj["view_count"]?.jsonPrimitive?.longOrNull,
        viewCountLabel = syncedCountLabel((obj["view_count_label"] as? JsonPrimitive)?.contentOrNull),
        likeCount = obj["like_count"]?.jsonPrimitive?.longOrNull,
        likeCountLabel = syncedCountLabel((obj["like_count_label"] as? JsonPrimitive)?.contentOrNull),
    )
}

internal fun commentInitial(name: String?): String {
    val trimmed = name?.trim().orEmpty()
    return trimmed.firstOrNull()?.uppercase() ?: "?"
}

internal fun youtubeCommentAuthorChannelId(authorId: String?): String? {
    var raw = authorId?.trim().orEmpty()
    if (raw.isEmpty()) return null
    if (raw.startsWith("youtube_")) {
        raw = raw.removePrefix("youtube_").trim()
    }
    if (!raw.startsWith("UC")) return null
    return "youtube_$raw"
}

internal fun normalizeCommentHandle(raw: String?): String =
    raw?.trim()?.removePrefix("@")?.takeIf { it.isNotBlank() } ?: ""

internal fun commentRenderDepth(comment: VideoCommentEntity): Int =
    comment.threadDepth.coerceAtLeast(0)

internal fun commentReplyAuthor(comment: VideoCommentEntity): String? =
    normalizeCommentHandle(comment.replyToAuthor).takeIf { it.isNotEmpty() }

internal fun commentIsCreator(comment: VideoCommentEntity): Boolean =
    comment.isCreator

internal fun commentLikeCountLabel(comment: VideoCommentEntity): String? =
    syncedCountLabel(comment.likeCountLabel)

private fun syncedCountLabel(raw: String?): String? =
    raw?.trim()?.takeIf { it.isNotEmpty() }
