package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mywio/git-ops/pkg/core"
)

func (p *UIPlugin) requireOperator(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := p.authenticatedUser(r)
		if p.isOperator(r, user) {
			next(w, r)
			return
		}
		http.Error(w, "operator permission required", http.StatusForbidden)
	}
}

func (p *UIPlugin) isOperator(r *http.Request, user string) bool {
	for _, allowed := range p.cfg.OperatorUsers {
		if strings.EqualFold(strings.TrimSpace(allowed), user) && user != "" {
			return true
		}
	}
	if !p.cfg.TrustAuthHeader && strings.TrimSpace(p.cfg.AuthVerifyURL) == "" {
		return false
	}
	groups := strings.FieldsFunc(r.Header.Get(p.cfg.GroupsHeader), func(char rune) bool { return char == ',' || char == ';' })
	for _, group := range groups {
		for _, allowed := range p.cfg.OperatorGroups {
			if strings.EqualFold(strings.TrimSpace(group), strings.TrimSpace(allowed)) && strings.TrimSpace(group) != "" {
				return true
			}
		}
	}
	return false
}

func (p *UIPlugin) executeCapability(w http.ResponseWriter, r *http.Request, capability core.Capability, action string, params map[string]interface{}) {
	plugins := p.registry.GetPluginsWithCapability(capability)
	if len(plugins) == 0 {
		http.Error(w, "capability unavailable", http.StatusNotFound)
		return
	}
	if params == nil {
		params = map[string]interface{}{}
	}
	if user := p.authenticatedUser(r); user != "" {
		params["requested_by"] = user
	}
	var lastErr error
	for _, plugin := range plugins {
		result, err := plugin.Execute(r.Context(), action, params)
		if err == nil {
			writeJSON(w, http.StatusOK, result)
			return
		}
		lastErr = err
	}
	http.Error(w, fmt.Sprint(lastErr), http.StatusBadGateway)
}

func (p *UIPlugin) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, uiMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}
	p.executeCapability(w, r, core.CapabilityListAgents, "list_agents", nil)
}
func (p *UIPlugin) handleActionWorkflows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, uiMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}
	p.executeCapability(w, r, core.CapabilityListActionWorkflows, "list_action_workflows", nil)
}
func (p *UIPlugin) handleActionRunners(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, uiMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}
	p.executeCapability(w, r, core.CapabilityListRunners, "list_runners", nil)
}
func (p *UIPlugin) handleActionsSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, uiMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}
	p.executeCapability(w, r, core.CapabilityActionsSetup, "actions_setup", nil)
}

type uiCapabilityAction struct {
	Action string                 `json:"action"`
	Params map[string]interface{} `json:"params"`
}

func decodeCapabilityAction(r *http.Request) (uiCapabilityAction, error) {
	var request uiCapabilityAction
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	request.Action = strings.TrimSpace(request.Action)
	if request.Action == "" {
		return request, fmt.Errorf("action is required")
	}
	if request.Params == nil {
		request.Params = map[string]interface{}{}
	}
	return request, nil
}

func (p *UIPlugin) handleFleetAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, uiMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}
	request, err := decodeCapabilityAction(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	allowed := map[string]bool{"create_enrollment_token": true, "revoke_agent": true}
	if !allowed[request.Action] {
		http.Error(w, "unsupported fleet action", http.StatusBadRequest)
		return
	}
	p.executeCapability(w, r, core.CapabilityManageAgents, request.Action, request.Params)
}

func (p *UIPlugin) handleGitHubAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, uiMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}
	request, err := decodeCapabilityAction(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	actions := map[string]core.Capability{
		"dispatch_workflow": core.CapabilityManageActionRuns, "cancel_workflow": core.CapabilityManageActionRuns,
		"rerun_workflow": core.CapabilityManageActionRuns, "rerun_failed_workflow": core.CapabilityManageActionRuns,
		"create_runner": core.CapabilityManageRunners, "runner_action": core.CapabilityManageRunners,
	}
	capability, ok := actions[request.Action]
	if !ok {
		http.Error(w, "unsupported GitHub action", http.StatusBadRequest)
		return
	}
	p.executeCapability(w, r, capability, request.Action, request.Params)
}
