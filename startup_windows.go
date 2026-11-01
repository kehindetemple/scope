//go:build windows

package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	startupTaskName = "Scope"
	// backgroundArg is passed by the logon task. It keeps the dashboard hidden,
	// so Scope starts quietly and only the search shortcut is ready.
	backgroundArg  = "--background"
	createNoWindow = 0x08000000
)

// launchedInBackground reports whether this start came from the logon task.
func launchedInBackground() bool {
	for _, a := range os.Args[1:] {
		if a == backgroundArg {
			return true
		}
	}
	return false
}

// syncStartup makes the Windows logon task match the setting. The task runs
// Scope when the user signs in, with administrator rights, so the index is
// fast from the start. A Run key cannot do this without a UAC prompt on every
// sign-in. Scope must run as administrator once for the task to be created.
func syncStartup(enabled bool, settingsDir string) {
	if !enabled {
		// Nothing to remove if the task was never created, so the error is ignored.
		_, _ = runSchtasks("/Delete", "/TN", startupTaskName, "/F")
		return
	}

	exe, err := os.Executable()
	if err != nil {
		logf("startup: cannot find Scope.exe: %v", err)
		return
	}
	who, err := user.Current()
	if err != nil {
		logf("startup: cannot find the current user: %v", err)
		return
	}

	xmlPath := filepath.Join(settingsDir, "startup-task.xml")
	if err := os.WriteFile(xmlPath, utf16LEWithBOM(startupTaskXML(exe, who.Username)), 0o600); err != nil {
		logf("startup: cannot write task file: %v", err)
		return
	}
	defer os.Remove(xmlPath)

	if out, err := runSchtasks("/Create", "/TN", startupTaskName, "/XML", xmlPath, "/F"); err != nil {
		logf("startup: could not create the logon task (run Scope as administrator once): %v %s", err, out)
	}
}

// startupTaskXML describes a task that runs exe at logon, with the highest
// privileges. It runs on battery power, and it is not stopped after the
// default three days (ExecutionTimeLimit PT0S means no limit).
func startupTaskXML(exe, username string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Starts Scope when you sign in.</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%[2]s</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%[2]s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>false</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%[1]s</Command>
      <Arguments>%[3]s</Arguments>
    </Exec>
  </Actions>
</Task>
`, xmlEscape(exe), xmlEscape(username), backgroundArg)
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// utf16LEWithBOM encodes text the way schtasks expects an XML file to be encoded.
func utf16LEWithBOM(s string) []byte {
	units, _ := syscall.UTF16FromString(s) // trailing NUL is dropped below
	units = units[:len(units)-1]
	out := make([]byte, 0, 2+2*len(units))
	out = append(out, 0xFF, 0xFE)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func runSchtasks(args ...string) (string, error) {
	cmd := exec.Command("schtasks", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
