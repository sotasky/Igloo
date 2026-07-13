package worker

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func (m *Manager) EnforceVideoRetentionForChannel(channelID string) error {
	channel, err := m.db.GetChannel(strings.TrimSpace(channelID))
	if err != nil {
		return err
	}
	if channel == nil {
		return nil
	}
	return m.enforceVideoRetention([]model.Channel{*channel})
}

func (m *Manager) EnforceVideoRetentionForPlatform(platform string) error {
	platform = strings.ToLower(strings.TrimSpace(platform))
	channels, err := m.db.GetSubscribedChannels()
	if err != nil {
		return err
	}
	selected := make([]model.Channel, 0, len(channels))
	for _, channel := range channels {
		if strings.EqualFold(channel.Platform, platform) {
			selected = append(selected, channel)
		}
	}
	return m.enforceVideoRetention(selected)
}

func (m *Manager) enforceVideoRetention(channels []model.Channel) error {
	var enforceErrors []error
	for _, channel := range channels {
		settings, err := m.db.GetChannelSettings(channel.ChannelID)
		if err != nil {
			enforceErrors = append(enforceErrors, fmt.Errorf("read %s retention: %w", channel.ChannelID, err))
			continue
		}
		if settings == nil {
			continue
		}
		if err := m.db.EnforceVideoDesireLimit(channel.ChannelID, settings.MaxVideos); err != nil {
			enforceErrors = append(enforceErrors, fmt.Errorf("enforce %s retention: %w", channel.ChannelID, err))
		}
	}
	if _, err := m.db.MaintainVideoRetention(time.Now().UnixMilli()); err != nil {
		enforceErrors = append(enforceErrors, fmt.Errorf("collect video retention: %w", err))
	}
	return errors.Join(enforceErrors...)
}
