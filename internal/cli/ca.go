package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hakanor/proxypilot/internal/ca"
)

func (a *App) loadCA() (*ca.RootCA, error) {
	if err := a.Config.EnsureDataDir(); err != nil {
		return nil, err
	}
	return ca.LoadOrCreate(filepath.Join(a.Config.DataDir, "ca"))
}

// CAInfo is the JSON schema of `ca info --json`.
type CAInfo struct {
	CommonName  string    `json:"common_name"`
	Fingerprint string    `json:"fingerprint_sha256"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	CertPath    string    `json:"cert_path"`
	KeyPath     string    `json:"key_path"`
	Trust       string    `json:"trust"` // trusted | not_trusted | unknown
}

func newCACmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "ca", Short: "Manage the ProxyPilot root certificate"}

	info := &cobra.Command{
		Use:   "info",
		Short: "Show CA details and trust status",
		RunE: func(*cobra.Command, []string) error {
			r, err := app.loadCA()
			if err != nil {
				return err
			}
			c := r.Certificate()
			out := CAInfo{
				CommonName: c.Subject.CommonName, Fingerprint: r.Fingerprint(),
				NotBefore: c.NotBefore, NotAfter: c.NotAfter,
				CertPath: r.CertPath(), KeyPath: r.KeyPath(), Trust: r.TrustStatus(),
			}
			if app.Flags.JSON {
				return app.PrintJSON(out)
			}
			app.Printf("Name:        %s\nFingerprint: %s\nValid:       %s → %s\nCertificate: %s\nTrust:       %s\n",
				out.CommonName, out.Fingerprint, out.NotBefore.Format(time.DateOnly), out.NotAfter.Format(time.DateOnly), out.CertPath, out.Trust)
			return nil
		},
	}

	var format, outPath string
	export := &cobra.Command{
		Use:   "export",
		Short: "Write the CA certificate (pem or der) to a file",
		RunE: func(*cobra.Command, []string) error {
			if outPath == "" {
				return Exitf(ExitUsage, "--out is required")
			}
			r, err := app.loadCA()
			if err != nil {
				return err
			}
			var data []byte
			switch format {
			case "pem":
				data = r.ExportPEM()
			case "der":
				data = r.ExportDER()
			default:
				return Exitf(ExitUsage, "--format must be pem or der")
			}
			if err := os.WriteFile(outPath, data, 0o644); err != nil {
				return err
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]string{"path": outPath, "format": format, "fingerprint_sha256": r.Fingerprint()})
			}
			app.Printf("Wrote %s (%s)\n", outPath, format)
			return nil
		},
	}
	export.Flags().StringVar(&format, "format", "pem", "pem|der")
	export.Flags().StringVar(&outPath, "out", "", "output file")

	var yes bool
	regen := &cobra.Command{
		Use:   "regenerate",
		Short: "Create a new CA (every device must reinstall it)",
		RunE: func(*cobra.Command, []string) error {
			if !yes {
				return Exitf(ExitUsage, "this replaces the CA; every device and browser must install the new one. Re-run with --yes")
			}
			r, err := app.loadCA()
			if err != nil {
				return err
			}
			if err := r.Regenerate(); err != nil {
				return err
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]string{"fingerprint_sha256": r.Fingerprint(), "cert_path": r.CertPath()})
			}
			app.Printf("New CA created: %s\nReinstall it on every device (proxypilot setup) and re-run `proxypilot trust`.\n", r.Fingerprint())
			return nil
		},
	}
	regen.Flags().BoolVar(&yes, "yes", false, "confirm regeneration")

	cmd.AddCommand(info, export, regen)
	return cmd
}

// newTrustCmd builds `trust` (add=true) or `untrust`.
func newTrustCmd(app *App, add bool) *cobra.Command {
	use, short := "trust", "Trust the CA in the macOS System keychain"
	if !add {
		use, short = "untrust", "Remove the CA from the macOS System keychain"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(*cobra.Command, []string) error {
			r, err := app.loadCA()
			if err != nil {
				return err
			}
			cmds := [][]string{r.TrustCommand()}
			if !add {
				cmds = r.UntrustCommands()
			}
			if runtime.GOOS != "darwin" {
				return linuxTrustHint(app, r, add)
			}
			if os.Geteuid() != 0 {
				lines := make([]string, len(cmds))
				for i, c := range cmds {
					lines[i] = "sudo " + shellJoin(c)
				}
				if app.Flags.JSON {
					_ = app.PrintJSON(map[string]any{"needs_root": true, "commands": lines})
				} else {
					fmt.Fprintf(app.ErrOut, "%s needs administrator rights. Run:\n\n  %s\n\n", use, strings.Join(lines, "\n  "))
				}
				return Exitf(ExitUnauthorized, "root required")
			}
			for _, c := range cmds {
				out, err := exec.Command(c[0], c[1:]...).CombinedOutput()
				if err != nil {
					return fmt.Errorf("%s: %v: %s", shellJoin(c), err, strings.TrimSpace(string(out)))
				}
			}
			if app.Flags.JSON {
				return app.PrintJSON(map[string]any{"ok": true, "trust": r.TrustStatus()})
			}
			app.Printf("Done. Trust status: %s\n", r.TrustStatus())
			return nil
		},
	}
}

func linuxTrustHint(app *App, r *ca.RootCA, add bool) error {
	steps := []string{
		"sudo cp " + r.CertPath() + " /usr/local/share/ca-certificates/proxypilot.crt",
		"sudo update-ca-certificates",
	}
	if !add {
		steps = []string{"sudo rm /usr/local/share/ca-certificates/proxypilot.crt", "sudo update-ca-certificates --fresh"}
	}
	if app.Flags.JSON {
		return app.PrintJSON(map[string]any{"automatic": false, "commands": steps, "note": "Debian/Ubuntu; Fedora uses /etc/pki/ca-trust/source/anchors + update-ca-trust"})
	}
	app.Printf("Automatic trust is only supported on macOS. On Debian/Ubuntu run:\n\n  %s\n\nFedora/RHEL: copy to /etc/pki/ca-trust/source/anchors/ and run update-ca-trust.\n", strings.Join(steps, "\n  "))
	return nil
}

func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " '\"$`\\") {
			q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			q[i] = a
		}
	}
	return strings.Join(q, " ")
}
