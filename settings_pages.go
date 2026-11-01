//go:build windows

package main

// settingsPage is one page inside Windows Settings. page is what follows
// "ms-settings:". keywords are extra words that should find the page.
type settingsPage struct {
	name     string
	page     string
	keywords string
}

var settingsPages = []settingsPage{
	// System
	{"Display Settings", "display", "screen monitor resolution brightness scale"},
	{"Night Light Settings", "nightlight", "blue light"},
	{"Sound Settings", "sound", "volume audio speaker microphone output input"},
	{"Notifications Settings", "notifications", "focus do not disturb"},
	{"Focus Settings", "quiethours", "focus assist"},
	{"Power & Sleep Settings", "powersleep", "power battery sleep screen timeout"},
	{"Battery Settings", "batterysaver", "battery saver"},
	{"Storage Settings", "storagesense", "disk space cleanup"},
	{"Multitasking Settings", "multitasking", "snap windows desktops"},
	{"Clipboard Settings", "clipboard", "clipboard history"},
	{"About This PC", "about", "device specifications version pc name"},
	{"Troubleshoot Settings", "troubleshoot", "fix problems"},
	{"Recovery Settings", "recovery", "reset this pc"},
	{"Activation Settings", "activation", "product key licence license"},

	// Bluetooth & devices
	{"Bluetooth & Devices Settings", "bluetooth", "pair devices"},
	{"Printers & Scanners Settings", "printers", "printer scanner"},
	{"Mouse Settings", "mousetouchpad", "pointer cursor speed"},
	{"Touchpad Settings", "devices-touchpad", "gestures"},
	{"Typing Settings", "typing", "autocorrect spelling keyboard"},
	{"AutoPlay Settings", "autoplay", "removable drives"},
	{"USB Settings", "usb", "usb devices"},
	{"Camera Settings", "camera", "webcam"},

	// Network & internet
	{"Network & Internet Settings", "network", "connection status"},
	{"Wi-Fi Settings", "network-wifi", "wifi wireless internet networks"},
	{"Ethernet Settings", "network-ethernet", "wired lan"},
	{"VPN Settings", "network-vpn", "virtual private network"},
	{"Airplane Mode Settings", "network-airplanemode", "flight mode"},
	{"Mobile Hotspot Settings", "network-mobilehotspot", "hotspot tethering"},
	{"Proxy Settings", "network-proxy", "proxy server"},
	{"Data Usage Settings", "datausage", "metered data"},
	{"Advanced Network Settings", "network-advancedsettings", "adapters dns"},

	// Personalization
	{"Personalization Settings", "personalization", "customize"},
	{"Background Settings", "personalization-background", "wallpaper desktop picture"},
	{"Colors Settings", "colors", "dark mode light mode accent theme"},
	{"Themes Settings", "themes", "desktop icons"},
	{"Lock Screen Settings", "lockscreen", "screen saver"},
	{"Start Settings", "personalization-start", "start menu"},
	{"Taskbar Settings", "taskbar", "tray icons"},
	{"Fonts Settings", "fonts", "typeface"},

	// Apps
	{"Installed Apps Settings", "appsfeatures", "uninstall programs apps features"},
	{"Default Apps Settings", "defaultapps", "default browser file associations"},
	{"Startup Apps Settings", "startupapps", "run at startup login"},
	{"Optional Features Settings", "optionalfeatures", "windows features"},
	{"Offline Maps Settings", "maps", "maps"},

	// Accounts
	{"Your Info Settings", "yourinfo", "account profile picture"},
	{"Email & Accounts Settings", "emailandaccounts", "microsoft account"},
	{"Sign-in Options Settings", "signinoptions", "password pin windows hello fingerprint"},
	{"Other Users Settings", "otherusers", "family add user"},
	{"Access Work or School Settings", "workplace", "work school account"},

	// Time & language
	{"Date & Time Settings", "dateandtime", "clock time zone"},
	{"Language & Region Settings", "regionlanguage", "language region country keyboard layout"},
	{"Speech Settings", "speech", "voice"},

	// Gaming
	{"Game Bar Settings", "gaming-gamebar", "xbox capture"},
	{"Game Mode Settings", "gaming-gamemode", "performance"},

	// Accessibility
	{"Accessibility Settings", "easeofaccess", "ease of access"},
	{"Accessibility Display Settings", "easeofaccess-display", "text size contrast"},
	{"Accessibility Narrator Settings", "easeofaccess-narrator", "screen reader"},
	{"Accessibility Magnifier Settings", "easeofaccess-magnifier", "zoom"},
	{"Accessibility Keyboard Settings", "easeofaccess-keyboard", "sticky keys on-screen keyboard"},
	{"Accessibility Mouse Settings", "easeofaccess-mouse", "pointer"},
	{"Accessibility Audio Settings", "easeofaccess-audio", "mono audio"},
	{"Accessibility Closed Captions Settings", "easeofaccess-closedcaptioning", "subtitles"},

	// Privacy & security
	{"Privacy & Security Settings", "privacy", "permissions"},
	{"Windows Security Settings", "windowsdefender", "antivirus defender firewall virus"},
	{"Windows Update Settings", "windowsupdate", "updates check for updates"},
	{"Find My Device Settings", "findmydevice", "locate"},
	{"For Developers Settings", "developers", "developer mode"},
	{"Location Privacy Settings", "privacy-location", "gps"},
	{"Camera Privacy Settings", "privacy-webcam", "webcam permission"},
	{"Microphone Privacy Settings", "privacy-microphone", "mic permission"},
	{"Activity History Privacy Settings", "privacy-activityhistory", "timeline"},
	{"Account Privacy Settings", "privacy-accountinfo", "account info"},
	{"Notifications Privacy Settings", "privacy-notifications", "notification access"},
	{"Background Apps Settings", "privacy-backgroundapps", "background"},
}
