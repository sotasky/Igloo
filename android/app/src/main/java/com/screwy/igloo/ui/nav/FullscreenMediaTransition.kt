package com.screwy.igloo.ui.nav

import androidx.lifecycle.SavedStateHandle
import androidx.navigation.NavController
import com.screwy.igloo.media.MediaUri
import java.io.File

private const val TransitionMediaIdKey = "igloo.fullscreen_transition.media_id"
private const val TransitionPosterKindKey = "igloo.fullscreen_transition.poster_kind"
private const val TransitionPosterValueKey = "igloo.fullscreen_transition.poster_value"

private const val PosterKindLocal = "local"
private const val PosterKindRemote = "remote"
private const val PosterKindMissing = "missing"

data class FullscreenMediaTransition(
    val mediaId: String,
    val posterUri: MediaUri,
)

fun NavController.prepareFullscreenMediaTransitionForNext(transition: FullscreenMediaTransition) {
    currentBackStackEntry?.savedStateHandle?.writeFullscreenMediaTransition(transition)
}

fun NavController.prepareFullscreenMediaTransitionForPrevious(transition: FullscreenMediaTransition) {
    previousBackStackEntry?.savedStateHandle?.writeFullscreenMediaTransition(transition)
}

fun NavController.consumeFullscreenMediaTransitionFromCurrent(): FullscreenMediaTransition? =
    currentBackStackEntry?.savedStateHandle?.consumeFullscreenMediaTransition()

fun NavController.consumeFullscreenMediaTransitionFromPrevious(): FullscreenMediaTransition? =
    previousBackStackEntry?.savedStateHandle?.consumeFullscreenMediaTransition()

private fun SavedStateHandle.writeFullscreenMediaTransition(transition: FullscreenMediaTransition) {
    this[TransitionMediaIdKey] = transition.mediaId
    when (val uri = transition.posterUri) {
        is MediaUri.Local -> {
            this[TransitionPosterKindKey] = PosterKindLocal
            this[TransitionPosterValueKey] = uri.file.absolutePath
        }
        is MediaUri.Remote -> {
            this[TransitionPosterKindKey] = PosterKindRemote
            this[TransitionPosterValueKey] = uri.url
        }
        is MediaUri.Missing -> {
            this[TransitionPosterKindKey] = PosterKindMissing
            this[TransitionPosterValueKey] = ""
        }
    }
}

private fun SavedStateHandle.consumeFullscreenMediaTransition(): FullscreenMediaTransition? {
    val mediaId = remove<String>(TransitionMediaIdKey)?.takeIf { it.isNotBlank() }
    val kind = remove<String>(TransitionPosterKindKey)
    val value = remove<String>(TransitionPosterValueKey).orEmpty()
    if (mediaId == null) return null
    val poster = when (kind) {
        PosterKindLocal -> value.takeIf { it.isNotBlank() }?.let { MediaUri.Local(File(it)) } ?: MediaUri.Missing
        PosterKindRemote -> value.takeIf { it.isNotBlank() }?.let { MediaUri.Remote(it) } ?: MediaUri.Missing
        else -> MediaUri.Missing
    }
    return FullscreenMediaTransition(mediaId = mediaId, posterUri = poster)
}
