package com.screwy.igloo.player

import com.screwy.igloo.data.entity.VideoCommentEntity
import com.screwy.igloo.ui.component.compactCount
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
        viewCountLabel = obj["view_count"]?.jsonPrimitive?.longOrNull?.let(::compactCount),
        likeCount = obj["like_count"]?.jsonPrimitive?.longOrNull,
        likeCountLabel = obj["like_count"]?.jsonPrimitive?.longOrNull?.let(::compactCount),
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

internal fun commentLikeCountLabel(comment: VideoCommentEntity): String? =
    comment.likeCount?.takeIf { it > 0 }?.let(::compactCount)

internal data class PresentedVideoComment(
    val comment: VideoCommentEntity,
    val depth: Int,
    val replyToAuthor: String?,
    val isCreator: Boolean,
)

internal fun presentVideoComments(
    comments: List<VideoCommentEntity>,
    creatorChannelId: String?,
): List<PresentedVideoComment> {
    val byId = comments.associateBy { it.commentId }
    val children = mutableMapOf<String, MutableList<VideoCommentEntity>>()
    val roots = mutableListOf<VideoCommentEntity>()
    comments.forEach { comment ->
        val parentId = comment.parentId.orEmpty()
        if (parentId.isNotEmpty() && byId.containsKey(parentId) && validCommentParent(comment.commentId, parentId, byId)) {
            children.getOrPut(parentId) { mutableListOf() } += comment
        } else {
            roots += comment
        }
    }
    return buildList {
        fun append(comment: VideoCommentEntity, depth: Int, parent: VideoCommentEntity?) {
            add(
                PresentedVideoComment(
                    comment = comment,
                    depth = depth,
                    replyToAuthor = normalizeCommentHandle(parent?.authorName).takeIf(String::isNotEmpty),
                    isCreator = youtubeCommentAuthorChannelId(comment.authorId) == creatorChannelId,
                ),
            )
            children[comment.commentId].orEmpty().forEach { append(it, depth + 1, comment) }
        }
        roots.forEach { append(it, 0, null) }
    }
}

private fun validCommentParent(
    commentId: String,
    parentId: String,
    byId: Map<String, VideoCommentEntity>,
): Boolean {
    val visited = mutableSetOf<String>()
    var current = parentId
    while (current.isNotEmpty()) {
        if (current == commentId || !visited.add(current)) return false
        current = byId[current]?.parentId.orEmpty()
    }
    return true
}
