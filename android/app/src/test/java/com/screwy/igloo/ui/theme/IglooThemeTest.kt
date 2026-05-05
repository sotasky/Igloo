package com.screwy.igloo.ui.theme

import androidx.compose.ui.graphics.Color
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Unit tests for the pure `resolveIglooColors` resolver and the string parsers wired
 * into [com.screwy.igloo.MainActivity]. The resolver is stateless, so no
 * Robolectric runtime is needed here.
 *
 * Covers the four guarantees from 07-ui-design-system.md §2:
 *   - Mocha+Red dark resolves to Mocha's red accent + Mocha base background.
 *   - Latte+Red light resolves to Latte's red accent + Latte base background.
 *   - accent=Red → `error` shifts to Maroon (distinct from `primary`).
 *   - accent=Green → `error` equals the flavor's Red.
 * Plus: the materialScheme is populated (non-null primary/background).
 */
class IglooThemeTest {

    @Test
    fun catalog_matches_web_theme_ids() {
        assertEquals(
            listOf(
                "system",
                "catppuccin-mocha",
                "catppuccin-macchiato",
                "catppuccin-frappe",
                "catppuccin-latte",
                "dracula",
                "ayu-dark",
                "github-dark",
                "github-light",
                "nord",
                "gruvbox-dark",
                "tokyo-night",
                "solarized-dark",
            ),
            allThemeSpecs().map { it.id },
        )
    }

    @Test
    fun resolve_system_theme_uses_latte_for_light_system() {
        val colors = resolveIglooColors(DefaultThemeId, DefaultThemeAccentHex, systemDark = false)

        assertEquals(Color(0xFFEFF1F5), colors.background)
        assertEquals(Color(0xFF4C4F69), colors.onSurface)
        assertEquals(Color(0xFFF38BA8), colors.primary)
    }

    @Test
    fun curated_theme_defaults_match_web_catalog() {
        val githubLightAccent = normalizeThemeAccentHex("github-light", "")
        val githubLight = resolveIglooColors("github-light", githubLightAccent, systemDark = false)
        assertEquals("#0969da", githubLightAccent)
        assertEquals(Color(0xFFFFFFFF), githubLight.background)
        assertEquals(Color(0xFF0969DA), githubLight.primary)

        val solarized = resolveIglooColors("solarized-dark", "", systemDark = true)
        assertEquals(Color(0xFF002B36), solarized.background)
        assertEquals(Color(0xFF268BD2), solarized.primary)
    }

    @Test
    fun custom_accent_hex_normalizes_and_sets_contrast_safe_on_primary() {
        val darkAccent = resolveIglooColors("github-light", "#123ABC", systemDark = false)
        assertEquals(Color(0xFF123ABC), darkAccent.primary)
        assertEquals(Color.White, darkAccent.onPrimary)

        val lightAccent = resolveIglooColors("github-dark", "  #F5C2E7 ", systemDark = true)
        assertEquals(Color(0xFFF5C2E7), lightAccent.primary)
        assertEquals(Color(0xFF11111B), lightAccent.onPrimary)
    }

    @Test
    fun handle_text_is_readable_for_low_contrast_theme_palettes() {
        for (themeId in listOf("solarized-dark", "github-light", "github-dark")) {
            val colors = resolveIglooColors(themeId, "", systemDark = themeId != "github-light")
            assertTrue(
                "$themeId handle contrast",
                contrastRatio(colors.background, colors.onSurfaceHandle) >= 4.5,
            )
        }
    }

    @Test
    fun catppuccin_accent_choices_are_only_exposed_for_catppuccin_themes() {
        val mochaChoices = catppuccinAccentChoices("catppuccin-mocha")

        assertEquals(14, mochaChoices.size)
        assertEquals("mauve", mochaChoices[3].id)
        assertEquals("#cba6f7", mochaChoices[3].hex)
        assertTrue(catppuccinAccentChoices("github-dark").isEmpty())
        assertTrue(catppuccinAccentChoices("system").isEmpty())
    }

    @Test
    fun resolve_mocha_red_dark_uses_mocha_red_and_base() {
        val colors = resolveIglooColors(Flavor.Mocha, Accent.Red, darkMode = true)

        assertEquals(Color(0xFFF38BA8), colors.primary)
        assertEquals(Color(0xFF1E1E2E), colors.background)
        assertEquals(Color(0xFFCDD6F4), colors.onSurface)
    }

    @Test
    fun resolve_latte_red_light_uses_latte_red_and_base() {
        val colors = resolveIglooColors(Flavor.Latte, Accent.Red, darkMode = false)

        assertEquals(Color(0xFFD20F39), colors.primary)
        assertEquals(Color(0xFFEFF1F5), colors.background)
        assertEquals(Color(0xFF4C4F69), colors.onSurface)
    }

    @Test
    fun accent_red_shifts_error_to_maroon_for_distinction() {
        val colors = resolveIglooColors(Flavor.Mocha, Accent.Red, darkMode = true)
        val mochaMaroon = CatppuccinAccents.getValue(Flavor.Mocha).getValue(Accent.Maroon)

        assertNotEquals(colors.primary, colors.error)
        assertEquals(mochaMaroon, colors.error)
    }

    @Test
    fun non_red_accent_keeps_error_as_flavor_red() {
        val colors = resolveIglooColors(Flavor.Mocha, Accent.Green, darkMode = true)
        val mochaRed = CatppuccinAccents.getValue(Flavor.Mocha).getValue(Accent.Red)
        val mochaGreen = CatppuccinAccents.getValue(Flavor.Mocha).getValue(Accent.Green)

        assertEquals(mochaGreen, colors.primary)
        assertEquals(mochaRed, colors.error)
    }

    @Test
    fun platform_brand_hints_match_spec() {
        val colors = resolveIglooColors(Flavor.Mocha, Accent.Blue, darkMode = true)
        val mocha = CatppuccinAccents.getValue(Flavor.Mocha)

        assertEquals(mocha.getValue(Accent.Red),   colors.platformYoutube)
        assertEquals(mocha.getValue(Accent.Sky),   colors.platformTwitter)
        assertEquals(mocha.getValue(Accent.Pink),  colors.platformTiktok)
        assertEquals(mocha.getValue(Accent.Mauve), colors.platformInstagram)
    }

    @Test
    fun material_scheme_is_populated_from_igloo_tokens() {
        val colors = resolveIglooColors(Flavor.Mocha, Accent.Red, darkMode = true)
        val scheme = colors.materialScheme

        assertNotNull(scheme)
        assertEquals(colors.primary,     scheme.primary)
        assertEquals(colors.onPrimary,   scheme.onPrimary)
        assertEquals(colors.background,  scheme.background)
        assertEquals(colors.surface,     scheme.surface)
        assertEquals(colors.onSurface,   scheme.onSurface)
        assertEquals(colors.error,       scheme.error)
        assertEquals(colors.border,      scheme.outline)
    }

    @Test
    fun light_material_scheme_picks_light_base_constructor() {
        val colors = resolveIglooColors(Flavor.Latte, Accent.Red, darkMode = false)
        val scheme = colors.materialScheme

        // All overridden roles must still surface the Latte tokens regardless of the
        // light-vs-dark base constructor. The key point is that `materialScheme` builds
        // without throwing in either mode.
        assertEquals(colors.primary,    scheme.primary)
        assertEquals(colors.background, scheme.background)
    }

    @Test
    fun parseFlavor_is_case_insensitive_with_mocha_fallback() {
        assertEquals(Flavor.Mocha,     parseFlavor("mocha"))
        assertEquals(Flavor.Macchiato, parseFlavor("MACCHIATO"))
        assertEquals(Flavor.Frappe,    parseFlavor("Frappe"))
        assertEquals(Flavor.Latte,     parseFlavor("latte"))
        assertEquals(Flavor.Mocha,     parseFlavor(null))
        assertEquals(Flavor.Mocha,     parseFlavor(""))
        assertEquals(Flavor.Mocha,     parseFlavor("garbage"))
    }

    @Test
    fun parseAccent_is_case_insensitive_with_red_fallback() {
        assertEquals(Accent.Red,       parseAccent("red"))
        assertEquals(Accent.Lavender,  parseAccent("LAVENDER"))
        assertEquals(Accent.Sapphire,  parseAccent("Sapphire"))
        assertEquals(Accent.Red,       parseAccent(null))
        assertEquals(Accent.Red,       parseAccent(""))
        assertEquals(Accent.Red,       parseAccent("chartreuse"))
    }

    @Test
    fun every_flavor_has_an_entry_in_the_base_palette() {
        assertTrue(
            "base palette missing flavors",
            CatppuccinBase.keys.containsAll(Flavor.values().toList()),
        )
        // And no entry left its data-class slots unset (Color.Unspecified).
        Flavor.values().forEach { flavor ->
            val base = CatppuccinBase.getValue(flavor)
            assertNotEquals("$flavor crust unset",    Color.Unspecified, base.crust)
            assertNotEquals("$flavor base unset",     Color.Unspecified, base.base)
            assertNotEquals("$flavor surface0 unset", Color.Unspecified, base.surface0)
            assertNotEquals("$flavor surface2 unset", Color.Unspecified, base.surface2)
            assertNotEquals("$flavor text unset",     Color.Unspecified, base.text)
        }
    }

    @Test
    fun every_flavor_has_all_fourteen_accent_slots_populated() {
        Flavor.values().forEach { flavor ->
            val accents = CatppuccinAccents.getValue(flavor)
            assertEquals("$flavor accent count", 14, accents.size)
            assertTrue(
                "$flavor missing accent keys",
                accents.keys.containsAll(Accent.values().toList()),
            )
            Accent.values().forEach { accent ->
                assertNotEquals(
                    "$flavor / $accent unset",
                    Color.Unspecified,
                    accents.getValue(accent),
                )
            }
        }
    }
}
