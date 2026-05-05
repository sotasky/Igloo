package com.screwy.igloo.settings

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.R
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.settings.components.DearrowModeRow
import com.screwy.igloo.settings.components.SectionDescription
import com.screwy.igloo.settings.components.SectionHeader
import com.screwy.igloo.settings.components.SettingsSubScreen
import com.screwy.igloo.settings.components.SponsorBlockRow
import org.koin.androidx.compose.koinViewModel

@Composable
fun SponsorBlockRoute(
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val vm: SponsorBlockSettingsViewModel = koinViewModel()

    val sponsor      by vm.sbSponsor.collectAsStateWithLifecycle()
    val selfPromo    by vm.sbSelfPromo.collectAsStateWithLifecycle()
    val interaction  by vm.sbInteraction.collectAsStateWithLifecycle()
    val intro        by vm.sbIntro.collectAsStateWithLifecycle()
    val outro        by vm.sbOutro.collectAsStateWithLifecycle()
    val preview      by vm.sbPreview.collectAsStateWithLifecycle()
    val filler       by vm.sbFiller.collectAsStateWithLifecycle()
    val music        by vm.sbMusicOffTopic.collectAsStateWithLifecycle()
    val dearrowMode  by vm.dearrowMode.collectAsStateWithLifecycle()

    SettingsSubScreen(
        title = stringResource(R.string.settings_sponsorblock_dearrow),
        onBack = { navController.popBackStack() },
        modifier = modifier,
    ) {
        SectionHeader(stringResource(R.string.settings_sponsorblock_section))
        SectionDescription(stringResource(R.string.settings_sponsorblock_description))
        SponsorBlockRow(stringResource(R.string.settings_sb_sponsors), sponsor)                   { vm.setSponsorBlock(PreferencesRepo.Keys.SB_SPONSOR, it) }
        SponsorBlockRow(stringResource(R.string.settings_sb_selfpromo), selfPromo)                { vm.setSponsorBlock(PreferencesRepo.Keys.SB_SELF_PROMO, it) }
        SponsorBlockRow(stringResource(R.string.settings_sb_interaction), interaction)            { vm.setSponsorBlock(PreferencesRepo.Keys.SB_INTERACTION, it) }
        SponsorBlockRow(stringResource(R.string.settings_sb_intro), intro)                        { vm.setSponsorBlock(PreferencesRepo.Keys.SB_INTRO, it) }
        SponsorBlockRow(stringResource(R.string.settings_sb_outro), outro)                        { vm.setSponsorBlock(PreferencesRepo.Keys.SB_OUTRO, it) }
        SponsorBlockRow(stringResource(R.string.settings_sb_preview), preview)                    { vm.setSponsorBlock(PreferencesRepo.Keys.SB_PREVIEW, it) }
        SponsorBlockRow(stringResource(R.string.settings_sb_filler), filler)                      { vm.setSponsorBlock(PreferencesRepo.Keys.SB_FILLER, it) }
        SponsorBlockRow(stringResource(R.string.settings_sb_music_offtopic), music)               { vm.setSponsorBlock(PreferencesRepo.Keys.SB_MUSIC_OFFTOPIC, it) }

        SectionHeader(stringResource(R.string.settings_dearrow_section))
        SectionDescription(stringResource(R.string.settings_dearrow_description))
        DearrowModeRow(dearrowMode) { vm.setDearrowMode(it) }
    }
}
