//go:build windows

package management

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestConsentDescriptorAllowsOnlySystemAndAdministrators(t *testing.T) {
	cases := []struct {
		sddl string
		want bool
	}{
		{"O:SYD:P(A;;FA;;;SY)(A;;FA;;;BA)", true},
		{"O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)", true},
		{"O:SYD:(A;;FA;;;SY)(A;;FA;;;BA)", false},
		{"O:SYD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;BU)", false},
		{"O:SYD:P(A;;FA;;;SY)", false},
		{"O:SYD:P(A;;FR;;;SY)(A;;FA;;;BA)", false},
		{"O:BUD:P(A;;FA;;;SY)(A;;FA;;;BA)", false},
		{"O:SYD:P(A;IO;FA;;;SY)(A;;FA;;;BA)", false},
		{"O:SYD:P(D;;FW;;;BU)(A;;FA;;;SY)(A;;FA;;;BA)", false},
	}
	for _, tc := range cases {
		t.Run(tc.sddl, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tc.sddl)
			if err != nil {
				t.Fatal(err)
			}
			if got := validateConsentDescriptor(sd); (got == nil) != tc.want {
				t.Fatalf("ACL validation: %v, want accepted %t", got, tc.want)
			}
		})
	}
	if validateConsentDescriptor(nil) == nil {
		t.Fatal("missing descriptor accepted")
	}
}

func TestDesktopAdministratorIsNotImplicitSystemConsent(t *testing.T) {
	identity, highest := runtimeIdentity()
	if highest != (identity == "LocalSystem") {
		t.Fatalf("incorrect highest identity classification: %s %t", identity, highest)
	}
	if !highest && New().Status().Authorized {
		t.Fatal("ordinary desktop identity received system management permission")
	}
}
