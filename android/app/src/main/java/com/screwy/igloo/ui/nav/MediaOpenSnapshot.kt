package com.screwy.igloo.ui.nav

import androidx.lifecycle.SavedStateHandle
import androidx.navigation.NavController
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.ui.component.MediaSet
import java.io.File
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong

private const val SnapshotTokenKey = "igloo.media_open.token"
private const val SnapshotOwnerKindKey = "igloo.media_open.owner_kind"
private const val SnapshotOwnerIdKey = "igloo.media_open.owner_id"
private const val SnapshotIndexKey = "igloo.media_open.index"
private const val SnapshotMediaCountKey = "igloo.media_open.media_count"
private const val SnapshotLikedKey = "igloo.media_open.liked"
private const val SnapshotBookmarkedKey = "igloo.media_open.bookmarked"
private const val SnapshotPosterKindKey = "igloo.media_open.poster_kind"
private const val SnapshotPosterValueKey = "igloo.media_open.poster_value"

private const val PosterKindLocal = "local"
private const val PosterKindRemote = "remote"
private const val PosterKindMissing = "missing"

data class MediaOpenSnapshot(
    val ownerKind: String,
    val ownerId: String,
    val index: Int,
    val mediaCount: Int,
    val posterUri: MediaUri,
    val isLiked: Boolean,
    val isBookmarked: Boolean,
    val mediaSet: MediaSet? = null,
)

fun NavController.prepareMediaOpenSnapshotForNext(snapshot: MediaOpenSnapshot) {
    currentBackStackEntry?.savedStateHandle?.writeMediaOpenSnapshot(snapshot)
}

fun NavController.consumeMediaOpenSnapshotFromPrevious(): MediaOpenSnapshot? =
    previousBackStackEntry?.savedStateHandle?.consumeMediaOpenSnapshot()

private fun SavedStateHandle.writeMediaOpenSnapshot(snapshot: MediaOpenSnapshot) {
    remove<String>(SnapshotTokenKey)?.let(PendingMediaOpenSnapshots::take)
    this[SnapshotTokenKey] = PendingMediaOpenSnapshots.put(snapshot)
    this[SnapshotOwnerKindKey] = snapshot.ownerKind
    this[SnapshotOwnerIdKey] = snapshot.ownerId
    this[SnapshotIndexKey] = snapshot.index
    this[SnapshotMediaCountKey] = snapshot.mediaCount
    this[SnapshotLikedKey] = snapshot.isLiked
    this[SnapshotBookmarkedKey] = snapshot.isBookmarked
    when (val uri = snapshot.posterUri) {
        is MediaUri.Local -> {
            this[SnapshotPosterKindKey] = PosterKindLocal
            this[SnapshotPosterValueKey] = uri.file.absolutePath
        }
        is MediaUri.Remote -> {
            this[SnapshotPosterKindKey] = PosterKindRemote
            this[SnapshotPosterValueKey] = uri.url
        }
        is MediaUri.Missing -> {
            this[SnapshotPosterKindKey] = PosterKindMissing
            this[SnapshotPosterValueKey] = ""
        }
    }
}

private fun SavedStateHandle.consumeMediaOpenSnapshot(): MediaOpenSnapshot? {
    val token = remove<String>(SnapshotTokenKey)
    val pending = token?.let(PendingMediaOpenSnapshots::take)
    val ownerKind = remove<String>(SnapshotOwnerKindKey)?.takeIf { it.isNotBlank() } ?: return null
    val ownerId = remove<String>(SnapshotOwnerIdKey)?.takeIf { it.isNotBlank() } ?: return null
    val index = remove<Int>(SnapshotIndexKey) ?: 0
    val mediaCount = remove<Int>(SnapshotMediaCountKey) ?: 0
    val isLiked = remove<Boolean>(SnapshotLikedKey) ?: false
    val isBookmarked = remove<Boolean>(SnapshotBookmarkedKey) ?: false
    val posterKind = remove<String>(SnapshotPosterKindKey)
    val posterValue = remove<String>(SnapshotPosterValueKey).orEmpty()
    val posterUri = when (posterKind) {
        PosterKindLocal -> posterValue.takeIf { it.isNotBlank() }?.let { MediaUri.Local(File(it)) } ?: MediaUri.Missing
        PosterKindRemote -> posterValue.takeIf { it.isNotBlank() }?.let(MediaUri::Remote) ?: MediaUri.Missing
        else -> MediaUri.Missing
    }
    if (pending != null) return pending
    return MediaOpenSnapshot(
        ownerKind = ownerKind,
        ownerId = ownerId,
        index = index,
        mediaCount = mediaCount,
        posterUri = posterUri,
        isLiked = isLiked,
        isBookmarked = isBookmarked,
    )
}

private object PendingMediaOpenSnapshots {
    private val nextToken = AtomicLong(1L)
    private val snapshots = ConcurrentHashMap<String, MediaOpenSnapshot>()

    fun put(snapshot: MediaOpenSnapshot): String {
        val token = nextToken.getAndIncrement().toString()
        snapshots[token] = snapshot
        return token
    }

    fun take(token: String): MediaOpenSnapshot? =
        snapshots.remove(token)
}
