package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type composeRefreshMockRegistry struct {
	cfg        map[string]map[string]any
	registered []core.EventTypeDesc
	events     []core.InternalEvent
}

func (m *composeRefreshMockRegistry) GetPlugin(name string) (core.Plugin, error) {
	return nil, assert.AnError
}
func (m *composeRefreshMockRegistry) GetPluginsWithCapability(cap core.Capability) []core.Plugin {
	return nil
}
func (m *composeRefreshMockRegistry) ListPlugins() []core.Plugin { return nil }
func (m *composeRefreshMockRegistry) RegisterEventType(desc core.EventTypeDesc) error {
	m.registered = append(m.registered, desc)
	return nil
}
func (m *composeRefreshMockRegistry) Publish(ctx context.Context, event core.InternalEvent) {
	m.events = append(m.events, event)
}
func (m *composeRefreshMockRegistry) Subscribe(pattern string, handler core.Listener) {}
func (m *composeRefreshMockRegistry) GetMuxServer() *http.ServeMux                    { return nil }
func (m *composeRefreshMockRegistry) GetHTTPClient() *http.Client                     { return nil }
func (m *composeRefreshMockRegistry) GetConfig() map[string]map[string]any            { return m.cfg }

func TestComposeRefreshCapabilities(t *testing.T) {
	plugin := &ComposeRefreshPlugin{}
	assert.Contains(t, plugin.Capabilities(), core.CapabilityRefreshStackImages)
	assert.Contains(t, plugin.Capabilities(), core.CapabilitySystemInfo)
	assert.NotContains(t, plugin.Capabilities(), core.CapabilityListDeployments)
}

func TestComposeRefreshLoadsTargetDirFromCoreConfig(t *testing.T) {
	registry := &composeRefreshMockRegistry{cfg: map[string]map[string]any{"core": {"target_dir": "/srv/stacks"}}}
	plugin := &ComposeRefreshPlugin{}

	require.NoError(t, plugin.Init(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), registry))

	assert.Equal(t, "/srv/stacks", plugin.targetDir)
	assert.True(t, plugin.enabled)
}

func TestComposeRefreshRunsUpOnlyWhenImagesChange(t *testing.T) {
	oldRun := runComposeRefreshCommand
	oldInspect := inspectComposeRefreshImage
	defer func() {
		runComposeRefreshCommand = oldRun
		inspectComposeRefreshImage = oldInspect
	}()

	var calls [][]string
	pulled := false
	runComposeRefreshCommand = func(stackPath string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		switch args[0] {
		case "config":
			return []byte("nginx:latest\n"), nil
		case "pull":
			pulled = true
			return []byte(""), nil
		case "up":
			return []byte(""), nil
		default:
			return []byte(""), nil
		}
	}
	inspectComposeRefreshImage = func(imageRef string) (string, error) {
		if pulled {
			return "new", nil
		}
		return "old", nil
	}

	plugin := &ComposeRefreshPlugin{enabled: true, targetDir: "/srv/stacks"}
	res, err := plugin.Execute(context.Background(), "refresh_stack_images", map[string]interface{}{"owner": "acme", "repo": "proxy"})
	require.NoError(t, err)

	result := res.(refreshResult)
	assert.True(t, result.Updated)
	assert.Equal(t, "updated", result.Status)
	assert.Equal(t, "/srv/stacks/acme/proxy", result.StackPath)
	assert.Equal(t, [][]string{{"config", "--images"}, {"pull"}, {"config", "--images"}, {"up", "-d", "--remove-orphans"}}, calls)
}

func TestComposeRefreshSkipsUpWhenImagesDoNotChange(t *testing.T) {
	oldRun := runComposeRefreshCommand
	oldInspect := inspectComposeRefreshImage
	defer func() {
		runComposeRefreshCommand = oldRun
		inspectComposeRefreshImage = oldInspect
	}()

	var calls [][]string
	runComposeRefreshCommand = func(stackPath string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if args[0] == "config" {
			return []byte("nginx:latest\n"), nil
		}
		return []byte(""), nil
	}
	inspectComposeRefreshImage = func(imageRef string) (string, error) { return "same", nil }

	plugin := &ComposeRefreshPlugin{enabled: true, targetDir: "/srv/stacks"}
	res, err := plugin.Execute(context.Background(), "refresh_stack_images", map[string]interface{}{"owner": "acme", "repo": "proxy"})
	require.NoError(t, err)

	result := res.(refreshResult)
	assert.False(t, result.Updated)
	assert.Equal(t, "no_update", result.Status)
	assert.Equal(t, [][]string{{"config", "--images"}, {"pull"}, {"config", "--images"}}, calls)
}
