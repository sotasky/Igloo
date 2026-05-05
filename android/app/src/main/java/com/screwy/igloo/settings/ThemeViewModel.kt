package com.screwy.igloo.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.ui.theme.DefaultThemeAccentHex
import com.screwy.igloo.ui.theme.DefaultThemeId
import com.screwy.igloo.ui.theme.catppuccinAccentChoices
import com.screwy.igloo.ui.theme.normalizeHex
import com.screwy.igloo.ui.theme.normalizeThemeAccentHex
import com.screwy.igloo.ui.theme.normalizeThemeId
import com.screwy.igloo.ui.theme.themeSpec
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/**
 * Theme picker state.
 * "Theme picker UI" + §4 `settings/theme`.
 *
 * Reactive — no save button. Writes go straight to `PreferencesRepo`; the pref
 * flows propagate through `MainActivity`'s `IglooTheme`, which recomposes the
 * entire UI (including this picker) with the new palette.
 */
class ThemeViewModel(
    private val prefs: PreferencesRepo,
) : ViewModel() {

    val themeId: StateFlow<String> = prefs.themeId()
        .map(::normalizeThemeId)
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = DefaultThemeId,
        )

    val accentHex: StateFlow<String> = combine(
        prefs.themeId().map(::normalizeThemeId),
        prefs.themeAccentHex(),
    ) { themeId, accentHex ->
        normalizeThemeAccentHex(themeId, accentHex)
    }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = DefaultThemeAccentHex,
        )

    val catppuccinAccents = themeId
        .map(::catppuccinAccentChoices)
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    val customCss: StateFlow<String> = prefs.themeCustomCss()
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = PreferencesRepo.Defaults.THEME_CUSTOM_CSS,
        )

    fun setThemeId(value: String) {
        val id = normalizeThemeId(value)
        val defaultAccent = themeSpec(id).defaultAccent.lowercase()
        viewModelScope.launch {
            prefs.setThemeId(id)
            prefs.setThemeAccentHex(defaultAccent)
        }
    }

    fun setAccentHex(value: String) {
        val normalized = normalizeHex(value) ?: return
        viewModelScope.launch { prefs.setThemeAccentHex(normalized) }
    }

    fun setCustomCss(value: String) {
        viewModelScope.launch { prefs.setThemeCustomCss(value) }
    }
}
