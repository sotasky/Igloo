package com.screwy.igloo.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.i18n.AppLanguageStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/**
 * Settings hub state holder.
 *
 * Each pref is exposed as a `StateFlow` collected from [PreferencesRepo]; setters
 * fire-and-forget into `viewModelScope`. No ui-state branching — the route
 * renders directly from the individual pref flows.
 */
class SettingsHubViewModel(
    private val prefs: PreferencesRepo,
    private val languageStore: AppLanguageStore,
) : ViewModel() {

    val momentsDefaultTab: StateFlow<String> =
        prefs.momentsDefaultTab().stateDefault(PreferencesRepo.Defaults.MOMENTS_DEFAULT_TAB)
    val momentsIncludeReposts: StateFlow<Boolean> =
        prefs.momentsIncludeRepostsDefault().stateDefault(PreferencesRepo.Defaults.MOMENTS_INCLUDE_REPOSTS_DEFAULT)
    val instagramIncludeTagged: StateFlow<Boolean> =
        prefs.instagramIncludeTaggedDefault().stateDefault(PreferencesRepo.Defaults.INSTAGRAM_INCLUDE_TAGGED_DEFAULT)

    // Display — which top-level destination opens when the app launches.
    val startingPage: StateFlow<String> =
        prefs.startingPage().stateDefault(PreferencesRepo.Defaults.STARTING_PAGE)

    val languageTag: StateFlow<String> = languageStore.languageTag

    val shareEmbedFriendlyLinks: StateFlow<Boolean> =
        prefs.shareEmbedFriendlyLinks().stateDefault(PreferencesRepo.Defaults.SHARE_EMBED_FRIENDLY_LINKS)

    // Debug logging
    val debugMode: StateFlow<Boolean> =
        prefs.debugMode().stateDefault(PreferencesRepo.Defaults.DEBUG_MODE)

    // ─── Setters ─────────────────────────────────────────────────────────────

    fun setMomentsDefaultTab(value: String) =
        launchSet { prefs.setMomentsDefaultTab(value) }
    fun setMomentsIncludeReposts(value: Boolean) =
        launchSet { prefs.setMomentsIncludeRepostsDefault(value) }
    fun setInstagramIncludeTagged(value: Boolean) =
        launchSet { prefs.setInstagramIncludeTaggedDefault(value) }
    fun setStartingPage(value: String) = launchSet { prefs.setStartingPage(value) }
    fun setShareEmbedFriendlyLinks(value: Boolean) = launchSet { prefs.setShareEmbedFriendlyLinks(value) }
    fun setLanguageTag(value: String) = languageStore.setLanguageTag(value)
    fun setDebugMode(value: Boolean) = launchSet { prefs.setDebugMode(value) }

    // ─── Helpers ─────────────────────────────────────────────────────────────

    private fun launchSet(block: suspend () -> Unit) {
        viewModelScope.launch { block() }
    }

    private fun <T> Flow<T>.stateDefault(initial: T): StateFlow<T> =
        stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), initial)

}
