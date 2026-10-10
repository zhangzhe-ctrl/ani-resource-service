package data

import (
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"strings"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Reads complete assigned networks at the existing KC/NodeFacts boundary.
// intranetNetworks describes routing reachability and is deliberately excluded.
func (p *KCProvider) PlatformCIDRs(ctx context.Context) ([]string, time.Time, error) {
	startedAt, err := p.repository.queries.DatabaseTime(ctx)
	if err != nil {
		return nil, time.Time{}, readFailure(err)
	}
	unknown := func() ([]string, time.Time, error) {
		return nil, time.Time{}, biz.Fail(biz.DependencyUnavailable, "required platform CIDR facts are unknown or expired")
	}
	nodes, err := p.listAll(ctx, kcNodes)
	if err != nil {
		return nil, time.Time{}, err
	}
	maps, err := p.listAll(ctx, kcConfigMaps)
	if err != nil {
		return nil, time.Time{}, err
	}
	services, err := p.listAll(ctx, kcServiceCIDRs)
	if err != nil {
		return nil, time.Time{}, err
	}
	subnets, err := p.listAll(ctx, kcSubnets)
	if err != nil {
		return nil, time.Time{}, err
	}
	controllers, err := p.listAll(ctx, pods)
	if err != nil {
		return nil, time.Time{}, err
	}
	// Normal collector renewal during the reads is not a future observation.
	now, err := p.repository.queries.DatabaseTime(ctx)
	if err != nil {
		return nil, time.Time{}, readFailure(err)
	}
	values := []string{}
	observedAt := startedAt
	add := func(raw string) bool {
		prefix, e := netip.ParsePrefix(raw)
		if e != nil {
			return false
		}
		if prefix.Addr().Is4() {
			values = append(values, prefix.Masked().String())
		}
		return true
	}
	defaultSubnet := ""
	foundController := false
	for _, pod := range controllers {
		if pod.GetNamespace() != kcSystemNamespace || pod.GetLabels()["networking.kubercloud.com/app"] != "controller" || pod.GetDeletionTimestamp() != nil {
			continue
		}
		containers, _, _ := unstructured.NestedSlice(pod.Object, "spec", "containers")
		for _, raw := range containers {
			container, ok := raw.(map[string]any)
			if !ok {
				return unknown()
			}
			command, _, _ := unstructured.NestedStringSlice(container, "command")
			args, _, _ := unstructured.NestedStringSlice(container, "args")
			if !slices.ContainsFunc(command, func(v string) bool { return strings.Contains(v, "controller") }) && !slices.Contains(args, "controller") {
				continue
			}
			name := "kcn-default" // Pinned KCN --default-subnet default; CIDR always comes from its CR.
			for j, arg := range args {
				if strings.HasPrefix(arg, "--default-subnet=") {
					name = strings.TrimPrefix(arg, "--default-subnet=")
				}
				if arg == "--default-subnet" {
					if j+1 == len(args) {
						return unknown()
					}
					name = args[j+1]
				}
			}
			if name == "" || (foundController && name != defaultSubnet) {
				return unknown()
			}
			defaultSubnet, foundController = name, true
		}
	}
	if !foundController || len(nodes) == 0 || len(services) == 0 {
		return unknown()
	}
	defaultFound := false
	for _, subnet := range subnets {
		kind := crString(&subnet, "spec", "type")
		if subnet.GetNamespace() != kcSystemNamespace && kind != "Public" && kind != "Intranet" && kind != "System" {
			continue
		}
		if subnet.GetUID() == "" || !add(crString(&subnet, "spec", "cidrBlock")) {
			return unknown()
		}
		if subnet.GetNamespace() == kcSystemNamespace && subnet.GetName() == defaultSubnet {
			defaultFound = true
		}
	}
	if !defaultFound {
		return unknown()
	}
	for _, service := range services {
		cidrs, _, _ := unstructured.NestedStringSlice(service.Object, "spec", "cidrs")
		if service.GetUID() == "" || len(cidrs) == 0 {
			return unknown()
		}
		for _, cidr := range cidrs {
			if !add(cidr) {
				return unknown()
			}
		}
	}
	for _, node := range nodes {
		if node.GetUID() == "" || node.GetDeletionTimestamp() != nil {
			return unknown()
		}
		var doc NodeFactsDocument
		count := 0
		for _, cm := range maps {
			if cm.GetNamespace() == kcSystemNamespace && cm.GetLabels()[ownerLabel] == factsOwner && cm.GetLabels()[factsNodeLabel] == string(node.GetUID()) {
				if cm.GetUID() == "" || cm.GetDeletionTimestamp() != nil || json.Unmarshal([]byte(crString(&cm, "data", "facts.json")), &doc) != nil {
					return unknown()
				}
				count++
			}
		}
		if count != 1 || doc.Version != 1 || doc.NodeName != node.GetName() || doc.NodeUID != string(node.GetUID()) || len(doc.Interfaces) == 0 || doc.CollectedAt.IsZero() || doc.CollectedAt.After(now) || now.Sub(doc.CollectedAt) > time.Minute {
			return unknown()
		}
		if doc.CollectedAt.Before(observedAt) {
			observedAt = doc.CollectedAt
		}
		nodeNetworks := []netip.Prefix{}
		names := map[string]bool{}
		for _, iface := range doc.Interfaces {
			if iface.NodeUID != doc.NodeUID || iface.NodeName != doc.NodeName || iface.Name == "" || names[iface.Name] {
				return unknown()
			}
			names[iface.Name] = true
			for _, address := range iface.Addresses {
				prefix, e := netip.ParsePrefix(address)
				if e != nil {
					return unknown()
				}
				if prefix.Addr().IsLoopback() {
					continue
				}
				if !add(address) {
					return unknown()
				}
				nodeNetworks = append(nodeNetworks, prefix.Masked())
			}
		}
		addresses, _, _ := unstructured.NestedSlice(node.Object, "status", "addresses")
		internalFound := false
		for _, raw := range addresses {
			address, ok := raw.(map[string]any)
			if !ok {
				return unknown()
			}
			if address["type"] == "InternalIP" {
				value, ok := address["address"].(string)
				if !ok {
					return unknown()
				}
				ip, e := netip.ParseAddr(value)
				if e != nil || !slices.ContainsFunc(nodeNetworks, func(prefix netip.Prefix) bool { return prefix.Contains(ip) }) {
					return unknown()
				}
				internalFound = true
			}
		}
		if !internalFound {
			return unknown()
		}
		podCIDRs, _, _ := unstructured.NestedStringSlice(node.Object, "spec", "podCIDRs")
		for _, cidr := range podCIDRs {
			if !add(cidr) {
				return unknown()
			}
		}
	}
	slices.Sort(values)
	return slices.Compact(values), observedAt, nil
}
