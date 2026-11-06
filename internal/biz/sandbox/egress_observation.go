package sandbox

import "context"

// EgressDeclaration describes Kubernetes NetworkPolicy declarations only.
// It is not evidence that a network plugin enforces them, and cannot describe
// FQDN allowlists or additional cluster-wide policies.
type EgressDeclaration struct {
	Declaration string
	Isolated    bool
	Rules       []EgressRule
}

// Empty Peers means any destination, and empty Ports means all protocols and
// ports. Multiple rules and multiple selecting policies are additive.
type EgressRule struct {
	Peers []EgressPeer
	Ports []EgressPort
}

type EgressPeer struct {
	PodSelector       *EgressLabelSelector
	NamespaceSelector *EgressLabelSelector
	IPBlock           *EgressIPBlock
}

type EgressLabelSelector struct {
	MatchLabels      map[string]string
	MatchExpressions []EgressLabelExpression
}

type EgressLabelExpression struct {
	Key, Operator string
	Values        []string
}

type EgressIPBlock struct {
	CIDR   string
	Except []string
}

// PortType is "all", "number", or "name". An omitted Kubernetes protocol
// defaults to TCP, while an empty Ports slice allows all protocols.
type EgressPort struct {
	Protocol string
	PortType string
	Port     string
	EndPort  int32
}

type EgressDeclarationReader interface {
	ReadEgressDeclaration(context.Context, Sandbox) (EgressDeclaration, error)
}
