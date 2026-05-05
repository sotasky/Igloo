package com.screwy.igloo.i18n

import android.content.Context
import android.content.res.Configuration
import android.os.LocaleList
import android.text.TextUtils
import android.view.View
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.unit.LayoutDirection
import java.util.Locale

@Composable
fun AppLocaleProvider(
    languageTag: String,
    content: @Composable () -> Unit,
) {
    val baseContext = LocalContext.current
    val baseConfiguration = LocalConfiguration.current
    val baseLayoutDirection = LocalLayoutDirection.current
    val normalized = remember(languageTag) {
        AppLanguageStore.normalizeLanguageTag(languageTag)
    }
    val localized = remember(baseContext, baseConfiguration, normalized) {
        localizedContext(baseContext, baseConfiguration, baseLayoutDirection, normalized)
    }

    CompositionLocalProvider(
        LocalContext provides localized.context,
        LocalConfiguration provides localized.configuration,
        LocalLayoutDirection provides localized.layoutDirection,
        content = content,
    )
}

private fun localizedContext(
    baseContext: Context,
    baseConfiguration: Configuration,
    baseLayoutDirection: LayoutDirection,
    languageTag: String,
): LocalizedContext {
    if (languageTag == AppLanguageStore.SYSTEM_LANGUAGE) {
        return LocalizedContext(
            context = baseContext,
            configuration = Configuration(baseConfiguration),
            layoutDirection = baseLayoutDirection,
        )
    }

    val locale = Locale.forLanguageTag(languageTag)
    val configuration = Configuration(baseConfiguration).apply {
        setLocales(LocaleList(locale))
    }
    val layoutDirection = if (TextUtils.getLayoutDirectionFromLocale(locale) == View.LAYOUT_DIRECTION_RTL) {
        LayoutDirection.Rtl
    } else {
        LayoutDirection.Ltr
    }
    return LocalizedContext(
        context = baseContext.createConfigurationContext(configuration),
        configuration = configuration,
        layoutDirection = layoutDirection,
    )
}

private data class LocalizedContext(
    val context: Context,
    val configuration: Configuration,
    val layoutDirection: LayoutDirection,
)
