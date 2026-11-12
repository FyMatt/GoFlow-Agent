//go:build !windows && !linux

package nativesandbox

func platformProbe() Report {
	return Report{Features: []Feature{
		{Name: "supported_platform", Available: false, Implemented: false, Enforced: false, Required: true},
	}}
}
