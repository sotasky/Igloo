package com.screwy.igloo.ui.theme

import androidx.compose.ui.graphics.Color

/**
 * Catppuccin palette data.
 *
 * Four flavors × 14 accents = 56 concrete themes. Hex values are the official
 * Catppuccin palette (https://catppuccin.com/palette) — purely declarative data,
 * no logic. Adding a new flavor or accent is a config entry, nothing more.
 */

/** Dark/light variant family. */
enum class Flavor { Mocha, Macchiato, Frappe, Latte }

/** User-pickable accent (14 per flavor). */
enum class Accent {
    Rosewater, Flamingo, Pink, Mauve, Red, Maroon, Peach,
    Yellow, Green, Teal, Sky, Sapphire, Blue, Lavender,
}

/** 12 accent-independent base colors per flavor — layered from darkest to lightest foreground. */
data class PaletteBase(
    val crust:    Color,
    val mantle:   Color,
    val base:     Color,
    val surface0: Color,
    val surface1: Color,
    val surface2: Color,
    val overlay0: Color,
    val overlay1: Color,
    val overlay2: Color,
    val subtext0: Color,
    val subtext1: Color,
    val text:     Color,
)

// ── Base palettes ────────────────────────────────────────────────────────────

private val MochaBase = PaletteBase(
    crust    = Color(0xFF11111B),
    mantle   = Color(0xFF181825),
    base     = Color(0xFF1E1E2E),
    surface0 = Color(0xFF313244),
    surface1 = Color(0xFF45475A),
    surface2 = Color(0xFF585B70),
    overlay0 = Color(0xFF6C7086),
    overlay1 = Color(0xFF7F849C),
    overlay2 = Color(0xFF9399B2),
    subtext0 = Color(0xFFA6ADC8),
    subtext1 = Color(0xFFBAC2DE),
    text     = Color(0xFFCDD6F4),
)

private val MacchiatoBase = PaletteBase(
    crust    = Color(0xFF181926),
    mantle   = Color(0xFF1E2030),
    base     = Color(0xFF24273A),
    surface0 = Color(0xFF363A4F),
    surface1 = Color(0xFF494D64),
    surface2 = Color(0xFF5B6078),
    overlay0 = Color(0xFF6E738D),
    overlay1 = Color(0xFF8087A2),
    overlay2 = Color(0xFF939AB7),
    subtext0 = Color(0xFFA5ADCB),
    subtext1 = Color(0xFFB8C0E0),
    text     = Color(0xFFCAD3F5),
)

private val FrappeBase = PaletteBase(
    crust    = Color(0xFF232634),
    mantle   = Color(0xFF292C3C),
    base     = Color(0xFF303446),
    surface0 = Color(0xFF414559),
    surface1 = Color(0xFF51576D),
    surface2 = Color(0xFF626880),
    overlay0 = Color(0xFF737994),
    overlay1 = Color(0xFF838BA7),
    overlay2 = Color(0xFF949CBB),
    subtext0 = Color(0xFFA5ADCE),
    subtext1 = Color(0xFFB5BFE2),
    text     = Color(0xFFC6D0F5),
)

private val LatteBase = PaletteBase(
    crust    = Color(0xFFDCE0E8),
    mantle   = Color(0xFFE6E9EF),
    base     = Color(0xFFEFF1F5),
    surface0 = Color(0xFFCCD0DA),
    surface1 = Color(0xFFBCC0CC),
    surface2 = Color(0xFFACB0BE),
    overlay0 = Color(0xFF9CA0B0),
    overlay1 = Color(0xFF8C8FA1),
    overlay2 = Color(0xFF7C7F93),
    subtext0 = Color(0xFF6C6F85),
    subtext1 = Color(0xFF5C5F77),
    text     = Color(0xFF4C4F69),
)

val CatppuccinBase: Map<Flavor, PaletteBase> = mapOf(
    Flavor.Mocha      to MochaBase,
    Flavor.Macchiato  to MacchiatoBase,
    Flavor.Frappe     to FrappeBase,
    Flavor.Latte      to LatteBase,
)

// ── Accent palettes ──────────────────────────────────────────────────────────

private val MochaAccents = mapOf(
    Accent.Rosewater to Color(0xFFF5E0DC),
    Accent.Flamingo  to Color(0xFFF2CDCD),
    Accent.Pink      to Color(0xFFF5C2E7),
    Accent.Mauve     to Color(0xFFCBA6F7),
    Accent.Red       to Color(0xFFF38BA8),
    Accent.Maroon    to Color(0xFFEBA0AC),
    Accent.Peach     to Color(0xFFFAB387),
    Accent.Yellow    to Color(0xFFF9E2AF),
    Accent.Green     to Color(0xFFA6E3A1),
    Accent.Teal      to Color(0xFF94E2D5),
    Accent.Sky       to Color(0xFF89DCEB),
    Accent.Sapphire  to Color(0xFF74C7EC),
    Accent.Blue      to Color(0xFF89B4FA),
    Accent.Lavender  to Color(0xFFB4BEFE),
)

private val MacchiatoAccents = mapOf(
    Accent.Rosewater to Color(0xFFF4DBD6),
    Accent.Flamingo  to Color(0xFFF0C6C6),
    Accent.Pink      to Color(0xFFF5BDE6),
    Accent.Mauve     to Color(0xFFC6A0F6),
    Accent.Red       to Color(0xFFED8796),
    Accent.Maroon    to Color(0xFFEE99A0),
    Accent.Peach     to Color(0xFFF5A97F),
    Accent.Yellow    to Color(0xFFEED49F),
    Accent.Green     to Color(0xFFA6DA95),
    Accent.Teal      to Color(0xFF8BD5CA),
    Accent.Sky       to Color(0xFF91D7E3),
    Accent.Sapphire  to Color(0xFF7DC4E4),
    Accent.Blue      to Color(0xFF8AADF4),
    Accent.Lavender  to Color(0xFFB7BDF8),
)

private val FrappeAccents = mapOf(
    Accent.Rosewater to Color(0xFFF2D5CF),
    Accent.Flamingo  to Color(0xFFEEBEBE),
    Accent.Pink      to Color(0xFFF4B8E4),
    Accent.Mauve     to Color(0xFFCA9EE6),
    Accent.Red       to Color(0xFFE78284),
    Accent.Maroon    to Color(0xFFEA999C),
    Accent.Peach     to Color(0xFFEF9F76),
    Accent.Yellow    to Color(0xFFE5C890),
    Accent.Green     to Color(0xFFA6D189),
    Accent.Teal      to Color(0xFF81C8BE),
    Accent.Sky       to Color(0xFF99D1DB),
    Accent.Sapphire  to Color(0xFF85C1DC),
    Accent.Blue      to Color(0xFF8CAAEE),
    Accent.Lavender  to Color(0xFFBABBF1),
)

private val LatteAccents = mapOf(
    Accent.Rosewater to Color(0xFFDC8A78),
    Accent.Flamingo  to Color(0xFFDD7878),
    Accent.Pink      to Color(0xFFEA76CB),
    Accent.Mauve     to Color(0xFF8839EF),
    Accent.Red       to Color(0xFFD20F39),
    Accent.Maroon    to Color(0xFFE64553),
    Accent.Peach     to Color(0xFFFE640B),
    Accent.Yellow    to Color(0xFFDF8E1D),
    Accent.Green     to Color(0xFF40A02B),
    Accent.Teal      to Color(0xFF179299),
    Accent.Sky       to Color(0xFF04A5E5),
    Accent.Sapphire  to Color(0xFF209FB5),
    Accent.Blue      to Color(0xFF1E66F5),
    Accent.Lavender  to Color(0xFF7287FD),
)

val CatppuccinAccents: Map<Flavor, Map<Accent, Color>> = mapOf(
    Flavor.Mocha      to MochaAccents,
    Flavor.Macchiato  to MacchiatoAccents,
    Flavor.Frappe     to FrappeAccents,
    Flavor.Latte      to LatteAccents,
)

// ── Pref parsers ─────────────────────────────────────────────────────────────

/**
 * Case-insensitive `Flavor` parse with a Mocha fallback. `flavor` prefs are stored as
 * lowercase strings (e.g. `"mocha"`, `"frappe"`) — we accept any casing so a hand-edit
 * of the DB row doesn't crash the UI.
 */
fun parseFlavor(raw: String?): Flavor = when (raw?.lowercase()) {
    "mocha"     -> Flavor.Mocha
    "macchiato" -> Flavor.Macchiato
    "frappe"    -> Flavor.Frappe
    "latte"     -> Flavor.Latte
    else        -> Flavor.Mocha
}

/** Lookup the `base` surface color for the given flavor — used by the theme picker swatches. */
fun flavorBase(flavor: Flavor): Color = CatppuccinBase.getValue(flavor).base

/** Lookup the accent color for a (flavor, accent) pair — used by the theme picker accent circles. */
fun accentColor(flavor: Flavor, accent: Accent): Color =
    CatppuccinAccents.getValue(flavor).getValue(accent)

/** Case-insensitive `Accent` parse with a Red fallback. */
fun parseAccent(raw: String?): Accent = when (raw?.lowercase()) {
    "rosewater" -> Accent.Rosewater
    "flamingo"  -> Accent.Flamingo
    "pink"      -> Accent.Pink
    "mauve"     -> Accent.Mauve
    "red"       -> Accent.Red
    "maroon"    -> Accent.Maroon
    "peach"     -> Accent.Peach
    "yellow"    -> Accent.Yellow
    "green"     -> Accent.Green
    "teal"      -> Accent.Teal
    "sky"       -> Accent.Sky
    "sapphire"  -> Accent.Sapphire
    "blue"      -> Accent.Blue
    "lavender"  -> Accent.Lavender
    else        -> Accent.Red
}
