import { useState, useEffect, useRef } from 'react';
import {
    Activity,
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
    owner: string;
    repo: string;
    path: string;
    status: string;
    container?: string;
    image?: string;
    docker_status?: string;
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

/* --- App Component --- */
function App() {
    const [activeTab, setActiveTab] = useState(() => tabFromPath(window.location.pathname));
    const [systemInfo, setSystemInfo] = useState<any>(null);
    const [deployments, setDeployments] = useState<Deployment[]>([]);
    const [plugins, setPlugins] = useState<PluginInfo[]>([]);
    const [loading, setLoading] = useState(false);

    // Logs state
    const [selectedLogStack, setSelectedLogStack] = useState<Deployment | null>(null);
    const [logs, setLogs] = useState<string[]>([]);
    const logContainerRef = useRef<HTMLDivElement>(null);
    const evtSource = useRef<EventSource | null>(null);

    useEffect(() => {
        fetchData();
    }, [activeTab]);

    useEffect(() => {
        const handlePopState = () => {
            setActiveTab(tabFromPath(window.location.pathname));
        };

        window.addEventListener('popstate', handlePopState);
        return () => window.removeEventListener('popstate', handlePopState);
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

    // Log Streamer
    useEffect(() => {
        if (activeTab === 'logs' && selectedLogStack) {
            if (evtSource.current) {
                evtSource.current.close();
            }
            setLogs([]);

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
                const lines = event.data.split('\\n');
                setLogs(prev => [...prev, ...lines]);

                // Auto scroll
                setTimeout(() => {
                    if (logContainerRef.current) {
                        logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
                    }
                }, 50);
            };

            sse.onerror = () => {
                setLogs(prev => [...prev, "--- Log stream disconnected ---"]);
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
        if (window.location.pathname !== nextPath) {
            window.history.pushState({}, '', nextPath);
        }
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
                        <div className="table-container">
                            {deployments.length === 0 ? (
                                <div className="empty-state">No deployments found.</div>
                            ) : (
                                <table className="stacks-table">
                                    <thead>
                                        <tr>
                                            <th>Runtime</th>
                                            <th>Execution</th>
                                            <th>Stage</th>
                                            <th>Owner</th>
                                            <th>Repository</th>
                                            <th>Last Error</th>
                                            <th>Path</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {deployments.map(dep => (
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
                                                    {dep.managed === false ? (
                                                        <div className="status-detail text-muted">{dep.image || 'unmanaged container'}</div>
                                                    ) : null}
                                                </td>
                                                <td className="error-cell">
                                                    {dep.last_error ? dep.last_error : <span className="text-muted">none</span>}
                                                </td>
                                                <td className="text-muted">{dep.path}</td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                            )}
                        </div>
                    )}

                    {/* TAB: Logs */}
                    {activeTab === 'logs' && (
                        <div className="logs-view">
                            <div className="logs-sidebar">
                                <h3>Select Stack</h3>
                                <div className="stack-list">
                                    {deployments.map(dep => (
                                        <button
                                            key={dep.id || `${dep.owner}-${dep.repo}`}
                                            className={`stack-select-btn ${selectedLogStack?.id === dep.id ? 'active' : ''}`}
                                            onClick={() => setSelectedLogStack(dep)}
                                        >
                                            {deploymentLabel(dep)}
                                        </button>
                                    ))}
                                </div>
                            </div>
                            <div className="logs-terminal" ref={logContainerRef}>
                                {logs.length === 0 ? (
                                    <div className="text-muted">Waiting for logs...</div>
                                ) : (
                                    logs.map((line, i) => <div key={i} className="log-line">{line}</div>)
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
