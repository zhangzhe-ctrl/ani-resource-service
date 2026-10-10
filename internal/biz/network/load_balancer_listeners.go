package biz

import (
	"github.com/google/uuid"
	"regexp"
	"slices"
)

type LoadBalancerListenerInput struct {
	ID, Name, Protocol string
	Port               *uint32
	Backends           []LoadBalancerBackendInput
	Health             LoadBalancerHealthInput
}

var lbListenerName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// EffectiveLoadBalancerListeners also projects historical acceptance receipts.
func EffectiveLoadBalancerListeners(v LoadBalancer) []LoadBalancerListener {
	if len(v.Listeners) != 0 {
		return v.Listeners
	}
	listener := v.Listener
	listener.Name, listener.Protocol = "http", "HTTP"
	listener.Backends, listener.Health = v.Backends, v.Health
	return []LoadBalancerListener{listener}
}

func normalizeLBListeners(i *LoadBalancerIntent, input []LoadBalancerListenerInput, creating bool) error {
	if len(input) < 1 || len(input) > 64 {
		return Fail(InvalidArgument, "listeners must contain 1..64 HTTP listeners; delete the LB to remove its last listener")
	}
	names, ports, ids := map[string]bool{}, map[uint32]bool{}, map[string]bool{}
	for _, r := range input {
		if len(r.Name) > 253 || !lbListenerName.MatchString(r.Name) || names[r.Name] {
			return Fail(InvalidArgument, "listener names must be unique DNS names within 1..253 characters")
		}
		if r.Protocol != "" && r.Protocol != "HTTP" {
			return Fail(InvalidArgument, "only HTTP listeners are supported")
		}
		if r.Port == nil || *r.Port == 0 || *r.Port > 65535 || ports[*r.Port] {
			return Fail(InvalidArgument, "listener ports must be unique and within 1..65535")
		}
		if r.ID != "" {
			id, err := uuid.Parse(r.ID)
			if creating || err != nil || id == uuid.Nil || id.String() != r.ID || ids[r.ID] {
				return Fail(InvalidArgument, "invalid listener identity")
			}
		}
		normalized := LoadBalancerIntent{IdempotencyKey: i.IdempotencyKey}
		if err := normalizeLBMutable(&normalized, LoadBalancerMutableInput{Name: i.Name, Description: i.Description, Backends: r.Backends, Health: r.Health}, creating); err != nil {
			return err
		}
		names[r.Name], ports[*r.Port], ids[r.ID] = true, true, true
		i.Listeners = append(i.Listeners, LoadBalancerListener{ID: r.ID, Name: r.Name, Protocol: "HTTP", Port: *r.Port, Backends: normalized.Backends, Health: normalized.Health})
	}
	slices.SortFunc(i.Listeners, func(a, b LoadBalancerListener) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return nil
}

func lbHasLegacyMutable(r LoadBalancerMutableInput) bool {
	h := r.Health
	return r.Backends != nil || h.Protocol != "" || h.Port != nil || h.IntervalSeconds != nil || h.TimeoutSeconds != nil || h.UnhealthyThreshold != nil || h.HealthyThreshold != nil
}

func normalizeLBInput(i *LoadBalancerIntent, r LoadBalancerMutableInput, creating bool) error {
	if r.Listeners == nil && len(r.UpdateMask) == 0 && (creating || lbHasLegacyMutable(r)) {
		return normalizeLBMutable(i, r, creating)
	}
	if r.Listeners != nil && lbHasLegacyMutable(r) {
		return Fail(InvalidArgument, "listeners cannot be combined with legacy backend or health fields")
	}
	var err error
	name := r.Name
	if !creating && len(r.UpdateMask) > 0 && !slices.Contains(r.UpdateMask, "name") {
		name = "unchanged"
	}
	i.Name, err = normalizeEgressName(name, r.Description, i.IdempotencyKey)
	if err != nil {
		return err
	}
	i.Description = r.Description
	if r.Listeners == nil && len(r.UpdateMask) == 0 {
		i.UpdateMask = []string{"description", "name"}
	}
	if len(r.UpdateMask) > 0 {
		if creating {
			return Fail(InvalidArgument, "update_mask is only supported for updates")
		}
		for _, path := range r.UpdateMask {
			if path != "name" && path != "description" && path != "listeners" {
				return Fail(InvalidArgument, "invalid load balancer update_mask")
			}
		}
		i.UpdateMask = slices.Clone(r.UpdateMask)
		slices.Sort(i.UpdateMask)
		i.UpdateMask = slices.Compact(i.UpdateMask)
		if slices.Contains(i.UpdateMask, "listeners") && r.Listeners == nil {
			return Fail(InvalidArgument, "an empty listener replacement is forbidden")
		}
		if !slices.Contains(i.UpdateMask, "listeners") && r.Listeners != nil {
			return Fail(InvalidArgument, "listeners must be included in update_mask")
		}
	}
	if r.Listeners != nil {
		return normalizeLBListeners(i, r.Listeners, creating)
	}
	if lbHasLegacyMutable(r) {
		return Fail(InvalidArgument, "legacy mutable input cannot be combined with update_mask")
	}
	return nil
}
