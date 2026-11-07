package main

import (
	"net/http"
	"strings"

	"github.com/mywio/git-ops/pkg/core"
)

func (p *UIPlugin) handleRollbackSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, uiMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	sha := strings.TrimSpace(r.URL.Query().Get("sha"))
	if err := core.ValidateStackIdentity(owner, repo); err != nil || sha == "" {
		http.Error(w, "valid owner, repo, and sha are required", http.StatusBadRequest)
		return
	}
	plugins := p.registry.GetPluginsWithCapability(core.CapabilityRollbackDeployment)
	for _, plugin := range plugins {
		result, err := plugin.Execute(r.Context(), "rollback_summary", map[string]interface{}{
			"owner": owner, "repo": repo, "sha": sha,
		})
		if err == nil {
			writeJSON(w, http.StatusOK, result)
			return
		}
		p.logger.Debug("Failed to load rollback summary", "plugin", plugin.Name(), "error", err)
	}
	http.Error(w, "rollback target is unavailable", http.StatusNotFound)
}
