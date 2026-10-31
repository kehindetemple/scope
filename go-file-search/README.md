# Scope

A native Windows search app. One `.exe`, no browser engine. Press the hotkey
from anywhere to open a search box over every file, folder and application on
your fixed drives. The dashboard holds the hotkey, the light/dark theme and the colors.

## Use it

- Open `scope.exe`. The dashboard shows the hotkey (default `Ctrl + Space`).
- Press the hotkey to open the search box. Type part of a name.
  - Up / Down move through results, Enter opens the highlighted item.
  - Click a result to open it.
  - Esc clears the box, then hides it.
  - Clicking anywhere else hides the box.
- Results are ordered like a launcher: applications first (with the app's icon and an
  "Application" label), then files and folders, then caches and logs last.
  Applications match by name without `.exe`, so `capcut` finds `CapCut`.
- Windows Store apps and Start menu apps (Settings, Photos, Calculator, ...) are found
  too, with the icon Windows shows in Start, and so are pages inside Windows Settings
  (Display, Wi-Fi, Bluetooth, Windows Update, ...), labelled "Windows Settings".
- Only eight rows show at a time, but every match can be reached: scroll with the mouse
  wheel, Up / Down, or Page Up / Page Down. More results load as you near the end.
- Start the box with `~` to ask a question instead of searching files, for example
  `~when was Claude created`. A short summary from Wikipedia appears in the results area
  after you pause typing. Enter or a click opens the article. This needs internet access.
  With a Serper API key in `settings.json` (`"searchApiKey": "..."`), the answers come from
  Google results (get a key at serper.dev). Without a key, Wikipedia is used.
- **AI answers:** the dashboard's **AI answers…** button opens a window where you choose
  where `~` questions are answered: an AI model, Wikipedia, or Google (through Serper).
  With the AI model, press Enter after typing the question. The model can answer any
  question, not only search questions. Choose a provider:
  - Claude (Anthropic), OpenAI, Gemini (Google), Grok (xAI), Kimi (Moonshot), DeepSeek,
    Mistral, Groq and OpenRouter: paste that service's API key there. Each service's key
    comes from its own developer console.
  - Ollama: runs a model on this PC for free. Install Ollama, run `ollama pull llama3.2`,
    keep Ollama running, and enter the model name from `ollama list` (for example
    `llama3.2:latest`). No key is needed.
  Keys are saved in plain text in `%AppData%\scope\settings.json`. Do not share
  that file. Any other service with an OpenAI-compatible address can be used by adding
  `"aiBaseUrl"` to settings.json.
- Each result shows the item's real icon, or a preview for images and videos. When an
  item has no icon of its own, Windows' generic icon for that kind of item is shown.
  The microscope (`assets/microscope.png`) is the app's own icon: the exe, the window
  title bar and the dashboard. See "Icon" below to change it.
- The search box has smooth rounded corners, a light and a dark theme, and scrolls smoothly.
- Change the hotkey on the dashboard: click **Change**, then press the shortcut you want.
  It takes effect straight away. Any combination works: the Windows key on its own,
  Ctrl + Shift, Alt + Space, F-keys, and so on. If Windows keeps the combination for itself
  (for example Win + L) or another app already uses it, the dashboard says so and the old
  hotkey stays active. **Reset** goes back to `Ctrl + Space`.
- The dashboard has a Light / Dark switch, and lets you pick the color of the search field
  and of the results box (ready-made colors, Auto, or any color with **Custom**).
  Settings are saved in `%AppData%\scope\settings.json`.
- **Quit** on the dashboard exits. The X button hides the dashboard; the app keeps running.
- Starting the app again opens the dashboard instead of a second copy.

## Icon

The exe icon comes from `rsrc_windows_amd64.syso`. To regenerate it from `assets/microscope.ico`,
install the tool with `go install github.com/tc-hib/go-winres@latest` and run
`go-winres simply --icon assets\microscope.ico` in the project folder. Then build again.

## Build

Requires Go 1.24 or newer. Run in PowerShell in the project folder:

```
go mod tidy
go build -ldflags "-H windowsgui" -o scope.exe .
```

`-H windowsgui` stops a console window from opening with the app.

Run as administrator for fast indexing. Without it, drives are scanned by walking folders.
The NTFS change journal must be active on each drive (on by default for C:).

## Files

- `settings.json`: `%AppData%\scope\`
- Index snapshot: `%LocalAppData%\scope\index.snap`
- Log: `%LocalAppData%\scope\scope.log`

## Layout

- `main.go`: startup, single-instance check, message loop
- `search_window.go`: the search box, results and keyboard handling
- `dashboard.go`: the dashboard (hotkey recorder, theme, colors), drawn by the app itself
- `theme.go`, `gfx.go`, `settings.go`: screen scaling, colors and fonts, smooth shapes, saved settings
- `icons.go`: icons and previews from the Windows shell (with fallbacks)
- `appicon.go`: the microscope icon, for the dashboard and the window icons
- `startapps.go`, `settings_pages.go`: Start menu apps (via `Get-StartApps`) and Settings pages
- `win32.go`: Windows API bindings
- `internal/engine`: keeps every drive indexed
- `internal/index`: in-memory index, ranking (`Kind`, `Less`) and snapshot file
- `internal/ntfs`: MFT reading and USN journal updates
- `internal/walk`: folder-scan fallback
- `internal/hotkey`: hotkey parsing, reserved combinations, the keyboard tracker used for the Windows key on its own, and the global hotkey loop
- `internal/volumes`: drive discovery

## Tests

```
go test ./internal/hotkey ./internal/index ./internal/ntfs
```
