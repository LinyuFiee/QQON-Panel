package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"panel/internal/tlsutil"
)

// Each invocation gets fresh CLI flags and never opens a listener or modifies
// a real account. The environment switch exists only in this test executable.
func TestCertificateCLIProcess(t *testing.T) {
	if os.Getenv("QQON_CERT_CLI_TEST") != "1" {
		return
	}
	for index, argument := range os.Args {
		if argument == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[index+1:]...)
			break
		}
	}
	flag.CommandLine = flag.NewFlagSet("panel-certificate-test", flag.ContinueOnError)
	if err := run(context.Background(), func() {}); err != nil {
		_, _ = os.Stderr.WriteString(err.Error())
		os.Exit(2)
	}
	os.Exit(0)
}

func invokeCertificateCLI(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	arguments := append([]string{"-test.run=^TestCertificateCLIProcess$", "--"}, args...)
	command := exec.Command(os.Args[0], arguments...)
	command.Env = append(os.Environ(), "QQON_CERT_CLI_TEST=1")
	return command.CombinedOutput()
}

func TestCertificateCLIIsolatedCreateInspect(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "tls")
	unusedData := filepath.Join(root, "not-an-account")
	output, err := invokeCertificateCLI(t, "-tls-create", "-tls-host", "127.0.0.1", "-tls-output", directory, "-data-dir", unusedData)
	if err != nil {
		t.Fatalf("create command failed: %v %s", err, output)
	}
	var info tlsutil.Info
	if err := json.Unmarshal(output, &info); err != nil {
		t.Fatalf("not clean certificate JSON: %v %s", err, output)
	}
	if _, err := os.Stat(unusedData); !os.IsNotExist(err) {
		t.Fatal("certificate command initialized an account directory")
	}
	if strings.Contains(string(output), "PRIVATE KEY") {
		t.Fatal("private key leaked into CLI output")
	}
	inspected, err := invokeCertificateCLI(t, "-tls-inspect", "-tls-host", "127.0.0.1", "-tls-cert", filepath.Join(directory, "cert.pem"), "-tls-key", filepath.Join(directory, "key.pem"))
	if err != nil {
		t.Fatalf("inspect failed: %v %s", err, inspected)
	}
	var createdJSON, inspectedJSON map[string]any
	_ = json.Unmarshal(output, &createdJSON)
	_ = json.Unmarshal(inspected, &inspectedJSON)
	if createdJSON["fingerprint"] == "" || createdJSON["fingerprint"] != inspectedJSON["fingerprint"] || inspectedJSON["host"] != "127.0.0.1" {
		t.Fatalf("certificate identity mismatch: %s", inspected)
	}
	if _, err := invokeCertificateCLI(t, "-tls-inspect", "-tls-host", "example.invalid", "-tls-cert", filepath.Join(directory, "cert.pem"), "-tls-key", filepath.Join(directory, "key.pem")); err == nil {
		t.Fatal("wrong certificate host accepted")
	}
	if _, err := invokeCertificateCLI(t, "-tls-create", "-tls-host", "127.0.0.1", "-tls-output", directory); err == nil {
		t.Fatal("certificate CLI overwrote an existing directory")
	}
}

func TestCertificateCLIRejectsAmbiguousFlags(t *testing.T) {
	for _, args := range [][]string{
		{"-tls-create", "-tls-inspect"},
		{"-tls-create", "-build-info"},
		{"-tls-create", "-tls-host", "127.0.0.1"},
		{"-tls-inspect", "-tls-host", "127.0.0.1"},
		{"-tls-host", "127.0.0.1"},
		{"-listen", "0.0.0.0:8443", "-origin", "http://127.0.0.1:8443"},
		{"-listen", "0.0.0.0:8443", "-origin", "https://example.invalid:8443"},
	} {
		if output, err := invokeCertificateCLI(t, args...); err == nil {
			t.Fatalf("ambiguous CLI accepted: %v %s", args, output)
		}
	}
}
