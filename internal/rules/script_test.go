package rules

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestScriptEditsMessage(t *testing.T) {
	p, err := compileScript(`var d = JSON.parse(msg.body); d.plan = "pro"; msg.body = JSON.stringify(d);
		msg.headers["X-Added"] = "1"; delete msg.headers["X-Old"]; msg.status = 201;`)
	if err != nil {
		t.Fatal(err)
	}
	m := &message{header: http.Header{"X-Old": {"a"}, "Set-Cookie": {"a=1", "b=2"}}, body: []byte(`{"plan":"free"}`), status: 200}
	if err := runScript(p, m, false); err != nil {
		t.Fatal(err)
	}
	if string(m.body) != `{"plan":"pro"}` || m.status != 201 || m.header.Get("X-Added") != "1" || m.header.Get("X-Old") != "" {
		t.Fatalf("%+v %s", m, m.body)
	}
	if len(m.header["Set-Cookie"]) != 2 {
		t.Fatalf("untouched repeated header changed: %v", m.header["Set-Cookie"])
	}
}

func TestScriptURLAndLimits(t *testing.T) {
	u, _ := url.Parse("https://a.com/x")
	p, _ := compileScript(`msg.url = "https://b.com/y"`)
	m := &message{header: http.Header{}, url: u}
	if err := runScript(p, m, true); err != nil || m.url.Host != "b.com" {
		t.Fatalf("%v %v", err, m.url)
	}
	loop, _ := compileScript(`while (true) {}`)
	if err := runScript(loop, &message{header: http.Header{}, status: 200}, false); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("loop: %v", err)
	}
	if _, err := compileScript(`this is not js`); err == nil {
		t.Fatal("bad script compiled")
	}
	noHost, _ := compileScript(`require("fs")`)
	if err := runScript(noHost, &message{header: http.Header{}, status: 200}, false); err == nil {
		t.Fatal("require should not exist")
	}
}
