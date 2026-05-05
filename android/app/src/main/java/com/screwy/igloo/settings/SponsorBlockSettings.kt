package com.screwy.igloo.settings

import com.screwy.igloo.data.PreferencesRepo

object SponsorBlockSettings {
    const val SB_OFF = "off"
    const val SB_SILENT = "silent"
    const val SB_ASK = "ask"

    fun sbDefault(key: String): String = PreferencesRepo.Defaults.sponsorBlockCategory(key)
}
