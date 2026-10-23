package app

import (
	"fmt"
	"net/http"
	_ "net/http/pprof" // registered on the default mux, served only when asked
	"os"
	"sort"
	"strings"
	"time"

	"github.com/AlexanderWeismannn/adroit/log"

	tea "github.com/charmbracelet/bubbletea"
)

// Profiling is off unless asked for, and asked for by environment rather than
// by flag: the point is to measure the interface as it is actually run, from
// the same shell alias and the same config, with nothing else changed.
//
//	ADROIT_PROFILE=1           log a render/update summary every profileEvery
//	ADROIT_PPROF=localhost:6060  serve net/http/pprof there as well
//
// The summary answers the two questions that matter for responsiveness -- how
// often is the interface redrawn, and what does a redraw cost -- and names the
// messages that drove the redraws, which is what says whether a tick is firing
// when nothing on screen has changed.
const (
	profileEnv   = "ADROIT_PROFILE"
	pprofEnv     = "ADROIT_PPROF"
	profileEvery = 5 * time.Second
)

// profiler accumulates one window of measurements. Single-threaded by
// construction: Update and View are both called on bubbletea's event loop.
type profiler struct {
	since      time.Time
	views      int
	viewTotal  time.Duration
	viewMax    time.Duration
	updates    int
	updTotal   time.Duration
	updMax     time.Duration
	updMaxName string
	byMsg      map[string]int
}

// newProfiler returns nil unless profiling was asked for, so the wrapper costs
// a nil check per message and nothing else when it is off.
func newProfiler() *profiler {
	if os.Getenv(profileEnv) == "" {
		return nil
	}
	if addr := os.Getenv(pprofEnv); addr != "" {
		go func() {
			if err := http.ListenAndServe(addr, nil); err != nil {
				log.WarningLog.Printf("pprof: %v", err)
			}
		}()
	}
	log.InfoLog.Printf("profiling on: a summary every %s", profileEvery)
	return &profiler{since: time.Now(), byMsg: map[string]int{}}
}

func (p *profiler) view(d time.Duration) {
	p.views++
	p.viewTotal += d
	p.viewMax = max(p.viewMax, d)
	p.flush()
}

func (p *profiler) update(msg tea.Msg, d time.Duration) {
	name := fmt.Sprintf("%T", msg)
	name = name[strings.LastIndex(name, ".")+1:]
	p.updates++
	p.updTotal += d
	if d > p.updMax {
		p.updMax, p.updMaxName = d, name
	}
	p.byMsg[name]++
}

// flush writes the window's summary once it is old enough, and starts the next.
func (p *profiler) flush() {
	elapsed := time.Since(p.since)
	if elapsed < profileEvery {
		return
	}
	log.InfoLog.Print(p.summary(elapsed))
	*p = profiler{since: time.Now(), byMsg: map[string]int{}}
}

func (p *profiler) summary(elapsed time.Duration) string {
	avg := func(total time.Duration, n int) time.Duration {
		if n == 0 {
			return 0
		}
		return total / time.Duration(n)
	}
	names := make([]string, 0, len(p.byMsg))
	for name := range p.byMsg {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if p.byMsg[names[i]] != p.byMsg[names[j]] {
			return p.byMsg[names[i]] > p.byMsg[names[j]]
		}
		return names[i] < names[j]
	})
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s %d", name, p.byMsg[name]))
	}
	secs := elapsed.Seconds()
	return fmt.Sprintf("profile %.1fs: view %d (%.1f/s, avg %s, max %s, %.1f%% of wall) · update %d (avg %s, max %s in %s) · %s",
		secs, p.views, float64(p.views)/secs, avg(p.viewTotal, p.views).Round(10*time.Microsecond),
		p.viewMax.Round(10*time.Microsecond), 100*p.viewTotal.Seconds()/secs,
		p.updates, avg(p.updTotal, p.updates).Round(10*time.Microsecond),
		p.updMax.Round(10*time.Microsecond), p.updMaxName, strings.Join(parts, ", "))
}

// profiled wraps the model so the timing sits outside it: home's Update and
// View stay exactly what they are, and the wrapper is not built at all when
// profiling is off.
type profiled struct {
	inner tea.Model
	p     *profiler
}

func (m profiled) Init() tea.Cmd { return m.inner.Init() }

func (m profiled) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	start := time.Now()
	inner, cmd := m.inner.Update(msg)
	m.p.update(msg, time.Since(start))
	m.inner = inner
	return m, cmd
}

func (m profiled) View() string {
	start := time.Now()
	s := m.inner.View()
	m.p.view(time.Since(start))
	return s
}

// maybeProfiled returns the model to run: the one given, or it under the
// profiler when the environment asks for one.
func maybeProfiled(model tea.Model) tea.Model {
	p := newProfiler()
	if p == nil {
		return model
	}
	return profiled{inner: model, p: p}
}
