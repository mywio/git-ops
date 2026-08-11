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
    PlayCircle
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

    const kindOrder: Record<string, number> = { 'git-ops': 0, compose: 1, docker: 2 };
    return Array.from(groups.values()).sort((a, b) => {
        const kindDifference = (kindOrder[a.kind] ?? 99) - (kindOrder[b.kind] ?? 99);
        return kindDifference || a.name.localeCompare(b.name);
    });
}

function groupKindLabel(kind: string) {
    switch (kind) {
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
    const [loading, setLoading] = useState(false);
    const [pendingActions, setPendingActions] = useState<Record<string, string>>({});
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
            } else if (activeTab === 'plugins') {
                const res = await fetch('/api/plugins?include_config=true');
                setPlugins(await res.json());
            }
        } catch (e) {
            console.error("Failed to fetch data:", e);
        }
        setLoading(false);
    };

    const handleStackAction = async (dep: Deployment, action: string) => {
        const key = dep.id || `${dep.owner}-${dep.repo}`;
        setPendingActions(prev => ({ ...prev, [key]: action }));
        setFlashMessage('');
        try {
            const res = await fetch('/api/ui/stacks/action', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ owner: dep.owner, repo: dep.repo, action }),
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
                                                                            {availableActions(dep).map(action => {
                                                                                const key = dep.id || `${dep.owner}-${dep.repo}`;
                                                                                const pending = pendingActions[key] === action;
                                                                                return (
                                                                                    <button
                                                                                        key={action}
                                                                                        className={`action-btn ${action}`}
                                                                                        onClick={() => handleStackAction(dep, action)}
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
