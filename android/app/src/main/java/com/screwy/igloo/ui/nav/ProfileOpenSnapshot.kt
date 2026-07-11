package com.screwy.igloo.ui.nav

import androidx.lifecycle.SavedStateHandle
import androidx.navigation.NavController

private const val ProfileSnapshotChannelIdKey = "igloo.profile_open.channel_id"
private const val ProfileSnapshotDisplayNameKey = "igloo.profile_open.display_name"
private const val ProfileSnapshotHandleKey = "igloo.profile_open.handle"
private const val ProfileSnapshotPlatformKey = "igloo.profile_open.platform"
private const val ProfileSnapshotFollowedKey = "igloo.profile_open.followed"
private const val ProfileSnapshotStarredKey = "igloo.profile_open.starred"

data class ProfileOpenSnapshot(
    val channelId: String,
    val displayName: String,
    val handle: String,
    val platform: String,
    val isFollowed: Boolean,
    val isStarred: Boolean,
)

fun NavController.prepareProfileOpenSnapshotForNext(snapshot: ProfileOpenSnapshot) {
    currentBackStackEntry?.savedStateHandle?.writeProfileOpenSnapshot(snapshot)
}

fun NavController.consumeProfileOpenSnapshotFromPrevious(): ProfileOpenSnapshot? =
    previousBackStackEntry?.savedStateHandle?.consumeProfileOpenSnapshot()

private fun SavedStateHandle.writeProfileOpenSnapshot(snapshot: ProfileOpenSnapshot) {
    this[ProfileSnapshotChannelIdKey] = snapshot.channelId
    this[ProfileSnapshotDisplayNameKey] = snapshot.displayName
    this[ProfileSnapshotHandleKey] = snapshot.handle
    this[ProfileSnapshotPlatformKey] = snapshot.platform
    this[ProfileSnapshotFollowedKey] = snapshot.isFollowed
    this[ProfileSnapshotStarredKey] = snapshot.isStarred
}

private fun SavedStateHandle.consumeProfileOpenSnapshot(): ProfileOpenSnapshot? {
    val channelId = remove<String>(ProfileSnapshotChannelIdKey)?.takeIf { it.isNotBlank() } ?: return null
    return ProfileOpenSnapshot(
        channelId = channelId,
        displayName = remove<String>(ProfileSnapshotDisplayNameKey).orEmpty(),
        handle = remove<String>(ProfileSnapshotHandleKey).orEmpty(),
        platform = remove<String>(ProfileSnapshotPlatformKey).orEmpty(),
        isFollowed = remove<Boolean>(ProfileSnapshotFollowedKey) ?: false,
        isStarred = remove<Boolean>(ProfileSnapshotStarredKey) ?: false,
    )
}
