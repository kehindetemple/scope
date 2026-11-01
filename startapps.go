//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"go-file-search/internal/index"
)

// The file index only knows files on disk, so it cannot see Windows Store apps
// (Settings, Photos, Calculator...), which have no ordinary .exe, or the pages
// inside Windows Settings. This file adds them, the way Start does: a list of
// every app Windows shows in the Start menu, plus a list of Settings pages.

const (
	// appsFolderPrefix turns an app ID into something the shell can open and
	// draw an icon for, for Store apps and classic apps alike.
	appsFolderPrefix = `shell:AppsFolder\`

	// settingsAppPath is the Windows Settings app. Its icon is used for the
	// Settings pages below.
	settingsAppPath = appsFolderPrefix + `windows.immersivecontrolpanel_cw5n1h2txyewy!microsoft.windows.immersivecontrolpanel`

	settingsURIPrefix = "ms-settings:"
	settingsLabel     = "Windows Settings"
	catalogRefresh    = 10 * time.Minute
)

// catalogEntry is one Start menu app or Settings page that can be searched.
type catalogEntry struct {
	hit      index.Hit
	lower    string // lower-case name
	keywords string // extra lower-case words that also match
	exeName  string // lower-case file name for classic apps, to hide duplicates
}

var catalog struct {
	mu      sync.RWMutex
	entries []catalogEntry
}

// startApp is one row of PowerShell's Get-StartApps.
type startApp struct {
	Name  string `json:"Name"`
	AppID string `json:"AppID"`
}

// startCatalog fills the catalog with the Settings pages straight away, then
// reads the Start menu apps in the background and again every few minutes, so
// newly installed apps appear.
func startCatalog(ctx context.Context) {
	setCatalog(nil)
	go func() {
		for {
			apps, err := listStartApps(ctx)
			if err != nil {
				logf("start menu apps: %v", err)
			} else {
				setCatalog(apps)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(catalogRefresh):
			}
		}
	}()
}

// listStartApps asks Windows for the apps in the Start menu. PowerShell is
// used because Get-StartApps already merges Store apps and classic shortcuts.
func listStartApps(ctx context.Context) ([]startApp, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	ps := "powershell.exe"
	if root := os.Getenv("SystemRoot"); root != "" {
		ps = filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	}
	script := "[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false; " +
		"Get-StartApps | Select-Object Name,AppID | ConvertTo-Json -Compress"

	cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-Command", script)
	// No console window may flash up: this app has no console of its own.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	out = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(out), []byte{0xEF, 0xBB, 0xBF}))
	if len(out) == 0 {
		return nil, errors.New("Get-StartApps returned nothing")
	}

	var apps []startApp
	if err := json.Unmarshal(out, &apps); err != nil {
		// One app is written as a bare object instead of a list.
		var one startApp
		if err2 := json.Unmarshal(out, &one); err2 != nil || one.AppID == "" {
			return nil, err
		}
		apps = []startApp{one}
	}
	return apps, nil
}

// setCatalog rebuilds the searchable list from the Start menu apps and the
// built-in Settings pages.
func setCatalog(apps []startApp) {
	var entries []catalogEntry
	seen := map[string]bool{}

	add := func(e catalogEntry) {
		key := strings.ToLower(e.hit.Path)
		if seen[key] {
			return
		}
		seen[key] = true
		e.lower = strings.ToLower(e.hit.Name)
		entries = append(entries, e)
	}

	for _, a := range apps {
		name := strings.TrimSpace(a.Name)
		id := strings.TrimSpace(a.AppID)
		if name == "" || id == "" {
			continue
		}
		e := catalogEntry{hit: index.Hit{
			Path:  appsFolderPrefix + id,
			Name:  name,
			Kind:  index.KindApp,
			Label: "Application",
		}}
		if strings.HasSuffix(strings.ToLower(id), ".exe") {
			e.exeName = strings.ToLower(id[strings.LastIndexAny(id, `\/`)+1:])
		}
		add(e)
	}

	// Always known, even if the Start menu list could not be read.
	add(catalogEntry{hit: index.Hit{
		Path: settingsAppPath, Name: "Settings", Kind: index.KindApp, Label: "Application",
	}})

	for _, p := range settingsPages {
		add(catalogEntry{
			hit: index.Hit{
				Path: settingsURIPrefix + p.page, Name: p.name, Kind: index.KindApp, Label: settingsLabel,
			},
			keywords: strings.ToLower(p.keywords),
		})
	}

	catalog.mu.Lock()
	catalog.entries = entries
	catalog.mu.Unlock()
}

// searchCatalog returns the catalog entries that match the query.
func searchCatalog(query string) []catalogEntry {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	catalog.mu.RLock()
	defer catalog.mu.RUnlock()

	var found []catalogEntry
	for _, e := range catalog.entries {
		rank := -1
		switch {
		case e.lower == q:
			rank = 0
		case strings.HasPrefix(e.lower, q):
			rank = 1
		case strings.Contains(e.lower, q):
			rank = 2
		case e.keywords != "" && strings.Contains(e.keywords, q):
			rank = 2
		}
		if rank < 0 {
			continue
		}
		e.hit.Rank = rank
		found = append(found, e)
	}
	return found
}

// searchAll answers a query from both the file index and the catalog. Catalog
// apps replace the .exe and shortcut hits for the same app, so Chrome is not
// listed twice.
func searchAll(query string, limit int) []index.Hit {
	var files []index.Hit
	if eng != nil {
		files = eng.Search(query, limit)
	}
	cat := searchCatalog(query)
	if len(cat) == 0 {
		return files
	}

	names := make(map[string]bool, len(cat))
	exes := make(map[string]bool, len(cat))
	all := make([]index.Hit, 0, len(files)+len(cat))
	for _, e := range cat {
		names[e.lower] = true
		if e.exeName != "" {
			exes[e.exeName] = true
		}
		all = append(all, e.hit)
	}
	for _, h := range files {
		if h.Kind == index.KindApp && !h.Dir &&
			(names[strings.ToLower(h.Name)] || exes[strings.ToLower(filepath.Base(h.Path))]) {
			continue
		}
		all = append(all, h)
	}

	sort.SliceStable(all, func(i, j int) bool { return index.Less(all[i], all[j]) })
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

// isShellItem reports whether a result is opened and drawn through the shell
// (a Start menu app or a Settings page) instead of being a file on disk.
func isShellItem(path string) bool {
	return strings.HasPrefix(path, appsFolderPrefix) || strings.HasPrefix(path, settingsURIPrefix)
}
