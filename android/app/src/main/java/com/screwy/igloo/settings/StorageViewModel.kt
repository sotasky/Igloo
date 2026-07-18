package com.screwy.igloo.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.media.CacheActions
import com.screwy.igloo.media.CacheStats
import com.screwy.igloo.sync.PeriodicSyncScheduler
import com.screwy.igloo.sync.SyncCoordinator
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/**
 * Backs [StorageRoute]. Owns sync cadence, retention windows, cache stats, and
 * cache/sync actions so the route has one state holder for data lifecycle state.
 *
 * Stats refresh eagerly on init and again after every clear — the route renders whatever
 * the flow holds.
 */
class StorageViewModel(
    private val cacheOps: CacheActions,
    private val prefs: PreferencesRepo,
    private val scheduler: SyncCoordinator? = null,
    private val periodicSyncScheduler: PeriodicSyncScheduler? = null,
) : ViewModel() {

    val syncEnabled: StateFlow<Boolean> =
        prefs.syncEnabled().stateDefault(PreferencesRepo.Defaults.SYNC_ENABLED)
    val syncIntervalMinutes: StateFlow<Int> =
        prefs.syncIntervalMinutes().stateDefault(PreferencesRepo.Defaults.SYNC_INTERVAL_MINUTES)
    val syncWifiOnly: StateFlow<Boolean> =
        prefs.syncWifiOnly().stateDefault(PreferencesRepo.Defaults.SYNC_WIFI_ONLY)

    val retentionDaysYoutube: StateFlow<Int> =
        prefs.retentionDaysYoutube().stateDefault(PreferencesRepo.Defaults.RETENTION_DAYS_YOUTUBE)
    val retentionDaysMoments: StateFlow<Int> =
        prefs.retentionDaysMoments().stateDefault(PreferencesRepo.Defaults.RETENTION_DAYS_MOMENTS)
    val retentionDaysFeed: StateFlow<Int> =
        prefs.retentionDaysFeed().stateDefault(PreferencesRepo.Defaults.RETENTION_DAYS_FEED)
    val storiesWindowHours: StateFlow<Int> =
        prefs.storiesWindowHours().stateDefault(PreferencesRepo.Defaults.STORIES_WINDOW_HOURS)

    private val _stats = MutableStateFlow<List<CacheStats>>(emptyList())
    val stats: StateFlow<List<CacheStats>> = _stats.asStateFlow()
    private val _statsLoading = MutableStateFlow(true)
    val statsLoading: StateFlow<Boolean> = _statsLoading.asStateFlow()

    init { refresh() }

    fun setSyncEnabled(value: Boolean) {
        viewModelScope.launch {
            prefs.setSyncEnabled(value)
            periodicSyncScheduler?.applyPreferences()
        }
    }

    fun setSyncIntervalMinutes(value: Int) {
        viewModelScope.launch {
            prefs.setSyncIntervalMinutes(value)
            periodicSyncScheduler?.applyPreferences()
        }
    }

    fun setSyncWifiOnly(value: Boolean) {
        viewModelScope.launch {
            prefs.setSyncWifiOnly(value)
            periodicSyncScheduler?.applyPreferences()
        }
    }

    fun setRetentionDaysYoutube(value: Int) {
        viewModelScope.launch { prefs.setRetentionDaysYoutube(value) }
    }

    fun setRetentionDaysMoments(value: Int) {
        viewModelScope.launch { prefs.setRetentionDaysMoments(value) }
    }

    fun setRetentionDaysFeed(value: Int) {
        viewModelScope.launch { prefs.setRetentionDaysFeed(value) }
    }

    fun setStoriesWindowHours(value: Int) {
        viewModelScope.launch { prefs.setStoriesWindowHours(value) }
    }

    fun refresh() {
        viewModelScope.launch(Dispatchers.IO) { loadStats() }
    }

    fun clearCache(bucket: String?) {
        viewModelScope.launch(Dispatchers.IO) {
            _statsLoading.value = true
            try {
                cacheOps.clearCache(bucket)
                _stats.value = cacheOps.stats()
            } finally {
                _statsLoading.value = false
            }
        }
    }

    fun clearCacheBuckets(buckets: Collection<String>) {
        viewModelScope.launch(Dispatchers.IO) {
            _statsLoading.value = true
            try {
                cacheOps.clearCaches(buckets)
                _stats.value = cacheOps.stats()
            } finally {
                _statsLoading.value = false
            }
        }
    }

    fun clearYoutubeDownloads() {
        viewModelScope.launch(Dispatchers.IO) {
            _statsLoading.value = true
            try {
                cacheOps.clearYoutubeDownloads()
                _stats.value = cacheOps.stats()
            } finally {
                _statsLoading.value = false
            }
        }
    }

    fun triggerSyncNow() {
        scheduler?.triggerAll()
    }

    private suspend fun loadStats() {
        _statsLoading.value = true
        try {
            _stats.value = cacheOps.stats()
        } finally {
            _statsLoading.value = false
        }
    }

    private fun <T> Flow<T>.stateDefault(initial: T): StateFlow<T> =
        stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), initial)
}
