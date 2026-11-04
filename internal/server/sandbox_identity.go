package server

import sandboxbiz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"

var sandboxGovernanceMethods = map[string]struct{}{
	"/sandbox.terminal.v1.SandboxTerminalService/ResolveTerminalSession": {},
	"/sandbox.v1.SandboxService/OpenTenantSpace":                         {},
	"/sandbox.v1.SandboxService/CloseTenantSpace":                        {},
	"/sandbox.v1.SandboxService/GetTenantSpace":                          {},
	"/sandbox.v1.SandboxService/ResolveCreateSpec":                       {},
	"/sandbox.v1.SandboxService/AcceptCreate":                            {},
	"/sandbox.v1.SandboxService/GetSandbox":                              {},
	"/sandbox.v1.SandboxService/ListSandboxes":                           {},
	"/sandbox.v1.SandboxService/GetOperation":                            {},
	"/sandbox.v1.SandboxService/ListOperations":                          {},
	"/sandbox.v1.SandboxService/ListSandboxPodHistory":                   {},
	"/sandbox.v1.SandboxService/ListEnvironmentVariables":                {},
	"/sandbox.v1.SandboxService/ListSandboxEvents":                       {},
	"/sandbox.v1.SandboxService/ListSandboxSecurityEvents":               {},
	"/sandbox.v1.SandboxService/ListSandboxCheckpoints":                  {},
	"/sandbox.v1.SandboxService/CreateSandboxCheckpoint":                 {},
	"/sandbox.v1.SandboxService/RestoreSandboxCheckpoint":                {},
	"/sandbox.v1.SandboxService/AcceptSandboxClone":                      {},
	"/sandbox.v1.SandboxService/GetSandboxEgress":                        {},
	"/sandbox.v1.SandboxService/Pause":                                   {},
	"/sandbox.v1.SandboxService/Resume":                                  {},
	"/sandbox.v1.SandboxService/Delete":                                  {},
	"/sandbox.v1.SandboxService/RunCode":                                 {},
	"/sandbox.v1.SandboxService/GetCodeRun":                              {},
	"/sandbox.v1.SandboxService/QueryFiles":                              {},
	"/sandbox.v1.SandboxService/MutateFiles":                             {},
	"/sandbox.v1.SandboxService/GetFileOperation":                        {},
	"/sandbox.v1.SandboxService/CreateToken":                             {},
	"/sandbox.v1.SandboxService/ValidateToken":                           {},
	"/sandbox.v1.SandboxService/RevokeToken":                             {},
	"/sandbox.v1.SandboxService/OpenPreview":                             {},
	"/sandbox.v1.SandboxService/ClosePreview":                            {},
	"/sandbox.v1.SandboxService/ListPreviews":                            {},
	"/sandbox.v1.SandboxService/GetPreviewOperation":                     {},
}

func tenantSpaceMethod(method string) bool {
	switch method {
	case "/sandbox.v1.SandboxService/OpenTenantSpace", "/sandbox.v1.SandboxService/CloseTenantSpace", "/sandbox.v1.SandboxService/GetTenantSpace":
		return true
	default:
		return false
	}
}

func sandboxCaller(tenant, actor string) sandboxbiz.Caller {
	return sandboxbiz.Caller{TenantID: tenant, Actor: actor}
}
