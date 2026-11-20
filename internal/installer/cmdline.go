package installer

import "strings"

// SplitCommand splits an ARP UninstallString into the executable and the
// remaining raw argument string, following CreateProcess rules: a quoted
// first token, or else the shortest prefix ending in ".exe".
func SplitCommand(s string) (exe, args string) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `"`) {
		if i := strings.Index(s[1:], `"`); i >= 0 {
			return s[1 : i+1], strings.TrimSpace(s[i+2:])
		}
		return strings.Trim(s, `"`), ""
	}
	if i := strings.Index(strings.ToLower(s), ".exe"); i >= 0 {
		end := i + 4
		if end == len(s) || s[end] == ' ' {
			return s[:end], strings.TrimSpace(s[end:])
		}
	}
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

// UninstallSilentArgs returns switches that make a given installer
// technology's uninstaller silent.
func UninstallSilentArgs(installerType string) string {
	switch strings.ToLower(installerType) {
	case "nullsoft":
		return "/S"
	case "inno":
		return "/VERYSILENT /SUPPRESSMSGBOXES /NORESTART"
	case "burn":
		return "/quiet /norestart"
	}
	return ""
}
