package com.screwy.igloo.i18n

import android.app.LocaleManager
import android.content.Context
import android.os.Build
import android.os.LocaleList
import java.util.Locale
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

class AppLanguageStore(
    context: Context,
) {
    private val appContext = context.applicationContext
    private val prefs = appContext.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
    private val language = MutableStateFlow(normalizeLanguageTag(prefs.getString(KEY_LANGUAGE_TAG, null)))

    val languageTag: StateFlow<String> = language.asStateFlow()

    init {
        applyPlatformLocale(language.value)
    }

    fun languageTagSync(): String = language.value

    fun setLanguageTag(value: String) {
        val normalized = normalizeLanguageTag(value)
        if (normalized == language.value) return
        prefs.edit().putString(KEY_LANGUAGE_TAG, normalized).apply()
        language.value = normalized
        applyPlatformLocale(normalized)
    }

    private fun applyPlatformLocale(tag: String) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return
        val localeManager = appContext.getSystemService(LocaleManager::class.java) ?: return
        localeManager.applicationLocales = LocaleList.forLanguageTags(tag)
    }

    companion object {
        const val SYSTEM_LANGUAGE = ""
        private const val PREFS_NAME = "igloo-app"
        private const val KEY_LANGUAGE_TAG = "language_tag"

        fun normalizeLanguageTag(value: String?): String {
            val raw = value?.trim().orEmpty()
            if (raw.isBlank() || raw == "system") return SYSTEM_LANGUAGE
            if (raw.any { !(it.isLetterOrDigit() || it == '-' || it == '_') }) {
                return SYSTEM_LANGUAGE
            }
            val tag = raw.replace('_', '-')
            val locale = Locale.forLanguageTag(tag)
            return locale.toLanguageTag().takeUnless { it == "und" } ?: SYSTEM_LANGUAGE
        }
    }
}
