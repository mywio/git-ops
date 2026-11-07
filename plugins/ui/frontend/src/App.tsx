import { useState, useEffect, useMemo, useRef } from 'react';
import {
    Activity,
    ChevronDown,
    ChevronRight,
    Terminal,
    Settings,
    Layers,
    Cpu,
    RefreshCw,
    PlayCircle,
    Server,
    Github,
    BookOpen
} from 'lucide-react';

/* --- Types --- */
type Deployment = {
    id?: string;
    kind?: string;
    source?: string;
    managed?: boolean;
    display_name?: string;
    group_id?: string;
    group_name?: string;
    group_kind?: string;
    group_path?: string;
    owner: string;
    repo: string;
    path: string;
    status: string;
    disabled?: boolean;
    container?: string;
    image?: string;
    docker_status?: string;
    compose_project?: string;
    compose_service?: string;
    current_sha?: string;
    pending_sha?: string;
    previous_success_sha?: string;
    verification?: string;
    elapsed?: string;
    successful_revisions?: Array<{ sha: string; deployed_at: string; verification: string }>;
    rollback_status?: string;
    compose_files?: string;
    adoption_status?: string;
    execution_status?: string;
    execution_stage?: string;
    last_error?: string;
    history?: ExecutionHistoryEntry[];
};

type ExecutionHistoryEntry = {
    ExecutionID: string;
    Status: string;
    Stage: string;
    LastError: string;
    UpdatedAt: string;
};

type DeploymentGroup = {
    id: string;
    name: string;
    kind: string;
    path: string;
    deployments: Deployment[];
};

type PluginInfo = {
    name: string;
    description?: string;
    status: string;
    capabilities: string[];
    config?: any;
};

const tabPaths: Record<string, string> = {
    system: '/ui/system',
    stacks: '/ui/stacks',
    agents: '/ui/agents',
    actions: '/ui/actions',
    setup: '/ui/setup',
    logs: '/ui/logs',
    plugins: '/ui/plugins',
};

function tabFromPath(pathname: string) {
    if (!pathname.startsWith('/ui/')) {
        return 'system';
    }

    const segment = pathname.slice('/ui/'.length).split('/')[0];
    if (segment in tabPaths) {
        return segment;
    }
    return 'system';
}

function formatExecutionHistory(history?: ExecutionHistoryEntry[]) {
    if (!history || history.length === 0) {
        return '';
    }

    return history
        .map((entry) => {
            const details = [`${entry.Status} @ ${entry.Stage}`];
            if (entry.UpdatedAt) {
                details.push(new Date(entry.UpdatedAt).toLocaleString());
            }
            if (entry.LastError) {
                details.push(`error: ${entry.LastError}`);
            }
            return details.join(' | ');
        })
        .join('\n');
}

function deploymentLabel(dep: Deployment) {
    if (dep.display_name) {
        return dep.display_name;
    }
    if (dep.owner && dep.repo) {
        return `${dep.owner}/${dep.repo}`;
    }
    if (dep.container) {
        return dep.container;
    }
    return dep.repo || 'unknown';
}

function availableActions(dep: Deployment) {
    if (dep.source === 'local_deploy') {
        const actions = ['deploy_latest'];
        if (dep.last_error) {
            actions.push('retry_deployment');
        }
        if (dep.successful_revisions && dep.successful_revisions.length > 0) {
            actions.push('rollback_deployment');
        }
        return actions;
    }
    if (dep.managed === false) {
        return [] as string[];
    }
    if (dep.disabled) {
        return ['enable_stack'];
    }
    return ['start_stack', 'stop_stack', 'restart_stack', 'disable_stack', 'reconcile_stack', 'refresh_stack_images'];
}

function actionLabel(action: string) {
    switch (action) {
        case 'start_stack':
            return 'Start';
        case 'stop_stack':
            return 'Stop';
        case 'restart_stack':
            return 'Restart';
        case 'disable_stack':
            return 'Disable';
        case 'enable_stack':
            return 'Enable';
        case 'reconcile_stack':
            return 'Reconcile';
        case 'refresh_stack_images':
            return 'Pull Images';
        case 'deploy_latest':
            return 'Deploy latest';
        case 'retry_deployment':
            return 'Retry';
        case 'rollback_deployment':
            return 'Roll back';
        default:
            return action;
    }
}

function groupDeployments(deployments: Deployment[]) {
    const groups = new Map<string, DeploymentGroup>();

    deployments.forEach((deployment) => {
        const fallbackID = deployment.managed === false ? 'docker-standalone' : `git-ops-owner:${deployment.owner || 'unknown'}`;
        const id = deployment.group_id || fallbackID;
        const existing = groups.get(id);
        if (existing) {
            existing.deployments.push(deployment);
            return;
        }

        groups.set(id, {
            id,
            name: deployment.group_name || deployment.owner || 'Standalone containers',
            kind: deployment.group_kind || (deployment.managed === false ? 'docker' : 'git-ops'),
            path: deployment.group_path || '',
            deployments: [deployment],
        });
    });

    const kindOrder: Record<string, number> = { 'local_deploy': 0, github_actions: 1, 'git-ops': 2, compose: 3, docker: 4 };
    return Array.from(groups.values()).sort((a, b) => {
        const kindDifference = (kindOrder[a.kind] ?? 99) - (kindOrder[b.kind] ?? 99);
        return kindDifference || a.name.localeCompare(b.name);
    });
}

function groupKindLabel(kind: string) {
    switch (kind) {
        case 'local_deploy':
            return 'Commit-pinned local deployment';
        case 'git-ops':
            return 'Managed GitOps';
        case 'compose':
            return 'Unmanaged Compose';
        default:
            return 'Unmanaged Docker';
    }
}

function groupRuntimeStatus(group: DeploymentGroup) {
    const statuses = new Set(group.deployments.map((deployment) => deployment.status));
    if (statuses.size === 1) {
        return group.deployments[0].status;
    }
    if (statuses.has('running')) {
        return 'partial';
    }
    return 'unknown';
}

/* --- App Component --- */
function App() {
    const [activeTab, setActiveTab] = useState(() => tabFromPath(globalThis.location.pathname));
    const [systemInfo, setSystemInfo] = useState<any>(null);
    const [deployments, setDeployments] = useState<Deployment[]>([]);
    const [plugins, setPlugins] = useState<PluginInfo[]>([]);
    const [agents, setAgents] = useState<any[]>([]);
    const [actionRepositories, setActionRepositories] = useState<any[]>([]);
    const [setupInfo, setSetupInfo] = useState<any>(null);
    const [loading, setLoading] = useState(false);
    const [pendingActions, setPendingActions] = useState<Record<string, string>>({});
    const [rollbackTargets, setRollbackTargets] = useState<Record<string, string>>({});
    const [flashMessage, setFlashMessage] = useState<string>('');
    const [collapsedGroups, setCollapsedGroups] = useState<Record<string, boolean>>({});
    const [adoptionHelpGroups, setAdoptionHelpGroups] = useState<Record<string, boolean>>({});

    // Logs state
    const [selectedLogStack, setSelectedLogStack] = useState<Deployment | null>(null);
    const [logs, setLogs] = useState<Array<{ id: number; line: string }>>([]);
    const logContainerRef = useRef<HTMLDivElement>(null);
    const evtSource = useRef<EventSource | null>(null);
    const nextLogID = useRef(0);
    const deploymentGroups = useMemo(() => groupDeployments(deployments), [deployments]);

    useEffect(() => {
        fetchData();
    }, [activeTab]);

    useEffect(() => {
        if (activeTab !== 'stacks') {
            return;
        }
        const timer = globalThis.setInterval(fetchData, 2000);
        return () => globalThis.clearInterval(timer);
    }, [activeTab]);

    useEffect(() => {
        const handlePopState = () => {
            setActiveTab(tabFromPath(globalThis.location.pathname));
        };

        globalThis.addEventListener('popstate', handlePopState);
        return () => globalThis.removeEventListener('popstate', handlePopState);
    }, []);

    const fetchData = async () => {
        setLoading(true);
        try {
            if (activeTab === 'system') {
                const res = await fetch('/api/ui/system/info');
                setSystemInfo(await res.json());
            } else if (activeTab === 'stacks' || activeTab === 'logs') {
                const res = await fetch('/api/ui/deployments');
                const data = await res.json();
                setDeployments(data || []);
                if (activeTab === 'logs' && data && data.length > 0 && !selectedLogStack) {
                    setSelectedLogStack(data[0]);
                }
            } else if (activeTab === 'agents') {
                const [agentResponse, repositoryResponse] = await Promise.all([
                    fetch('/api/ui/fleet/agents'), fetch('/api/ui/github-actions/workflows'),
                ]);
                setAgents(await agentResponse.json() || []);
                setActionRepositories(await repositoryResponse.json() || []);
            } else if (activeTab === 'actions') {
                const res = await fetch('/api/ui/github-actions/workflows');
                setActionRepositories(await res.json() || []);
            } else if (activeTab === 'setup') {
                const res = await fetch('/api/ui/github-actions/setup');
                setSetupInfo(await res.json());
            } else if (activeTab === 'plugins') {
                const res = await fetch('/api/plugins?include_config=true');
                setPlugins(await res.json());
            }
        } catch (e) {
            console.error("Failed to fetch data:", e);
        }
        setLoading(false);
    };

    const handleCapabilityAction = async (endpoint: string, action: string, params: Record<string, any>) => {
        setFlashMessage('');
        const response = await fetch(endpoint, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ action, params }),
        });
        const text = await response.text();
        if (!response.ok) {
            setFlashMessage(text || `${action} failed`);
            return null;
        }
        const result = text ? JSON.parse(text) : {};
        setFlashMessage(`${action.replaceAll('_', ' ')} accepted`);
        await fetchData();
        return result;
    };

    const handleStackAction = async (dep: Deployment, action: string, targetSHA = '') => {
        const key = dep.id || `${dep.owner}-${dep.repo}`;
        setPendingActions(prev => ({ ...prev, [key]: action }));
        setFlashMessage('');
        try {
            let confirmed = false;
            if (action === 'rollback_deployment') {
                if (!targetSHA) {
                    throw new Error('Select a retained successful revision first');
                }
                const params = new URLSearchParams({ owner: dep.owner, repo: dep.repo, sha: targetSHA });
                const summaryResponse = await fetch(`/api/ui/local-deploy/rollback-summary?${params.toString()}`);
                if (!summaryResponse.ok) {
                    throw new Error(await summaryResponse.text());
                }
                const summary = await summaryResponse.json();
                const commands = Array.isArray(summary.commands) ? summary.commands.join('\n') : 'Unavailable';
                confirmed = globalThis.confirm(
                    `Roll back ${deploymentLabel(dep)}?\n\nCurrent: ${summary.current_sha || 'none'}\nTarget: ${summary.target_sha}\n\nRollback commands:\n${commands}`
                );
                if (!confirmed) {
                    return;
                }
            }
            const res = await fetch('/api/ui/stacks/action', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    owner: dep.owner,
                    repo: dep.repo,
                    action,
                    sha: targetSHA || undefined,
                    confirmed,
                }),
            });
            if (!res.ok) {
                const text = await res.text();
                throw new Error(text || `Request failed with status ${res.status}`);
            }
            setFlashMessage(`${actionLabel(action)} requested for ${deploymentLabel(dep)}`);
            await fetchData();
        } catch (e) {
            const message = e instanceof Error ? e.message : 'Unknown error';
            setFlashMessage(`Failed to ${actionLabel(action).toLowerCase()} ${deploymentLabel(dep)}: ${message}`);
        } finally {
            setPendingActions(prev => {
                const next = { ...prev };
                delete next[key];
                return next;
            });
        }
    };

    // Log Streamer
    useEffect(() => {
        if (activeTab === 'logs' && selectedLogStack) {
            if (evtSource.current) {
                evtSource.current.close();
            }
            setLogs([]);
            nextLogID.current = 0;

            const params = new URLSearchParams({ lines: '100' });
            if (selectedLogStack.kind === 'container' && selectedLogStack.container) {
                params.set('container', selectedLogStack.container);
            } else {
                params.set('owner', selectedLogStack.owner);
                params.set('repo', selectedLogStack.repo);
            }

            const sse = new EventSource(`/api/ui/logs?${params.toString()}`);
            evtSource.current = sse;

            sse.onmessage = (event) => {
                // Handle escaped newlines
                const lines = event.data.split(String.raw`\n`).map((line: string) => ({
                    id: nextLogID.current++,
                    line,
                }));
                setLogs(prev => [...prev, ...lines]);

                // Auto scroll
                setTimeout(() => {
                    if (logContainerRef.current) {
                        logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
                    }
                }, 50);
            };

            sse.onerror = () => {
                setLogs(prev => [...prev, { id: nextLogID.current++, line: "--- Log stream disconnected ---" }]);
                sse.close();
            };
        }

        return () => {
            if (evtSource.current) {
                evtSource.current.close();
            }
        };
    }, [activeTab, selectedLogStack]);

    const navItems = [
        { id: 'system', label: 'System Info', icon: <Activity size={18} /> },
        { id: 'stacks', label: 'Deployments', icon: <Layers size={18} /> },
        { id: 'agents', label: 'Agents & Runners', icon: <Server size={18} /> },
        { id: 'actions', label: 'GitHub Actions', icon: <Github size={18} /> },
        { id: 'setup', label: 'Runner Setup', icon: <BookOpen size={18} /> },
        { id: 'logs', label: 'Live Logs', icon: <Terminal size={18} /> },
        { id: 'plugins', label: 'Plugin Config', icon: <Settings size={18} /> },
    ];

    const handleTabClick = (tabId: string) => {
        setActiveTab(tabId);
        const nextPath = tabPaths[tabId] || '/ui/system';
        if (globalThis.location.pathname !== nextPath) {
            globalThis.history.pushState({}, '', nextPath);
        }
    };

    const toggleGroup = (groupID: string, currentlyCollapsed: boolean) => {
        setCollapsedGroups((current) => ({ ...current, [groupID]: !currentlyCollapsed }));
    };

    const toggleAdoptionHelp = (groupID: string) => {
        setAdoptionHelpGroups((current) => ({ ...current, [groupID]: !current[groupID] }));
    };

    return (
        <div className="app-container">
            {/* Sidebar */}
            <aside className="sidebar">
                <div className="sidebar-header">
                    <Cpu className="logo" size={28} />
                    <h2>GHOps</h2>
                </div>
                <nav className="sidebar-nav">
                    {navItems.map(item => (
                        <button
                            key={item.id}
                            className={`nav-btn ${activeTab === item.id ? 'active' : ''}`}
                            onClick={() => handleTabClick(item.id)}
                        >
                            {item.icon}
                            <span>{item.label}</span>
                        </button>
                    ))}
                </nav>
            </aside>

            {/* Main Content */}
            <main className="main-content">
                <header className="main-header">
                    <h1>{navItems.find(i => i.id === activeTab)?.label}</h1>
                    <button className="btn-icon" onClick={fetchData} disabled={loading}>
                        <RefreshCw size={18} className={loading ? 'spinning' : ''} />
                    </button>
                </header>

                <div className="content-area">
                    {flashMessage ? <div className="flash-message">{flashMessage}</div> : null}
                    {/* TAB: System Info */}
                    {activeTab === 'system' && (
                        <div className="card grid-card">
                            <h3>Agent capabilities</h3>
                            {systemInfo ? (
                                <pre className="code-block">{JSON.stringify(systemInfo, null, 2)}</pre>
                            ) : (
                                <div className="empty-state">No system info retrieved</div>
                            )}
                        </div>
                    )}

                    {/* TAB: Stacks */}
                    {activeTab === 'stacks' && (
                        <div className="deployment-groups">
                            {deployments.length === 0 ? (
                                <div className="empty-state">No deployments found.</div>
                            ) : (
                                deploymentGroups.map((group) => {
                                    const collapsed = collapsedGroups[group.id] ?? group.kind !== 'git-ops';
                                    const runtimeStatus = groupRuntimeStatus(group);
                                    const adoptionCandidate = group.kind === 'compose';
                                    const sample = group.deployments[0];
                                    return (
                                        <section className="deployment-group" key={group.id}>
                                            <div className="deployment-group-header">
                                                <button
                                                    className="deployment-group-toggle"
                                                    onClick={() => toggleGroup(group.id, collapsed)}
                                                    aria-expanded={!collapsed}
                                                >
                                                    {collapsed ? <ChevronRight size={18} /> : <ChevronDown size={18} />}
                                                    <span className="deployment-group-title">
                                                        <strong>{group.name}</strong>
                                                        <span className="text-muted">{groupKindLabel(group.kind)}</span>
                                                    </span>
                                                </button>
                                                <div className="deployment-group-summary">
                                                    <span className={`status-badge ${runtimeStatus}`}>{runtimeStatus}</span>
                                                    <span className="deployment-count">
                                                        {group.deployments.length} {group.deployments.length === 1 ? 'deployment' : 'deployments'}
                                                    </span>
                                                    {adoptionCandidate ? (
                                                        <button className="adoption-toggle" onClick={() => toggleAdoptionHelp(group.id)}>
                                                            {adoptionHelpGroups[group.id] ? 'Hide adoption steps' : 'How to adopt'}
                                                        </button>
                                                    ) : null}
                                                </div>
                                            </div>
                                            {group.path ? <div className="deployment-group-path">{group.path}</div> : null}
                                            {adoptionCandidate && adoptionHelpGroups[group.id] ? (
                                                <div className="adoption-guide">
                                                    <strong>Adopt this Compose project without duplicating its containers</strong>
                                                    <ol>
                                                        <li>Put a self-contained copy of {sample.compose_files || 'the Compose file'} in a GitHub repository owned by a configured GitHub user. GHOps fetches the Compose file and hooks, not the full checkout, so use published images and explicitly managed runtime files.</li>
                                                        <li>Keep the Compose project identity by adding <code>name: {sample.compose_project || group.name}</code> at the top level.</li>
                                                        <li>Add a configured deployment topic to the repository, then let GHOps discover it or request Reconcile.</li>
                                                        <li>Confirm the services are healthy here. Keep {group.path} until any bind-mounted files or data have been migrated explicitly.</li>
                                                    </ol>
                                                </div>
                                            ) : null}
                                            {!collapsed ? (
                                                <div className="deployment-group-table">
                                                    <table className="stacks-table">
                                                        <thead>
                                                            <tr>
                                                                <th>Runtime</th>
                                                                <th>Execution</th>
                                                                <th>Stage</th>
                                                                <th>Owner</th>
                                                                <th>Repository / Container</th>
                                                                <th>Last Error</th>
                                                                <th>Path</th>
                                                                <th>Actions</th>
                                                            </tr>
                                                        </thead>
                                                        <tbody>
                                                            {group.deployments.map(dep => (
                                                                <tr key={dep.id || `${dep.owner}-${dep.repo}`}>
                                                                    <td>
                                                                        <span className={`status-badge ${dep.status}`}>
                                                                            {dep.status === 'running' ? <PlayCircle size={14} /> : <Activity size={14} />}
                                                                            {dep.status}
                                                                        </span>
                                                                    </td>
                                                                    <td title={formatExecutionHistory(dep.history)}>
                                                                        {dep.execution_status ? (
                                                                            <div className="execution-cell">
                                                                                <span className={`status-badge ${dep.execution_status}`}>
                                                                                    {dep.execution_status}
                                                                                </span>
                                                                                {dep.history && dep.history.length > 0 ? (
                                                                                    <div className="status-detail text-muted">
                                                                                        {dep.history.length} recent runs
                                                                                    </div>
                                                                                ) : null}
                                                                            </div>
                                                                        ) : (
                                                                            <span className="text-muted">n/a</span>
                                                                        )}
                                                                    </td>
                                                                    <td>
                                                                        {dep.execution_stage ? (
                                                                            <span className="stage-pill">{dep.execution_stage}</span>
                                                                        ) : (
                                                                            <span className="text-muted">n/a</span>
                                                                        )}
                                                                    </td>
                                                                    <td>{dep.owner || <span className="text-muted">docker</span>}</td>
                                                                    <td>
                                                                        <strong>{deploymentLabel(dep)}</strong>
                                                                        {dep.source === 'local_deploy' ? (
                                                                            <div className="revision-details text-muted">
                                                                                <div>current: {dep.current_sha?.slice(0, 12) || 'none'}</div>
                                                                                <div>pending: {dep.pending_sha?.slice(0, 12) || 'none'}</div>
                                                                                <div>previous: {dep.previous_success_sha?.slice(0, 12) || 'none'}</div>
                                                                                <div>{dep.verification || 'pending'}{dep.elapsed ? ` · ${dep.elapsed}` : ''}</div>
                                                                            </div>
                                                                        ) : null}
                                                                        {dep.compose_service ? (
                                                                            <div className="status-detail text-muted">service: {dep.compose_service}</div>
                                                                        ) : null}
                                                                        {dep.managed === false ? (
                                                                            <div className="status-detail text-muted">{dep.image || 'unmanaged container'}</div>
                                                                        ) : null}
                                                                    </td>
                                                                    <td className="error-cell">
                                                                        {dep.last_error ? dep.last_error : <span className="text-muted">none</span>}
                                                                    </td>
                                                                    <td className="text-muted">
                                                                        {dep.managed === false ? dep.compose_files || dep.path : dep.path}
                                                                        {dep.disabled ? <div className="status-detail disabled-label">Disabled</div> : null}
                                                                    </td>
                                                                    <td>
                                                                        <div className="action-group">
                                                                            {dep.source === 'local_deploy' && dep.successful_revisions?.length ? (
                                                                                <select
                                                                                    className="rollback-select"
                                                                                    aria-label={`Rollback target for ${deploymentLabel(dep)}`}
                                                                                    value={rollbackTargets[dep.id || `${dep.owner}-${dep.repo}`] || dep.successful_revisions.find(revision => revision.sha !== dep.current_sha)?.sha || dep.successful_revisions[0].sha}
                                                                                    onChange={(event) => setRollbackTargets(current => ({
                                                                                        ...current,
                                                                                        [dep.id || `${dep.owner}-${dep.repo}`]: event.target.value,
                                                                                    }))}
                                                                                >
                                                                                    {dep.successful_revisions.map(revision => (
                                                                                        <option key={revision.sha} value={revision.sha}>
                                                                                            {revision.sha.slice(0, 12)} · {revision.verification}
                                                                                        </option>
                                                                                    ))}
                                                                                </select>
                                                                            ) : null}
                                                                            {availableActions(dep).map(action => {
                                                                                const key = dep.id || `${dep.owner}-${dep.repo}`;
                                                                                const pending = pendingActions[key] === action;
                                                                                const rollbackSHA = rollbackTargets[key] || dep.successful_revisions?.find(revision => revision.sha !== dep.current_sha)?.sha || dep.successful_revisions?.[0]?.sha || '';
                                                                                return (
                                                                                    <button
                                                                                        key={action}
                                                                                        className={`action-btn ${action}`}
                                                                                        onClick={() => handleStackAction(dep, action, action === 'rollback_deployment' ? rollbackSHA : '')}
                                                                                        disabled={pending || loading}
                                                                                    >
                                                                                        {pending ? 'Working…' : actionLabel(action)}
                                                                                    </button>
                                                                                );
                                                                            })}
                                                                            {dep.managed === false ? (
                                                                                <span className="text-muted">
                                                                                    {dep.adoption_status === 'candidate' ? 'Adopt first' : 'n/a'}
                                                                                </span>
                                                                            ) : null}
                                                                        </div>
                                                                    </td>
                                                                </tr>
                                                            ))}
                                                        </tbody>
                                                    </table>
                                                </div>
                                            ) : null}
                                        </section>
                                    );
                                })
                            )}
                        </div>
                    )}

                    {/* TAB: Logs */}
                    {activeTab === 'logs' && (
                        <div className="logs-view">
                            <div className="logs-sidebar">
                                <h3>Select Stack</h3>
                                <div className="stack-list">
                                    {deploymentGroups.map((group) => (
                                        <div className="stack-list-group" key={group.id}>
                                            <div className="stack-list-group-label">{group.name}</div>
                                            {group.deployments.map(dep => (
                                                <button
                                                    key={dep.id || `${dep.owner}-${dep.repo}`}
                                                    className={`stack-select-btn ${selectedLogStack?.id === dep.id ? 'active' : ''}`}
                                                    onClick={() => setSelectedLogStack(dep)}
                                                >
                                                    {deploymentLabel(dep)}
                                                </button>
                                            ))}
                                        </div>
                                    ))}
                                </div>
                            </div>
                            <div className="logs-terminal" ref={logContainerRef}>
                                {logs.length === 0 ? (
                                    <div className="text-muted">Waiting for logs...</div>
                                ) : (
                                    logs.map((entry) => <div key={entry.id} className="log-line">{entry.line}</div>)
                                )}
                            </div>
                        </div>
                    )}

                    {/* TAB: Agents & Runners */}
                    {activeTab === 'agents' && (
                        <div className="deployment-groups">
                            <div className="card">
                                <div className="plugin-header">
                                    <h3>Fleet agents</h3>
                                    <button className="action-btn" onClick={async () => {
                                        const pool = globalThis.prompt('Pool for this one-time enrollment token');
                                        if (!pool) return;
                                        const result = await handleCapabilityAction('/api/ui/fleet/action', 'create_enrollment_token', { pool });
                                        if (result?.token) globalThis.prompt('Copy this token now; it is shown once', result.token);
                                    }}>Create enrollment token</button>
                                </div>
                                {agents.length === 0 ? <div className="empty-state">No enrolled agents.</div> : agents.map(agent => (
                                    <div className="card" key={agent.id}>
                                        <div className="plugin-header"><h3>{agent.facts?.node_name || agent.id}</h3><span className={`status-badge ${agent.online ? 'healthy' : 'unhealthy'}`}>{agent.online ? 'ONLINE' : 'OFFLINE'}</span></div>
                                        <p className="text-muted">Pool {agent.facts?.pool} · {agent.facts?.os}/{agent.facts?.architecture} · last seen {new Date(agent.last_seen).toLocaleString()}</p>
                                        <div className="caps-list">{agent.facts?.labels?.map((label: string) => <span className="cap-badge" key={label}>{label}</span>)}</div>
                                        <button className="action-btn" disabled={!agent.online} onClick={async () => {
                                            const eligible = actionRepositories.map(repo => `${repo.owner}/${repo.repo}`);
                                            const suggested = eligible[0] || '';
                                            const target = globalThis.prompt(`Eligible private repository (owner/repo):\n${eligible.join('\n')}`, suggested);
                                            if (!target) return;
                                            const [owner, repository, ...extra] = target.split('/');
                                            if (!owner || !repository || extra.length > 0 || !eligible.some(value => value.toLowerCase() === target.toLowerCase())) {
                                                setFlashMessage('Select an eligible private topic-matched repository');
                                                return;
                                            }
                                            const name = globalThis.prompt('Runner name', `${agent.facts?.node_name || 'ghops'}-${repository}`);
                                            if (!name) return;
                                            const scopeInput = globalThis.prompt('Runner scope: repository or organization', 'repository');
                                            const scope = scopeInput?.toLowerCase() === 'organization' ? 'organization' : 'repository';
                                            const group = scope === 'organization' ? (globalThis.prompt('Existing GitHub runner group (optional)', '') || '') : '';
                                            const groupsInput = globalThis.prompt('Allowed supplementary Linux groups (comma separated)', '') || '';
                                            await handleCapabilityAction('/api/ui/github-actions/action', 'create_runner', {
                                                spec: {
                                                    agent_id: agent.id,
                                                    name,
                                                    scope,
                                                    owner,
                                                    repository,
                                                    group,
                                                    labels: agent.facts?.labels || [],
                                                    supplementary_groups: groupsInput.split(',').map(value => value.trim()).filter(Boolean),
                                                    auto_update: true,
                                                },
                                            });
                                        }}>Create runner on this agent</button>
                                        {(agent.runners || []).map((runner: any) => (
                                            <div className="config-block" key={runner.id}>
                                                <strong>{runner.name}</strong> · {runner.scope} · {runner.service} {runner.busy ? '· busy' : ''}
                                                <div className="action-group">
                                                    {['start','stop','restart','drain','update','remove'].map(operation => (
                                                        <button key={operation} className="action-btn" onClick={() => {
                                                            if (operation === 'remove' && !globalThis.confirm(`Remove runner ${runner.name}?`)) return;
                                                            handleCapabilityAction('/api/ui/github-actions/action', 'runner_action', { runner_id: runner.id, operation, force: false, busy: runner.busy });
                                                        }}>{operation}</button>
                                                    ))}
                                                </div>
                                            </div>
                                        ))}
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}

                    {/* TAB: GitHub Actions */}
                    {activeTab === 'actions' && (
                        <div className="deployment-groups">
                            {actionRepositories.length === 0 ? <div className="empty-state">No eligible Actions repositories.</div> : actionRepositories.map(repo => (
                                <section className="deployment-group" key={`${repo.owner}/${repo.repo}`}>
                                    <h3>{repo.owner}/{repo.repo}</h3>
                                    {repo.manifest_error ? <div className="flash-message">{repo.manifest_error}</div> : null}
                                    {(repo.workflows || []).map((workflow: any) => (
                                        <div className="card" key={workflow.id}>
                                            <div className="plugin-header"><strong>{workflow.name}</strong><button className="action-btn" onClick={() => {
                                                const rawInputs = globalThis.prompt('workflow_dispatch inputs as JSON', '{}');
                                                if (rawInputs === null) return;
                                                try {
                                                    const inputs = JSON.parse(rawInputs);
                                                    handleCapabilityAction('/api/ui/github-actions/action', 'dispatch_workflow', { owner: repo.owner, repo: repo.repo, workflow: workflow.id.toString(), inputs });
                                                } catch {
                                                    setFlashMessage('Workflow inputs must be a JSON object');
                                                }
                                            }}>Dispatch default branch</button></div>
                                            {(repo.runs || []).filter((run: any) => run.workflow_id === workflow.id).slice(0, 5).map((run: any) => (
                                                <div className="config-block" key={run.id}>
                                                    <span className={`status-badge ${run.conclusion || run.status}`}>{run.conclusion || run.status}</span> {run.head_branch} · {run.head_sha?.slice(0, 12)} · attempt {run.run_attempt}
                                                    <div className="action-group">
                                                        {(run.status === 'queued' || run.status === 'in_progress') ? <button className="action-btn" onClick={() => globalThis.confirm(`Cancel run ${run.id}?`) && handleCapabilityAction('/api/ui/github-actions/action', 'cancel_workflow', { owner: repo.owner, repo: repo.repo, run_id: run.id.toString() })}>Cancel</button> : null}
                                                        <button className="action-btn" onClick={() => handleCapabilityAction('/api/ui/github-actions/action', 'rerun_workflow', { owner: repo.owner, repo: repo.repo, run_id: run.id.toString() })}>Rerun</button>
                                                        <a href={run.html_url} target="_blank" rel="noreferrer">Open in GitHub</a>
                                                    </div>
                                                </div>
                                            ))}
                                        </div>
                                    ))}
                                </section>
                            ))}
                        </div>
                    )}

                    {/* TAB: Runner Setup */}
                    {activeTab === 'setup' && (
                        <div className="card grid-card">
                            <h3>Runner fleet readiness</h3>
                            <p className="text-muted">Reusable GitHub credentials remain on this controller. Agents receive only task-scoped registration or removal tokens over mTLS.</p>
                            <pre className="code-block">{JSON.stringify(setupInfo, null, 2)}</pre>
                            <p>Configure controller connections, TLS, pools, and operator groups in <code>config.yaml</code>, then create a one-time enrollment token from Agents &amp; Runners.</p>
                            <p>Repositories opt in with their configured GitHub topic and <code>.ghops/actions.yaml</code>. Persistent runners must only execute trusted private-repository workflows.</p>
                        </div>
                    )}

                    {/* TAB: Plugins */}
                    {activeTab === 'plugins' && (
                        <div className="plugins-grid">
                            {plugins.length === 0 ? (
                                <div className="empty-state">No plugins loaded.</div>
                            ) : (
                                plugins.map(plugin => (
                                    <div key={plugin.name} className="card plugin-card">
                                        <div className="plugin-header">
                                            <h3>{plugin.name}</h3>
                                            <span className={`status-badge ${plugin.status.toLowerCase()}`}>{plugin.status}</span>
                                        </div>
                                        <p className="text-muted">{plugin.description || 'No description available.'}</p>

                                        <div className="caps-list">
                                            {plugin.capabilities?.map(c => <span key={c} className="cap-badge">{c}</span>)}
                                        </div>

                                        {plugin.config && (
                                            <div className="config-block">
                                                <h4>Configuration</h4>
                                                <pre>{JSON.stringify(plugin.config, null, 2)}</pre>
                                            </div>
                                        )}
                                    </div>
                                ))
                            )}
                        </div>
                    )}
                </div>
            </main>
        </div>
    );
}

export default App;
