package server

// Domain registration extends the existing trusted Governance listener.
var instanceGovernanceMethods = map[string]struct{}{
	"/instance.v1.InstanceService/GetInstanceRuntime":    {},
	"/instance.v1.InstanceService/ListInstanceEvents":    {},
	"/instance.v1.InstanceService/ListPresets":           {},
	"/instance.v1.InstanceService/ResolveCreateSpec":     {},
	"/instance.v1.InstanceService/AcceptCreate":          {},
	"/instance.v1.InstanceService/AcceptDelete":          {},
	"/instance.v1.InstanceService/Start":                 {},
	"/instance.v1.InstanceService/Stop":                  {},
	"/instance.v1.InstanceService/GetInstance":           {},
	"/instance.v1.InstanceService/ListInstances":         {},
	"/instance.v1.InstanceService/GetOperation":          {},
	"/instance.v1.InstanceService/ResolveTerminalTarget": {},
}

var instancePlatformMethods = map[string]struct{}{
	"/instance.v1.InstanceService/AdminListPresets":      {},
	"/instance.v1.InstanceService/AdminGetPreset":        {},
	"/instance.v1.InstanceService/AdminCreatePreset":     {},
	"/instance.v1.InstanceService/AdminUpdatePreset":     {},
	"/instance.v1.InstanceService/AdminSetPresetEnabled": {},
	"/instance.v1.InstanceService/AdminDeletePreset":     {},
}
