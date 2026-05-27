package com.screwy.igloo.ui.theme

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import kotlin.math.max
import kotlin.math.min
import kotlin.math.pow

const val SystemThemeId = "system"
const val DefaultThemeId = "occult-umbral"
const val DefaultThemeAccentHex = "#e6c27a"

internal data class ThemeSpec(
    val id: String,
    val label: String,
    val catppuccin: Boolean,
    val dark: Boolean,
    val defaultAccent: String,
    val rosewater: String,
    val flamingo: String,
    val pink: String,
    val mauve: String,
    val red: String,
    val maroon: String,
    val peach: String,
    val yellow: String,
    val green: String,
    val teal: String,
    val sky: String,
    val sapphire: String,
    val blue: String,
    val lavender: String,
    val text: String,
    val subtext1: String,
    val subtext0: String,
    val overlay2: String,
    val overlay1: String,
    val overlay0: String,
    val surface2: String,
    val surface1: String,
    val surface0: String,
    val base: String,
    val mantle: String,
    val crust: String,
)

data class CatppuccinAccentChoice(
    val id: String,
    val label: String,
    val hex: String,
)

private val ThemeCatalog = listOf(
    systemThemeSpec(),
    catppuccinMochaSpec(),
    catppuccinMacchiatoSpec(),
    catppuccinFrappeSpec(),
    catppuccinLatteSpec(),
    ThemeSpec(
        id = "dracula", label = "Dracula", catppuccin = false, dark = true, defaultAccent = "#bd93f9",
        red = "#ff5555", maroon = "#ff6e6e", peach = "#ffb86c", yellow = "#f1fa8c", green = "#50fa7b",
        teal = "#8be9fd", sky = "#8be9fd", sapphire = "#8be9fd", blue = "#6272a4", mauve = "#bd93f9",
        pink = "#ff79c6", lavender = "#bd93f9", rosewater = "#f8f8f2", flamingo = "#ffb3d9",
        text = "#f8f8f2", subtext1 = "#e6e6e6", subtext0 = "#cfcfd7", overlay2 = "#a5adc6",
        overlay1 = "#858ba3", overlay0 = "#6272a4", surface2 = "#6272a4", surface1 = "#44475a",
        surface0 = "#343746", base = "#282a36", mantle = "#21222c", crust = "#191a21",
    ),
    ThemeSpec(
        id = "occult-umbral", label = "Occult Umbral", catppuccin = false, dark = true,
        defaultAccent = "#8b2e2e",
        red = "#c25b5b", maroon = "#a83a3a", peach = "#e6c27a", yellow = "#e6c27a", green = "#8baa82",
        teal = "#95b3b0", sky = "#95b3b0", sapphire = "#6270a8", blue = "#6270a8", mauve = "#9a7398",
        pink = "#9a7398", lavender = "#9a7398", rosewater = "#f2eadf", flamingo = "#c25b5b",
        text = "#e4ded2", subtext1 = "#cfc8bb", subtext0 = "#aba397", overlay2 = "#6f6a7d",
        overlay1 = "#5b5b75", overlay0 = "#3a3a4a", surface2 = "#2a2a38", surface1 = "#1c1c28",
        surface0 = "#14141e", base = "#0a0a12", mantle = "#0f0f18", crust = "#040407",
    ),
    ThemeSpec(
        id = "ayu-dark", label = "Ayu Dark", catppuccin = false, dark = true, defaultAccent = "#ffb454",
        red = "#f07178", maroon = "#ff8f40", peach = "#ff8f40", yellow = "#ffee99", green = "#aad94c",
        teal = "#95e6cb", sky = "#59c2ff", sapphire = "#39bae6", blue = "#59c2ff", mauve = "#d2a6ff",
        pink = "#ff77aa", lavender = "#b8b4ff", rosewater = "#e6b673", flamingo = "#f29e74",
        text = "#e6e1cf", subtext1 = "#b8cfe6", subtext0 = "#a6accd", overlay2 = "#7f8a99",
        overlay1 = "#6c7680", overlay0 = "#5c6773", surface2 = "#3a4350", surface1 = "#2d3340",
        surface0 = "#1f2430", base = "#0f1419", mantle = "#0b1015", crust = "#090d12",
    ),
    ThemeSpec(
        id = "github-dark", label = "GitHub Dark", catppuccin = false, dark = true, defaultAccent = "#58a6ff",
        red = "#f85149", maroon = "#ff7b72", peach = "#d29922", yellow = "#d29922", green = "#3fb950",
        teal = "#39c5cf", sky = "#79c0ff", sapphire = "#58a6ff", blue = "#58a6ff", mauve = "#bc8cff",
        pink = "#db61a2", lavender = "#a5d6ff", rosewater = "#ffa198", flamingo = "#ffb3ad",
        text = "#c9d1d9", subtext1 = "#b1bac4", subtext0 = "#8b949e", overlay2 = "#7d8590",
        overlay1 = "#6e7681", overlay0 = "#484f58", surface2 = "#30363d", surface1 = "#21262d",
        surface0 = "#161b22", base = "#0d1117", mantle = "#010409", crust = "#010409",
    ),
    ThemeSpec(
        id = "github-light", label = "GitHub Light", catppuccin = false, dark = false, defaultAccent = "#0969da",
        red = "#cf222e", maroon = "#a40e26", peach = "#bc4c00", yellow = "#9a6700", green = "#1a7f37",
        teal = "#3192aa", sky = "#0969da", sapphire = "#0969da", blue = "#0969da", mauve = "#8250df",
        pink = "#bf3989", lavender = "#6639ba", rosewater = "#953800", flamingo = "#cf222e",
        text = "#24292f", subtext1 = "#57606a", subtext0 = "#6e7781", overlay2 = "#8c959f",
        overlay1 = "#afb8c1", overlay0 = "#d0d7de", surface2 = "#d8dee4", surface1 = "#eaeef2",
        surface0 = "#f6f8fa", base = "#ffffff", mantle = "#f6f8fa", crust = "#f0f3f6",
    ),
    ThemeSpec(
        id = "green-eyes", label = "Green Eyes", catppuccin = false, dark = true, defaultAccent = "#a0d57a",
        red = "#ffb4ab", maroon = "#ffdad6", peach = "#d9e7ca", yellow = "#bdcbaf", green = "#a0d57a",
        teal = "#a0cfce", sky = "#bbecea", sapphire = "#a0cfce", blue = "#a0cfce", mauve = "#bdcbaf",
        pink = "#d9e7ca", lavender = "#bdcbaf", rosewater = "#d9e7ca", flamingo = "#ffdad6",
        text = "#e3e3dc", subtext1 = "#c4c8bb", subtext0 = "#a7ab9f", overlay2 = "#8e9286",
        overlay1 = "#6a6e63", overlay0 = "#44483e", surface2 = "#343531", surface1 = "#292b26",
        surface0 = "#1e201c", base = "#121410", mantle = "#1a1c18", crust = "#0b0d09",
    ),
    ThemeSpec(
        id = "nord", label = "Nord", catppuccin = false, dark = true, defaultAccent = "#88c0d0",
        red = "#bf616a", maroon = "#d08770", peach = "#d08770", yellow = "#ebcb8b", green = "#a3be8c",
        teal = "#8fbcbb", sky = "#88c0d0", sapphire = "#81a1c1", blue = "#5e81ac", mauve = "#b48ead",
        pink = "#b48ead", lavender = "#b48ead", rosewater = "#d8dee9", flamingo = "#e5e9f0",
        text = "#eceff4", subtext1 = "#e5e9f0", subtext0 = "#d8dee9", overlay2 = "#a9b5c5",
        overlay1 = "#81a1c1", overlay0 = "#6f7d91", surface2 = "#4c566a", surface1 = "#434c5e",
        surface0 = "#3b4252", base = "#2e3440", mantle = "#242933", crust = "#1f232c",
    ),
    ThemeSpec(
        id = "gruvbox-dark", label = "Gruvbox Dark", catppuccin = false, dark = true, defaultAccent = "#fabd2f",
        red = "#fb4934", maroon = "#cc241d", peach = "#fe8019", yellow = "#fabd2f", green = "#b8bb26",
        teal = "#8ec07c", sky = "#83a598", sapphire = "#458588", blue = "#83a598", mauve = "#d3869b",
        pink = "#d3869b", lavender = "#b16286", rosewater = "#ebdbb2", flamingo = "#fbf1c7",
        text = "#ebdbb2", subtext1 = "#d5c4a1", subtext0 = "#bdae93", overlay2 = "#928374",
        overlay1 = "#7c6f64", overlay0 = "#665c54", surface2 = "#665c54", surface1 = "#504945",
        surface0 = "#3c3836", base = "#282828", mantle = "#1d2021", crust = "#1b1b1b",
    ),
    ThemeSpec(
        id = "tokyo-night", label = "Tokyo Night", catppuccin = false, dark = true, defaultAccent = "#7aa2f7",
        red = "#f7768e", maroon = "#ff9e64", peach = "#ff9e64", yellow = "#e0af68", green = "#9ece6a",
        teal = "#73daca", sky = "#7dcfff", sapphire = "#7aa2f7", blue = "#7aa2f7", mauve = "#bb9af7",
        pink = "#f7768e", lavender = "#c0caf5", rosewater = "#cfc9c2", flamingo = "#ff9e9e",
        text = "#c0caf5", subtext1 = "#a9b1d6", subtext0 = "#9aa5ce", overlay2 = "#737aa2",
        overlay1 = "#565f89", overlay0 = "#414868", surface2 = "#414868", surface1 = "#30344a",
        surface0 = "#24283b", base = "#1a1b26", mantle = "#16161e", crust = "#11111a",
    ),
    ThemeSpec(
        id = "solarized-dark", label = "Solarized Dark", catppuccin = false, dark = true, defaultAccent = "#268bd2",
        red = "#dc322f", maroon = "#cb4b16", peach = "#cb4b16", yellow = "#b58900", green = "#859900",
        teal = "#2aa198", sky = "#268bd2", sapphire = "#268bd2", blue = "#268bd2", mauve = "#6c71c4",
        pink = "#d33682", lavender = "#6c71c4", rosewater = "#eee8d5", flamingo = "#fdf6e3",
        text = "#839496", subtext1 = "#93a1a1", subtext0 = "#657b83", overlay2 = "#586e75",
        overlay1 = "#586e75", overlay0 = "#073642", surface2 = "#174652", surface1 = "#0d3a45",
        surface0 = "#073642", base = "#002b36", mantle = "#00212b", crust = "#001a22",
    ),
)

private val ThemeById = ThemeCatalog.associateBy { it.id }

internal fun allThemeSpecs(): List<ThemeSpec> = ThemeCatalog.toList()

internal fun themeSpec(themeId: String?): ThemeSpec =
    ThemeById[themeId?.trim()?.lowercase()] ?: ThemeById.getValue(DefaultThemeId)

internal fun normalizeThemeId(themeId: String?): String = themeSpec(themeId).id

internal fun normalizeThemeAccentHex(themeId: String?, raw: String?): String {
    normalizeHex(raw)?.let { return it }
    return themeSpec(themeId).defaultAccent.lowercase()
}

internal fun catppuccinAccentChoices(themeId: String?): List<CatppuccinAccentChoice> {
    val theme = themeSpec(themeId)
    if (!theme.catppuccin) return emptyList()
    return listOf(
        CatppuccinAccentChoice("rosewater", "Rosewater", theme.rosewater),
        CatppuccinAccentChoice("flamingo", "Flamingo", theme.flamingo),
        CatppuccinAccentChoice("pink", "Pink", theme.pink),
        CatppuccinAccentChoice("mauve", "Mauve", theme.mauve),
        CatppuccinAccentChoice("red", "Red", theme.red),
        CatppuccinAccentChoice("maroon", "Maroon", theme.maroon),
        CatppuccinAccentChoice("peach", "Peach", theme.peach),
        CatppuccinAccentChoice("yellow", "Yellow", theme.yellow),
        CatppuccinAccentChoice("green", "Green", theme.green),
        CatppuccinAccentChoice("teal", "Teal", theme.teal),
        CatppuccinAccentChoice("sky", "Sky", theme.sky),
        CatppuccinAccentChoice("sapphire", "Sapphire", theme.sapphire),
        CatppuccinAccentChoice("blue", "Blue", theme.blue),
        CatppuccinAccentChoice("lavender", "Lavender", theme.lavender),
    ).map { it.copy(hex = it.hex.lowercase()) }
}

fun resolveIglooColors(
    themeId: String,
    accentHex: String,
    systemDark: Boolean,
): IglooColors {
    val requested = themeSpec(themeId)
    val theme = if (requested.id == SystemThemeId) {
        if (systemDark) catppuccinMochaSpec() else catppuccinLatteSpec()
    } else {
        requested
    }
    val accent = normalizeThemeAccentHex(theme.id, accentHex)
    val base = ThemePaletteBase(
        crust = color(theme.crust),
        mantle = color(theme.mantle),
        base = color(theme.base),
        surface0 = color(theme.surface0),
        surface1 = color(theme.surface1),
        surface2 = color(theme.surface2),
        overlay0 = color(theme.overlay0),
        overlay1 = color(theme.overlay1),
        overlay2 = color(theme.overlay2),
        subtext0 = color(theme.subtext0),
        subtext1 = color(theme.subtext1),
        text = color(theme.text),
    )
    return IglooColors(
        background = base.base,
        surface = base.surface0,
        surfaceElevated = base.surface1,
        surfaceHighest = base.surface2,
        surfaceVariant = base.surface1,

        onSurface = base.text,
        onSurfaceMuted = base.subtext1,
        onSurfaceFaint = base.subtext0,
        onSurfaceHandle = readableHandleText(base),

        border = base.overlay1,
        borderSubtle = base.surface2,
        overlayDim = base.overlay0.copy(alpha = 0.80f),

        primary = color(accent),
        onPrimary = onAccent(accent),
        primaryMuted = color(accent).copy(alpha = 0.20f),

        success = color(theme.green),
        warning = color(theme.yellow),
        error = color(theme.red),
        info = color(theme.blue),

        platformYoutube = color(theme.red),
        platformTwitter = color(theme.sky),
        platformTiktok = color(theme.pink),
        platformInstagram = color(theme.mauve),

        darkMode = theme.dark,
    )
}

private data class ThemePaletteBase(
    val crust: Color,
    val mantle: Color,
    val base: Color,
    val surface0: Color,
    val surface1: Color,
    val surface2: Color,
    val overlay0: Color,
    val overlay1: Color,
    val overlay2: Color,
    val subtext0: Color,
    val subtext1: Color,
    val text: Color,
)

private fun systemThemeSpec(): ThemeSpec =
    catppuccinMochaSpec().copy(id = SystemThemeId, label = "System", catppuccin = false)

private fun catppuccinMochaSpec() = ThemeSpec(
    id = "catppuccin-mocha", label = "Catppuccin Mocha", catppuccin = true, dark = true,
    defaultAccent = "#f38ba8",
    rosewater = "#f5e0dc", flamingo = "#f2cdcd", pink = "#f5c2e7", mauve = "#cba6f7",
    red = "#f38ba8", maroon = "#eba0ac", peach = "#fab387", yellow = "#f9e2af",
    green = "#a6e3a1", teal = "#94e2d5", sky = "#89dceb", sapphire = "#74c7ec",
    blue = "#89b4fa", lavender = "#b4befe",
    text = "#cdd6f4", subtext1 = "#bac2de", subtext0 = "#a6adc8", overlay2 = "#9399b2",
    overlay1 = "#7f849c", overlay0 = "#6c7086", surface2 = "#585b70", surface1 = "#45475a",
    surface0 = "#313244", base = "#1e1e2e", mantle = "#181825", crust = "#11111b",
)

private fun catppuccinMacchiatoSpec() = ThemeSpec(
    id = "catppuccin-macchiato", label = "Catppuccin Macchiato", catppuccin = true, dark = true,
    defaultAccent = "#ed8796",
    rosewater = "#f4dbd6", flamingo = "#f0c6c6", pink = "#f5bde6", mauve = "#c6a0f6",
    red = "#ed8796", maroon = "#ee99a0", peach = "#f5a97f", yellow = "#eed49f",
    green = "#a6da95", teal = "#8bd5ca", sky = "#91d7e3", sapphire = "#7dc4e4",
    blue = "#8aadf4", lavender = "#b7bdf8",
    text = "#cad3f5", subtext1 = "#b8c0e0", subtext0 = "#a5adcb", overlay2 = "#939ab7",
    overlay1 = "#8087a2", overlay0 = "#6e738d", surface2 = "#5b6078", surface1 = "#494d64",
    surface0 = "#363a4f", base = "#24273a", mantle = "#1e2030", crust = "#181926",
)

private fun catppuccinFrappeSpec() = ThemeSpec(
    id = "catppuccin-frappe", label = "Catppuccin Frappé", catppuccin = true, dark = true,
    defaultAccent = "#e78284",
    rosewater = "#f2d5cf", flamingo = "#eebebe", pink = "#f4b8e4", mauve = "#ca9ee6",
    red = "#e78284", maroon = "#ea999c", peach = "#ef9f76", yellow = "#e5c890",
    green = "#a6d189", teal = "#81c8be", sky = "#99d1db", sapphire = "#85c1dc",
    blue = "#8caaee", lavender = "#babbf1",
    text = "#c6d0f5", subtext1 = "#b5bfe2", subtext0 = "#a5adce", overlay2 = "#949cbb",
    overlay1 = "#838ba7", overlay0 = "#737994", surface2 = "#626880", surface1 = "#51576d",
    surface0 = "#414559", base = "#303446", mantle = "#292c3c", crust = "#232634",
)

private fun catppuccinLatteSpec() = ThemeSpec(
    id = "catppuccin-latte", label = "Catppuccin Latte", catppuccin = true, dark = false,
    defaultAccent = "#d20f39",
    rosewater = "#dc8a78", flamingo = "#dd7878", pink = "#ea76cb", mauve = "#8839ef",
    red = "#d20f39", maroon = "#e64553", peach = "#fe640b", yellow = "#df8e1d",
    green = "#40a02b", teal = "#179299", sky = "#04a5e5", sapphire = "#209fb5",
    blue = "#1e66f5", lavender = "#7287fd",
    text = "#4c4f69", subtext1 = "#5c5f77", subtext0 = "#6c6f85", overlay2 = "#7c7f93",
    overlay1 = "#8c8fa1", overlay0 = "#9ca0b0", surface2 = "#acb0be", surface1 = "#bcc0cc",
    surface0 = "#ccd0da", base = "#eff1f5", mantle = "#e6e9ef", crust = "#dce0e8",
)

internal fun normalizeHex(raw: String?): String? {
    val value = raw?.trim()?.lowercase() ?: return null
    if (value.length != 7 || value.firstOrNull() != '#') return null
    if (value.drop(1).any { it !in '0'..'9' && it !in 'a'..'f' }) return null
    return value
}

internal fun themeColor(hex: String?): Color? =
    normalizeHex(hex)?.let(::color)

private fun color(hex: String): Color {
    val normalized = normalizeHex(hex) ?: DefaultThemeAccentHex
    val rgb = normalized.drop(1).toInt(16)
    return Color(0xFF000000L or rgb.toLong())
}

internal fun contrastRatio(a: Color, b: Color): Double {
    val la = luminance(a)
    val lb = luminance(b)
    val lighter = max(la, lb)
    val darker = min(la, lb)
    return (lighter + 0.05) / (darker + 0.05)
}

private fun readableHandleText(base: ThemePaletteBase): Color =
    firstReadableColor(
        background = base.base,
        candidates = listOf(base.overlay0, base.subtext0, base.subtext1, base.text),
        minimumContrast = 4.5,
    )

internal fun firstReadableColor(
    background: Color,
    candidates: List<Color>,
    minimumContrast: Double,
): Color {
    var best = candidates.last()
    var bestContrast = 0.0
    for (candidate in candidates) {
        val ratio = contrastRatio(background, candidate)
        if (ratio > bestContrast) {
            best = candidate
            bestContrast = ratio
        }
        if (ratio >= minimumContrast) return candidate
    }
    return best
}

private fun onAccent(hex: String): Color =
    if (luminance(color(hex)) > 0.35) Color(0xFF11111B) else Color.White

private fun luminance(color: Color): Double {
    val argb = color.toArgb()
    fun channel(shift: Int): Double {
        val x = ((argb shr shift) and 0xff) / 255.0
        return if (x <= 0.03928) x / 12.92 else ((x + 0.055) / 1.055).pow(2.4)
    }
    return 0.2126 * channel(16) + 0.7152 * channel(8) + 0.0722 * channel(0)
}
