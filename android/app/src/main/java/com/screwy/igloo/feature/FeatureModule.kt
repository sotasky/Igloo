package com.screwy.igloo.feature

import com.screwy.igloo.bookmarks.BookmarksViewModel
import com.screwy.igloo.channel.ChannelViewModel
import com.screwy.igloo.data.DatabaseHolder
import com.screwy.igloo.feed.FeedViewModel
import com.screwy.igloo.liked.LikedViewModel
import com.screwy.igloo.logs.LogsViewModel
import com.screwy.igloo.media.MediaRouteViewModel
import com.screwy.igloo.moments.MomentsViewModel
import com.screwy.igloo.moments.ShortsPlaylistSpec
import com.screwy.igloo.moments.ShortsRouteViewModel
import com.screwy.igloo.player.PlayerViewModel
import com.screwy.igloo.settings.AccountSettingsViewModel
import com.screwy.igloo.settings.FeedSettingsViewModel
import com.screwy.igloo.settings.MutedAccountsViewModel
import com.screwy.igloo.settings.PlaybackSettingsViewModel
import com.screwy.igloo.settings.SettingsHubViewModel
import com.screwy.igloo.settings.SponsorBlockSettingsViewModel
import com.screwy.igloo.settings.StorageViewModel
import com.screwy.igloo.settings.ThemeViewModel
import com.screwy.igloo.thread.ThreadViewModel
import com.screwy.igloo.videos.VideosViewModel
import org.koin.core.module.dsl.viewModel
import org.koin.dsl.module

/**
 * Koin wiring for feature-route ViewModels. `IglooDatabase` is resolved fresh
 * per VM so login/logout swaps propagate transparently.
 */
val iglooFeatureModule = module {
    viewModel {
        FeedViewModel(
            db = get<DatabaseHolder>().requireCurrent(),
            outboxWriter = get(),
            scheduler = get(),
            uiEffects = get(),
            baseUrlProvider = get(),
        )
    }
    viewModel {
        VideosViewModel(
            db = get<DatabaseHolder>().requireCurrent(),
            scheduler = get(),
        )
    }
    viewModel {
        BookmarksViewModel(
            db = get<DatabaseHolder>().requireCurrent(),
            outboxWriter = get(),
            prefs = get(),
        )
    }
    viewModel {
        LikedViewModel(
            db = get<DatabaseHolder>().requireCurrent(),
            outboxWriter = get(),
            scheduler = get(),
            uiEffects = get(),
            baseUrlProvider = get(),
        )
    }
    viewModel {
        MomentsViewModel(
            db = get<DatabaseHolder>().requireCurrent(),
            outboxWriter = get(),
            prefs = get(),
            scheduler = get(),
            uiEffects = get(),
            resolvers = get(),
        )
    }
    viewModel { (playlistSpec: ShortsPlaylistSpec, startVideoId: String) ->
        ShortsRouteViewModel(
            playlistSpec = playlistSpec,
            startVideoId = startVideoId,
            db = get<DatabaseHolder>().requireCurrent(),
            outboxWriter = get(),
            prefs = get(),
            uiEffects = get(),
            baseUrlProvider = get(),
        )
    }
    viewModel { (channelId: String) ->
        ChannelViewModel(
            channelId = channelId,
            db = get<DatabaseHolder>().requireCurrent(),
            outboxWriter = get(),
            prefs = get(),
            scheduler = get(),
            uiEffects = get(),
            reachability = get(),
            baseUrlProvider = get(),
        )
    }
    viewModel { (videoId: String) ->
        PlayerViewModel(
            videoId = videoId,
            db = get<DatabaseHolder>().requireCurrent(),
            outboxWriter = get(),
            prefs = get(),
            scheduler = get(),
            uiEffects = get(),
            resolvers = get(),
        )
    }
    viewModel { (ownerKind: String, ownerId: String, index: Int) ->
        MediaRouteViewModel(
            ownerKind = ownerKind,
            ownerId = ownerId,
            requestedIndex = index,
            db = get<DatabaseHolder>().requireCurrent(),
            outboxWriter = get(),
            baseUrlProvider = get(),
            uiEffects = get(),
        )
    }
    viewModel {
        ThreadViewModel(
            db = get<DatabaseHolder>().requireCurrent(),
            outboxWriter = get(),
            uiEffects = get(),
            baseUrlProvider = get(),
        )
    }
    viewModel {
        SettingsHubViewModel(
            prefs = get(),
            languageStore = get(),
        )
    }
    viewModel {
        MutedAccountsViewModel(
            db = get<DatabaseHolder>().requireCurrent(),
            outboxWriter = get(),
        )
    }
    viewModel { StorageViewModel(cacheOps = get(), prefs = get(), scheduler = get()) }
    viewModel { AccountSettingsViewModel(prefs = get(), authRepo = get()) }
    viewModel { FeedSettingsViewModel(prefs = get()) }
    viewModel { PlaybackSettingsViewModel(prefs = get()) }
    viewModel { SponsorBlockSettingsViewModel(prefs = get()) }
    viewModel {
        ThemeViewModel(prefs = get())
    }
    viewModel {
        LogsViewModel(db = get<DatabaseHolder>().requireCurrent())
    }
}
