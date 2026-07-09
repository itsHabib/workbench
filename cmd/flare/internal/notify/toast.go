package notify

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/itsHabib/workbench/cmd/flare/internal/event"
)

// powershellAUMID is Windows PowerShell's app-user-model ID. Toasts must be
// raised under a registered AUMID to display; borrowing PowerShell's is the
// standard dependency-free route for an unpackaged binary.
const powershellAUMID = `{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe`

// toast raises a Windows toast by shelling to powershell.exe (5.1): the WinRT
// toast types project only in Windows PowerShell, not pwsh 7 (verified on
// this box 2026-07-08). Event text travels XML-escaped inside a single-quoted
// PS string; single quotes are doubled per PS quoting rules.
func toast(ev event.Event) error {
	xml := fmt.Sprintf(
		`<toast><visual><binding template="ToastGeneric"><text>%s</text><text>%s</text></binding></visual></toast>`,
		xmlEscape(ev.Title), xmlEscape(clip(ev.Body, 300)))
	script := strings.Join([]string{
		`[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null`,
		`[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom, ContentType = WindowsRuntime] | Out-Null`,
		`$doc = New-Object Windows.Data.Xml.Dom.XmlDocument`,
		fmt.Sprintf(`$doc.LoadXml('%s')`, psQuote(xml)),
		`$toast = New-Object Windows.UI.Notifications.ToastNotification($doc)`,
		fmt.Sprintf(`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('%s').Show($toast)`, psQuote(powershellAUMID)),
	}, "; ")
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("notify: toast: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

func psQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
