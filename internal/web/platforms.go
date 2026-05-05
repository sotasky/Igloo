package web

import (
	"strings"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/config"
)

func (s *Server) enabledPlatforms() []string {
	if s == nil || s.cfg == nil {
		return append([]string(nil), config.SupportedPlatforms...)
	}
	if s.cfg.EnabledPlatforms == nil {
		return append([]string(nil), config.SupportedPlatforms...)
	}
	return append([]string(nil), s.cfg.EnabledPlatforms...)
}

func (s *Server) platformEnabled(platform string) bool {
	platform = config.NormalizePlatform(platform)
	if s == nil || s.cfg == nil {
		for _, p := range config.SupportedPlatforms {
			if p == platform {
				return true
			}
		}
		return false
	}
	return s.cfg.PlatformEnabled(platform)
}

func (s *Server) effectivePlatforms(platforms []string) []string {
	if s == nil || s.cfg == nil {
		return configFallbackEffectivePlatforms(platforms)
	}
	return s.cfg.EffectivePlatforms(platforms)
}

func configFallbackEffectivePlatforms(platforms []string) []string {
	if len(platforms) == 0 {
		return append([]string(nil), config.SupportedPlatforms...)
	}
	enabled := make(map[string]bool, len(config.SupportedPlatforms))
	for _, p := range config.SupportedPlatforms {
		enabled[p] = true
	}
	var out []string
	seen := make(map[string]bool)
	for _, p := range platforms {
		p = config.NormalizePlatform(p)
		if enabled[p] && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) platformChoices() []components.PlatformChoice {
	choices := make([]components.PlatformChoice, 0, len(s.enabledPlatforms()))
	for _, p := range s.enabledPlatforms() {
		choices = append(choices, components.PlatformChoice{
			Value: p,
			Label: platformChoiceLabel(p),
		})
	}
	return choices
}

func (s *Server) normalizeRequestedPlatforms(platforms []string) ([]string, error) {
	if len(platforms) == 0 {
		return s.enabledPlatforms(), nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, raw := range platforms {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		p := config.NormalizePlatform(raw)
		if !s.platformEnabled(p) {
			return nil, errPlatformDisabled(p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return s.enabledPlatforms(), nil
	}
	return out, nil
}

type errPlatformDisabled string

func (e errPlatformDisabled) Error() string {
	return "platform is not enabled: " + string(e)
}

func platformChoiceLabel(platform string) string {
	switch platform {
	case "youtube":
		return "YouTube"
	case "twitter":
		return "X"
	case "tiktok":
		return "TikTok"
	case "instagram":
		return "Instagram"
	default:
		return platform
	}
}

func filterEnabledPlatforms(platforms []string, enabled map[string]bool) []string {
	var out []string
	seen := make(map[string]bool)
	for _, p := range platforms {
		p = config.NormalizePlatform(p)
		if enabled[p] && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
