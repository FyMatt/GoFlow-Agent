//go:build windows

package nativesandbox

func platformProbe() Report {
	appContainerAvailable := windowsAppContainerAvailable()
	return Report{Features: []Feature{
		{Name: "appcontainer", Available: appContainerAvailable, Implemented: true, Enforced: appContainerAvailable, Required: true},
		{Name: "workspace_acl", Available: true, Implemented: true, Enforced: appContainerAvailable, Required: true},
		{Name: "restricted_token", Available: true, Implemented: true, Enforced: true, Required: true},
		{Name: "job_object", Available: true, Implemented: true, Enforced: true, Required: true},
		{Name: "network_policy", Available: appContainerAvailable, Implemented: true, Enforced: appContainerAvailable, Required: true},
	}}
}
