// Package brand holds the ProxyPilot app icon. icon.svg here is the source of
// truth; `make icons` copies it into the web UI and renders the PNG sizes.
package brand

import _ "embed"

//go:embed icon.svg
var IconSVG []byte
