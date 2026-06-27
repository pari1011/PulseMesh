import { useState, useEffect } from 'react'
import './index.css'

function App() {
  const [metrics, setMetrics] = useState([])
  const [algorithm, setAlgorithm] = useState("least_connections")
  const [newServerUrl, setNewServerUrl] = useState('')
  const [newServerWeight, setNewServerWeight] = useState(1)

  useEffect(() => {
    const fetchMetrics = async () => {
      try {
        const response = await fetch('http://localhost:9001/api/metrics')
        if (response.ok) {
          const data = await response.json()
          setMetrics(data.backends || [])
          if (data.algorithm) {
            setAlgorithm(data.algorithm)
          }
        }
      } catch (error) {
        console.error("Error fetching metrics:", error)
      }
    }

    fetchMetrics()
    const interval = setInterval(fetchMetrics, 2000)
    return () => clearInterval(interval)
  }, [])

  const handleAddServer = async (e) => {
    e.preventDefault()
    if (!newServerUrl) return
    try {
      await fetch(`http://localhost:9001/api/add?url=${encodeURIComponent(newServerUrl)}&weight=${newServerWeight}`, { method: 'POST' })
      setNewServerUrl('')
      setNewServerWeight(1)
    } catch (error) {
      console.error(error)
    }
  }

  const handleRemove = async (url) => {
    try {
      await fetch(`http://localhost:9001/api/remove?url=${encodeURIComponent(url)}`, { method: 'POST' })
    } catch (error) {
      console.error(error)
    }
  }

  const handleStatusChange = async (url, status) => {
    try {
      await fetch(`http://localhost:9001/api/status?url=${encodeURIComponent(url)}&status=${status}`, { method: 'POST' })
    } catch (error) {
      console.error(error)
    }
  }

  const handleAlgorithmChange = async (e) => {
    const algo = e.target.value
    setAlgorithm(algo)
    try {
      await fetch(`http://localhost:9001/api/algorithm?algo=${algo}`, { method: 'POST' })
    } catch (error) {
      console.error(error)
    }
  }

  return (
    <div className="dashboard-container">
      <header className="header">
        <div>
          <h1 className="title">PulseMesh Admin</h1>
          <p className="subtitle">Enterprise Traffic Management</p>
        </div>
        <div className="controls">
          <label>Routing Algorithm: </label>
          <select value={algorithm} onChange={handleAlgorithmChange} className="algo-select">
            <option value="least_connections">Least Connections</option>
            <option value="round_robin">Round Robin</option>
            <option value="weighted_round_robin">Weighted Round Robin</option>
          </select>
        </div>
      </header>

      <form className="add-server-form" onSubmit={handleAddServer}>
        <input 
          type="text" 
          className="input-field"
          placeholder="Server URL (e.g., http://localhost:8084)"
          value={newServerUrl}
          onChange={(e) => setNewServerUrl(e.target.value)}
        />
        <input 
          type="number" 
          className="input-field weight-input"
          min="1"
          placeholder="Weight"
          title="Weight (for WRR algorithm)"
          value={newServerWeight}
          onChange={(e) => setNewServerWeight(parseInt(e.target.value) || 1)}
        />
        <button type="submit" className="btn-primary">Add Server</button>
      </form>

      <div className="grid">
        {metrics.length === 0 ? (
          <p style={{ color: '#94a3b8' }}>Waiting for servers...</p>
        ) : (
          metrics.map((server, index) => (
            <div className={`card ${!server.is_routable ? 'card-disabled' : ''}`} key={index}>
              <div className="server-header">
                <div>
                  <h2 className="server-url">{server.url}</h2>
                  <span className={`status-badge ${server.is_routable ? 'status-up' : 'status-down'}`}>
                    {server.is_routable ? 'ROUTABLE' : 'OFFLINE'}
                  </span>
                </div>
                <button className="btn-remove" onClick={() => handleRemove(server.url)} title="Remove Server">
                  ✕
                </button>
              </div>
              
              <div className="metric-row" style={{marginTop: '1rem', marginBottom: '1rem'}}>
                <span className="metric-label">Admin Override:</span>
                <select 
                  className="status-select"
                  value={server.admin_status}
                  onChange={(e) => handleStatusChange(server.url, e.target.value)}
                >
                  <option value="Auto">Auto (Health Checks)</option>
                  <option value="ForceOnline">Force Online</option>
                  <option value="ForceOffline">Force Offline</option>
                </select>
              </div>

              <div className="metric-row">
                <span className="metric-label">Server Weight</span>
                <span className="metric-value">{server.weight}</span>
              </div>
              <div className="metric-row">
                <span className="metric-label">Active Connections</span>
                <span className="metric-value">{server.active_connections}</span>
              </div>
              <div className="metric-row">
                <span className="metric-label">Total Requests</span>
                <span className="metric-value">{server.total_requests}</span>
              </div>
              <div className="metric-row">
                <span className="metric-label">Avg Response Time</span>
                <span className="metric-value">{server.avg_response_time_ms.toFixed(1)} ms</span>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  )
}

export default App
