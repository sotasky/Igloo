package worker

import (
	"fmt"
	"log"
	"time"

	"github.com/screwys/igloo/internal/db"
)

func (m *Manager) xMediaDownloadLimit() int {
	if m == nil || m.db == nil {
		return 1
	}
	limit := m.db.IntSetting("media_download_limit_default")
	if limit < 1 {
		return 1
	}
	return limit
}

func (m *Manager) enforceXFeedSourceLimit(sourceID string, limit int) error {
	if m == nil || m.db == nil {
		return nil
	}
	m.xRetentionMu.Lock()
	defer m.xRetentionMu.Unlock()
	result, err := m.db.PruneXFeedSourceRetention(sourceID, limit, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("prune feed source %s: %w", sourceID, err)
	}
	m.finishXMediaRetention(result)
	return nil
}

func (m *Manager) EnforceXMediaRetentionForChannel(channelID string) error {
	if m == nil || m.db == nil {
		return nil
	}
	m.xRetentionMu.Lock()
	defer m.xRetentionMu.Unlock()
	result, err := m.db.PruneXMediaRetentionForChannel(channelID, db.XMediaRetentionOptions{NowMs: time.Now().UnixMilli()})
	if err != nil {
		return err
	}
	m.finishXMediaRetention(result)
	return nil
}

func (m *Manager) EnforceXMediaRetention() error {
	if m == nil || m.db == nil {
		return nil
	}
	m.xRetentionMu.Lock()
	defer m.xRetentionMu.Unlock()
	result, err := m.db.PruneXMediaRetention(db.XMediaRetentionOptions{NowMs: time.Now().UnixMilli()})
	if err != nil {
		return err
	}
	m.finishXMediaRetention(result)
	return nil
}

func (m *Manager) ExpandXMediaRetentionForChannel(channelID string) error {
	if err := m.EnforceXMediaRetentionForChannel(channelID); err != nil {
		return err
	}
	m.TriggerChannelCheck(channelID)
	return nil
}

func (m *Manager) ExpandXMediaRetention() error {
	if err := m.EnforceXMediaRetention(); err != nil {
		return err
	}
	m.TriggerPlatformRefresh("twitter")
	return nil
}

func (m *Manager) ApplyAndroidFeedRetention(feedDays int) error {
	if m == nil || m.db == nil {
		return nil
	}
	if !db.IsValidRetentionDays(feedDays) {
		return fmt.Errorf("invalid Android feed retention: %d", feedDays)
	}
	current, err := m.androidFeedRetentionCurrent(feedDays)
	if err != nil || current {
		return err
	}
	m.xRetentionMu.Lock()
	defer m.xRetentionMu.Unlock()
	nowMs := time.Now().UnixMilli()
	current, err = m.androidFeedRetentionCurrent(feedDays)
	if err != nil || current {
		return err
	}
	result, err := m.db.RestoreXMediaForAndroidFeed(feedDays, nowMs)
	if err != nil {
		return err
	}
	if err := m.db.RecordAndroidFeedRetention(feedDays, nowMs); err != nil {
		return err
	}
	m.finishXMediaRetention(result)
	return nil
}

func (m *Manager) androidFeedRetentionCurrent(feedDays int) (bool, error) {
	state, err := m.db.GetAndroidFeedRetention()
	if err != nil || state == nil {
		return false, err
	}
	return state.FeedDays == feedDays, nil
}

func (m *Manager) finishXMediaRetention(result db.XMediaRetentionResult) {
	if result.AssetsRestored > 0 {
		m.KickMediaWork()
	}
	if result.PrunedItems == 0 && result.AssetsPruned == 0 && result.AssetsRestored == 0 && result.FileRemoval.Removed == 0 {
		return
	}
	log.Printf("[x_ingest] reconciled X media retention: items=%d pruned=%d restored=%d files=%d bytes=%d",
		result.PrunedItems, result.AssetsPruned, result.AssetsRestored,
		result.FileRemoval.Removed, result.FileRemoval.RemovedBytes)
}
