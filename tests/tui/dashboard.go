package tui

import (
	"fmt"
	"sort"
	"time"

	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
	"github.com/pior/memcache/tests/metrics"
)

const (
	refreshRate = 200 * time.Millisecond
)

// Dashboard manages the TUI display
type Dashboard struct {
	collector *metrics.Collector

	// Header
	header *widgets.Paragraph

	// Scenario status
	scenarioBox  *widgets.Paragraph
	scenarioName string
	scenarioDesc string

	// Workload metrics
	opsChart        *widgets.Plot
	errorChart      *widgets.Plot
	throughputGauge *widgets.Gauge

	// Server pool stats
	poolTable *widgets.Table

	// Logs (scenario messages and circuit breaker events)
	logsList              *widgets.List
	logs                  []string
	lastCircuitChangeIdx  int

	// Historical data
	opsHistory        []float64
	errorHistory      []float64
	maxDataPoints     int // Calculated based on chart width to fill the panel
	lastSnapshotIndex int
	lastTotalOps      int64
	lastFailedOps     int64
	lastTimestamp     time.Time
	startTime         time.Time
	currentOpsPerSec  float64
	currentErrorRate  float64

	// Scenario control
	scenarioNames    []string
	scenarioSwitchCh chan string
}

// NewDashboard creates a new TUI dashboard
func NewDashboard(collector *metrics.Collector) *Dashboard {
	return &Dashboard{
		collector:        collector,
		opsHistory:       make([]float64, 0, 100), // Initial capacity, will be adjusted in layout()
		errorHistory:     make([]float64, 0, 100),
		maxDataPoints:    30, // Default, will be recalculated in layout()
		startTime:        time.Now(),
		scenarioSwitchCh: make(chan string, 1),
	}
}

// SetAvailableScenarios sets the list of available scenarios for keyboard navigation
func (d *Dashboard) SetAvailableScenarios(names []string) {
	d.scenarioNames = names
}

// GetScenarioSwitchChannel returns the channel for scenario switch events
func (d *Dashboard) GetScenarioSwitchChannel() <-chan string {
	return d.scenarioSwitchCh
}

// Init initializes the dashboard widgets
func (d *Dashboard) Init() error {
	if err := ui.Init(); err != nil {
		return fmt.Errorf("failed to initialize termui: %w", err)
	}

	// Header
	d.header = widgets.NewParagraph()
	d.header.Title = "Memcache Reliability Test"
	if len(d.scenarioNames) > 0 {
		d.header.Text = "Press 'q' to quit | 'n' next scenario"
	} else {
		d.header.Text = "Press 'q' to quit"
	}
	d.header.BorderStyle.Fg = ui.ColorCyan
	d.header.TitleStyle.Fg = ui.ColorWhite
	d.header.TitleStyle.Modifier = ui.ModifierBold

	// Scenario status
	d.scenarioBox = widgets.NewParagraph()
	d.scenarioBox.Title = "Scenario Status"
	// Use pre-set scenario if SetScenario was called before Init
	if d.scenarioName != "" {
		d.scenarioBox.Text = fmt.Sprintf("[%s]\n%s", d.scenarioName, d.scenarioDesc)
		d.scenarioBox.BorderStyle.Fg = ui.ColorYellow
	} else if d.scenarioDesc != "" {
		// Description set but no name - used for "Scenario complete"
		d.scenarioBox.Text = d.scenarioDesc
		d.scenarioBox.BorderStyle.Fg = ui.ColorWhite
	} else {
		d.scenarioBox.Text = "No active scenario"
		d.scenarioBox.BorderStyle.Fg = ui.ColorWhite
	}
	d.scenarioBox.TextStyle.Fg = ui.ColorWhite

	// Operations/sec plot
	d.opsChart = widgets.NewPlot()
	d.opsChart.Title = "Operations/sec (thousands)"
	d.opsChart.Data = make([][]float64, 1)
	d.opsChart.Data[0] = []float64{0, 0} // Need at least 2 points
	d.opsChart.LineColors[0] = ui.ColorGreen
	d.opsChart.AxesColor = ui.ColorWhite
	d.opsChart.BorderStyle.Fg = ui.ColorGreen
	d.opsChart.Marker = widgets.MarkerBraille
	d.opsChart.HorizontalScale = 1000 // Very high value to effectively hide X-axis labels

	// Error rate plot
	d.errorChart = widgets.NewPlot()
	d.errorChart.Title = "Error Rate %"
	d.errorChart.Data = make([][]float64, 1)
	d.errorChart.Data[0] = []float64{0, 0} // Need at least 2 points
	d.errorChart.LineColors[0] = ui.ColorRed
	d.errorChart.AxesColor = ui.ColorWhite
	d.errorChart.BorderStyle.Fg = ui.ColorYellow
	d.errorChart.Marker = widgets.MarkerBraille
	d.errorChart.HorizontalScale = 1000 // Very high value to effectively hide X-axis labels

	// Throughput gauge
	d.throughputGauge = widgets.NewGauge()
	d.throughputGauge.Title = "Throughput"
	d.throughputGauge.Percent = 0
	d.throughputGauge.BarColor = ui.ColorClear
	d.throughputGauge.BorderStyle.Fg = ui.ColorCyan
	d.throughputGauge.LabelStyle.Fg = ui.ColorWhite

	// Pool stats table
	d.poolTable = widgets.NewTable()
	d.poolTable.Title = "Server Pool Status"
	d.poolTable.Rows = [][]string{
		{"Server", "Circuit", "Conns", "Active", "Idle", "Consec", "Total"},
	}
	d.poolTable.TextStyle = ui.NewStyle(ui.ColorWhite)
	d.poolTable.RowSeparator = false
	d.poolTable.BorderStyle.Fg = ui.ColorMagenta
	d.poolTable.TextAlignment = ui.AlignLeft
	d.poolTable.RowStyles[0] = ui.NewStyle(ui.ColorWhite, ui.ColorClear, ui.ModifierBold)

	// Logs
	d.logsList = widgets.NewList()
	d.logsList.Title = "Logs"
	d.logsList.Rows = []string{"Waiting for events..."}
	d.logsList.TextStyle = ui.NewStyle(ui.ColorWhite)
	d.logsList.BorderStyle.Fg = ui.ColorCyan

	d.layout()

	return nil
}

// layout arranges widgets on screen
func (d *Dashboard) layout() {
	termWidth, termHeight := ui.TerminalDimensions()

	// Calculate max data points to fill the chart width
	// Chart width is termWidth/2, minus borders (2 chars) and Y-axis space (~8 chars)
	chartWidth := termWidth / 2
	d.maxDataPoints = chartWidth - 10
	if d.maxDataPoints < 10 {
		d.maxDataPoints = 10 // Minimum to ensure plot works
	}

	// Header at top (3 rows)
	d.header.SetRect(0, 0, termWidth, 3)

	// Scenario status (3 rows) - show if there's scenario info or status
	scenarioHeight := 0
	if d.scenarioName != "" || d.scenarioDesc != "" {
		scenarioHeight = 3
		d.scenarioBox.SetRect(0, 3, termWidth, 6)
	}

	// Charts in upper area (10 rows each)
	chartTop := 3 + scenarioHeight
	d.opsChart.SetRect(0, chartTop, termWidth/2, chartTop+10)
	d.errorChart.SetRect(termWidth/2, chartTop, termWidth, chartTop+10)

	// Gauge below charts
	gaugeTop := chartTop + 10
	d.throughputGauge.SetRect(0, gaugeTop, termWidth, gaugeTop+3)

	// Pool table in middle
	tableTop := gaugeTop + 3
	tableHeight := 10
	d.poolTable.SetRect(0, tableTop, termWidth, tableTop+tableHeight)

	// Logs at bottom
	d.logsList.SetRect(0, tableTop+tableHeight, termWidth, termHeight)
}

// Update refreshes the dashboard with latest data
func (d *Dashboard) Update() {
	snapshots := d.collector.GetSnapshots()
	if len(snapshots) == 0 {
		return
	}

	// Only update if we have new snapshot data
	if len(snapshots) <= d.lastSnapshotIndex {
		// No new data, just re-render with current values
		return
	}

	latest := snapshots[len(snapshots)-1]
	d.lastSnapshotIndex = len(snapshots)

	// Calculate instantaneous metrics since last data point
	// Only update history if we have a previous data point to calculate from
	if d.lastTotalOps > 0 && !d.lastTimestamp.IsZero() {
		duration := latest.Timestamp.Sub(d.lastTimestamp).Seconds()
		if duration > 0 {
			opsInInterval := latest.WorkloadStats.TotalOps - d.lastTotalOps
			failedInInterval := latest.WorkloadStats.FailedOps - d.lastFailedOps

			// Instantaneous ops/sec
			d.currentOpsPerSec = float64(opsInInterval) / duration

			// Instantaneous error rate
			if opsInInterval > 0 {
				d.currentErrorRate = float64(failedInInterval) / float64(opsInInterval)
			} else {
				d.currentErrorRate = 0
			}

			// Update historical data (scale ops/sec to thousands for cleaner Y-axis)
			d.opsHistory = append(d.opsHistory, d.currentOpsPerSec/1000)
			if len(d.opsHistory) > d.maxDataPoints {
				d.opsHistory = d.opsHistory[1:]
			}

			// Store instantaneous error rate as percentage
			d.errorHistory = append(d.errorHistory, d.currentErrorRate*100)
			if len(d.errorHistory) > d.maxDataPoints {
				d.errorHistory = d.errorHistory[1:]
			}
		}
	}

	d.lastTotalOps = latest.WorkloadStats.TotalOps
	d.lastFailedOps = latest.WorkloadStats.FailedOps
	d.lastTimestamp = latest.Timestamp

	// Update charts (ensure we have at least 2 points for Plot to render)
	// Make a copy of the data to avoid issues with the Plot widget holding stale references
	if len(d.opsHistory) >= 2 {
		d.opsChart.Data[0] = append([]float64{}, d.opsHistory...)
		d.opsChart.Title = fmt.Sprintf("Operations/sec (thousands) - current: %.1fk", d.currentOpsPerSec/1000)
	}

	if len(d.errorHistory) >= 2 {
		d.errorChart.Data[0] = append([]float64{}, d.errorHistory...)
		d.errorChart.Title = fmt.Sprintf("Error Rate %% (current: %.2f%%)", d.currentErrorRate*100)
	}

	// Update gauge (cap at 10k for display)
	gaugePercent := int((d.currentOpsPerSec / 10000) * 100)
	if gaugePercent > 100 {
		gaugePercent = 100
	}
	d.throughputGauge.Percent = gaugePercent
	d.throughputGauge.Label = fmt.Sprintf("%.0f ops/sec | Total: %d | Success: %d | Failed: %d",
		d.currentOpsPerSec,
		latest.WorkloadStats.TotalOps,
		latest.WorkloadStats.SuccessOps,
		latest.WorkloadStats.FailedOps,
	)

	// Update pool table
	rows := [][]string{
		{"Server", "Circuit", "Conns", "Active", "Idle", "Consec", "Total"},
	}

	// Sort pool stats by server address for consistent ordering
	poolStats := make([]metrics.PoolSnapshot, len(latest.PoolStats))
	copy(poolStats, latest.PoolStats)
	sort.Slice(poolStats, func(i, j int) bool {
		return poolStats[i].ServerAddr < poolStats[j].ServerAddr
	})

	for _, pool := range poolStats {
		// Color-code circuit state
		circuitState := pool.CircuitBreakerState
		if circuitState == "" {
			circuitState = "none"
		}

		rows = append(rows, []string{
			pool.ServerAddr,
			circuitState,
			fmt.Sprintf("%d", pool.TotalConns),
			fmt.Sprintf("%d", pool.ActiveConns),
			fmt.Sprintf("%d", pool.IdleConns),
			fmt.Sprintf("%d", pool.ConsecutiveFailures),
			fmt.Sprintf("%d", pool.TotalFailures),
		})
	}
	d.poolTable.Rows = rows

	// Update logs with new circuit breaker changes
	changes := d.collector.GetCircuitChanges()
	if len(changes) > d.lastCircuitChangeIdx {
		// Add new circuit breaker events to logs
		for _, change := range changes[d.lastCircuitChangeIdx:] {
			logMsg := fmt.Sprintf("[%s] Circuit: %s: %s → %s",
				change.Timestamp.Format("15:04:05"),
				change.ServerAddr,
				change.OldState,
				change.NewState,
			)
			d.logs = append(d.logs, logMsg)
		}
		d.lastCircuitChangeIdx = len(changes)
	}

	// Update logs list widget (show last 20 entries)
	if len(d.logs) > 0 {
		startIdx := 0
		if len(d.logs) > 20 {
			startIdx = len(d.logs) - 20
		}
		d.logsList.Rows = d.logs[startIdx:]
	}

	// Update header with runtime info
	runtime := time.Since(d.startTime).Round(time.Second)
	if len(d.scenarioNames) > 0 {
		d.header.Text = fmt.Sprintf("Runtime: %s | Press 'q' to quit | 'n' next scenario", runtime)
	} else {
		d.header.Text = fmt.Sprintf("Runtime: %s | Press 'q' to quit", runtime)
	}
}

// Render draws the dashboard
func (d *Dashboard) Render() {
	widgets := []ui.Drawable{
		d.header,
	}

	// Add scenario box if there's scenario info or status message
	if d.scenarioName != "" || d.scenarioDesc != "" {
		widgets = append(widgets, d.scenarioBox)
	}

	widgets = append(widgets,
		d.opsChart,
		d.errorChart,
		d.throughputGauge,
		d.poolTable,
		d.logsList,
	)

	ui.Render(widgets...)
}

// SetScenario updates the active scenario status
func (d *Dashboard) SetScenario(name, description string) {
	d.scenarioName = name
	d.scenarioDesc = description

	// Only update widget if it's been initialized
	if d.scenarioBox != nil {
		if name == "" && description == "" {
			d.scenarioBox.Text = "No active scenario"
			d.scenarioBox.BorderStyle.Fg = ui.ColorWhite
		} else if name == "" {
			// Description without name - status message like "Scenario complete"
			d.scenarioBox.Text = description
			d.scenarioBox.BorderStyle.Fg = ui.ColorWhite
		} else {
			d.scenarioBox.Text = fmt.Sprintf("[%s]\n%s", name, description)
			d.scenarioBox.BorderStyle.Fg = ui.ColorYellow
		}

		d.layout() // Recalculate layout when scenario status changes
	}
}

// AddLog appends a message to the logs panel
func (d *Dashboard) AddLog(message string) {
	timestamp := time.Now().Format("15:04:05")
	logMsg := fmt.Sprintf("[%s] %s", timestamp, message)
	d.logs = append(d.logs, logMsg)
}

// Close cleans up the dashboard
func (d *Dashboard) Close() {
	ui.Close()
}

// Run starts the dashboard event loop
func (d *Dashboard) Run(done <-chan struct{}) error {
	if err := d.Init(); err != nil {
		return err
	}
	defer d.Close()

	// Handle terminal resize
	uiEvents := ui.PollEvents()
	ticker := time.NewTicker(refreshRate)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return nil
		case e := <-uiEvents:
			switch e.ID {
			case "q", "<C-c>":
				return nil
			case "n":
				// Switch to next scenario
				if len(d.scenarioNames) > 0 {
					currentIdx := -1
					for i, name := range d.scenarioNames {
						if name == d.scenarioName {
							currentIdx = i
							break
						}
					}
					nextIdx := (currentIdx + 1) % len(d.scenarioNames)
					nextScenario := d.scenarioNames[nextIdx]

					// Send switch signal (non-blocking)
					select {
					case d.scenarioSwitchCh <- nextScenario:
						d.AddLog(fmt.Sprintf("Switching to scenario: %s", nextScenario))
					default:
						d.AddLog("Cannot switch scenario right now")
					}
				}
			case "<Resize>":
				payload := e.Payload.(ui.Resize)
				d.layout()
				ui.Clear()
				d.Render()
				_ = payload // Suppress unused warning
			}
		case <-ticker.C:
			d.Update()
			d.Render()
		}
	}
}
