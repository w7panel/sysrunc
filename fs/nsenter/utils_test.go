package nsenter

import "testing"

func TestIsPayloadMountpoint(t *testing.T) {
	for _, test := range []struct {
		path   string
		prefix string
		want   bool
	}{
		{"/dev/.sysrunc-procfs-123", ".sysrunc-procfs-", true},
		{"/dev/.sysrunc-sysfs-123", ".sysrunc-sysfs-", true},
		{"/.sysrunc-procfs-123", ".sysrunc-procfs-", false},
		{"/dev/not-sysbox", ".sysrunc-procfs-", false},
	} {
		if got := isPayloadMountpoint(test.path, test.prefix); got != test.want {
			t.Errorf("isPayloadMountpoint(%q, %q) = %v, want %v", test.path, test.prefix, got, test.want)
		}
	}
}
