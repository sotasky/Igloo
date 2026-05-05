package com.screwy.igloo.ui.nav

import androidx.lifecycle.SavedStateHandle
import androidx.navigation.NavController
import com.screwy.igloo.media.MediaUri
import java.io.File
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong

private const val ProfileSnapshotTokenKey = "igloo.profile_open.token"
private const val ProfileSnapshotChannelIdKey = "igloo.profile_open.channel_id"
private const val ProfileSnapshotDisplayNameKey = "igloo.profile_open.display_name"
private const val ProfileSnapshotHandleKey = "igloo.profile_open.handle"
private const val ProfileSnapshotPlatformKey = "igloo.profile_open.platform"
private const val ProfileSnapshotFollowedKey = "igloo.profile_open.followed"
private const val ProfileSnapshotStarredKey = "igloo.profile_open.starred"
private const val ProfileSnapshotAvatarKindKey = "igloo.profile_open.avatar_kind"
private const val ProfileSnapshotAvatarValueKey = "igloo.profile_open.avatar_value"
private const val ProfileSnapshotBannerKindKey = "igloo.profile_open.banner_kind"
private const val ProfileSnapshotBannerValueKey = "igloo.profile_open.banner_value"

data class ProfileOpenSnapshot(
    val channelId: String,
    val displayName: String,
    val handle: String,
    val platform: String,
    val isFollowed: Boolean,
    val isStarred: Boolean,
    val avatarUri: MediaUri = MediaUri.Missing,
    val bannerUri: MediaUri = MediaUri.Missing,
)

fun NavController.prepareProfileOpenSnapshotForNext(snapshot: ProfileOpenSnapshot) {
    currentBackStackEntry?.savedStateHandle?.writeProfileOpenSnapshot(snapshot)
}

fun NavController.consumeProfileOpenSnapshotFromPrevious(): ProfileOpenSnapshot? =
    previousBackStackEntry?.savedStateHandle?.consumeProfileOpenSnapshot()

private fun SavedStateHandle.writeProfileOpenSnapshot(snapshot: ProfileOpenSnapshot) {
    this[ProfileSnapshotTokenKey] = PendingProfileOpenSnapshots.put(snapshot)
    this[ProfileSnapshotChannelIdKey] = snapshot.channelId
    this[ProfileSnapshotDisplayNameKey] = snapshot.displayName
    this[ProfileSnapshotHandleKey] = snapshot.handle
    this[ProfileSnapshotPlatformKey] = snapshot.platform
    this[ProfileSnapshotFollowedKey] = snapshot.isFollowed
    this[ProfileSnapshotStarredKey] = snapshot.isStarred
    writeMediaUri(ProfileSnapshotAvatarKindKey, ProfileSnapshotAvatarValueKey, snapshot.avatarUri)
    writeMediaUri(ProfileSnapshotBannerKindKey, ProfileSnapshotBannerValueKey, snapshot.bannerUri)
}

private fun SavedStateHandle.consumeProfileOpenSnapshot(): ProfileOpenSnapshot? {
    val token = remove<String>(ProfileSnapshotTokenKey)
    val pending = token?.let(PendingProfileOpenSnapshots::take)
    val channelId = remove<String>(ProfileSnapshotChannelIdKey)?.takeIf { it.isNotBlank() } ?: return null
    val fallback = ProfileOpenSnapshot(
        channelId = channelId,
        displayName = remove<String>(ProfileSnapshotDisplayNameKey).orEmpty(),
        handle = remove<String>(ProfileSnapshotHandleKey).orEmpty(),
        platform = remove<String>(ProfileSnapshotPlatformKey).orEmpty(),
        isFollowed = remove<Boolean>(ProfileSnapshotFollowedKey) ?: false,
        isStarred = remove<Boolean>(ProfileSnapshotStarredKey) ?: false,
        avatarUri = consumeMediaUri(ProfileSnapshotAvatarKindKey, ProfileSnapshotAvatarValueKey),
        bannerUri = consumeMediaUri(ProfileSnapshotBannerKindKey, ProfileSnapshotBannerValueKey),
    )
    return pending ?: fallback
}

private fun SavedStateHandle.writeMediaUri(kindKey: String, valueKey: String, uri: MediaUri) {
    when (uri) {
        is MediaUri.Local -> {
            this[kindKey] = MediaUriKindLocal
            this[valueKey] = uri.file.absolutePath
        }
        is MediaUri.Remote -> {
            this[kindKey] = MediaUriKindRemote
            this[valueKey] = uri.url
        }
        is MediaUri.Missing -> {
            this[kindKey] = MediaUriKindMissing
            this[valueKey] = ""
        }
    }
}

private fun SavedStateHandle.consumeMediaUri(kindKey: String, valueKey: String): MediaUri =
    when (remove<String>(kindKey)) {
        MediaUriKindLocal -> remove<String>(valueKey)
            ?.takeIf { it.isNotBlank() }
            ?.let { MediaUri.Local(File(it)) }
            ?: MediaUri.Missing
        MediaUriKindRemote -> remove<String>(valueKey)
            ?.takeIf { it.isNotBlank() }
            ?.let(MediaUri::Remote)
            ?: MediaUri.Missing
        else -> MediaUri.Missing
    }

private const val MediaUriKindLocal = "local"
private const val MediaUriKindRemote = "remote"
private const val MediaUriKindMissing = "missing"

private object PendingProfileOpenSnapshots {
    private val nextToken = AtomicLong(1L)
    private val snapshots = ConcurrentHashMap<String, ProfileOpenSnapshot>()

    fun put(snapshot: ProfileOpenSnapshot): String {
        val token = nextToken.getAndIncrement().toString()
        snapshots[token] = snapshot
        return token
    }

    fun take(token: String): ProfileOpenSnapshot? =
        snapshots.remove(token)
}
