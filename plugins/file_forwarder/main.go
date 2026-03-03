package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

type FileForwarderPlugin struct {
	logger  *slog.Logger
	files   []forwardFileSpec
	enabled bool

	statsMu sync.RWMutex
	stats   fileForwarderStats
}

type fileForwarderConfig struct {
	Files []forwardFileSpec `yaml:"files"`
}

type forwardFileSpec struct {
	Env      string `yaml:"env"`
	Path     string `yaml:"path"`
	Filename string `yaml:"filename"`
	Mode     string `yaml:"mode"`
	Required *bool  `yaml:"required"`
}

type fileForwarderStats struct {
	ConfiguredFiles int
	ForwardedFiles  int
	MissingFiles    []string
	LastUpdated     time.Time
}

type fileForwarderStatsView struct {
	ConfiguredFiles int      `json:"configured_files"`
	ForwardedFiles  int      `json:"forwarded_files"`
	MissingFiles    []string `json:"missing_files,omitempty"`
	LastUpdated     string   `json:"last_updated,omitempty"`
}

type fileForwarderConfigView struct {
	Enabled bool                   `json:"enabled"`
	Files   []forwardFileSpecView  `json:"files,omitempty"`
	Stats   fileForwarderStatsView `json:"stats"`
}

type forwardFileSpecView struct {
	Env      string `json:"env"`
	Path     string `json:"path"`
	Filename string `json:"filename,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Required bool   `json:"required"`
}

var Plugin core.Plugin = &FileForwarderPlugin{}

func (p *FileForwarderPlugin) Name() string {
	return "file_forwarder"
}

func (p *FileForwarderPlugin) Description() string {
	return "Forwards allowlisted host files into docker compose as runtime file paths"
}

func (p *FileForwarderPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger

	if registry != nil {
		cfg := registry.GetConfig()
		if section, ok := cfg["file_forwarder"]; ok {
			var fcfg fileForwarderConfig
			if err := core.DecodeConfigSection(section, &fcfg); err != nil {
				p.logger.WarnContext(ctx, "Invalid file_forwarder config", "error", err)
			}
			p.files = normalizeFileSpecs(fcfg.Files)
		}
	}

	if len(p.files) == 0 {
		p.enabled = false
		p.logger.WarnContext(ctx, "file_forwarder has no files configured, disabled")
		p.setStats(fileForwarderStats{ConfiguredFiles: 0})
		return nil
	}

	p.enabled = true
	p.setStats(fileForwarderStats{ConfiguredFiles: len(p.files)})
	p.logger.InfoContext(ctx, "file_forwarder initialized", "files", len(p.files))
	return nil
}

func (p *FileForwarderPlugin) Start(ctx context.Context) error { return nil }

func (p *FileForwarderPlugin) Stop(ctx context.Context) error { return nil }

func (p *FileForwarderPlugin) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityRuntimeFiles}
}

func (p *FileForwarderPlugin) Status() core.ServiceStatus {
	if p.enabled {
		return core.StatusHealthy
	}
	return core.StatusDegraded
}

func (p *FileForwarderPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	switch action {
	case "get_runtime_files":
		files, stats, err := p.collectRuntimeFiles()
		p.setStats(stats)
		return files, err
	case "get_stats":
		return p.getStatsView(), nil
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

func (p *FileForwarderPlugin) Config() any {
	viewFiles := make([]forwardFileSpecView, 0, len(p.files))
	for _, file := range p.files {
		viewFiles = append(viewFiles, forwardFileSpecView{
			Env:      file.Env,
			Path:     file.Path,
			Filename: file.Filename,
			Mode:     file.Mode,
			Required: isRequired(file.Required),
		})
	}
	return fileForwarderConfigView{
		Enabled: p.enabled,
		Files:   viewFiles,
		Stats:   p.getStatsView(),
	}
}

func (p *FileForwarderPlugin) collectRuntimeFiles() ([]core.RuntimeFile, fileForwarderStats, error) {
	stats := fileForwarderStats{
		ConfiguredFiles: len(p.files),
		MissingFiles:    []string{},
	}
	if !p.enabled {
		return []core.RuntimeFile{}, stats, nil
	}

	out := make([]core.RuntimeFile, 0, len(p.files))
	for _, spec := range p.files {
		content, err := os.ReadFile(spec.Path)
		if err != nil {
			if isRequired(spec.Required) {
				stats.LastUpdated = time.Now().UTC()
				return nil, stats, fmt.Errorf("failed reading required file %s: %w", spec.Path, err)
			}
			stats.MissingFiles = append(stats.MissingFiles, spec.Path)
			continue
		}

		mode := uint32(0600)
		if spec.Mode != "" {
			parsed, err := parseFileMode(spec.Mode)
			if err != nil {
				return nil, stats, fmt.Errorf("invalid mode for %s: %w", spec.Path, err)
			}
			mode = parsed
		}

		filename := spec.Filename
		if filename == "" {
			filename = filepath.Base(spec.Path)
		}
		filename = filepath.Base(filename)
		if filename == "" || filename == "." || filename == string(filepath.Separator) {
			return nil, stats, fmt.Errorf("invalid filename for env %s", spec.Env)
		}

		out = append(out, core.RuntimeFile{
			EnvKey:   spec.Env,
			Filename: filename,
			Content:  content,
			Mode:     mode,
		})
	}

	stats.ForwardedFiles = len(out)
	stats.LastUpdated = time.Now().UTC()
	return out, stats, nil
}

func parseFileMode(raw string) (uint32, error) {
	modeRaw := strings.TrimSpace(raw)
	if modeRaw == "" {
		return 0, fmt.Errorf("empty mode")
	}
	value, err := strconv.ParseUint(modeRaw, 8, 32)
	if err != nil {
		return 0, err
	}
	return uint32(value), nil
}

func normalizeFileSpecs(in []forwardFileSpec) []forwardFileSpec {
	out := make([]forwardFileSpec, 0, len(in))
	seen := map[string]struct{}{}
	for _, spec := range in {
		spec.Env = strings.TrimSpace(spec.Env)
		spec.Path = strings.TrimSpace(spec.Path)
		spec.Filename = strings.TrimSpace(spec.Filename)
		spec.Mode = strings.TrimSpace(spec.Mode)

		if spec.Env == "" || spec.Path == "" {
			continue
		}
		if _, exists := seen[spec.Env]; exists {
			continue
		}
		seen[spec.Env] = struct{}{}
		out = append(out, spec)
	}
	return out
}

func (p *FileForwarderPlugin) setStats(stats fileForwarderStats) {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	p.stats = stats
}

func (p *FileForwarderPlugin) getStatsView() fileForwarderStatsView {
	p.statsMu.RLock()
	defer p.statsMu.RUnlock()

	lastUpdated := ""
	if !p.stats.LastUpdated.IsZero() {
		lastUpdated = p.stats.LastUpdated.Format(time.RFC3339)
	}

	return fileForwarderStatsView{
		ConfiguredFiles: p.stats.ConfiguredFiles,
		ForwardedFiles:  p.stats.ForwardedFiles,
		MissingFiles:    append([]string(nil), p.stats.MissingFiles...),
		LastUpdated:     lastUpdated,
	}
}

func isRequired(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}
