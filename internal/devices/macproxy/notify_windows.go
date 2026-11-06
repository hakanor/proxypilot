package macproxy

import "syscall"

// settingsChanged tells running apps to reload the WinINet proxy settings now.
func settingsChanged() {
	p, err := syscall.LoadDLL("wininet.dll")
	if err != nil {
		return
	}
	defer p.Release()
	f, err := p.FindProc("InternetSetOptionW")
	if err != nil {
		return
	}
	f.Call(0, 39, 0, 0) // INTERNET_OPTION_SETTINGS_CHANGED
	f.Call(0, 37, 0, 0) // INTERNET_OPTION_REFRESH
}
