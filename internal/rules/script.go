package rules

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// scriptTimeout stops a script that loops forever; the flow continues with the error logged.
const scriptTimeout = time.Second

func compileScript(code string) (*goja.Program, error) {
	if strings.TrimSpace(code) == "" {
		return nil, errors.New("script is empty")
	}
	return goja.Compile("script", code, false)
}

// runScript runs a step in a fresh sandbox (no file, network or host access). It sees msg:
// headers (name → value), body (text), status (response) and url (request).
func runScript(prog *goja.Program, m *message, request bool) error {
	vm := goja.New()
	headers := map[string]any{}
	for k, v := range m.header {
		headers[k] = strings.Join(v, ", ")
	}
	in := map[string]any{"headers": headers, "body": string(m.body)}
	if request {
		in["url"] = m.url.String()
	} else {
		in["status"] = m.status
	}
	msg := vm.NewObject()
	for k, v := range in {
		if err := msg.Set(k, v); err != nil {
			return err
		}
	}
	if err := vm.Set("msg", msg); err != nil {
		return err
	}
	timer := time.AfterFunc(scriptTimeout, func() { vm.Interrupt("timed out") })
	defer timer.Stop()
	if _, err := vm.RunProgram(prog); err != nil {
		return fmt.Errorf("script: %v", err)
	}
	outHeaders, _ := msg.Get("headers").Export().(map[string]any)
	// Only touch what changed, so repeated headers and binary bodies stay intact.
	for k := range m.header {
		if _, ok := outHeaders[k]; !ok { // deleted by the script
			m.header.Del(k)
		}
	}
	for k, v := range outHeaders {
		text := fmt.Sprint(v)
		if strings.Join(m.header[k], ", ") != text {
			m.header.Set(http.CanonicalHeaderKey(k), text)
		}
	}
	if body := msg.Get("body").String(); body != in["body"] {
		m.body = []byte(body)
	}
	if request {
		if next := msg.Get("url").String(); next != in["url"] {
			u, err := url.Parse(next)
			if err != nil || !u.IsAbs() {
				return fmt.Errorf("script: msg.url must be an absolute URL")
			}
			*m.url = *u
		}
	} else if status := int(msg.Get("status").ToInteger()); status != m.status {
		if status < 100 || status > 599 {
			return fmt.Errorf("script: msg.status must be 100..599")
		}
		m.status = status
	}
	return nil
}
