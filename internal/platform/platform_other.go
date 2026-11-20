//go:build !windows

package platform

import "context"

// IsElevated is always false off Windows.
func IsElevated() bool { return false }

// Run is unsupported off Windows.
func Run(ctx context.Context, exe, args string, elevate bool) (int64, error) {
	return -1, ErrUnsupported
}

// ReadARP returns no entries off Windows.
func ReadARP() ([]ARPEntry, error) { return nil, nil }

// AddToUserPath is unsupported off Windows.
func AddToUserPath(dir string) (bool, error) { return false, ErrUnsupported }

// FindNativeWinget never finds anything off Windows.
func FindNativeWinget() string { return "" }

// ReadMSIX returns no packages off Windows.
func ReadMSIX(ctx context.Context) ([]MSIXPackage, error) { return nil, nil }

// RemoveMSIX is unsupported off Windows.
func RemoveMSIX(ctx context.Context, fullName string) error { return ErrUnsupported }
