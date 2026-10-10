package biz

// LoadBalancerFlavor describes the fixed, platform-provisioned deployment.
// Class mapping and installation/runtime observation use this same contract.
type LoadBalancerFlavor struct {
	Name                                             string
	Replicas                                         int64
	RequestCPU, RequestMemory, LimitCPU, LimitMemory string
}

func LoadBalancerFlavors() [3]LoadBalancerFlavor {
	return [3]LoadBalancerFlavor{
		{Name: "small", Replicas: 2, RequestCPU: "1", RequestMemory: "1Gi", LimitCPU: "2", LimitMemory: "2Gi"},
		{Name: "medium", Replicas: 4, RequestCPU: "2", RequestMemory: "2Gi", LimitCPU: "4", LimitMemory: "4Gi"},
		{Name: "large", Replicas: 6, RequestCPU: "4", RequestMemory: "4Gi", LimitCPU: "8", LimitMemory: "8Gi"},
	}
}

func FindLoadBalancerFlavor(name string) (LoadBalancerFlavor, bool) {
	if name == "" {
		name = "small"
	}
	for _, flavor := range LoadBalancerFlavors() {
		if flavor.Name == name {
			return flavor, true
		}
	}
	return LoadBalancerFlavor{}, false
}

func (f LoadBalancerFlavor) GatewayClass(exposure string) string {
	name := "lb-" + f.Name
	if exposure == "private" {
		name += "-noeip"
	}
	return name
}
