package com.screwy.igloo.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier

/**
 * Root theme composable.
 *
 * `system` theme semantics:
 *   - System picks Catppuccin Mocha when the OS is dark.
 *   - System picks Catppuccin Latte when the OS is light.
 *   - Explicit theme IDs are respected as-is.
 *
 * Content is wrapped in a `Surface` so `LocalContentColor` flows from the scheme's
 * `onBackground` — without this, free-floating `Text` composables render in the default
 * `Color.Black` and disappear on dark backgrounds.
 */
@Composable
fun IglooTheme(
    themeId: String = DefaultThemeId,
    accentHex: String = DefaultThemeAccentHex,
    content: @Composable () -> Unit,
) {
    val systemDark = isSystemInDarkTheme()
    val normalizedThemeId = normalizeThemeId(themeId)
    val normalizedAccentHex = normalizeThemeAccentHex(normalizedThemeId, accentHex)
    val colors = remember(normalizedThemeId, normalizedAccentHex, systemDark) {
        resolveIglooColors(
            themeId = normalizedThemeId,
            accentHex = normalizedAccentHex,
            systemDark = systemDark,
        )
    }

    CompositionLocalProvider(LocalIglooColors provides colors) {
        MaterialTheme(colorScheme = colors.materialScheme) {
            Surface(
                modifier = Modifier.fillMaxSize(),
                color = colors.background,
                contentColor = colors.onSurface,
                content = content,
            )
        }
    }
}

/** Composition local carrying the active [IglooColors]. Resolved via `MaterialTheme.iglooColors`. */
val LocalIglooColors = staticCompositionLocalOf<IglooColors> {
    error("IglooTheme not installed")
}

/** Convenience accessor — `MaterialTheme.iglooColors.primary` etc. */
@Suppress("UnusedReceiverParameter")
val MaterialTheme.iglooColors: IglooColors
    @Composable get() = LocalIglooColors.current

/**
 * Pure, side-effect-free resolver mapping a (flavor, accent, darkMode) triple onto the
 * full semantic token set. Easy to unit-test.
 *
 * Signal colors are flavor-scoped Catppuccin accents (not user-pickable):
 *   - success = Green, warning = Yellow, error = Red, info = Blue.
 *   - When the user's accent is Red, `error` shifts to Maroon so it stays visually
 *     distinct from `primary`.
 *
 * Platform brand hints: youtube=Red, twitter=Sky, tiktok=Pink, instagram=Mauve.
 */
fun resolveIglooColors(
    flavor: Flavor,
    accent: Accent,
    darkMode: Boolean,
): IglooColors {
    val base = CatppuccinBase.getValue(flavor)
    val accents = CatppuccinAccents.getValue(flavor)

    val primary = accents.getValue(accent)
    val errorAccent = if (accent == Accent.Red) Accent.Maroon else Accent.Red

    // surface tokens follow canonical Catppuccin elevation (spec's mantle→surface comment had layering inverted).
    return IglooColors(
        background       = base.base,
        surface          = base.surface0,
        surfaceElevated  = base.surface1,
        surfaceHighest   = base.surface2,
        surfaceVariant   = base.surface1,

        onSurface        = base.text,
        onSurfaceMuted   = base.subtext1,
        onSurfaceFaint   = base.subtext0,
        onSurfaceHandle  = firstReadableColor(
            background = base.base,
            candidates = listOf(base.overlay0, base.subtext0, base.subtext1, base.text),
            minimumContrast = 4.5,
        ),

        border           = base.overlay1,
        borderSubtle     = base.surface2,
        overlayDim       = base.overlay0.copy(alpha = 0.80f),

        primary          = primary,
        onPrimary        = base.crust,
        primaryMuted     = primary.copy(alpha = 0.20f),

        success          = accents.getValue(Accent.Green),
        warning          = accents.getValue(Accent.Yellow),
        error            = accents.getValue(errorAccent),
        info             = accents.getValue(Accent.Blue),

        platformYoutube   = accents.getValue(Accent.Red),
        platformTwitter   = accents.getValue(Accent.Sky),
        platformTiktok    = accents.getValue(Accent.Pink),
        platformInstagram = accents.getValue(Accent.Mauve),

        darkMode         = darkMode,
    )
}
