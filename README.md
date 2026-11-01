# Scope

Scope is a native Windows search box. Press a hotkey anywhere, type part of a
name, and open a file, folder, application or Settings page. It is one `.exe`
written in Go, with no browser engine (no Chrome, Chromium, Electron, Tauri or
WebView). The window is drawn directly with the Win32 API.

It is inspired by Raycast: results appear as you type, applications are listed
first with their real icons, and the box is slightly see-through.

---

## Contents

1. [Quick start](#quick-start)
2. [Using Scope](#using-scope)
3. [How it works (system flow)](#how-it-works-system-flow)
4. [Architecture](#architecture)
5. [Indexing in detail](#indexing-in-detail)
6. [Search and ranking](#search-and-ranking)
7. [The search box](#the-search-box)
8. [The dashboard and settings](#the-dashboard-and-settings)
9. [Questions with `~`](#questions-with-)
10. [Files on disk](#files-on-disk)
11. [Building and testing](#building-and-testing)
12. [Limitations](#limitations)

---

## Quick start

1. Run `scope.exe` as **administrator** (right-click → *Run as administrator*).
   Administrator rights let Scope read the drive's change journal, which makes
   the first index take seconds instead of minutes.
2. The dashboard opens. It shows the current hotkey, which defaults to **Ctrl + Space**.
3. Press **Ctrl + Space** from any window. Type to search. Press **Enter** to open.
4. Press **Esc** to close the box.

Scope runs in the background after you close the dashboard. Start it again and
the dashboard opens instead of a second copy.

**Starts with Windows.** The first time Scope runs as administrator, it creates
a Task Scheduler task named `Scope`. The task starts Scope at every sign-in,
with administrator rights, and keeps it running on battery power. At sign-in
the dashboard stays hidden and only the hotkey is ready, as with Raycast. To turn
this off, set `"startupDisabled": true` in `settings.json` and start Scope once;
Scope then deletes the task. The task only exists when Scope runs as
administrator, because it must run with administrator rights.

---

## Using Scope

| Action | How |
|---|---|
| Open the search box | Press the hotkey (default Ctrl + Space) |
| Move through results | Up / Down arrow (or the mouse wheel) |
| Open the highlighted result | Enter, or click it |
| Clear, then close the box | Esc (first press clears the text, a later press closes it) |
| Close the box | Click anywhere else |
| Ask a question | Start the text with `~` (see [Questions with `~`](#questions-with-)) |
| Open settings | Start Scope again, or open the dashboard from the tray-less background |

Each result row shows:

- the item's real icon (an `.exe`, a folder, an image thumbnail, a video frame, or the Windows icon for that file type)
- its name, and its full path below
- a label on the right: **Application**, **Folder**, **File**, or **Windows Settings**
- the footer shows what Enter will do, for example **Open Application ↵**

---

## How it works (system flow)

### 1. Startup

```
scope.exe starts
  ├─ single-instance check (named mutex)
  │    └─ already running? → tell the running copy to show its dashboard, exit
  ├─ COM init, DPI, fonts, colours
  ├─ load settings.json   (hotkey, theme, colours, ~ source, API keys)
  ├─ create the search window (hidden) and the dashboard window
  ├─ engine.Start()       (loads the saved index snapshot, starts indexing drives)
  ├─ startCatalog()       (adds Start menu apps and Settings pages)
  ├─ register the global hotkey
  └─ message loop         (runs until Quit)
```

Nothing is slow on startup. The saved index (the *snapshot*) loads first, so
searches work immediately. Each drive then catches up in the background.

### 2. Pressing the hotkey

```
Windows sends WM_HOTKEY to Scope's hotkey thread
  → the thread posts wmAppToggle to the search window
  → the search window's UI thread toggles it:
       hidden → show at the upper quarter of the monitor under the mouse,
                take keyboard focus, clear the text
       shown  → hide
```

The hotkey is registered with `RegisterHotKey` on its own locked OS thread,
so it still works when the dashboard is closed or another app has focus.

### 3. Typing

```
each keystroke in the EDIT box
  → EN_CHANGE → refreshResults()
  → q = trimmed text
       q == ""        → no results, box shows only the input row
       q starts "~"   → question mode (see below), no file search
       otherwise      → runSearch(q)
  → runSearch:
       1. applications from the Start menu catalog   (up to 5 shown)
       2. files, folders and .exe items from the file index   (up to 8 rows in total)
       3. drop duplicates (see "Deduplication")
  → resize the popup to fit the rows, repaint
```

Searches run on the UI thread. They are fast because every index lives in
memory.

### 4. Opening a result

```
Enter / click on a row
  → hide the box
  → explorer.exe "<path>"
```

Scope asks Explorer to open the item. Explorer runs it with *your* normal
rights, not the administrator rights Scope has. Start Menu apps and Settings
pages are opened through the shell the same way.

### 5. Live updates (no rescans)

```
every 500 ms, for each NTFS drive:
  read USN journal records after our last position
  → apply create / delete / rename / change to the in-memory tree
  → new file appears in search within about a second
```

A full disk scan happens only the first time a drive is seen, or after the
journal is reset.

### 6. Saving and shutdown

```
every 5 minutes:  write the index snapshot   (temp file, then rename)
on Quit:          write the index snapshot once more
```

The next start reads the snapshot and catches up with the journal, so only
the changes made while Scope was closed need reading.

---

## Architecture

```
scope.exe
│
├─ main.go              startup, single-instance check, message loop
├─ win32.go             Win32 procedure bindings and constants
├─ theme.go             DPI scaling, layout, fonts, light/dark palettes
├─ gfx.go               drawing helpers (double-buffered canvas, rounded rects)
│
├─ search_window.go     the popup: input box, results, keyboard, mouse, positioning
├─ search_web.go        "~" question mode, waits for typing to pause
├─ ai_window.go         "AI answers…" window: provider, model, API key
├─ dashboard.go         the settings window: hotkey, theme, colours, Quit
├─ settings.go          settings.json read/write
├─ settings_pages.go    built-in Windows Settings pages (Display, Wi-Fi, …)
├─ startapps.go         Start menu catalog (apps via PowerShell Get-StartApps)
├─ appx.go              logos for Store apps from AppxManifest.xml
├─ appicon.go           the app's own icon (microscope.png) at several sizes
├─ icons.go             per-item icons and previews via the Windows shell
│
└─ internal/
   ├─ engine/           owns one index per drive; merges results across drives
   ├─ ntfs/             MFT enumeration, USN journal reading, live updates, snapshots
   ├─ walk/             folder-walk index for drives without a journal (FAT32, exFAT, …)
   ├─ index/            the in-memory name tree, search, ranking, snapshot file format
   ├─ volumes/          lists fixed drives, their type and file system
   ├─ hotkey/           key names, parsing, RegisterHotKey on a dedicated thread
   └─ answer/           "~" answers: Wikipedia, Google (Serper), or an AI model
```

### Threads

| Thread | Does |
|---|---|
| UI thread | Windows messages, painting, typing, search queries, opening results |
| Hotkey thread | Owns `RegisterHotKey`; posts a message to the UI thread on press |
| One goroutine per NTFS drive | Reads the journal every 500 ms; applies changes under a lock |
| One goroutine per non-NTFS drive | Walks the folder tree every 30 minutes |
| Catalog goroutine | Runs PowerShell `Get-StartApps` every 10 minutes |
| Snapshot goroutine | Saves the index every 5 minutes |
| Answer goroutines | Fetch `~` answers over the internet, so the UI never waits |

The UI thread never blocks on the network or the disk.

---

## Indexing in detail

### Which drives

Scope indexes every **fixed** drive (`GetDriveType` = fixed). Removable drives
and network drives are skipped.

### NTFS drives (the fast path)

NTFS keeps every file's record in the **Master File Table (MFT)** and records
every change in the **USN change journal**. Scope uses both:

1. **First index.** `FSCTL_ENUM_USN_DATA` reads the MFT in large blocks. Each
   record gives an id, its parent id, its name and whether it is a folder.
   These go into an in-memory tree. Metafiles (`$MFT`, `$Bitmap`, …) and the
   volume root are skipped.
2. **Journal position.** The journal's id and the next USN are recorded. Any
   change made during the MFT read is re-applied afterwards, so nothing is
   missed. Applying a change twice does no harm.
3. **Live updates.** `FSCTL_READ_USN_JOURNAL` is read from the saved position
   every 500 ms. Each record is one of: create, delete, rename, or
   metadata/data change. Deletes remove the entry. Renames and changes update
   it. Creates add it.
4. **Journal reset.** If the journal was recreated or the saved position is too
   old, Scope re-reads the whole MFT (`rebuild`), then continues from the journal.

Paths are not stored. Each entry keeps only its name and its parent's id, and a
full path is built on demand by walking parent links up to the root. That keeps
memory low for millions of files.

### Non-NTFS drives (the fallback)

FAT32, exFAT, ReFS and similar have no journal. Scope walks the folder tree
(`walk.Build`), skipping folders it cannot read, and repeats the walk every 30
minutes. Results for those drives can therefore be up to 30 minutes old.

### No administrator rights

Without administrator rights, opening the volume fails. Scope logs the reason
and falls back to the folder walk for that drive. Results still work, but the
first index takes longer and updates are slower. Running as administrator is
strongly recommended.

### The snapshot file

`index.snap` stores every NTFS volume's tree, its journal id and its next USN.

- It is written to a temporary file first, then renamed. A crash cannot leave a
  half-written file.
- It begins with the magic bytes `GFSNAP01`. Anything else is ignored.
- Reading checks sizes and limits before allocating memory, so a corrupt file
  cannot exhaust memory. A bad snapshot is discarded and the drive is indexed
  from the MFT again.

### Applications and Settings pages (the catalog)

The file index cannot see everything Start shows. Store apps such as
Calculator or Photos have no ordinary `.exe`, and Settings pages are not
files. Scope adds two sources:

- **Start menu apps.** PowerShell's `Get-StartApps` returns every app Start
  shows, with its launch ID. This is refreshed every 10 minutes.
- **Settings pages.** A built-in list (Display, Sound, Wi-Fi, Bluetooth,
  Windows Update, and so on) that opens the matching `ms-settings:` page.
  These are always available, even before the first PowerShell scan finishes.

Icons for Store apps come from the logo file named in the app's
`AppxManifest.xml`, scaled to the row size.

---

## Search and ranking

### Matching

Matching is case-insensitive and substring-based. Spaces around the query are
trimmed. Each name is compared to the query in one of three ways:

| Rank | Meaning | Example for `cap` |
|---|---|---|
| 0 | the name equals the query | `cap` |
| 1 | the name starts with the query | `CapCut`, `Capture` |
| 2 | the name contains the query | `Screencap Tool` |

### Order of the results

Results are sorted by these keys, in order:

1. **Kind.** Applications come before files and folders.
2. **Rank** (above).
3. **Name length.** Shorter names first.
4. **Full path**, alphabetically. This makes the order stable, so a longer query
   keeps the same first rows.

Applications and Settings pages are listed first, up to 5 rows. Files and
folders then fill the remaining rows, up to 8 in total.

### Deduplication

The same app often appears more than once: as a Start menu shortcut, as its
`.exe`, and as a Store entry. Scope keeps one:

- If the catalog has an app with the same name, or the same `.exe`, the file
  index's copy of that app is dropped. The catalog entry wins because it has
  the Start icon and the correct launch ID.
- A `.lnk` shortcut is dropped when a catalog app with the same name exists.

### Limit

Only 8 rows fit on screen. The search asks each drive for up to 8 matches,
merges them, and keeps the best 8. Scrolling loads more as needed.

---

## The search box

- **Frameless and rounded.** The popup has no title bar and rounded corners
  (a window region).
- **Slightly transparent.** The window uses layered-window alpha. Its content
  is drawn sharp, and the transparency applies to the whole window.
- **Placeholder.** The input shows "Search apps and files" when empty.
- **Keyboard.** The text box is subclassed. Up, Down, Enter, Esc and Tab are
  handled before the text box sees them, so arrow keys move the selection
  instead of the caret.
- **Hides when you click away.** When the window loses focus it hides. A
  400 ms grace period after showing stops the focus change caused by showing
  the window from hiding it straight away.
- **Icons.** Each row asks the shell for its icon through
  `IShellItemImageFactory::GetImage`. Icons are cached: per file for `.exe`,
  shortcuts, images and videos; per extension for documents; one icon for all
  folders. Images and videos show a real thumbnail.
- **Drawing.** Everything is painted into an off-screen bitmap, then copied to
  the window in one step, so nothing flickers.
- **Themes and colours.** Light or dark, plus custom input and results colours,
  chosen on the dashboard.

---

## The dashboard and settings

The dashboard is the only normal window. It has:

- **Hotkey.** Shows the current combination. **Change** starts listening; press
  the new combination and it takes effect straight away. **Reset** restores
  Ctrl + Space. If Windows keeps the combination (for example Win + L) or
  another app already uses it, the dashboard reports that and keeps the old
  hotkey.
- **Win key as hotkey.** Scope sends a neutral key the moment Win goes down, so
  Windows Start does not open on top of Scope. The search box opens on every
  press, not only some of them.
- **Light / Dark.** Switches the theme for the search box and the dashboard.
- **Colours.** Ready-made swatches, **Auto**, or **Custom** (a colour picker)
  for the search field and the results box.
- **AI answers…** Opens the window where you choose where `~` questions go, and
  enter any API key.
- **Quit.** Saves the index and exits. The X button only hides the dashboard.

Settings are saved to `settings.json` as soon as they change.

---

## Questions with `~`

Start the search text with `~` to ask a question instead of searching files.
Example: `~when was Claude created`.

Three sources, chosen in **AI answers…**:

| Source | Needs | How it answers |
|---|---|---|
| **Wikipedia** (default) | Internet, no key | Summary of the best matching article, after you pause typing for half a second |
| **Google** | Serper API key in `searchApiKey` | Results from Google through serper.dev |
| **AI model** | A key for a provider, or Ollama running locally | The model answers when you press **Enter** |

AI providers: Anthropic (Claude), OpenAI, Gemini, Grok, Kimi, DeepSeek,
Mistral, Groq, OpenRouter, and Ollama (local, free, no key). Any other service
with an OpenAI-compatible address can be used by setting `aiBaseUrl`.

Answers are capped at about 900 characters. Enter or a click on the answer
opens the source article.

Keys are stored in plain text in `settings.json`. Do not share that file.

---

## Files on disk

| Path | Contents |
|---|---|
| `%AppData%\scope\settings.json` | Hotkey, theme, colours, answer source, API keys |
| `%LocalAppData%\scope\index.snap` | Saved index snapshot (large; safe to delete, it is rebuilt) |
| `%LocalAppData%\scope\scope.log` | Log file. The app has no console, so errors are written here |

To reset the index, quit Scope and delete `index.snap`.

---

## Building and testing

Requirements: Go 1.24 or newer, on Windows.

```powershell
go mod tidy
go build -ldflags "-H windowsgui" -o scope.exe .
go test ./internal/...
```

- `-H windowsgui` builds a Windows GUI app, so no console window appears.
- `rsrc_windows_amd64.syso` is linked automatically. It carries the microscope
  icon and the administrator request in the manifest.
- Tests cover hotkey parsing, the name tree and ranking, scrolling, record
  parsing of the journal, and the snapshot format. The GUI and the NTFS calls
  need a real Windows session to test.

### Changing the icon

The icon is in `assets/microscope.ico` (used for the `.exe`) and
`assets/microscope.png` (used inside the app). To regenerate the `.exe` icon,
install `go-winres` (`go install github.com/tc-hib/go-winres@latest`) and run
it against the icon, or rebuild the `.syso` with `windres`.

---

## Limitations

- **Windows only.** The UI and the NTFS code use Win32 and cannot run on Linux or macOS.
- **Administrator rights are recommended.** Without them, drives are walked, not read from the journal.
- **Folder-walk drives lag.** Non-NTFS drives refresh every 30 minutes.
- **Catalog refresh.** New Start menu apps appear within about 10 minutes.
- **Keys in plain text.** API keys are stored unencrypted in `settings.json`.
- **No tray icon.** Scope runs in the background. Quit it from the dashboard.
- **Internet needed for `~`.** Wikipedia, Google and AI answers need a connection.
- **Not yet tested on every PC.** The GUI and the NTFS path were built and checked for compile errors, but they should be tried on the target machine. If something fails, the log at `%LocalAppData%\scope\scope.log` shows why.

---

## Licence

Add a licence file before publishing a release.
