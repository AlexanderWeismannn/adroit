package overlay

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/secrets"
	"github.com/AlexanderWeismannn/adroit/theme"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Settings is the in-app editor for what used to need a hand-edited
// config.json: agent profiles and their API keys, per-repository settings, and
// the global switches.
//
// It edits a copy of the config and hands every change to Save as the
// top-level keys that changed, so the caller writes them with
// config.UpdateConfigFile (which keeps anything in the file this does not know
// about) and refreshes its own copy. Keys go to the secrets store, never into
// the config.
type Settings struct {
	cfg      *config.Config
	store    secrets.Store
	repoPath string
	save     func(changes map[string]any) error

	tab    int
	cursor [3]int

	// mode is what the keys are doing right now.
	mode         settingsMode
	input        textinput.Model
	inputFor     string
	onSubmit     func(string)
	choices      []string
	choiceFor    string
	onChoose     func(int)
	chooseCursor int

	// repo is the workspace being edited in the Workspaces tab, "" for the list.
	repo string

	// armed is the row a destructive key was pressed on once; the second press
	// does it.
	armed string

	status  string
	isError bool
	// checks holds the last key-check result per profile.
	checks map[string]string

	width int
}

type settingsMode int

const (
	modeBrowse settingsMode = iota
	modeInput
	modeChoose
)

const (
	tabAgents = iota
	tabWorkspaces
	tabGeneral
)

var settingsTabs = []string{"Agents", "Workspaces", "General"}

// SettingsCheckMsg carries the result of a key check back to the overlay.
type SettingsCheckMsg struct {
	Profile string
	OK      bool
	Detail  string
}

// SettingsCheck runs a profile's check command. Supplied by the caller so the
// overlay never shells out itself.
type SettingsCheck func(profile config.Profile, command string) tea.Cmd

// NewSettings opens the editor on a copy of cfg.
func NewSettings(cfg *config.Config, store secrets.Store, repoPath string,
	save func(map[string]any) error) *Settings {
	copied := *cfg
	copied.Profiles = append([]config.Profile(nil), cfg.Profiles...)
	copied.Repos = map[string]*config.RepoConfig{}
	for k, v := range cfg.Repos {
		if v != nil {
			r := *v
			copied.Repos[k] = &r
		}
	}
	s := &Settings{cfg: &copied, store: store, repoPath: repoPath, save: save, checks: map[string]string{}}
	if len(s.cfg.Profiles) == 0 {
		// No profiles yet: show the agent Adroit actually runs as one, so the
		// tab opens on something to give a key to. It is written only when
		// changed.
		s.cfg.Profiles = []config.Profile{profileFromProgram(cfg.GetProgram())}
	}
	return s
}

func profileFromProgram(program string) config.Profile {
	name := "agent"
	if f := strings.Fields(program); len(f) > 0 {
		name = filepath.Base(f[0])
	}
	p := config.Profile{Name: name, Program: program}
	if preset := config.PresetFor(program); preset != nil {
		p.Name = preset.Name
		p.Keys = append([]string(nil), preset.Keys...)
	}
	return p
}

// SetWidth bounds the overlay to the terminal.
func (s *Settings) SetWidth(w int) { s.width = w }

// Config is the edited configuration, for the caller to adopt after a save.
func (s *Settings) Config() *config.Config { return s.cfg }

// HandleCheck records a key check's result.
func (s *Settings) HandleCheck(msg SettingsCheckMsg) {
	if msg.OK {
		s.checks[msg.Profile] = "✓ works"
	} else {
		s.checks[msg.Profile] = "✗ " + msg.Detail
	}
}

// HandleKeyPress returns a command to run, and whether the overlay closed.
func (s *Settings) HandleKeyPress(msg tea.KeyMsg, check SettingsCheck) (tea.Cmd, bool) {
	switch s.mode {
	case modeInput:
		return s.handleInput(msg), false
	case modeChoose:
		s.handleChoose(msg)
		return nil, false
	}

	key := msg.String()
	if key != "x" {
		s.armed = ""
	}
	switch key {
	case "esc", "q":
		if s.tab == tabWorkspaces && s.repo != "" {
			s.repo = ""
			return nil, false
		}
		return nil, true
	case "tab", "right", "l":
		s.tab = (s.tab + 1) % len(settingsTabs)
		s.repo, s.status = "", ""
		return nil, false
	case "shift+tab", "left", "h":
		s.tab = (s.tab + len(settingsTabs) - 1) % len(settingsTabs)
		s.repo, s.status = "", ""
		return nil, false
	case "up", "k":
		if s.cursor[s.tab] > 0 {
			s.cursor[s.tab]--
		}
		return nil, false
	case "down", "j":
		if s.cursor[s.tab] < s.rowCount()-1 {
			s.cursor[s.tab]++
		}
		return nil, false
	}

	switch s.tab {
	case tabAgents:
		return s.agentsKey(key, check), false
	case tabWorkspaces:
		s.workspacesKey(key)
	case tabGeneral:
		s.generalKey(key)
	}
	return nil, false
}

func (s *Settings) rowCount() int {
	switch s.tab {
	case tabAgents:
		return len(s.cfg.Profiles) + 1 // + "add an agent"
	case tabWorkspaces:
		if s.repo != "" {
			return len(s.workspaceFields(s.repo))
		}
		return len(s.workspaceList())
	default:
		return len(s.generalFields())
	}
}

// ---------- agents ----------

func (s *Settings) agentsKey(key string, check SettingsCheck) tea.Cmd {
	i := s.cursor[tabAgents]
	if i >= len(s.cfg.Profiles) { // the "add" row
		if key == "enter" || key == "a" {
			s.chooseNewAgent()
		}
		return nil
	}
	p := &s.cfg.Profiles[i]
	switch key {
	case "a":
		s.chooseNewAgent()
	case "enter", "e":
		s.ask("Command for "+p.Name, p.Program, false, func(v string) {
			v = strings.TrimSpace(v)
			if v == "" {
				s.fail("the command cannot be empty")
				return
			}
			p.Program = v
			s.commitProfiles("saved " + p.Name)
		})
	case "K", "s":
		s.setKey(p)
	case "d", " ":
		s.cfg.DefaultProgram = p.Name
		s.commit(map[string]any{"default_program": p.Name, "profiles": s.cfg.Profiles}, p.Name+" is the default agent")
	case "x":
		if s.armed != p.Name {
			s.armed = p.Name
			s.note(fmt.Sprintf("press x again to remove %s (its stored keys are deleted too)", p.Name))
			return nil
		}
		s.armed = ""
		name := p.Name
		for _, k := range p.Keys {
			_ = s.store.Delete(secrets.Key(name, k))
		}
		s.cfg.Profiles = append(s.cfg.Profiles[:i], s.cfg.Profiles[i+1:]...)
		if s.cursor[tabAgents] > 0 && s.cursor[tabAgents] >= len(s.cfg.Profiles) {
			s.cursor[tabAgents]--
		}
		s.commitProfiles("removed " + name)
	case "t":
		preset := config.PresetFor(p.Program)
		if preset == nil || preset.Check == "" {
			s.fail("no check is known for " + p.Name + "; start a session to try it")
			return nil
		}
		if check == nil {
			return nil
		}
		s.checks[p.Name] = "checking…"
		return check(*p, preset.Check)
	}
	return nil
}

func (s *Settings) chooseNewAgent() {
	names := make([]string, 0, len(config.AgentPresets)+1)
	for _, p := range config.AgentPresets {
		names = append(names, fmt.Sprintf("%-8s %s", p.Name, strings.Join(p.Keys, ", ")))
	}
	names = append(names, "other    a command of your own")
	s.choose("Add an agent", names, func(i int) {
		if i == len(config.AgentPresets) {
			s.ask("Command to run", "", false, func(v string) {
				v = strings.TrimSpace(v)
				if v == "" {
					return
				}
				p := profileFromProgram(v)
				p.Name = s.uniqueName(p.Name)
				s.cfg.Profiles = append(s.cfg.Profiles, p)
				s.cursor[tabAgents] = len(s.cfg.Profiles) - 1
				s.commitProfiles("added " + p.Name)
			})
			return
		}
		preset := config.AgentPresets[i]
		p := config.Profile{Name: s.uniqueName(preset.Name), Program: preset.Program,
			Keys: append([]string(nil), preset.Keys...)}
		s.cfg.Profiles = append(s.cfg.Profiles, p)
		s.cursor[tabAgents] = len(s.cfg.Profiles) - 1
		s.commitProfiles(fmt.Sprintf("added %s; press s to store its key. Not installed? %s", p.Name, preset.Install))
	})
}

func (s *Settings) uniqueName(base string) string {
	taken := map[string]bool{}
	for _, p := range s.cfg.Profiles {
		taken[p.Name] = true
	}
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		if c := fmt.Sprintf("%s-%d", base, n); !taken[c] {
			return c
		}
	}
}

func (s *Settings) setKey(p *config.Profile) {
	keys := p.Keys
	if len(keys) == 0 {
		// An agent with no known key variable: ask which one it reads.
		s.ask("Environment variable "+p.Name+" reads its key from (e.g. OPENAI_API_KEY)", "", false, func(v string) {
			v = strings.ToUpper(strings.TrimSpace(v))
			if v == "" {
				return
			}
			p.Keys = []string{v}
			s.commitProfiles("")
			s.setKey(p)
		})
		return
	}
	enter := func(envVar string) {
		s.ask(fmt.Sprintf("%s for %s (stored in %s; empty removes it)", envVar, p.Name, s.store.Backend()),
			"", true, func(v string) {
				v = strings.TrimSpace(v)
				name := secrets.Key(p.Name, envVar)
				if v == "" {
					_ = s.store.Delete(name)
					s.note("removed " + envVar + " for " + p.Name)
					return
				}
				if err := s.store.Set(name, v); err != nil {
					s.fail("could not store the key: " + err.Error())
					return
				}
				delete(s.checks, p.Name)
				s.note(fmt.Sprintf("stored %s for %s in %s", envVar, p.Name, s.store.Backend()))
			})
	}
	if len(keys) == 1 {
		enter(keys[0])
		return
	}
	s.choose("Which key?", keys, func(i int) { enter(keys[i]) })
}

func (s *Settings) commitProfiles(msg string) {
	s.commit(map[string]any{"profiles": s.cfg.Profiles, "default_program": s.cfg.DefaultProgram}, msg)
}

// ---------- workspaces ----------

func (s *Settings) workspaceList() []string {
	seen := map[string]bool{}
	var out []string
	if s.repoPath != "" {
		out = append(out, s.repoPath)
		seen[filepath.Clean(s.repoPath)] = true
	}
	var rest []string
	for k := range s.cfg.Repos {
		if !seen[filepath.Clean(expandHome(k))] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := userHome(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// repoEntryKey is the key a workspace is stored under: the existing one if the
// config already names it (however it was written), else its path.
func (s *Settings) repoEntryKey(repo string) string {
	want := filepath.Clean(expandHome(repo))
	for k := range s.cfg.Repos {
		if filepath.Clean(expandHome(k)) == want {
			return k
		}
	}
	return repo
}

func (s *Settings) repoEntry(repo string) *config.RepoConfig {
	k := s.repoEntryKey(repo)
	e := s.cfg.Repos[k]
	if e == nil {
		e = &config.RepoConfig{}
		s.cfg.Repos[k] = e
	}
	return e
}

type settingsField struct {
	label  string
	value  func() string
	toggle func()
	edit   func()
	hint   string
}

func (s *Settings) workspaceFields(repo string) []settingsField {
	e := func() *config.RepoConfig { return s.repoEntry(repo) }
	saveRepos := func(msg string) { s.commit(map[string]any{"repos": s.cfg.Repos}, msg) }
	return []settingsField{
		{label: "branch prefix", value: func() string {
			if p := e().BranchPrefix; p != nil {
				if *p == "" {
					return "(none)"
				}
				return *p
			}
			return "global: " + s.cfg.BranchPrefix
		}, edit: func() {
			cur := ""
			if p := e().BranchPrefix; p != nil {
				cur = *p
			}
			s.ask("Branch prefix for this repository (empty = none; type - to use the global one)", cur, false, func(v string) {
				if strings.TrimSpace(v) == "-" {
					e().BranchPrefix = nil
				} else {
					v = strings.TrimSpace(v)
					e().BranchPrefix = &v
				}
				saveRepos("saved the branch prefix")
			})
		}},
		{label: "keep branch case", value: func() string {
			if p := e().PreserveBranchCase; p != nil {
				return onOff(*p)
			}
			return "global: " + onOff(s.cfg.PreserveBranchCase)
		}, toggle: func() {
			v := !s.cfg.PreserveBranchCase
			if p := e().PreserveBranchCase; p != nil {
				v = !*p
			}
			e().PreserveBranchCase = &v
			saveRepos("")
		}},
		{label: "agent", value: func() string {
			if a := e().Agent; a != "" {
				return a
			}
			return "global default"
		}, edit: func() {
			names := []string{"global default"}
			for _, p := range s.cfg.Profiles {
				names = append(names, p.Name)
			}
			s.choose("Agent for new sessions here", names, func(i int) {
				if i == 0 {
					e().Agent = ""
				} else {
					e().Agent = names[i]
				}
				saveRepos("new sessions here start with " + names[i])
			})
		}},
		{label: "dev command", value: func() string {
			if d := e().Dev; d != nil && d.Command != "" {
				return d.Command
			}
			return "(none)"
		}, edit: func() {
			cur := ""
			if d := e().Dev; d != nil {
				cur = d.Command
			}
			s.ask("Command d runs in this repository (empty = no dev stack)", cur, false, func(v string) {
				v = strings.TrimSpace(v)
				if e().Dev == nil {
					e().Dev = &config.DevConfig{}
				}
				e().Dev.Command = v
				saveRepos("saved the dev command")
			})
		}, hint: "readiness checks are edited in config.json (adroit debug shows where)"},
	}
}

func (s *Settings) workspacesKey(key string) {
	if s.repo == "" {
		list := s.workspaceList()
		if len(list) == 0 {
			return
		}
		if key == "enter" || key == "e" {
			s.repo = list[s.cursor[tabWorkspaces]]
			s.cursor[tabWorkspaces] = 0
		}
		return
	}
	s.activateField(s.workspaceFields(s.repo), key)
}

// ---------- general ----------

func (s *Settings) generalFields() []settingsField {
	toggle := func(label, key string, get func() bool, set func(bool), hint string) settingsField {
		return settingsField{label: label, value: func() string { return onOff(get()) }, toggle: func() {
			v := !get()
			set(v)
			s.commit(map[string]any{key: v}, "")
		}, hint: hint}
	}
	ptr := func(p **bool) (func() bool, func(bool)) {
		return func() bool { return *p == nil || **p }, func(v bool) { *p = &v }
	}
	bellGet, bellSet := ptr(&s.cfg.Bell)
	ciGet, ciSet := ptr(&s.cfg.GitHubCIStatus)
	upGet, upSet := ptr(&s.cfg.UpstreamStatus)
	syncGet, syncSet := ptr(&s.cfg.SyncBaseBranch)
	return []settingsField{
		{label: "theme", value: func() string {
			if s.cfg.Theme == "" {
				return "default"
			}
			return s.cfg.Theme
		}, hint: "t on the main screen previews themes as you move"},
		toggle("bell", "bell", bellGet, bellSet, "rings when a session finishes or needs you"),
		{label: "notify command", value: func() string {
			if s.cfg.NotifyCommand == "" {
				return "(none)"
			}
			return s.cfg.NotifyCommand
		}, edit: func() {
			s.ask("Run on finished / needs-input ($ADROIT_SESSION, $ADROIT_EVENT)", s.cfg.NotifyCommand, false, func(v string) {
				s.cfg.NotifyCommand = strings.TrimSpace(v)
				s.commit(map[string]any{"notify_command": s.cfg.NotifyCommand}, "saved the notify command")
			})
		}},
		toggle("CI and pull requests", "github_ci_status", ciGet, ciSet, "needs gh; polls every 30s"),
		toggle("upstream drift", "upstream_status", upGet, upSet, "fetches each repository at most once a minute"),
		toggle("sync base branch", "sync_base_branch", syncGet, syncSet, "fast-forwards main before cutting a session"),
		toggle("auto-yes", "auto_yes", func() bool { return s.cfg.AutoYes }, func(v bool) { s.cfg.AutoYes = v },
			"experimental; applies from the next launch"),
		{label: "branch prefix", value: func() string { return s.cfg.BranchPrefix }, edit: func() {
			s.ask("Prefix for new branches (per-repository ones win)", s.cfg.BranchPrefix, false, func(v string) {
				s.cfg.BranchPrefix = strings.TrimSpace(v)
				s.commit(map[string]any{"branch_prefix": s.cfg.BranchPrefix}, "saved the branch prefix")
			})
		}},
	}
}

func (s *Settings) generalKey(key string) { s.activateField(s.generalFields(), key) }

func (s *Settings) activateField(fields []settingsField, key string) {
	i := s.cursor[s.tab]
	if i >= len(fields) || (key != "enter" && key != " " && key != "e") {
		return
	}
	f := fields[i]
	switch {
	case f.toggle != nil:
		f.toggle()
	case f.edit != nil:
		f.edit()
	}
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// ---------- shared ----------

func (s *Settings) commit(changes map[string]any, msg string) {
	if s.save != nil {
		if err := s.save(changes); err != nil {
			s.fail("not saved: " + err.Error())
			return
		}
	}
	if msg != "" {
		s.note(msg)
	} else {
		s.note("saved")
	}
}

func (s *Settings) note(msg string) { s.status, s.isError = msg, false }
func (s *Settings) fail(msg string) { s.status, s.isError = msg, true }

func (s *Settings) ask(prompt, initial string, secret bool, onSubmit func(string)) {
	ti := textinput.New()
	ti.SetValue(initial)
	ti.CursorEnd()
	ti.CharLimit = 4096
	ti.Width = 60
	if secret {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '•'
	}
	ti.Focus()
	s.input, s.inputFor, s.onSubmit, s.mode = ti, prompt, onSubmit, modeInput
}

func (s *Settings) handleInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		s.mode = modeBrowse
		return nil
	case "enter":
		s.mode = modeBrowse
		v := s.input.Value()
		s.input.SetValue("") // nothing typed lingers in memory longer than needed
		if s.onSubmit != nil {
			s.onSubmit(v)
		}
		return nil
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return cmd
}

func (s *Settings) choose(title string, options []string, onChoose func(int)) {
	s.choices, s.choiceFor, s.onChoose, s.mode = options, title, onChoose, modeChoose
	s.chooseCursor = 0
}

func (s *Settings) handleChoose(msg tea.KeyMsg) {
	switch msg.String() {
	case "esc":
		s.mode = modeBrowse
	case "up", "k":
		if s.chooseCursor > 0 {
			s.chooseCursor--
		}
	case "down", "j":
		if s.chooseCursor < len(s.choices)-1 {
			s.chooseCursor++
		}
	case "enter":
		s.mode = modeBrowse
		if s.onChoose != nil {
			s.onChoose(s.chooseCursor)
		}
	}
}

// ---------- rendering ----------

func (s *Settings) Render() string {
	accent := lipgloss.NewStyle().Foreground(theme.Color(theme.Accent))
	pill := lipgloss.NewStyle().Background(theme.Color(theme.Accent)).Foreground(theme.Color(theme.AccentText)).Bold(true)
	muted := lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	subtle := lipgloss.NewStyle().Foreground(theme.Color(theme.Subtle))
	text := lipgloss.NewStyle().Foreground(theme.Color(theme.Text))
	ok := lipgloss.NewStyle().Foreground(theme.Color(theme.Success))
	bad := lipgloss.NewStyle().Foreground(theme.Color(theme.Danger))
	sel := lipgloss.NewStyle().Background(theme.Color(theme.SelectionBg)).Foreground(theme.Color(theme.SelectionFg))

	width := s.width - 8
	if width > 100 {
		width = 100
	}
	if width < 50 {
		width = 50
	}

	var b strings.Builder
	// The tab bar, drawn like the main window's.
	var tabs []string
	for i, name := range settingsTabs {
		if i == s.tab {
			tabs = append(tabs, pill.Render(" "+name+" "))
		} else {
			tabs = append(tabs, muted.Render(" "+name+" "))
		}
	}
	b.WriteString(accent.Render("Settings  ") + strings.Join(tabs, subtle.Render("─┬─")))
	b.WriteString("\n\n")

	row := func(i int, line string) {
		if i == s.cursor[s.tab] && s.mode == modeBrowse {
			b.WriteString(sel.Render("▸ " + padTo(line, width-4)))
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
	}

	switch s.tab {
	case tabAgents:
		b.WriteString(muted.Render(fmt.Sprintf("  %-3s %-10s %-24s %s", "", "agent", "command", "API key")) + "\n")
		for i, p := range s.cfg.Profiles {
			def := "  "
			if s.cfg.DefaultProgram == p.Name || (s.cfg.DefaultProgram == p.Program && i == 0) {
				def = ok.Render("● ")
			}
			keyCol := s.keyStatus(p, ok, bad, muted)
			if c, found := s.checks[p.Name]; found {
				if strings.HasPrefix(c, "✓") {
					keyCol += "  " + ok.Render(c)
				} else {
					keyCol += "  " + bad.Render(clipText(c, 40))
				}
			}
			row(i, fmt.Sprintf("%s %-10s %-24s %s", def, clipText(p.Name, 10), clipText(p.Program, 24), keyCol))
		}
		row(len(s.cfg.Profiles), accent.Render("+ add an agent"))
		b.WriteString("\n" + muted.Render("keys go to "+s.store.Backend()+", never config.json") + "\n")
	case tabWorkspaces:
		if s.repo == "" {
			for i, r := range s.workspaceList() {
				label := shortPath(r)
				if r == s.repoPath {
					label += muted.Render("  (this repository)")
				}
				row(i, label)
			}
		} else {
			b.WriteString(text.Render(shortPath(s.repo)) + "\n\n")
			for i, f := range s.workspaceFields(s.repo) {
				// One line each: a dev command is often longer than the box, and
				// wrapping it pushed every other field out of view.
				row(i, fmt.Sprintf("%-18s %s", f.label, clipText(f.value(), width-28)))
			}
			if f := s.workspaceFields(s.repo); s.cursor[tabWorkspaces] < len(f) && f[s.cursor[tabWorkspaces]].hint != "" {
				b.WriteString("\n" + muted.Render(f[s.cursor[tabWorkspaces]].hint) + "\n")
			}
		}
	case tabGeneral:
		fields := s.generalFields()
		for i, f := range fields {
			row(i, fmt.Sprintf("%-22s %s", f.label, clipText(f.value(), width-32)))
		}
		if i := s.cursor[tabGeneral]; i < len(fields) && fields[i].hint != "" {
			b.WriteString("\n" + muted.Render(fields[i].hint) + "\n")
		}
	}

	switch s.mode {
	case modeInput:
		b.WriteString("\n" + accent.Render(s.inputFor) + "\n" + s.input.View() + "\n")
		b.WriteString(muted.Render("enter save · esc cancel"))
	case modeChoose:
		b.WriteString("\n" + accent.Render(s.choiceFor) + "\n")
		for i, c := range s.choices {
			if i == s.chooseCursor {
				b.WriteString(sel.Render("▸ "+c) + "\n")
			} else {
				b.WriteString("  " + c + "\n")
			}
		}
		b.WriteString(muted.Render("↑↓ choose · enter pick · esc cancel"))
	default:
		if s.status != "" {
			if s.isError {
				b.WriteString("\n" + bad.Render(s.status) + "\n")
			} else {
				b.WriteString("\n" + ok.Render(s.status) + "\n")
			}
		}
		b.WriteString("\n" + muted.Render(s.help()))
	}

	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(theme.Color(theme.Accent)).
		Padding(1, 2).Width(width).Render(b.String())
}

func (s *Settings) help() string {
	switch s.tab {
	case tabAgents:
		return "↑↓ move · s set key · t test key · d make default · a add · enter edit command · x remove · tab next · esc close"
	case tabWorkspaces:
		if s.repo == "" {
			return "↑↓ move · enter open · tab next · esc close"
		}
		return "↑↓ move · enter change · esc back"
	}
	return "↑↓ move · enter change · tab next · esc close"
}

func (s *Settings) keyStatus(p config.Profile, ok, bad, muted lipgloss.Style) string {
	if len(p.Keys) == 0 {
		return muted.Render("none needed (s to add one)")
	}
	var parts []string
	for _, k := range p.Keys {
		v, err := s.store.Get(secrets.Key(p.Name, k))
		if err == nil && v != "" {
			parts = append(parts, ok.Render(secrets.Mask(v)))
		} else {
			parts = append(parts, muted.Render(k+" not set"))
		}
	}
	return strings.Join(parts, " ")
}

func padTo(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

var userHome = os.UserHomeDir

func shortPath(p string) string {
	if home, err := userHome(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
