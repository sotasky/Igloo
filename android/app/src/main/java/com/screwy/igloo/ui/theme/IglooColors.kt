package com.screwy.igloo.ui.theme

import androidx.compose.material3.ColorScheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Immutable
import androidx.compose.ui.graphics.Color

/**
 * Semantic color tokens.
 *
 * Composables reference either M3 roles (`MaterialTheme.colorScheme.primary`) for
 * standard widgets, or `MaterialTheme.iglooColors.<token>` for Igloo-specific
 * surfaces. `materialScheme` projects the Igloo tokens onto Material3 roles so
 * default Material composables (TextField, Button, Switch …) render with the
 * correct palette.
 */
@Immutable
data class IglooColors(
    // Surfaces — layered from flavor's base to surface2
    val background:        Color,
    val surface:           Color,
    val surfaceElevated:   Color,
    val surfaceHighest:    Color,
    val surfaceVariant:    Color,   // used for broken-thumbnail fallback (spec §8)

    // Text
    val onSurface:         Color,
    val onSurfaceMuted:    Color,
    val onSurfaceFaint:    Color,
    val onSurfaceHandle:   Color,

    // Lines + overlays
    val border:            Color,
    val borderSubtle:      Color,
    val overlayDim:        Color,

    // Accent (primary action — user-picked)
    val primary:           Color,
    val onPrimary:         Color,
    val primaryMuted:      Color,

    // Semantic signal colors (Catppuccin-named, not user-pickable)
    val success:           Color,
    val warning:           Color,
    val error:             Color,
    val info:              Color,

    // Platform brand hints (used sparingly as faint tints)
    val platformYoutube:   Color,
    val platformTwitter:   Color,
    val platformTiktok:    Color,
    val platformInstagram: Color,

    // `darkMode` is carried alongside the tokens so `materialScheme` can pick the
    // right Material3 base constructor (darkColorScheme vs lightColorScheme) —
    // the base constructor seeds roles we don't override (e.g. surfaceTint).
    private val darkMode:  Boolean,
) {
    val materialScheme: ColorScheme by lazy { buildMaterialScheme() }

    private fun buildMaterialScheme(): ColorScheme {
        val base = if (darkMode) darkColorScheme() else lightColorScheme()
        return base.copy(
            primary             = primary,
            onPrimary           = onPrimary,
            primaryContainer    = primaryMuted,
            onPrimaryContainer  = onSurface,
            secondary           = primaryMuted,
            onSecondary         = onSurface,
            secondaryContainer  = surfaceElevated,
            onSecondaryContainer = onSurface,
            tertiary            = info,
            onTertiary          = onPrimary,
            tertiaryContainer   = surfaceElevated,
            onTertiaryContainer = onSurface,
            background          = background,
            onBackground        = onSurface,
            surface             = surface,
            onSurface           = onSurface,
            surfaceVariant      = surfaceVariant,
            onSurfaceVariant    = onSurfaceMuted,
            surfaceContainerLowest  = background,
            surfaceContainerLow     = surface,
            surfaceContainer        = surfaceElevated,
            surfaceContainerHigh    = surfaceHighest,
            surfaceContainerHighest = surfaceHighest,
            inverseSurface      = onSurface,
            inverseOnSurface    = background,
            error               = error,
            onError             = onPrimary,
            errorContainer      = error.copy(alpha = 0.20f),
            onErrorContainer    = onSurface,
            outline             = border,
            outlineVariant      = borderSubtle,
            scrim               = overlayDim,
        )
    }
}
