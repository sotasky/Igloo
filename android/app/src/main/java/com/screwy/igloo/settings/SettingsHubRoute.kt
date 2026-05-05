package com.screwy.igloo.settings

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.R
import com.screwy.igloo.settings.components.SettingsHubDivider
import com.screwy.igloo.settings.components.SettingsHubRow
import com.screwy.igloo.settings.components.SettingsHubSection
import com.screwy.igloo.settings.components.SettingsSubScreen
import com.screwy.igloo.settings.components.SettingsSwitchRow
import com.screwy.igloo.settings.components.LanguageRow
import com.screwy.igloo.settings.components.MomentsDefaultTabRow
import com.screwy.igloo.settings.components.StartingPageRow
import com.screwy.igloo.ui.nav.IglooDestination
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import org.koin.androidx.compose.koinViewModel

/**
 * Settings hub — short grouped list of category rows. Each row drills into a focused
 * sub-screen.
 *
 * Registered directly from the route registry as the `settings` destination.
 */
@Composable
fun SettingsHubRoute(
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val vm: SettingsHubViewModel = koinViewModel()
    val startingPage by vm.startingPage.collectAsStateWithLifecycle()
    val languageTag by vm.languageTag.collectAsStateWithLifecycle()
    val shareEmbedFriendlyLinks by vm.shareEmbedFriendlyLinks.collectAsStateWithLifecycle()
    val debugMode by vm.debugMode.collectAsStateWithLifecycle()
    val momentsDefaultTab by vm.momentsDefaultTab.collectAsStateWithLifecycle()
    val momentsIncludeReposts by vm.momentsIncludeReposts.collectAsStateWithLifecycle()
    val instagramIncludeTagged by vm.instagramIncludeTagged.collectAsStateWithLifecycle()
    val navigator = rememberIglooNavigator(navController)

    SettingsSubScreen(
        title = stringResource(R.string.settings_screen_title),
        onBack = { navController.popBackStack() },
        modifier = modifier.fillMaxSize(),
    ) {
        SettingsHubSection(stringResource(R.string.settings_section_general)) {
            StartingPageRow(value = startingPage, onSelect = vm::setStartingPage)
            SettingsHubDivider()
            LanguageRow(value = languageTag, onSelect = vm::setLanguageTag)
            SettingsHubDivider()
            SettingsSwitchRow(
                label = stringResource(R.string.settings_share_embed_friendly_links),
                checked = shareEmbedFriendlyLinks,
                onToggle = vm::setShareEmbedFriendlyLinks,
            )
            SettingsHubDivider()
            SettingsHubRow(stringResource(R.string.settings_appearance)) {
                navigator.openDestination(IglooDestination.ThemeSettings, IglooNavigationSource.Settings)
            }
        }

        SettingsHubSection(stringResource(R.string.settings_section_content)) {
            SettingsHubRow(stringResource(R.string.settings_playback)) {
                navigator.openDestination(IglooDestination.PlaybackSettings, IglooNavigationSource.Settings)
            }
            SettingsHubDivider()
            SettingsHubRow(stringResource(R.string.nav_feed)) {
                navigator.openDestination(IglooDestination.FeedSettings, IglooNavigationSource.Settings)
            }
            SettingsHubDivider()
            MomentsDefaultTabRow(value = momentsDefaultTab, onSelect = vm::setMomentsDefaultTab)
            SettingsHubDivider()
            SettingsSwitchRow(
                label = stringResource(R.string.settings_moments_include_tiktok_reposts),
                checked = momentsIncludeReposts,
                onToggle = vm::setMomentsIncludeReposts,
            )
            SettingsHubDivider()
            SettingsSwitchRow(
                label = stringResource(R.string.settings_moments_include_instagram_tagged),
                checked = instagramIncludeTagged,
                onToggle = vm::setInstagramIncludeTagged,
            )
            SettingsHubDivider()
            SettingsHubRow(stringResource(R.string.settings_sponsorblock_dearrow)) {
                navigator.openDestination(IglooDestination.SponsorBlockSettings, IglooNavigationSource.Settings)
            }
        }

        SettingsHubSection(stringResource(R.string.settings_section_system)) {
            SettingsHubRow(stringResource(R.string.settings_storage_sync)) {
                navigator.openDestination(IglooDestination.StorageSettings, IglooNavigationSource.Settings)
            }
            SettingsHubDivider()
            SettingsSwitchRow(
                label = stringResource(R.string.settings_debug_mode),
                checked = debugMode,
                onToggle = vm::setDebugMode,
            )
        }

        SettingsHubSection(stringResource(R.string.settings_tab_account)) {
            SettingsHubRow(stringResource(R.string.settings_tab_account)) {
                navigator.openDestination(IglooDestination.AccountSettings, IglooNavigationSource.Settings)
            }
        }
    }
}
