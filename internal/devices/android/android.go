// Package android points Android emulators and USB devices at the proxy over adb
// and installs the ProxyPilot CA as a user or system certificate.
package android

import (
	"context"
	"crypto/md5"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/hakanor/proxypilot/internal/devices/execx"
)

var (
	ErrNoADB    = errors.New("adb not found: install Android SDK platform-tools, set ANDROID_HOME, or set android.adb in config.yaml")
	ErrNoDevice = errors.New("no Android device or emulator connected (check `adb devices`)")
	ErrNoRoot   = errors.New("system CA needs root: use an emulator image without Google Play (\"Google APIs\"), or a rooted device. Use --ca user instead")
	ErrMany     = errors.New("more than one device connected: pass --serial")
)

// Modes for routing traffic to the proxy.
const (
	ModeAuto = "" // emulator → emulator, device → usb
	ModeEmu  = "emulator"
	ModeUSB  = "usb"  // adb reverse; independent of Wi-Fi
	ModeWiFi = "wifi" // device and Mac on the same network
)

// UserCAPath is where the user certificate is pushed for manual install.
const UserCAPath = "/sdcard/Download/proxypilot-ca.crt"

type Device struct {
	Serial   string `json:"serial"`
	State    string `json:"state"` // device, unauthorized, offline
	Model    string `json:"model,omitempty"`
	Emulator bool   `json:"emulator"`
	API      int    `json:"api_level,omitempty"`
	// Proxy is the current global http_proxy ("" or ":0" = none).
	Proxy string `json:"proxy,omitempty"`
	// Ours reports whether Proxy points at ProxyPilot.
	Ours     bool `json:"ours"`
	UserCA   bool `json:"user_ca_pushed"`
	SystemCA bool `json:"system_ca"`
}

type ADB struct {
	Run  execx.Runner
	Path string
}

// Find locates adb: explicit path, $ANDROID_HOME, the default SDK location, then PATH.
func Find(configured string) (string, error) {
	cands := []string{configured}
	for _, env := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if v := os.Getenv(env); v != "" {
			cands = append(cands, filepath.Join(v, "platform-tools", "adb"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if runtime.GOOS == "darwin" {
			cands = append(cands, filepath.Join(home, "Library/Android/sdk/platform-tools/adb"))
		} else {
			cands = append(cands, filepath.Join(home, "Android/Sdk/platform-tools/adb"))
		}
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	if p, err := exec.LookPath("adb"); err == nil {
		return p, nil
	}
	return "", ErrNoADB
}

func New(configured string) (*ADB, error) {
	p, err := Find(configured)
	if err != nil {
		return nil, err
	}
	return &ADB{Run: execx.OS{}, Path: p}, nil
}

func (a *ADB) run(ctx context.Context, serial string, args ...string) (string, error) {
	if serial != "" {
		args = append([]string{"-s", serial}, args...)
	}
	out, err := a.Run.Run(ctx, a.Path, args...)
	return strings.TrimSpace(string(out)), err
}

func (a *ADB) shell(ctx context.Context, serial, cmd string) (string, error) {
	return a.run(ctx, serial, "shell", cmd)
}

// Devices lists attached devices with API level and current ProxyPilot state.
func (a *ADB) Devices(ctx context.Context, proxyPort string, caHash string) ([]Device, error) {
	out, err := a.run(ctx, "", "devices", "-l")
	if err != nil {
		return nil, err
	}
	var list []Device
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		d := Device{Serial: f[0], State: f[1], Emulator: strings.HasPrefix(f[0], "emulator-")}
		for _, kv := range f[2:] {
			if k, v, ok := strings.Cut(kv, ":"); ok && k == "model" {
				d.Model = strings.ReplaceAll(v, "_", " ")
			}
		}
		if d.State == "device" {
			a.inspect(ctx, &d, proxyPort, caHash)
		}
		list = append(list, d)
	}
	return list, nil
}

func (a *ADB) inspect(ctx context.Context, d *Device, port, hash string) {
	if v, err := a.shell(ctx, d.Serial, "getprop ro.build.version.sdk"); err == nil {
		d.API, _ = strconv.Atoi(v)
	}
	if v, err := a.shell(ctx, d.Serial, "getprop ro.kernel.qemu"); err == nil && v == "1" {
		d.Emulator = true
	}
	if v, err := a.shell(ctx, d.Serial, "settings get global http_proxy"); err == nil && v != "null" && v != ":0" {
		d.Proxy = v
		d.Ours = strings.HasSuffix(v, ":"+port)
	}
	if v, _ := a.shell(ctx, d.Serial, "ls "+UserCAPath+" 2>/dev/null"); v == UserCAPath {
		d.UserCA = true
	}
	if hash != "" {
		path := "/system/etc/security/cacerts/" + hash + ".0"
		if d.API >= 34 {
			path = "/apex/com.android.conscrypt/cacerts/" + hash + ".0"
		}
		if v, _ := a.shell(ctx, d.Serial, "ls "+path+" 2>/dev/null"); v == path {
			d.SystemCA = true
		}
	}
}

// Pick returns the device for serial, or the only ready device.
func Pick(list []Device, serial string) (Device, error) {
	var ready []Device
	for _, d := range list {
		if serial != "" && d.Serial == serial {
			if d.State != "device" {
				return d, fmt.Errorf("%s is %s (unlock it and accept the USB debugging prompt)", d.Serial, d.State)
			}
			return d, nil
		}
		if d.State == "device" {
			ready = append(ready, d)
		}
	}
	switch {
	case serial != "":
		return Device{}, fmt.Errorf("device %s not found", serial)
	case len(ready) == 0:
		return Device{}, ErrNoDevice
	case len(ready) > 1:
		return Device{}, ErrMany
	}
	return ready[0], nil
}

// SubjectHashOld is OpenSSL's `x509 -subject_hash_old`: the first 4 bytes of
// MD5(DER subject), little-endian, as 8 hex digits. Android names CA files by it.
func SubjectHashOld(c *x509.Certificate) string {
	sum := md5.Sum(c.RawSubject)
	return fmt.Sprintf("%08x", binary.LittleEndian.Uint32(sum[:4]))
}

// Result describes what Setup did, plus steps left to the user.
type Result struct {
	Device   Device   `json:"device"`
	Mode     string   `json:"mode"`
	Proxy    string   `json:"proxy"`
	CA       string   `json:"ca"`
	Done     []string `json:"done"`
	Manual   []string `json:"manual,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// SetupOptions configure Setup.
type SetupOptions struct {
	Serial  string
	Mode    string // "", emulator, usb, wifi
	CA      string // "user" (default), "system", "none"
	Port    string // proxy port
	LANIP   string // for wifi mode
	CertPEM []byte
	CertDER []byte
	Cert    *x509.Certificate
}

// Setup routes the device's traffic through the proxy and installs the CA.
func (a *ADB) Setup(ctx context.Context, o SetupOptions) (*Result, error) {
	hash := SubjectHashOld(o.Cert)
	list, err := a.Devices(ctx, o.Port, hash)
	if err != nil {
		return nil, err
	}
	d, err := Pick(list, o.Serial)
	if err != nil {
		return nil, err
	}
	r := &Result{Device: d, Mode: o.Mode, CA: o.CA}
	if r.Mode == "" {
		r.Mode = ModeUSB
		if d.Emulator {
			r.Mode = ModeEmu
		}
	}
	// The certificate first: if it fails (no root), the device is left untouched.
	switch o.CA {
	case "", "user":
		r.CA = "user"
		if err := a.pushUserCA(ctx, d.Serial, o.CertDER); err != nil {
			return nil, err
		}
		r.Done = append(r.Done, "certificate copied to "+UserCAPath)
		r.Manual = append(r.Manual, "On the device: Settings → Security → Encryption & credentials → Install a certificate → CA certificate → Downloads → proxypilot-ca.crt")
		r.Warnings = append(r.Warnings, "user_ca_apps")
	case "system":
		if err := a.installSystemCA(ctx, d, hash, o.CertPEM); err != nil {
			return nil, err
		}
		r.Done = append(r.Done, "CA installed as a system certificate ("+hash+".0); it lasts until the next reboot")
	case "none":
	default:
		return nil, fmt.Errorf("unknown CA mode %q (user, system or none)", o.CA)
	}
	switch r.Mode {
	case ModeEmu:
		r.Proxy = "10.0.2.2:" + o.Port // the emulator's alias for the host loopback
	case ModeUSB:
		if _, err := a.run(ctx, d.Serial, "reverse", "tcp:"+o.Port, "tcp:"+o.Port); err != nil {
			return nil, fmt.Errorf("adb reverse: %w", err)
		}
		r.Proxy = "127.0.0.1:" + o.Port
		r.Done = append(r.Done, "adb reverse tcp:"+o.Port+" (traffic goes over USB, Wi-Fi not needed)")
	case ModeWiFi:
		if o.LANIP == "" {
			return nil, errors.New("wifi mode needs this Mac's LAN address and the proxy listening on 0.0.0.0")
		}
		r.Proxy = o.LANIP + ":" + o.Port
	default:
		return nil, fmt.Errorf("unknown mode %q (emulator, usb or wifi)", r.Mode)
	}
	if _, err := a.shell(ctx, d.Serial, "settings put global http_proxy "+r.Proxy); err != nil {
		return nil, fmt.Errorf("set proxy: %w", err)
	}
	r.Done = append(r.Done, "global HTTP proxy set to "+r.Proxy)

	d.Proxy, d.Ours = r.Proxy, true
	r.Device = d
	return r, nil
}

func (a *ADB) pushFile(ctx context.Context, serial string, data []byte, remote string) error {
	f, err := os.CreateTemp("", "proxypilot-ca-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if _, err := a.run(ctx, serial, "push", f.Name(), remote); err != nil {
		return fmt.Errorf("adb push: %w", err)
	}
	return nil
}

func (a *ADB) pushUserCA(ctx context.Context, serial string, der []byte) error {
	return a.pushFile(ctx, serial, der, UserCAPath)
}

// installSystemCA overlays the system CA directory with a tmpfs that contains the
// existing certificates plus ours (no remount or reboot needed). On Android 14+
// the certificates live in the conscrypt APEX, which is bind-mounted into the
// zygote and every running app's mount namespace.
func (a *ADB) installSystemCA(ctx context.Context, d Device, hash string, pem []byte) error {
	out, err := a.run(ctx, d.Serial, "root")
	if err != nil || strings.Contains(out, "cannot run as root") {
		return ErrNoRoot
	}
	_, _ = a.run(ctx, d.Serial, "wait-for-device")
	if id, err := a.shell(ctx, d.Serial, "id -u"); err != nil || id != "0" {
		return ErrNoRoot
	}
	tmp := "/data/local/tmp/" + hash + ".0"
	if err := a.pushFile(ctx, d.Serial, pem, tmp); err != nil {
		return err
	}
	src := "/system/etc/security/cacerts"
	if d.API >= 34 {
		src = "/apex/com.android.conscrypt/cacerts"
	}
	script := strings.Join([]string{
		"set -e",
		"rm -rf /data/local/tmp/pp-cacerts && mkdir -p -m 700 /data/local/tmp/pp-cacerts",
		"cp " + src + "/* /data/local/tmp/pp-cacerts/",
		"mount -t tmpfs tmpfs /system/etc/security/cacerts",
		"mv /data/local/tmp/pp-cacerts/* /system/etc/security/cacerts/",
		"mv " + tmp + " /system/etc/security/cacerts/",
		"chown root:root /system/etc/security/cacerts/*",
		"chmod 644 /system/etc/security/cacerts/*",
		"chcon u:object_r:system_file:s0 /system/etc/security/cacerts/*",
	}, " && ")
	if d.API >= 34 {
		script += " && for Z in $(pidof zygote zygote64); do nsenter --mount=/proc/$Z/ns/mnt -- /bin/mount --bind /system/etc/security/cacerts /apex/com.android.conscrypt/cacerts; done" +
			" && for P in $(ps -o PID= -P $(pidof zygote zygote64 | tr ' ' ',')); do nsenter --mount=/proc/$P/ns/mnt -- /bin/mount --bind /system/etc/security/cacerts /apex/com.android.conscrypt/cacerts 2>/dev/null || true; done"
	}
	if out, err := a.shell(ctx, d.Serial, script); err != nil {
		return fmt.Errorf("installing the system CA failed (API %d): %v %s", d.API, err, out)
	}
	return nil
}

// Remove clears the proxy setting and the USB reverse port. The tmpfs system CA
// goes away on reboot; the user CA must be removed on the device.
func (a *ADB) Remove(ctx context.Context, serial, port string) (*Result, error) {
	list, err := a.Devices(ctx, port, "")
	if err != nil {
		return nil, err
	}
	d, err := Pick(list, serial)
	if err != nil {
		return nil, err
	}
	r := &Result{Device: d}
	if _, err := a.shell(ctx, d.Serial, "settings put global http_proxy :0"); err != nil {
		return nil, err
	}
	_, _ = a.shell(ctx, d.Serial, "settings delete global global_http_proxy_host; settings delete global global_http_proxy_port")
	r.Done = append(r.Done, "global HTTP proxy cleared")
	if _, err := a.run(ctx, d.Serial, "reverse", "--remove", "tcp:"+port); err == nil {
		r.Done = append(r.Done, "adb reverse tcp:"+port+" removed")
	}
	if d.UserCA {
		_, _ = a.shell(ctx, d.Serial, "rm -f "+UserCAPath)
		r.Done = append(r.Done, "removed "+UserCAPath)
	}
	r.Manual = append(r.Manual,
		"If you installed the user CA: Settings → Security → Encryption & credentials → User credentials → ProxyPilot CA → Remove",
		"A system CA installed by ProxyPilot disappears on the next reboot")
	r.Device.Proxy, r.Device.Ours = "", false
	return r, nil
}

// DevNote is the copy-paste snippet for trusting user CAs in debug builds.
const DevNote = `<!-- res/xml/network_security_config.xml -->
<network-security-config>
    <debug-overrides>
        <trust-anchors>
            <certificates src="user" />
            <certificates src="system" />
        </trust-anchors>
    </debug-overrides>
</network-security-config>

<!-- AndroidManifest.xml, on <application> -->
android:networkSecurityConfig="@xml/network_security_config"`

// FlutterNote makes Dart's HttpClient use the system proxy in debug builds
// (Flutter ignores the Android proxy setting by default).
const FlutterNote = `// main.dart (debug builds only)
class ProxyPilotOverrides extends HttpOverrides {
  @override
  HttpClient createHttpClient(SecurityContext? context) {
    final client = super.createHttpClient(context);
    client.findProxy = (uri) => 'PROXY 10.0.2.2:9090'; // emulator; use 127.0.0.1:9090 with adb reverse
    client.badCertificateCallback = (cert, host, port) => true; // debug only!
    return client;
  }
}

void main() {
  if (kDebugMode) HttpOverrides.global = ProxyPilotOverrides();
  runApp(const MyApp());
}`
